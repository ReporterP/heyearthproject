package main

// The node-agent CA lives on the Hub (Beads cudk.77).
//
// It used to be generated on the operator's machine (ensureLocalMTLSAssets) and uploaded; its
// private key ended up in the public git repository. Now the CA key, the Hub's client key and
// every signature with them stay on the Hub: keys are generated there with openssl, the
// certificates are built here in Go and signed there (provision.HubCASigner); only certificates
// and public keys travel.
//
// Replacing the old CA without losing the exits that still trust it:
//
//   - the old ca-cert.pem and the old client pair are kept next to the new files as
//     legacy-ca-cert.pem / legacy-client-{cert,key}.pem (provision.Legacy*); heyearth-api's mTLS
//     client (provision.NodeAgentClientTLS) presents both client certificates — TLS 1.3 picks
//     the one whose issuer the agent names — and trusts both CAs, so an unconverged exit (shared
//     old server certificate, trusts only the old CA) stays reachable;
//   - converging an exit (admin «Переустановить» or deploy --setup-exit-node) installs the new CA
//     as its only trust and its own certificate from the new CA: from then on it rejects anything
//     signed with the leaked key;
//   - once every exit is converged, the owner deletes the legacy files (printed below); the API
//     reads them on every connection, no restart either way.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
	"github.com/heyearth/backend/internal/shellquote"
)

const (
	// hubCertsDir: the API's mTLS files (api.env TLS_*), the CA the exit recipe installs and signs with.
	hubCertsDir = appDir + "/certs"
	// hubCAMarker is the OU of a CA made on the Hub: a CA without it (the old one) is replaced by
	// --ensure-hub-provisioning. It also makes the subject differ from the old CA's, which the
	// Hub's TLS client relies on to pick the right certificate per agent.
	hubCAMarker = "generated on the Hub"
	// hubCARenewBefore: the Hub's client certificate (and a CA) closer than this to its end is renewed.
	hubCARenewBefore = 30 * 24 * time.Hour
)

type caMode int

const (
	caKeep   caMode = iota // --main / --update-main: create a CA only on a Hub without any, never replace one
	caRotate               // --ensure-hub-provisioning: also replace a CA not made on the Hub, keeping it as legacy
)

// hubCAFiles is what the Hub has now: certificates (nil when absent) and the public halves of the
// keys (PEM, "" when absent) — never a private key.
type hubCAFiles struct {
	caCert, clientCert, legacyCA, legacyClientCert []byte
	caKeyPub, clientKeyPub, legacyClientKeyPub     string
}

// hubCAPlan is what ensureHubNodeCA does (comparable: tests compare whole plans).
type hubCAPlan struct {
	keepLegacyCA, keepLegacyClient bool // copy the current CA / client pair to legacy-* first
	newCA, newClient               bool
	notOurs                        bool   // the CA is not the Hub's own and was left in place (caKeep)
	note                           string // a warning for the operator
}

func parseCertPEM(p []byte) (*x509.Certificate, error) {
	rest := p
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			return nil, errors.New("нет PEM-блока CERTIFICATE")
		}
		if b.Type == "CERTIFICATE" {
			return x509.ParseCertificate(b.Bytes)
		}
	}
}

// keyIsPair: pubPEM ("PUBLIC KEY" PEM of a key file) is the public key of c.
func keyIsPair(c *x509.Certificate, pubPEM string) bool {
	b, _ := pem.Decode([]byte(pubPEM))
	if b == nil || b.Type != "PUBLIC KEY" {
		return false
	}
	pub, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return false
	}
	eq, ok := c.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if ok {
		return eq.Equal(pub)
	}
	return false
}

func isHubCA(c *x509.Certificate) bool {
	return c != nil && c.IsCA && slices.Contains(c.Subject.OrganizationalUnit, hubCAMarker)
}

// hubCAUsable returns the Hub's own CA when ca-cert.pem is one, ca-key.pem is its pair and it has
// more than hubCARenewBefore left (node certificates are capped at the CA's end).
func hubCAUsable(f hubCAFiles, now time.Time) (*x509.Certificate, error) {
	if len(f.caCert) == 0 {
		return nil, errors.New("ca-cert.pem нет")
	}
	c, err := parseCertPEM(f.caCert)
	switch {
	case err != nil:
		return nil, fmt.Errorf("ca-cert.pem не читается: %w", err)
	case !isHubCA(c):
		return nil, fmt.Errorf("ca-cert.pem — прежний CA «%s», не созданный на Hub", c.Subject.CommonName)
	case !keyIsPair(c, f.caKeyPub):
		return nil, errors.New("ca-key.pem нет или он не пара ca-cert.pem")
	case now.Before(c.NotBefore) || c.NotAfter.Sub(now) < hubCARenewBefore:
		return nil, fmt.Errorf("CA действует %s — %s", c.NotBefore.UTC().Format("2006-01-02"), c.NotAfter.UTC().Format("2006-01-02"))
	}
	return c, nil
}

// clientUsable: certPEM is a client certificate from ca, keyPub its key's public half, valid now
// and for more than hubCARenewBefore.
func clientUsable(certPEM []byte, keyPub string, ca *x509.Certificate, now time.Time) error {
	if len(certPEM) == 0 {
		return errors.New("сертификата нет")
	}
	c, err := parseCertPEM(certPEM)
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := c.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return err
	}
	if !keyIsPair(c, keyPub) {
		return errors.New("ключ — не пара сертификата")
	}
	if c.NotAfter.Sub(now) < hubCARenewBefore {
		return fmt.Errorf("истекает %s", c.NotAfter.UTC().Format("2006-01-02"))
	}
	return nil
}

// planHubCA decides from what the Hub has (pure: tested without a Hub).
func planHubCA(f hubCAFiles, now time.Time, mode caMode, forceClient bool) (hubCAPlan, error) {
	var p hubCAPlan
	if ca, err := hubCAUsable(f, now); err == nil {
		p.newClient = forceClient || clientUsable(f.clientCert, f.clientKeyPub, ca, now) != nil
		return p, nil
	} else if len(f.caCert) > 0 && mode == caKeep {
		p.notOurs = true
		p.note = "CA node-agent на Hub не заменён (" + err.Error() + "): это делает go run ./cmd/deploy --ensure-hub-provisioning"
		return p, nil
	}
	p.newCA, p.newClient = true, true
	if len(f.caCert) == 0 { // a Hub without a CA: nothing to keep
		return p, nil
	}
	switch {
	case len(f.legacyCA) == 0:
		p.keepLegacyCA = true
	case !bytes.Equal(f.legacyCA, f.caCert):
		return hubCAPlan{}, fmt.Errorf("на Hub уже лежит %s от прежней замены CA, а текущий ca-cert.pem не годится — "+
			"сначала доведите прежнюю замену: сведите все exit (карточка ноды → «Переустановить»), удалите legacy-* и повторите",
			provision.LegacyCACertFile)
	}
	if len(f.legacyClientCert) > 0 || f.legacyClientKeyPub != "" {
		return p, nil
	}
	old, err := parseCertPEM(f.caCert)
	if err == nil && clientUsableUntilEnd(f.clientCert, f.clientKeyPub, old, now) {
		p.keepLegacyClient = true
	} else {
		p.note = "клиентский сертификат Hub не от прежнего CA (или не пара ключа) — прежняя пара не сохраняется: " +
			"exit с прежним CA будут недоступны агенту до сведения"
	}
	return p, nil
}

// clientUsableUntilEnd: like clientUsable, but an old client pair is worth keeping until it expires.
func clientUsableUntilEnd(certPEM []byte, keyPub string, ca *x509.Certificate, now time.Time) bool {
	err := clientUsable(certPEM, keyPub, ca, now)
	return err == nil || strings.HasPrefix(err.Error(), "истекает")
}

// readHubCAFiles reads the certificates and the public halves of the keys in dir on the Hub.
func readHubCAFiles(ctx context.Context, hub provision.Remote, dir string) (hubCAFiles, error) {
	var f hubCAFiles
	cat := func(name string) ([]byte, error) {
		out, err := hub.Run(ctx, config.CatScript(dir+"/"+name))
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", dir, name, err)
		}
		if strings.TrimSpace(out) == "" {
			return nil, nil
		}
		return []byte(out), nil
	}
	pub := func(name string) (string, error) {
		out, err := hub.Run(ctx, config.KeyPublicScript(dir+"/"+name))
		if err != nil {
			return "", fmt.Errorf("%s/%s: %w", dir, name, err)
		}
		if strings.TrimSpace(out) == "NO_OPENSSL" {
			return "", errors.New("на Hub нет openssl (apt-get install openssl)")
		}
		return out, nil
	}
	var err error
	for _, x := range []struct {
		dst  *[]byte
		name string
	}{{&f.caCert, "ca-cert.pem"}, {&f.clientCert, "client-cert.pem"}, {&f.legacyCA, provision.LegacyCACertFile}, {&f.legacyClientCert, provision.LegacyClientCertFile}} {
		if *x.dst, err = cat(x.name); err != nil {
			return f, err
		}
	}
	for _, x := range []struct {
		dst  *string
		name string
	}{{&f.caKeyPub, "ca-key.pem"}, {&f.clientKeyPub, "client-key.pem"}, {&f.legacyClientKeyPub, provision.LegacyClientKeyFile}} {
		if *x.dst, err = pub(x.name); err != nil {
			return f, err
		}
	}
	return f, nil
}

// hubCATemplate: ten years, no intermediates, a subject of its own (hubCAMarker + the time).
func hubCATemplate(now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{CommonName: "HiEarth node-agent CA " + now.UTC().Format("2006-01-02 15:04"),
			Organization: []string{"HiEarth"}, OrganizationalUnit: []string{hubCAMarker}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}, nil
}

// hubClientTemplate: heyearth-api's client certificate (CN main-api, clientAuth), one year,
// never past the CA; backdated an hour like node certificates.
func hubClientTemplate(ca *x509.Certificate, now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	notAfter := now.AddDate(1, 0, 0)
	if notAfter.After(ca.NotAfter) {
		notAfter = ca.NotAfter
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "main-api", Organization: []string{"HiEarth"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"main-api"},
	}, nil
}

// genKeyScript makes a fresh EC P-256 key at path on the Hub (0600, temp + mv); prints nothing.
func genKeyScript(path string) string {
	q := shellquote.Quote(path)
	return "# hp:ca-genkey " + path + "\n" + `set -euo pipefail
command -v openssl >/dev/null 2>&1 || { echo "[ca] ERROR: на Hub нет openssl" >&2; exit 1; }
umask 077
mkdir -p "$(dirname ` + q + `)"
rm -f ` + q + `.tmp
openssl ecparam -name prime256v1 -genkey -noout -out ` + q + `.tmp
chmod 600 ` + q + `.tmp
mv -f ` + q + `.tmp ` + q + `
`
}

// keepLegacyScript copies the current CA certificate and/or client pair to legacy-* — never over
// an existing legacy file (the plan was made on their absence).
func keepLegacyScript(dir string, ca, client bool) string {
	d := shellquote.Quote(dir)
	s := "# hp:ca-legacy " + dir + "\nset -euo pipefail\numask 077\ncd " + d + "\n"
	if ca {
		s += `[ ! -e ` + provision.LegacyCACertFile + ` ] || { echo "[ca] ERROR: ` + provision.LegacyCACertFile + ` уже есть" >&2; exit 1; }
cp ca-cert.pem ` + provision.LegacyCACertFile + `.tmp && chmod 644 ` + provision.LegacyCACertFile + `.tmp
mv -f ` + provision.LegacyCACertFile + `.tmp ` + provision.LegacyCACertFile + `
`
	}
	if client {
		s += `[ ! -e ` + provision.LegacyClientCertFile + ` ] && [ ! -e ` + provision.LegacyClientKeyFile + ` ] || { echo "[ca] ERROR: прежняя клиентская пара уже сохранена" >&2; exit 1; }
cp client-key.pem ` + provision.LegacyClientKeyFile + `.tmp && chmod 600 ` + provision.LegacyClientKeyFile + `.tmp
cp client-cert.pem ` + provision.LegacyClientCertFile + `.tmp && chmod 644 ` + provision.LegacyClientCertFile + `.tmp
mv -f ` + provision.LegacyClientKeyFile + `.tmp ` + provision.LegacyClientKeyFile + ` && mv -f ` + provision.LegacyClientCertFile + `.tmp ` + provision.LegacyClientCertFile + `
`
	}
	return s
}

// installPairScript moves a new key and its certificate (written as <name>.new) into place in one
// command: the window in which the pair does not match is two renames. A .bak PutFile left of a
// .new from an interrupted run goes too.
func installPairScript(dir, key, cert string) string {
	d := shellquote.Quote(dir)
	return "# hp:ca-install " + dir + "/" + cert + "\nset -euo pipefail\ncd " + d + "\n" +
		"mv -f " + key + ".new " + key + " && mv -f " + cert + ".new " + cert + "\nrm -f " + cert + ".new.bak\n"
}

// hubClock is the Hub's time: certificates are judged by the Hub's API and by the nodes, not by
// this machine, so the offset of the Hub's clock is applied to this machine's (second resolution;
// certificates are backdated an hour anyway). This machine's clock when the Hub does not answer.
func hubClock(ctx context.Context, hub provision.Remote) func() time.Time {
	t0 := time.Now()
	out, err := hub.Run(ctx, "date -u +%s")
	if err != nil {
		return time.Now
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return time.Now
	}
	offset := time.Unix(sec, 0).Sub(t0)
	return func() time.Time { return time.Now().Add(offset) }
}

// signOnHub generates a key at keyPath (on the Hub), builds tmpl for its public half and signs it
// with parent's key, which is at parentKeyPath on the Hub (the new key itself for a self-signed CA).
func signOnHub(ctx context.Context, hub provision.Remote, keyPath string, tmpl, parent *x509.Certificate, parentKeyPath string) ([]byte, error) {
	if out, err := hub.Run(ctx, genKeyScript(keyPath)); err != nil {
		return nil, fmt.Errorf("ключ %s на Hub: %w %s", keyPath, err, out)
	}
	out, err := hub.Run(ctx, config.KeyPublicScript(keyPath))
	if err != nil {
		return nil, err
	}
	b, _ := pem.Decode([]byte(out))
	if b == nil {
		return nil, fmt.Errorf("публичный ключ %s не прочитан", keyPath)
	}
	pub, err := x509.ParsePKIXPublicKey(b.Bytes)
	if err != nil {
		return nil, err
	}
	if parent == nil { // self-signed: the parent is the certificate itself, its key the new one
		parent, parentKeyPath = tmpl, keyPath
	}
	parentPub := pub
	if parent != tmpl {
		parentPub = parent.PublicKey
	}
	signer, err := provision.HubCASigner(ctx, hub, parentKeyPath, parentPub)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// ensureHubNodeCA converges the node-agent CA and heyearth-api's client pair in dir on the Hub
// (see the file comment). Idempotent; an interrupted run is finished by the next one. It
// restarts nothing: the API reads these files on every connection to an agent.
func ensureHubNodeCA(ctx context.Context, hub provision.Remote, dir string, now time.Time, mode caMode, forceClient bool) (hubCAPlan, error) {
	f, err := readHubCAFiles(ctx, hub, dir)
	if err != nil {
		return hubCAPlan{}, err
	}
	origCA := f.caCert
	p, err := planHubCA(f, now, mode, forceClient)
	if err != nil {
		return p, err
	}
	if p.keepLegacyCA || p.keepLegacyClient {
		if out, err := hub.Run(ctx, keepLegacyScript(dir, p.keepLegacyCA, p.keepLegacyClient)); err != nil {
			return p, fmt.Errorf("прежний CA не сохранён: %w %s", err, out)
		}
	}
	if p.newCA {
		tmpl, err := hubCATemplate(now)
		if err != nil {
			return p, err
		}
		cert, err := signOnHub(ctx, hub, dir+"/ca-key.pem.new", tmpl, nil, "")
		if err != nil {
			return p, fmt.Errorf("новый CA: %w", err)
		}
		if _, err := hub.PutFile(ctx, dir+"/ca-cert.pem.new", cert, 0o644); err != nil {
			return p, err
		}
		if out, err := hub.Run(ctx, installPairScript(dir, "ca-key.pem", "ca-cert.pem")); err != nil {
			return p, fmt.Errorf("новый CA не установлен: %w %s", err, out)
		}
		f.caCert = cert
	}
	if p.newClient {
		ca, err := parseCertPEM(f.caCert)
		if err != nil {
			return p, err
		}
		tmpl, err := hubClientTemplate(ca, now)
		if err != nil {
			return p, err
		}
		cert, err := signOnHub(ctx, hub, dir+"/client-key.pem.new", tmpl, ca, dir+"/ca-key.pem")
		if err != nil {
			return p, fmt.Errorf("клиентский сертификат Hub: %w", err)
		}
		if _, err := hub.PutFile(ctx, dir+"/client-cert.pem.new", cert, 0o644); err != nil {
			return p, err
		}
		if out, err := hub.Run(ctx, installPairScript(dir, "client-key.pem", "client-cert.pem")); err != nil {
			return p, fmt.Errorf("клиентский сертификат Hub не установлен: %w %s", err, out)
		}
	}
	if !p.newCA && !p.newClient && !p.keepLegacyCA && !p.keepLegacyClient {
		return p, nil
	}
	// What the next run would do must be nothing (and the legacy copies must be the old files).
	after, err := readHubCAFiles(ctx, hub, dir)
	if err != nil {
		return p, err
	}
	if again, err := planHubCA(after, now, caKeep, false); err != nil || again.newCA || again.newClient || again.notOurs {
		return p, fmt.Errorf("после записи CA на Hub не сошёлся: %+v %v", again, err)
	}
	if p.keepLegacyCA && !bytes.Equal(after.legacyCA, origCA) {
		return p, errors.New("legacy-ca-cert.pem не совпадает с прежним CA")
	}
	return p, nil
}

// agentProbeScript is the deploy CLI's Hooks.ProbeAgent, run on the Hub: the Hub's own mTLS call
// to node-agent at ip:port with openssl s_client — the API's client pair, then the legacy one
// (heyearth-api presents both, TLS picks), against both CA generations, the agent's certificate
// verified (node-agent, -verify_return_error). A TLS 1.3 client is done before the agent checked
// its certificate, so s_client reads for two seconds: an agent that refuses the client answers
// with an alert, which fails the try. Prints PROBE=ok | ok-legacy | fail | no-openssl (fail with
// up to three lines of why, printable characters only).
func agentProbeScript(dir, ip string, port int) string {
	d := shellquote.Quote(dir)
	return "# hp:agent-probe " + ip + "\n" + `set -uo pipefail
command -v openssl >/dev/null 2>&1 || { echo PROBE=no-openssl; exit 0; }
d=` + d + `
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
cat "$d/ca-cert.pem" > "$t/ca.pem" 2>/dev/null
if [ -f "$d/` + provision.LegacyCACertFile + `" ]; then cat "$d/` + provision.LegacyCACertFile + `" >> "$t/ca.pem"; fi
try() {
  sleep 2 | timeout 8 openssl s_client -connect ` + shellquote.Quote(net.JoinHostPort(ip, strconv.Itoa(port))) + ` -servername node-agent -alpn h2 \
    -cert "$1" -key "$2" -CAfile "$t/ca.pem" -verify_return_error -verify_hostname node-agent > "$t/out" 2>&1
  rc=$?
  if [ "$rc" -eq 0 ] && grep -aq 'Verify return code: 0 (ok)' "$t/out" && ! grep -aqi 'alert' "$t/out"; then return 0; fi
  grep -aiE 'alert|verify error|errno|refused|timed out|error' "$t/out" | tr -cd '[:print:]\n' | head -3
  [ "$rc" -eq 124 ] && echo "нет ответа за 8 с"
  return 1
}
if try "$d/client-cert.pem" "$d/client-key.pem"; then echo PROBE=ok; exit 0; fi
if [ -f "$d/` + provision.LegacyClientCertFile + `" ] && try "$d/` + provision.LegacyClientCertFile + `" "$d/` + provision.LegacyClientKeyFile + `"; then
  echo PROBE=ok-legacy; exit 0
fi
echo PROBE=fail
`
}

// cliProbeAgent: Hooks.ProbeAgent of --setup-exit-node — agentProbeScript on the Hub (the
// certificates and keys there never leave it). nil only for PROBE=ok / ok-legacy.
func cliProbeAgent(hub provision.Remote, dir string) func(ctx context.Context, ip string) error {
	return func(ctx context.Context, ip string) error {
		out, err := hub.Run(ctx, agentProbeScript(dir, ip, config.NodeAgentPort))
		if err != nil {
			return err
		}
		var why []string
		for _, line := range strings.Split(out, "\n") {
			switch line = strings.TrimSpace(line); line {
			case "PROBE=ok", "PROBE=ok-legacy":
				return nil
			case "PROBE=no-openssl":
				return errors.New("на Hub нет openssl — проверить агент по mTLS нечем")
			case "PROBE=fail", "":
			default:
				why = append(why, line)
			}
		}
		if len(why) == 0 {
			why = []string{"нет ответа"}
		}
		return fmt.Errorf("mTLS Hub → агент %s не прошло: %s", ip, strings.Join(why, "; "))
	}
}
