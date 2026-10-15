package provision

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

var testNow = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

// testCA mimics deploy's CA: RSA, PKCS#1 key, ten years.
func testCA(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	return testCAValid(t, testNow.Add(-time.Hour), testNow.AddDate(10, 0, 0))
}

func testCAValid(t *testing.T, notBefore, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "HeYearth Node-Agent CA"},
		NotBefore: notBefore, NotAfter: notAfter, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

func TestIssueAndCheckNodeCert(t *testing.T) {
	ca, caKey := testCA(t)
	cert, key, err := IssueNodeCert(ca, caKey, "203.0.113.7", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckNodeCert(cert, ca, "203.0.113.7", testNow); err != nil {
		t.Fatalf("fresh cert: %v", err)
	}
	if err := CheckNodeCert(cert, ca, "203.0.113.8", testNow); err == nil {
		t.Error("a cert for another node must be refused")
	}
	if err := CheckNodeCert(cert, ca, "203.0.113.7", testNow.AddDate(0, 11, 10)); err == nil || !strings.Contains(err.Error(), "истекает") {
		t.Errorf("must be renewed 30 days before expiry: %v", err)
	}
	otherCA, _ := testCA(t)
	if err := CheckNodeCert(cert, otherCA, "203.0.113.7", testNow); err == nil {
		t.Error("a cert of another CA must be refused")
	}
	if err := CheckNodeCert([]byte(""), ca, "203.0.113.7", testNow); err == nil {
		t.Error("no cert on the node must be reported")
	}
	// heyearth-api's mTLS client (ServerName "node-agent", the CA as root) accepts it.
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca)
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "node-agent", MinVersion: tls.VersionTLS13,
		Time: func() time.Time { return testNow }})
	if err != nil {
		t.Fatalf("api-side handshake: %v", err)
	}
	conn.Close()
}

// The node's own key leaves IssueNodeCert as a fresh ECDSA P-256 key matching the certificate,
// never the CA key; the certificate is a one-year server leaf, not a CA.
func TestIssueNodeCertShape(t *testing.T) {
	ca, caKey := testCA(t)
	certPEM, keyPEM, err := IssueNodeCert(ca, caKey, " 203.0.113.7\n", testNow)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := parseCert(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if cert.IsCA || !cert.NotAfter.Equal(testNow.AddDate(1, 0, 0)) || cert.NotBefore.After(testNow) {
		t.Errorf("leaf: IsCA=%v %s…%s", cert.IsCA, cert.NotBefore, cert.NotAfter)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ext key usage %v", cert.ExtKeyUsage)
	}
	if strings.Join(cert.DNSNames, ",") != "node-agent,localhost" || len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "203.0.113.7" {
		t.Errorf("SAN %v %v", cert.DNSNames, cert.IPAddresses)
	}
	b, _ := pem.Decode(keyPEM)
	if b == nil || b.Type != "EC PRIVATE KEY" {
		t.Fatalf("key PEM %v", b)
	}
	k, err := x509.ParseECPrivateKey(b.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if k.Curve != elliptic.P256() || !k.PublicKey.Equal(cert.PublicKey) {
		t.Error("the key is not the certificate's P-256 key")
	}
	if strings.Contains(string(keyPEM), strings.TrimSpace(string(caKey))) {
		t.Error("the CA key leaked into the node key")
	}
	// Two nodes never share a key or a serial.
	cert2PEM, key2PEM, err := IssueNodeCert(ca, caKey, "203.0.113.7", testNow)
	if err != nil {
		t.Fatal(err)
	}
	cert2, _ := parseCert(cert2PEM)
	if string(key2PEM) == string(keyPEM) || cert2.SerialNumber.Cmp(cert.SerialNumber) == 0 {
		t.Error("key or serial reused")
	}
	// Surrounding whitespace in the node IP is tolerated by the check as well.
	if err := CheckNodeCert(certPEM, ca, "203.0.113.7 ", testNow); err != nil {
		t.Errorf("trimmed IP: %v", err)
	}
}

func TestIssueNodeCertRefuses(t *testing.T) {
	ca, caKey := testCA(t)
	otherCA, otherKey := testCA(t)
	leaf, leafKey, err := IssueNodeCert(ca, caKey, "203.0.113.7", testNow)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		ca, key []byte
		ip      string
	}{
		"bad ip":            {ca, caKey, "node.example"},
		"empty ip":          {ca, caKey, ""},
		"no CA":             {nil, caKey, "203.0.113.7"},
		"no CA key":         {ca, nil, "203.0.113.7"},
		"garbage CA key":    {ca, []byte("-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n"), "203.0.113.7"},
		"key of another CA": {ca, otherKey, "203.0.113.7"},
		"CA of another key": {otherCA, caKey, "203.0.113.7"},
		"leaf as CA":        {leaf, leafKey, "203.0.113.7"},
	} {
		if cert, key, err := IssueNodeCert(c.ca, c.key, c.ip, testNow); err == nil || cert != nil || key != nil {
			t.Errorf("%s: issued (%v)", name, err)
		}
	}
	if err := CheckNodeCert(leaf, ca, "", testNow); err == nil {
		t.Error("an empty node IP must not pass")
	}
	if err := CheckNodeCert(leaf, ca, "203.0.113.7", testNow.AddDate(1, 0, 1)); err == nil {
		t.Error("an expired cert must not pass")
	}
	if err := CheckNodeCert(ca, ca, "203.0.113.7", testNow); err == nil {
		t.Error("the CA itself is not a node cert")
	}
}

// A CA whose key is EC PKCS#8 (not what deploy writes, but a valid Hub CA) signs as well.
func TestIssueNodeCertPKCS8CA(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "EC CA"},
		NotBefore: testNow.Add(-time.Hour), NotAfter: testNow.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	p8, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, _, err := IssueNodeCert(ca, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: p8}), "2001:db8::7", testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckNodeCert(cert, ca, "2001:db8::7", testNow); err != nil {
		t.Fatal(err)
	}
}

// The Hub CA's own validity: a leaf never outlives the CA, an ageing or not-yet-valid CA is
// refused with its own message, and the check names the CA instead of "not from the Hub CA".
func TestNodeCertCAValidity(t *testing.T) {
	const ip = "203.0.113.7"
	short, shortKey := testCAValid(t, testNow.Add(-time.Hour), testNow.AddDate(0, 0, 40))
	shortCA, err := parseCert(short)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM, _, err := IssueNodeCert(short, shortKey, ip, testNow)
	if err != nil {
		t.Fatalf("a CA with 40 days left still signs: %v", err)
	}
	leaf, _ := parseCert(leafPEM)
	if !leaf.NotAfter.Equal(shortCA.NotAfter) {
		t.Errorf("leaf NotAfter %s, CA NotAfter %s: the leaf must be capped", leaf.NotAfter, shortCA.NotAfter)
	}
	if err := CheckNodeCert(leafPEM, short, ip, testNow); err != nil {
		t.Errorf("capped leaf, 40 days left: %v", err)
	}
	for name, at := range map[string]time.Time{
		"CA ageing":  testNow.AddDate(0, 0, 15),
		"CA expired": testNow.AddDate(0, 0, 41),
	} {
		err := CheckNodeCert(leafPEM, short, ip, at)
		if err == nil || !strings.Contains(err.Error(), "CA Hub истекает") || strings.Contains(err.Error(), "не от CA Hub") {
			t.Errorf("%s: %v", name, err)
		}
		if _, _, err := IssueNodeCert(short, shortKey, ip, at); err == nil || !strings.Contains(err.Error(), "CA Hub истекает") {
			t.Errorf("%s: issued (%v)", name, err)
		}
	}
	if err := CheckNodeCert(leafPEM, short, ip, testNow.Add(-2*time.Hour)); err == nil || !strings.Contains(err.Error(), "CA Hub действует только с") {
		t.Errorf("CA not valid yet: %v", err)
	}
	for name, c := range map[string][2]time.Time{
		"20 days left": {testNow.Add(-time.Hour), testNow.AddDate(0, 0, 20)},
		"expired":      {testNow.AddDate(-2, 0, 0), testNow.AddDate(0, 0, -1)},
		"not yet":      {testNow.Add(time.Hour), testNow.AddDate(10, 0, 0)},
	} {
		ca, caKey := testCAValid(t, c[0], c[1])
		cert, key, err := IssueNodeCert(ca, caKey, ip, testNow)
		if err == nil || cert != nil || key != nil || !strings.Contains(err.Error(), "CA Hub") {
			t.Errorf("%s: issued (%v)", name, err)
		}
	}
}

type funcRemote func(script string) (string, error)

func (f funcRemote) Run(_ context.Context, s string) (string, error)               { return f(s) }
func (f funcRemote) PutFile(context.Context, string, []byte, uint32) (bool, error) { return false, nil }

func TestEnsureHubKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sp, _ := ssh.NewPublicKey(pub)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp)))
	got, err := EnsureHubKey(context.Background(), funcRemote(func(s string) (string, error) {
		if !strings.HasPrefix(s, "# hp:hub-key") {
			t.Fatalf("unexpected script %q", s)
		}
		return line + " heyearth-hub\n", nil
	}))
	if err != nil || got != line || HubKeyFingerprint(got) != ssh.FingerprintSHA256(sp) {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := EnsureHubKey(context.Background(), funcRemote(func(string) (string, error) { return "garbage\n", nil })); err == nil {
		t.Error("garbage accepted")
	}
}

func TestEnsureHubKeyRefuses(t *testing.T) {
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsp, _ := ssh.NewPublicKey(&rk.PublicKey)
	rsaLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(rsp)))
	for name, c := range map[string]struct {
		out string
		err error
	}{
		"empty":       {"", nil},
		"rsa":         {rsaLine + " heyearth-hub\n", nil},
		"script fail": {"ssh-keygen: broken\n", errors.New("exit status 1")},
	} {
		got, err := EnsureHubKey(context.Background(), funcRemote(func(string) (string, error) { return c.out, c.err }))
		if err == nil || got != "" {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	if fp := HubKeyFingerprint("garbage"); fp != "" {
		t.Errorf("fingerprint of garbage: %q", fp)
	}
}
