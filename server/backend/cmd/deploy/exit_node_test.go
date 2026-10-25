package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/provision"
	"golang.org/x/crypto/ssh"
)

func TestParseExitNodeAddr(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"89.124.89.126", "89.124.89.126"},
		{"89.124.89.126:443", "89.124.89.126"},
		{" 89.124.89.126 ", "89.124.89.126"},
		{"2001:DB8::5", "2001:db8::5"},
		{"[2001:db8::5]:443", "2001:db8::5"},
	} {
		if got, err := parseExitNodeAddr(c.in); err != nil || got != c.want {
			t.Errorf("parseExitNodeAddr(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "exit.example.com", "exit.example.com:443", "1.2.3.4;reboot", "1.2.3.4$(id)", "1.2.3.4:443:1", "999.1.1.1"} {
		if got, err := parseExitNodeAddr(bad); err == nil {
			t.Errorf("parseExitNodeAddr(%q) = %q, want an error", bad, got)
		}
	}
}

// testSSHServer accepts password "pw" for root with an ed25519 host key and returns its address
// and the host key's fingerprint.
func testSSHServer(t *testing.T) (string, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
		if c.User() == "root" && string(pw) == "pw" {
			return nil, nil
		}
		return nil, os.ErrPermission
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
				if err != nil {
					c.Close()
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "test")
				}
				sc.Close()
			}()
		}
	}()
	return ln.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey())
}

// The deploy tool reaches a host by password only with its host key pinned: the right fingerprint
// connects, a wrong one is refused, and without DEPLOY_HOST_KEY_FINGERPRINT the Hub is not even
// dialled (no InsecureIgnoreHostKey anywhere). The exit's password is never the Hub's.
func TestDialExitNodePinned(t *testing.T) {
	addr, fp := testSSHServer(t)
	c, err := sshDialAlgos(addr, "root", "pw", fp, []string{ssh.KeyAlgoED25519})
	if err != nil {
		t.Fatalf("pinned dial with the right fingerprint: %v", err)
	}
	c.Close()
	if c, err := sshDialAlgos(addr, "root", "pw", "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", nil); err == nil {
		c.Close()
		t.Fatal("a wrong host key fingerprint was accepted")
	} else if !strings.Contains(err.Error(), "unexpected host key fingerprint") {
		t.Errorf("error: %v", err)
	}
	for _, fp := range []string{"", "  ", "md5:aa:bb"} {
		c, err := sshDial(addr, "root", "pw", fp)
		if err == nil {
			c.Close()
			t.Fatalf("fingerprint %q: dialled without a pinned host key", fp)
		}
		if !strings.Contains(err.Error(), "DEPLOY_HOST_KEY_FINGERPRINT") {
			t.Errorf("fingerprint %q: the refusal must name DEPLOY_HOST_KEY_FINGERPRINT: %v", fp, err)
		}
	}
	if _, err := exitAccessFor(envMap{"EXIT_NODE_SSH_PASSWORD": "pw"}, ""); err == nil {
		t.Error("exitAccessFor without EXIT_NODE_HOST_KEY_FINGERPRINT must fail")
	}
	if _, err := exitAccessFor(envMap{"DEPLOY_SSH_PASSWORD": "hub", "EXIT_NODE_HOST_KEY_FINGERPRINT": fp}, ""); err == nil {
		t.Error("the Hub's deploy password must never be used for the exit")
	}
	if pw, _, _ := exitNodeCredentials(envMap{"DEPLOY_SSH_PASSWORD": "hub"}, ""); pw != "" {
		t.Error("--setup-exit-node must never take the Hub's deploy password")
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "InsecureIgnoreHostKey") {
		t.Error("main.go still accepts any host key")
	}
}

func TestExitNodeCredentials(t *testing.T) {
	fp := "SHA256:" + strings.Repeat("A", 43)
	for name, tc := range map[string]struct {
		env     envMap
		flag    string
		pw, wfp string
		wantErr bool
	}{
		"flag wins":               {envMap{"EXIT_NODE_SSH_PASSWORD": "env"}, "flag", "flag", "", false},
		"env password":            {envMap{"EXIT_NODE_SSH_PASSWORD": "env", "EXIT_NODE_HOST_KEY_FINGERPRINT": fp}, "", "env", fp, false},
		"nothing: the DB decides": {envMap{}, "", "", "", false},
		"bad fingerprint refused": {envMap{"EXIT_NODE_HOST_KEY_FINGERPRINT": "SHA256:short"}, "", "", "", true},
	} {
		pw, gotFP, err := exitNodeCredentials(tc.env, tc.flag)
		if (err != nil) != tc.wantErr || (!tc.wantErr && (pw != tc.pw || gotFP != tc.wfp)) {
			t.Errorf("%s: %q %q %v", name, pw, gotFP, err)
		}
	}
}

// Which host key --setup-exit-node pins: server/.env's fingerprint, else the one confirmed in the
// database — always one the exit presents right now; a different confirmed key must be presented
// too (exit 2 has ed25519 and ECDSA), otherwise the server is not the one the owner confirmed.
func TestChooseExitHostKey(t *testing.T) {
	ed := provision.HostKey{Type: ssh.KeyAlgoED25519, Fingerprint: "SHA256:" + strings.Repeat("E", 43), Key: "ed-blob"}
	ec := provision.HostKey{Type: ssh.KeyAlgoECDSA256, Fingerprint: "SHA256:" + strings.Repeat("C", 43), Key: "ec-blob"}
	stranger := "SHA256:" + strings.Repeat("X", 43)
	both := provision.ProbeResult{Keys: []provision.HostKey{ed, ec}}
	onlyEd := provision.ProbeResult{Keys: []provision.HostKey{ed}}
	for name, tc := range map[string]struct {
		row   exitRowState
		env   string
		probe provision.ProbeResult
		want  provision.HostKey
		err   string
	}{
		"new exit, .env fingerprint":                  {exitRowState{}, ed.Fingerprint, both, ed, ""},
		"new exit without a fingerprint":              {exitRowState{}, "", both, provision.HostKey{}, "EXIT_NODE_HOST_KEY_FINGERPRINT"},
		"known exit, DB-confirmed key":                {exitRowState{Found: true, FP: ec.Fingerprint, Confirmed: true}, "", both, ec, ""},
		"known exit, unconfirmed key":                 {exitRowState{Found: true, FP: ec.Fingerprint}, "", both, provision.HostKey{}, "не подтверждён"},
		"another key of the same server":              {exitRowState{Found: true, FP: ec.Fingerprint, Confirmed: true}, ed.Fingerprint, both, ed, ""},
		"confirmed key gone: reinstalled or not ours": {exitRowState{Found: true, FP: ec.Fingerprint, Confirmed: true}, ed.Fingerprint, onlyEd, provision.HostKey{}, ec.Fingerprint},
		"fingerprint not presented":                   {exitRowState{}, stranger, both, provision.HostKey{}, "предъявляет"},
	} {
		got, err := chooseExitHostKey(tc.row, tc.env, tc.probe)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: %+v %v, want an error with %q", name, got, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", name, got, err, tc.want)
		}
	}
}

// The rows --setup-exit-node writes are the admin wizard's: a new exit is 'pending' with its host
// key confirmed and only an encrypted password; an update drops a plaintext password only when it
// stores a new encrypted one (ssh_password is NOT NULL: emptied, never NULLed).
func TestExitRowQueries(t *testing.T) {
	ins := norm(insertExitRowQuery)
	for _, w := range []string{"'pending'", "ssh_host_key_confirmed_at", "ssh_password_enc", "ssh_host_key, ssh_host_key_fp", "'exit', TRUE"} {
		if !strings.Contains(ins, w) {
			t.Errorf("insert lacks %q:\n%s", w, ins)
		}
	}
	if regexp.MustCompile(`ssh_password[,) ]`).MatchString(insertExitRowQuery) {
		t.Errorf("insert writes a plaintext password:\n%s", ins)
	}
	upd := norm(updateExitRowQuery)
	for _, w := range []string{"ssh_password = CASE WHEN $5::bytea IS NULL THEN ssh_password ELSE '' END", "ssh_password_enc = COALESCE($5::bytea, ssh_password_enc)",
		"ssh_host_key_confirmed_at = CASE WHEN ssh_host_key_fp IS NOT DISTINCT FROM $4", "WHERE id = $1"} {
		if !strings.Contains(upd, w) {
			t.Errorf("update lacks %q:\n%s", w, upd)
		}
	}
	if strings.Contains(upd, "ssh_password = NULL") {
		t.Error("ssh_password is NOT NULL")
	}
}

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestCollectCLIHubIPs(t *testing.T) {
	ipOut := "2: ens3    inet 138.16.181.225/24 brd 138.16.181.255 scope global ens3\n" +
		"2: ens3    inet 45.150.39.236/32 scope global ens3\n" +
		"3: awg0    inet 10.66.66.1/24 scope global awg0\n" +
		"4: docker0    inet 172.17.0.1/16 scope global docker0\n" +
		"2: ens3    inet6 2a03:6f00::5/64 scope global\n"
	lookup := func(h string) ([]net.IP, error) {
		if h == "hiearth.ru" {
			return []net.IP{net.ParseIP("138.16.181.225")}, nil
		}
		return nil, os.ErrNotExist
	}
	got := collectCLIHubIPs(ipOut, []string{"hiearth.ru", "109.172.86.40", "", "broken.invalid"}, lookup)
	want := []string{"109.172.86.40", "138.16.181.225", "2a03:6f00::5", "45.150.39.236"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%v, want %v", got, want)
	}
}

func TestParsePlanSpec(t *testing.T) {
	for spec, want := range map[string]string{"all-paid": "all-paid", "none": "none", "keep": "keep", "1, 2,3": "ids"} {
		kind, ids, err := parsePlanSpec(spec)
		if err != nil || kind != want || (want == "ids" && len(ids) != 3) {
			t.Errorf("%q: %s %v %v", spec, kind, ids, err)
		}
	}
	for _, bad := range []string{"all", "1,x", "-1", "0"} {
		if _, _, err := parsePlanSpec(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// --setup-exit-node is the provisioning core, not a second provisioning path.
func TestSetupExitNodeUsesTheCore(t *testing.T) {
	b, _ := os.ReadFile("main.go")
	src := string(b)
	i := strings.Index(src, "func setupExitNodeDeploy(")
	if i < 0 {
		t.Fatal("setupExitNodeDeploy not found")
	}
	body := src[i : i+strings.Index(src[i:], "\n}\n")]
	for _, must := range []string{"exitNodeCredentials(", "provision.Runner{", "provision.RecipeFor(", "ensureExitRow(",
		"JobRunner: provision.RunnerCLI", "provision.DialNode", "provision.Probe(", "cliExitOptions("} {
		if !strings.Contains(body, must) {
			t.Errorf("setupExitNodeDeploy lacks %s", must)
		}
	}
	for _, banned := range []string{"restart heyearth-api", "exitNodeSetupScript", "sshDial(", "sshDialAlgos(", "deployPassword", "ca-key.pem\")", "InsecureIgnoreHostKey"} {
		if strings.Contains(body, banned) {
			t.Errorf("setupExitNodeDeploy still uses %s", banned)
		}
	}
	for _, gone := range []string{"func exitNodeSetupScript(", "func hubHy2ClientScript(", "func exitNodeSetupError(", "func ensureLocalMTLSAssets("} {
		if strings.Contains(src, gone) {
			t.Errorf("main.go still has %s", gone)
		}
	}
	// The CA key never comes to this machine: node certificates are signed on the Hub.
	b, _ = os.ReadFile("exit_node.go")
	src = string(b)
	i = strings.Index(src, "func cliExitOptions(")
	body = src[i : i+strings.Index(src[i:], "\n}\n")]
	if !strings.Contains(body, "CASigner: ") || !strings.Contains(body, "provision.HubCASigner(") || strings.Contains(body, "ca-key.pem\")") ||
		!strings.Contains(body, "return []byte(ca), nil, nil") {
		t.Errorf("cliExitOptions must hand over the CA certificate without its key and sign on the Hub:\n%s", body)
	}
}

// --setup-exit-node checks the agent over the Hub's mTLS before it reports success.
func TestSetupExitNodeProbesTheAgent(t *testing.T) {
	b, _ := os.ReadFile("main.go")
	src := string(b)
	i := strings.Index(src, "func setupExitNodeDeploy(")
	body := src[i : i+strings.Index(src[i:], "\n}\n")]
	if !strings.Contains(body, "cliProbeAgent(hub, hubCertsDir)") || strings.Contains(body, "}, nil))") {
		t.Error("the CLI recipe must get Hooks.ProbeAgent = cliProbeAgent(hub, hubCertsDir)")
	}
}
