package provision

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// A fingerprint pins one key, and a key has one type: the handshake must negotiate exactly the
// confirmed key's type. Go prefers ECDSA, OpenSSH clients ed25519, providers show either — exit 2
// has both and its confirmed key is the ECDSA one.

// hostKeyFamilies are the host key algorithm sets the Hub can pin, in the order Probe lists the
// keys (ed25519 first: the preferred key) and DialNode looks for an unknown key type. One family =
// one key on the server; an RSA key is negotiated with either SHA-2 signature (never SHA-1).
var hostKeyFamilies = [][]string{
	{ssh.KeyAlgoED25519},
	{ssh.KeyAlgoECDSA256},
	{ssh.KeyAlgoECDSA384},
	{ssh.KeyAlgoECDSA521},
	{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
}

// hostKeyAlgorithms returns the HostKeyAlgorithms that make the server present its key of keyType
// (ssh.PublicKey.Type()).
func hostKeyAlgorithms(keyType string) ([]string, error) {
	switch keyType {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return []string{keyType}, nil
	case ssh.KeyAlgoRSA:
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}, nil
	}
	return nil, fmt.Errorf("тип ключа хоста %q не поддерживается (ed25519, ecdsa, rsa)", keyType)
}

var fingerprintRe = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// ValidFingerprint reports whether fp is a SHA256 host key fingerprint as `ssh-keygen -lf` prints
// it (servers.ssh_host_key_fp).
func ValidFingerprint(fp string) bool { return fingerprintRe.MatchString(fp) }

// StoredHostKeyType returns the type of a host key stored as the base64 of its wire blob
// (servers.ssh_host_key — the old TOFU's encoding, HostKey.Key of a probe) when that key has the
// fingerprint fp, and "" otherwise (then DialNode finds the type itself).
func StoredHostKeyType(blob, fp string) string {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob))
	if err != nil || len(raw) == 0 {
		return ""
	}
	key, err := ssh.ParsePublicKey(raw)
	if err != nil || ssh.FingerprintSHA256(key) != fp {
		return ""
	}
	if _, err := hostKeyAlgorithms(key.Type()); err != nil {
		return ""
	}
	return key.Type()
}

// HostKey is one host key a node presented in a handshake-only probe.
type HostKey struct {
	Type        string `json:"type"`        // ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa, …
	Fingerprint string `json:"fingerprint"` // SHA256:…
	Key         string `json:"key"`         // base64 wire blob; "<type> <key>" is the known_hosts form
}

// ProbeResult is what the owner compares with the provider's console before confirming a key.
type ProbeResult struct {
	Fingerprint string    `json:"fingerprint"` // the preferred key (ed25519 when offered)
	KeyType     string    `json:"key_type"`
	Keys        []HostKey `json:"keys"` // every supported key the node offers, in hostKeyFamilies order
}

var (
	errProbeDone   = errors.New("probe: host key captured")
	errNotOffered  = errors.New("probe: host key type not offered")
	handshakeLimit = 30 * time.Second
)

// Probe lists the node's host keys with key exchanges only: every handshake is aborted in the
// host key callback (after the server proved it holds the key), so no user name matters and no
// password or key is ever offered.
func Probe(ctx context.Context, ip string, port int) (ProbeResult, error) {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	keys, err := scanHostKeys(ctx, addr, nil)
	if err != nil {
		return ProbeResult{}, err
	}
	if len(keys) == 0 {
		return ProbeResult{}, fmt.Errorf("SSH на %s не предъявил ни одного поддерживаемого ключа хоста (ed25519, ecdsa, rsa)", addr)
	}
	return ProbeResult{Fingerprint: keys[0].Fingerprint, KeyType: keys[0].Type, Keys: keys}, nil
}

// findHostKeyType finds which of the node's keys has the confirmed fingerprint (handshake-only).
func findHostKeyType(ctx context.Context, addr, fp string) (string, error) {
	keys, err := scanHostKeys(ctx, addr, func(k HostKey) bool { return k.Fingerprint == fp })
	if err != nil {
		return "", err
	}
	var seen []string
	for _, k := range keys {
		if k.Fingerprint == fp {
			return k.Type, nil
		}
		seen = append(seen, k.Type+" "+k.Fingerprint)
	}
	return "", fmt.Errorf("%w: %s, подтверждён %s (сервер предъявил: %s)", ErrHostKeyMismatch, addr, fp, strings.Join(seen, ", "))
}

// scanHostKeys runs one handshake per key family until stop returns true. The first family the
// server lacks fails the negotiation with the server's whole list, so the families it does not
// offer are skipped: at most one connection per offered key plus one.
func scanHostKeys(ctx context.Context, addr string, stop func(HostKey) bool) ([]HostKey, error) {
	var keys []HostKey
	var offered []string // nil until a failed negotiation reveals the server's list
	for _, fam := range hostKeyFamilies {
		if offered != nil && !slices.ContainsFunc(fam, func(a string) bool { return slices.Contains(offered, a) }) {
			continue
		}
		k, serverAlgos, err := probeHostKey(ctx, addr, fam)
		if errors.Is(err, errNotOffered) {
			offered = serverAlgos
			continue
		}
		if err != nil {
			return keys, err
		}
		keys = append(keys, k)
		if stop != nil && stop(k) {
			break
		}
	}
	return keys, nil
}

// probeHostKey runs one key exchange restricted to algos and aborts it once the host key is in.
func probeHostKey(ctx context.Context, addr string, algos []string) (HostKey, []string, error) {
	conn, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return HostKey{}, nil, fmt.Errorf("нет TCP-соединения с %s: %w", addr, err)
	}
	defer conn.Close()
	defer context.AfterFunc(ctx, func() { conn.Close() })()
	_ = conn.SetDeadline(time.Now().Add(handshakeLimit))
	var got ssh.PublicKey
	_, _, _, err = ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: "probe",
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			got = key
			return errProbeDone
		},
		HostKeyAlgorithms: algos,
	})
	if got != nil {
		return HostKey{Type: got.Type(), Fingerprint: ssh.FingerprintSHA256(got),
			Key: base64.StdEncoding.EncodeToString(got.Marshal())}, nil, nil
	}
	var neg *ssh.AlgorithmNegotiationError
	if errors.As(err, &neg) && neg.What == "host key" {
		return HostKey{}, neg.RequestedAlgorithms, errNotOffered
	}
	if ctx.Err() != nil {
		return HostKey{}, nil, ctx.Err()
	}
	return HostKey{}, nil, fmt.Errorf("SSH-рукопожатие с %s: %w", addr, err)
}
