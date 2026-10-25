package provision

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/shellquote"
)

// IssueNodeCertWith signs with any crypto.Signer (the deploy CLI's is on the Hub) exactly the
// certificate IssueNodeCert does.
func TestIssueNodeCertWithSigner(t *testing.T) {
	ca, caKeyPEM := testCA(t)
	signer, err := parseKey(caKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := IssueNodeCertWith(ca, signer, "203.0.113.7", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckNodeCert(cert, ca, "203.0.113.7", testNow); err != nil {
		t.Fatalf("cert signed through a Signer: %v", err)
	}
	if err := keyPairErr(cert, publicPEM(t, key)); err != nil {
		t.Fatalf("returned key is not the cert's: %v", err)
	}
	if _, _, err := IssueNodeCertWith(ca, nil, "203.0.113.7", testNow); err == nil {
		t.Error("no signer must be refused")
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, _, err := IssueNodeCertWith(ca, other, "203.0.113.7", testNow); err == nil {
		t.Error("a signer that is not the CA's key must be refused")
	}
}

func publicPEM(t *testing.T, keyPEM []byte) string {
	t.Helper()
	k, err := parseKey(keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(k.Public())
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func haveOpenSSL() bool {
	if !haveBash() {
		return false
	}
	return exec.Command("bash", "-c", "command -v openssl >/dev/null").Run() == nil
}

// The CA key never leaves the Hub: HubCASigner checks the key file is the CA certificate's pair
// and signs with `openssl pkeyutl` there (here: the local bash + openssl, EC and RSA keys); the
// result is a certificate Go verifies.
func TestHubCASignerOpenSSL(t *testing.T) {
	if !haveOpenSSL() {
		t.Skip("no bash/openssl")
	}
	ctx := context.Background()
	dir := t.TempDir()
	for name, gen := range map[string]string{
		"ec":  "openssl ecparam -name prime256v1 -genkey -noout -out ",
		"rsa": "openssl genrsa -out ",
	} {
		keyPath := localShellPath(filepath.Join(dir, name+"-key.pem"))
		if out, err := (LocalRemote{}).Run(ctx, gen+shellquote.Quote(keyPath)+" 2>&1"); err != nil {
			t.Fatalf("%s keygen: %v %s", name, err, out)
		}
		out, err := (LocalRemote{}).Run(ctx, config.KeyPublicScript(keyPath))
		if err != nil {
			t.Fatal(err)
		}
		pub, err := parsePublicPEM(out)
		if err != nil {
			t.Fatalf("%s pub: %v %q", name, err, out)
		}
		signer, err := HubCASigner(ctx, LocalRemote{}, keyPath, pub)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// a self-signed CA over the remote key, then a node certificate from it
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "test CA " + name},
			NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true,
			KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, signer)
		if err != nil {
			t.Fatalf("%s: self-signed CA over the Hub key: %v", name, err)
		}
		ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		cert, _, err := IssueNodeCertWith(ca, signer, "203.0.113.7", testNow)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := CheckNodeCert(cert, ca, "203.0.113.7", testNow); err != nil {
			t.Fatalf("%s: node cert signed on the Hub: %v", name, err)
		}
		// another public key: the file is not the CA's pair — refused before any signature
		other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if _, err := HubCASigner(ctx, LocalRemote{}, keyPath, other.Public()); err == nil || !strings.Contains(err.Error(), "не пара") {
			t.Errorf("%s: a key that is not the CA's pair: %v", name, err)
		}
	}
	if _, err := HubCASigner(ctx, LocalRemote{}, localShellPath(filepath.Join(dir, "missing.pem")), nil); err == nil {
		t.Error("a missing key file must be refused")
	}
}

// The signing script carries the digest only (octal escapes, no key material), and refuses a
// digest of the wrong length before anything runs.
func TestHubSignerScript(t *testing.T) {
	s := caSignScript("/opt/heyearth/mainserver/certs/ca-key.pem", "sha256", []byte{0, 1, 0o377, '\'', '%'})
	if !strings.HasPrefix(s, "# hp:ca-sign /opt/heyearth/mainserver/certs/ca-key.pem\n") || !strings.Contains(s, `printf '\000\001\377\047\045'`) ||
		!strings.Contains(s, "-pkeyopt digest:sha256") || !strings.Contains(s, "set -euo pipefail") {
		t.Fatalf("script:\n%s", s)
	}
	var h hubSigner
	if _, err := h.Sign(nil, make([]byte, 31), crypto.SHA256); err == nil {
		t.Error("a 31-byte SHA-256 digest must be refused")
	}
	if _, err := h.Sign(nil, make([]byte, 20), crypto.SHA1); err == nil {
		t.Error("SHA-1 must be refused")
	}
}

// ── node-agent client TLS during the CA change ──

type tlsCA struct {
	cert *x509.Certificate
	pem  []byte
	key  crypto.Signer
}

func newTLSCA(t *testing.T, subject pkix.Name) tlsCA {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: subject, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, k.Public(), k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return tlsCA{c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), k}
}

// leaf issues a client (EKU clientAuth) or a node-agent server certificate.
func (ca tlsCA) leaf(t *testing.T, client bool) (certPEM, keyPEM []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "main-api"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if !client {
		tmpl.Subject.CommonName, tmpl.ExtKeyUsage, tmpl.DNSNames = "node-agent", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []string{"node-agent", "localhost"}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, k.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

// agentServer is node-agent's TLS (cmd/node-agent loadTLSCredentials): its certificate, client
// certificates required and verified against the CA bundle it trusts.
func agentServer(t *testing.T, certPEM, keyPEM []byte, trusts ...tlsCA) *tls.Config {
	t.Helper()
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	for _, ca := range trusts {
		pool.AddCert(ca.cert)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS13}
}

// handshake runs one TLS 1.3 handshake and returns the issuer CN of the client certificate the
// server accepted.
func handshake(t *testing.T, client, server *tls.Config) (string, error) {
	t.Helper()
	c1, c2 := net.Pipe()
	deadline := time.Now().Add(10 * time.Second) // a stuck handshake fails the test instead of hanging it
	_ = c1.SetDeadline(deadline)
	_ = c2.SetDeadline(deadline)
	defer c1.Close()
	defer c2.Close()
	srv := tls.Server(c2, server)
	errc := make(chan error, 1)
	go func() { errc <- srv.Handshake() }()
	cli := tls.Client(c1, client)
	cerr := cli.Handshake()
	if cerr != nil {
		c1.Close()
	} else {
		// A TLS 1.3 client is done before the server checked its certificate: keep reading, so
		// the server's refusal (an alert on the unbuffered pipe) is delivered, not blocked.
		go func() { _, _ = io.Copy(io.Discard, cli) }()
	}
	serr := <-errc
	if cerr != nil {
		return "", cerr
	}
	if serr != nil {
		return "", serr
	}
	peers := srv.ConnectionState().PeerCertificates
	if len(peers) == 0 {
		return "", nil
	}
	return peers[0].Issuer.CommonName, nil
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Beads cudk.77: while exits still run node-agent with the old (leaked) CA, the Hub keeps the old
// client pair and trusts the old CA beside its own: an agent that trusts only the old CA gets the
// old client certificate, a converged agent (trusts only the new CA) the new one — chosen by the
// CA names the agent sends — and the Hub verifies both generations of agent certificates. Without
// the legacy files the old agent is unreachable; without the new pair the converged one.
func TestNodeAgentClientTLSTransition(t *testing.T) {
	oldCA := newTLSCA(t, pkix.Name{CommonName: "HeYearth Node-Agent CA", Organization: []string{"HeYearth"}})
	newCA := newTLSCA(t, pkix.Name{CommonName: "HiEarth node-agent CA 2026-10-25 12:00", Organization: []string{"HiEarth"},
		OrganizationalUnit: []string{"generated on the Hub"}})
	dir := t.TempDir()
	cert, key, ca := filepath.Join(dir, "client-cert.pem"), filepath.Join(dir, "client-key.pem"), filepath.Join(dir, "ca-cert.pem")
	newCli, newCliKey := newCA.leaf(t, true)
	oldCli, oldCliKey := oldCA.leaf(t, true)
	writeFile(t, cert, newCli)
	writeFile(t, key, newCliKey)
	writeFile(t, ca, newCA.pem)

	oldSrv, oldSrvKey := oldCA.leaf(t, false) // exit 2 before its converge: shared old server cert, trusts only the old CA
	newSrv, newSrvKey := newCA.leaf(t, false) // a converged exit: own cert from the new CA, trusts only the new CA
	exit2 := agentServer(t, oldSrv, oldSrvKey, oldCA)
	converged := agentServer(t, newSrv, newSrvKey, newCA)

	cfg, err := NodeAgentClientTLS(cert, key, ca)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handshake(t, cfg, exit2); err == nil {
		t.Fatal("without the legacy files an agent of the old CA must not be reachable (the test would prove nothing)")
	}
	if got, err := handshake(t, cfg, converged); err != nil || got != newCA.cert.Subject.CommonName {
		t.Fatalf("converged agent without legacy files: %q %v", got, err)
	}

	writeFile(t, filepath.Join(dir, LegacyCACertFile), oldCA.pem)
	writeFile(t, filepath.Join(dir, LegacyClientCertFile), oldCli)
	writeFile(t, filepath.Join(dir, LegacyClientKeyFile), oldCliKey)
	cfg, err = NodeAgentClientTLS(cert, key, ca)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := handshake(t, cfg, exit2); err != nil || got != oldCA.cert.Subject.CommonName {
		t.Fatalf("exit 2 before its converge: issuer %q, %v (want the old client certificate)", got, err)
	}
	if got, err := handshake(t, cfg, converged); err != nil || got != newCA.cert.Subject.CommonName {
		t.Fatalf("converged agent: issuer %q, %v (want the new client certificate)", got, err)
	}

	// The new client pair unreadable (cut between key and certificate): the legacy pair still
	// serves the old agents instead of the Hub losing every agent.
	writeFile(t, key, oldCliKey)
	cfg, err = NodeAgentClientTLS(cert, key, ca)
	if err != nil {
		t.Fatalf("one usable pair is enough: %v", err)
	}
	if _, err := handshake(t, cfg, exit2); err != nil {
		t.Fatalf("legacy pair alone: %v", err)
	}
	os.Remove(filepath.Join(dir, LegacyClientCertFile))
	if _, err := NodeAgentClientTLS(cert, key, ca); err == nil {
		t.Fatal("no usable client pair at all must be an error")
	}
	// Broken legacy files (half removed by hand, garbage) cost only the old agents: the converged
	// ones keep their channel.
	writeFile(t, key, newCliKey)
	writeFile(t, filepath.Join(dir, LegacyCACertFile), []byte("garbage"))
	cfg, err = NodeAgentClientTLS(cert, key, ca)
	if err != nil {
		t.Fatalf("broken legacy files must not cut off converged agents: %v", err)
	}
	if _, err := handshake(t, cfg, converged); err != nil {
		t.Fatalf("converged agent with broken legacy files: %v", err)
	}
	if _, err := handshake(t, cfg, exit2); err == nil {
		t.Fatal("an agent of the old CA cannot be reachable without a usable legacy pair and CA")
	}
}

// The exit recipe in the deploy CLI: CA() hands over no key and CASigner signs on the Hub. The
// node gets its own certificate from the Hub CA; a rerun on a good certificate signs nothing.
func TestExitAgentCertSignedOnHub(t *testing.T) {
	e := exitFixture(t, false)
	caKey, err := parseKey(e.caKey)
	if err != nil {
		t.Fatal(err)
	}
	signs := 0
	e.opts.CA = func() ([]byte, []byte, error) { return e.ca, nil, nil }
	e.opts.CASigner = func(_ context.Context, ca *x509.Certificate) (crypto.Signer, error) {
		signs++
		if !ca.IsCA {
			t.Errorf("CASigner got a non-CA certificate")
		}
		return caKey, nil
	}
	id := e.start(t, KindProvision)
	if st := e.store.jobs[id].Status; st != JobSucceeded {
		t.Fatalf("job %s: %s\n%v", st, e.store.jobs[id].Error, e.steps(id))
	}
	if err := CheckNodeCert(e.node.files[config.NodeAgentCert], e.ca, "203.0.113.7", testNow); err != nil {
		t.Fatalf("node certificate: %v", err)
	}
	if signs != 1 {
		t.Fatalf("signed %d times, want once", signs)
	}
	e.h.lifecycle = "online"
	id = e.start(t, KindReprovision)
	if st := e.store.jobs[id].Status; st != JobSucceeded || signs != 1 {
		t.Fatalf("rerun: %s, signed %d times", st, signs)
	}
}
