package provision

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// HubCASigner is a crypto.Signer whose private key never leaves the Hub (Beads cudk.77: the old
// node-agent CA key was public because it lived on the operator's machine and in git). The key
// file at keyPath is first checked to be the pair of pub (its public half, read with
// `openssl pkey -pubout`); every Sign is then one `openssl pkeyutl -sign` on the Hub — the digest
// goes into the script (it is not secret), the signature comes back base64. The deploy CLI signs
// the Hub CA, the Hub's client certificate and node-agent certificates with it. ctx bounds every
// signature (crypto.Signer takes none).
func HubCASigner(ctx context.Context, hub Remote, keyPath string, pub crypto.PublicKey) (crypto.Signer, error) {
	out, err := hub.Run(ctx, config.KeyPublicScript(keyPath))
	if err != nil {
		return nil, fmt.Errorf("ключ CA %s на Hub не прочитан: %w", keyPath, err)
	}
	if strings.TrimSpace(out) == "NO_OPENSSL" {
		return nil, errors.New("на Hub нет openssl — ключ CA не использовать")
	}
	got, err := parsePublicPEM(out)
	if err != nil {
		return nil, fmt.Errorf("ключа CA %s на Hub нет или он не читается", keyPath)
	}
	eq, ok := pub.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !eq.Equal(got) {
		return nil, fmt.Errorf("ключ %s на Hub — не пара сертификата CA", keyPath)
	}
	return &hubSigner{ctx: ctx, hub: hub, keyPath: keyPath, pub: got}, nil
}

// parsePublicPEM reads the first "PUBLIC KEY" block of out (KeyPublicScript's output).
func parsePublicPEM(out string) (crypto.PublicKey, error) {
	rest := []byte(out)
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return nil, errors.New("нет PEM-блока PUBLIC KEY")
		}
		if b.Type == "PUBLIC KEY" {
			return x509.ParsePKIXPublicKey(b.Bytes)
		}
	}
}

type hubSigner struct {
	ctx     context.Context
	hub     Remote
	keyPath string
	pub     crypto.PublicKey
}

func (s *hubSigner) Public() crypto.PublicKey { return s.pub }

// pkeyutlDigests are the hashes x509 picks for the keys a Hub CA can have (EC P-256/384/521, RSA).
var pkeyutlDigests = map[crypto.Hash]string{crypto.SHA256: "sha256", crypto.SHA384: "sha384", crypto.SHA512: "sha512"}

// Sign: ECDSA (ASN.1 signature, as crypto/ecdsa returns it) or RSA PKCS#1 v1.5 over digest.
func (s *hubSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if _, pss := opts.(*rsa.PSSOptions); pss {
		return nil, errors.New("подпись RSA-PSS на Hub не поддерживается")
	}
	name, ok := pkeyutlDigests[opts.HashFunc()]
	if !ok || len(digest) != opts.HashFunc().Size() {
		return nil, fmt.Errorf("подпись на Hub: неподдерживаемый хеш %v (длина %d)", opts.HashFunc(), len(digest))
	}
	out, err := s.hub.Run(s.ctx, caSignScript(s.keyPath, name, digest))
	if err != nil {
		return nil, fmt.Errorf("подпись ключом CA на Hub: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "SIG="); ok {
			sig, err := base64.StdEncoding.DecodeString(v)
			if err != nil || len(sig) == 0 {
				return nil, errors.New("подпись с Hub не читается")
			}
			return sig, nil
		}
	}
	return nil, errors.New("Hub не вернул подпись")
}

// caSignScript signs digest with the key at keyPath on the Hub: the digest is written to a private
// temp file (octal escapes: binary-safe, no shell metacharacters), openssl reads it as a file (no
// stdin text-mode surprises), the signature comes back as one base64 line "SIG=…".
func caSignScript(keyPath, digestName string, digest []byte) string {
	var oct strings.Builder
	for _, b := range digest {
		fmt.Fprintf(&oct, `\%03o`, b)
	}
	return "# hp:ca-sign " + keyPath + "\n" + `set -euo pipefail
command -v openssl >/dev/null 2>&1 || { echo "[ca] ERROR: на Hub нет openssl" >&2; exit 1; }
umask 077
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
printf '` + oct.String() + `' > "$t/digest"
openssl pkeyutl -sign -inkey ` + shellquote.Quote(keyPath) + ` -pkeyopt digest:` + digestName + ` -in "$t/digest" -out "$t/sig"
echo "SIG=$(openssl base64 -A -in "$t/sig")"
`
}
