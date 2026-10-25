package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
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
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/provision"
)

var caNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// testOldCA is the CA deploy used to generate on the operator's machine (its key was public):
// RSA, "HeYearth Node-Agent CA", with the shared client certificate signed by it.
type genCA struct {
	cert    *x509.Certificate
	certPEM []byte
	key     crypto.Signer
}

func genOldCA(t *testing.T) genCA {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return selfSign(t, k, pkix.Name{CommonName: "HeYearth Node-Agent CA", Organization: []string{"HeYearth"}})
}

func selfSign(t *testing.T, k crypto.Signer, subject pkix.Name) genCA {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: subject, NotBefore: caNow.AddDate(-1, 0, 0),
		NotAfter: caNow.AddDate(9, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, k.Public(), k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return genCA{c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), k}
}

// genHubCA is a CA as ensureHubNodeCA makes it (template hubCATemplate), key in Go.
func genHubCA(t *testing.T, now time.Time) genCA {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl, err := hubCATemplate(now)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, k.Public(), k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return genCA{c, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), k}
}

func pubPEM(t *testing.T, k crypto.Signer) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(k.Public())
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// client issues a client certificate (as the old deploy did: CN main-api, clientAuth) valid until notAfter.
func (ca genCA) client(t *testing.T, notAfter time.Time) (certPEM []byte, key crypto.Signer, keyPEM []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "main-api"}, NotBefore: caNow.AddDate(0, -1, 0),
		NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, k.Public(), ca.key)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), k, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

func TestPlanHubCA(t *testing.T) {
	old := genOldCA(t)
	oldCli, oldCliKey, _ := old.client(t, caNow.AddDate(0, 6, 0))
	hub := genHubCA(t, caNow.AddDate(0, 0, -1))
	hubCli, hubCliKey, _ := hub.client(t, caNow.AddDate(1, 0, 0))
	soonCli, soonCliKey, _ := hub.client(t, caNow.AddDate(0, 0, 10))
	other := genHubCA(t, caNow)

	legacyHub := hubCAFiles{caCert: old.certPEM, caKeyPub: "", clientCert: oldCli, clientKeyPub: pubPEM(t, oldCliKey)}
	ours := hubCAFiles{caCert: hub.certPEM, caKeyPub: pubPEM(t, hub.key), clientCert: hubCli, clientKeyPub: pubPEM(t, hubCliKey)}
	with := func(f hubCAFiles, mut func(*hubCAFiles)) hubCAFiles { mut(&f); return f }

	for name, tc := range map[string]struct {
		f     hubCAFiles
		mode  caMode
		force bool
		want  hubCAPlan
		err   string
	}{
		"fresh Hub: a CA and a client":    {f: hubCAFiles{}, mode: caKeep, want: hubCAPlan{newCA: true, newClient: true}},
		"old CA left alone by deploys":    {f: legacyHub, mode: caKeep, want: hubCAPlan{notOurs: true}},
		"old CA replaced, kept as legacy": {f: legacyHub, mode: caRotate, want: hubCAPlan{keepLegacyCA: true, keepLegacyClient: true, newCA: true, newClient: true}},
		"old CA key on the Hub changes nothing": {f: with(legacyHub, func(f *hubCAFiles) { f.caKeyPub = pubPEM(t, old.key) }), mode: caRotate,
			want: hubCAPlan{keepLegacyCA: true, keepLegacyClient: true, newCA: true, newClient: true}},
		"converged Hub: nothing":         {f: ours, mode: caRotate, want: hubCAPlan{}},
		"converged Hub, deploy: nothing": {f: ours, mode: caKeep, want: hubCAPlan{}},
		"client expiring: reissued":      {f: with(ours, func(f *hubCAFiles) { f.clientCert, f.clientKeyPub = soonCli, pubPEM(t, soonCliKey) }), want: hubCAPlan{newClient: true}},
		"client of another CA: reissued": {f: with(ours, func(f *hubCAFiles) { f.clientCert = oldCli; f.clientKeyPub = pubPEM(t, oldCliKey) }), want: hubCAPlan{newClient: true}},
		"client key not the pair":        {f: with(ours, func(f *hubCAFiles) { f.clientKeyPub = pubPEM(t, oldCliKey) }), want: hubCAPlan{newClient: true}},
		"forced client renewal":          {f: ours, force: true, want: hubCAPlan{newClient: true}},
		"Hub CA without its key: replaced, kept as legacy": {f: with(ours, func(f *hubCAFiles) { f.caKeyPub = pubPEM(t, other.key) }), mode: caRotate,
			want: hubCAPlan{keepLegacyCA: true, keepLegacyClient: true, newCA: true, newClient: true}},
		"interrupted rotation resumes (legacy already copied)": {f: with(legacyHub, func(f *hubCAFiles) {
			f.legacyCA, f.caKeyPub = old.certPEM, pubPEM(t, other.key)
		}), mode: caRotate, want: hubCAPlan{keepLegacyClient: true, newCA: true, newClient: true}},
		"legacy client already copied": {f: with(legacyHub, func(f *hubCAFiles) {
			f.legacyCA, f.legacyClientCert, f.legacyClientKeyPub = old.certPEM, oldCli, pubPEM(t, oldCliKey)
		}), mode: caRotate, want: hubCAPlan{newCA: true, newClient: true}},
		"a second unconverged generation is refused": {f: with(ours, func(f *hubCAFiles) { f.caKeyPub, f.legacyCA = "", old.certPEM }), mode: caRotate,
			err: "legacy-ca-cert.pem"},
		"old client not from the old CA: CA kept, client not": {f: with(legacyHub, func(f *hubCAFiles) { f.clientCert, f.clientKeyPub = hubCli, pubPEM(t, hubCliKey) }),
			mode: caRotate, want: hubCAPlan{keepLegacyCA: true, newCA: true, newClient: true}},
	} {
		got, err := planHubCA(tc.f, caNow, tc.mode, tc.force)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", name, err, tc.err)
			}
			continue
		}
		got.note = ""
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, tc.want)
		}
	}
}

// The Hub CA's subject differs from the old CA's (the Hub's TLS client picks its certificate by
// the CA names an agent sends) and carries the marker planHubCA recognises.
func TestHubCATemplate(t *testing.T) {
	c := genHubCA(t, caNow).cert
	if !isHubCA(c) || !c.IsCA || !c.MaxPathLenZero || c.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatalf("hub CA: %+v", c.Subject)
	}
	if bytes.Equal(c.RawSubject, genOldCA(t).cert.RawSubject) || isHubCA(genOldCA(t).cert) {
		t.Fatal("the Hub CA must not share the old CA's subject")
	}
	if got := c.NotAfter.Sub(caNow); got < 9*365*24*time.Hour {
		t.Errorf("CA valid for %s", got)
	}
}

// shellDir is dir as the local bash sees it (C:\x → /c/x on Windows).
func shellDir(dir string) string {
	p := filepath.ToSlash(dir)
	if runtime.GOOS == "windows" && len(p) >= 2 && p[1] == ':' {
		p = "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

func haveOpenSSL() bool {
	return haveBash() && exec.Command("bash", "-c", "command -v openssl >/dev/null").Run() == nil
}

func readAll(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out[e.Name()] = string(b)
	}
	return out
}

// ensureHubNodeCA on a real bash + openssl (the Hub's role played by a temp dir): a fresh Hub gets
// a CA and a client pair; a Hub with the old CA keeps it on deploys, and --ensure-hub-provisioning
// replaces it keeping the old certificate and client pair as legacy; reruns change nothing; the
// result serves both an unconverged agent (old CA) and a converged one (its certificate signed on
// the Hub) through provision.NodeAgentClientTLS.
func TestEnsureHubNodeCAOpenSSL(t *testing.T) {
	if !haveOpenSSL() {
		t.Skip("no bash/openssl")
	}
	ctx := context.Background()
	hub := provision.LocalRemote{}

	t.Run("fresh Hub", func(t *testing.T) {
		dir := t.TempDir()
		p, err := ensureHubNodeCA(ctx, hub, shellDir(dir), caNow, caKeep, false)
		if err != nil || !p.newCA || !p.newClient {
			t.Fatalf("%+v %v", p, err)
		}
		before := readAll(t, dir)
		for _, f := range []string{"ca-cert.pem", "ca-key.pem", "client-cert.pem", "client-key.pem"} {
			if before[f] == "" {
				t.Fatalf("%s not written: %v", f, before)
			}
		}
		if p, err := ensureHubNodeCA(ctx, hub, shellDir(dir), caNow, caRotate, false); err != nil || p != (hubCAPlan{}) {
			t.Fatalf("rerun: %+v %v", p, err)
		}
		if after := readAll(t, dir); !mapsEqual(before, after) {
			t.Fatal("a rerun on a converged Hub changed files")
		}
	})

	t.Run("old CA replaced without losing exit 2", func(t *testing.T) {
		dir := t.TempDir()
		old := genOldCA(t)
		oldCli, _, oldCliKey := old.client(t, caNow.AddDate(0, 6, 0))
		for name, b := range map[string][]byte{"ca-cert.pem": old.certPEM, "client-cert.pem": oldCli, "client-key.pem": oldCliKey} {
			if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if p, err := ensureHubNodeCA(ctx, hub, shellDir(dir), caNow, caKeep, false); err != nil || !p.notOurs || p.newCA {
			t.Fatalf("a deploy must not replace the CA: %+v %v", p, err)
		}
		p, err := ensureHubNodeCA(ctx, hub, shellDir(dir), caNow, caRotate, false)
		if err != nil || !p.keepLegacyCA || !p.keepLegacyClient || !p.newCA || !p.newClient {
			t.Fatalf("%+v %v", p, err)
		}
		files := readAll(t, dir)
		if files[provision.LegacyCACertFile] != string(old.certPEM) || files[provision.LegacyClientCertFile] != string(oldCli) ||
			files[provision.LegacyClientKeyFile] != string(oldCliKey) {
			t.Fatal("the old CA and client pair must be kept verbatim as legacy")
		}
		newCA := mustCert(t, []byte(files["ca-cert.pem"]))
		if !isHubCA(newCA) || files["ca-cert.pem"] == string(old.certPEM) {
			t.Fatal("ca-cert.pem is not the Hub's own CA")
		}
		for _, leftover := range []string{"ca-key.pem.new", "ca-cert.pem.new", "client-key.pem.new", "client-cert.pem.new"} {
			if _, ok := files[leftover]; ok {
				t.Errorf("%s left behind", leftover)
			}
		}
		if runtime.GOOS != "windows" {
			for _, k := range []string{"ca-key.pem", "client-key.pem", provision.LegacyClientKeyFile} {
				if st, _ := os.Stat(filepath.Join(dir, k)); st.Mode().Perm() != 0o600 {
					t.Errorf("%s mode %v", k, st.Mode().Perm())
				}
			}
		}
		if p, err := ensureHubNodeCA(ctx, hub, shellDir(dir), caNow, caRotate, false); err != nil || p != (hubCAPlan{}) {
			t.Fatalf("rerun after the replacement: %+v %v", p, err)
		}

		// exit 2 before its converge: old shared server cert, trusts only the old CA
		oldSrv, oldSrvKey := serverCert(t, old.cert, old.key, "203.0.113.2")
		// a converged exit: its certificate signed with the Hub's key (on the "Hub"), trusts only the new CA
		signer, err := provision.HubCASigner(ctx, hub, shellDir(dir)+"/ca-key.pem", newCA.PublicKey)
		if err != nil {
			t.Fatal(err)
		}
		nodeCert, nodeKey, err := provision.IssueNodeCertWith([]byte(files["ca-cert.pem"]), signer, "203.0.113.3", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := provision.NodeAgentClientTLS(filepath.Join(dir, "client-cert.pem"), filepath.Join(dir, "client-key.pem"), filepath.Join(dir, "ca-cert.pem"))
		if err != nil {
			t.Fatal(err)
		}
		if err := tlsHandshake(cfg, agentTLS(t, oldSrv, oldSrvKey, old.cert)); err != nil {
			t.Errorf("unconverged exit 2: %v", err)
		}
		if err := tlsHandshake(cfg, agentTLS(t, nodeCert, nodeKey, newCA)); err != nil {
			t.Errorf("converged exit: %v", err)
		}

		// --rotate-node-agent-certs: a new client certificate, the CA and legacy untouched
		p, err = ensureHubNodeCA(ctx, hub, shellDir(dir), caNow, caKeep, true)
		after := readAll(t, dir)
		if err != nil || !p.newClient || p.newCA || after["client-cert.pem"] == files["client-cert.pem"] ||
			after["ca-cert.pem"] != files["ca-cert.pem"] || after[provision.LegacyCACertFile] != files[provision.LegacyCACertFile] {
			t.Fatalf("forced client renewal: %+v %v", p, err)
		}
	})
}

// Certificates are dated by the Hub's clock (its API and the nodes verify them): the offset of
// the Hub's `date` is applied; a Hub that does not answer falls back to this machine's clock.
func TestHubClock(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	if d := time.Since(hubClock(context.Background(), provision.LocalRemote{})()); d < -2*time.Second || d > 2*time.Second {
		t.Fatalf("local Hub clock off by %s", d)
	}
	ahead := fakeClockHub{out: strconv.FormatInt(time.Now().Add(3*time.Hour).Unix(), 10) + "\n"}
	if d := hubClock(context.Background(), ahead)().Sub(time.Now()); d < 3*time.Hour-2*time.Second || d > 3*time.Hour+2*time.Second {
		t.Fatalf("a Hub 3h ahead: offset %s", d)
	}
	if d := time.Since(hubClock(context.Background(), fakeClockHub{out: "garbage"})()); d < -time.Second || d > time.Second {
		t.Fatalf("an unreadable Hub clock must fall back to the local one: %s", d)
	}
}

type fakeClockHub struct{ out string }

func (f fakeClockHub) Run(context.Context, string) (string, error) { return f.out, nil }
func (f fakeClockHub) PutFile(context.Context, string, []byte, uint32) (bool, error) {
	return false, nil
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mustCert(t *testing.T, p []byte) *x509.Certificate {
	t.Helper()
	c, err := parseCertPEM(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func serverCert(t *testing.T, ca *x509.Certificate, caKey crypto.Signer, ip string) (certPEM, keyPEM []byte) {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "node-agent"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"node-agent", "localhost"}, IPAddresses: []net.IP{net.ParseIP(ip)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, k.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})
}

// agentTLS is node-agent's server side: its pair, client certificates verified against one CA.
func agentTLS(t *testing.T, certPEM, keyPEM []byte, trust *x509.Certificate) *tls.Config {
	t.Helper()
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(trust)
	return &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS13}
}

func tlsHandshake(client, server *tls.Config) error {
	c1, c2 := net.Pipe()
	deadline := time.Now().Add(10 * time.Second) // a stuck handshake fails the test instead of hanging it
	_ = c1.SetDeadline(deadline)
	_ = c2.SetDeadline(deadline)
	defer c1.Close()
	defer c2.Close()
	errc := make(chan error, 1)
	go func() { errc <- tls.Server(c2, server).Handshake() }()
	cli := tls.Client(c1, client)
	cerr := cli.Handshake()
	if cerr != nil {
		c1.Close()
	} else {
		// a TLS 1.3 client is done before the server checked its certificate: keep reading so the
		// server's refusal is delivered over the unbuffered pipe
		go func() { _, _ = io.Copy(io.Discard, cli) }()
	}
	serr := <-errc
	if cerr != nil {
		return cerr
	}
	return serr
}
