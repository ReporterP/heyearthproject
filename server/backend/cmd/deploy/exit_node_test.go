package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
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

// --setup-exit-node reaches the exit only with the pinned ed25519 host key and the exit's own
// password; there is no unpinned fallback and no Hub-password fallback.
func TestDialExitNodePinned(t *testing.T) {
	addr, fp := testSSHServer(t)
	c, err := dialExitNode(addr, exitAccess{password: "pw", fingerprint: fp})
	if err != nil {
		t.Fatalf("pinned dial with the right fingerprint: %v", err)
	}
	c.Close()
	if c, err := dialExitNode(addr, exitAccess{password: "pw", fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}); err == nil {
		c.Close()
		t.Fatal("a wrong host key fingerprint was accepted")
	} else if !strings.Contains(err.Error(), "unexpected host key fingerprint") {
		t.Errorf("error: %v", err)
	}
	// no fingerprint, or only the Hub's password in server/.env: refused before any dial
	if _, err := exitAccessFor(envMap{"EXIT_NODE_SSH_PASSWORD": "pw"}, ""); err == nil {
		t.Error("exitAccessFor without EXIT_NODE_HOST_KEY_FINGERPRINT must fail")
	}
	if _, err := exitAccessFor(envMap{"DEPLOY_SSH_PASSWORD": "hub", "EXIT_NODE_HOST_KEY_FINGERPRINT": fp}, ""); err == nil {
		t.Error("the Hub's deploy password must never be used for the exit")
	}
	// the source keeps it that way: no unpinned dial and no deployPassword in setupExitNodeDeploy
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "func setupExitNodeDeploy(")
	body := src[i : i+strings.Index(src[i:], "\n}\n")]
	if strings.Contains(body, "deployPassword") || regexp.MustCompile(`sshDial\([^)]*""\)`).MatchString(body) || !strings.Contains(body, "dialExitNode(") {
		t.Error("setupExitNodeDeploy must dial through dialExitNode with exitAccessFor, never sshDial(..., \"\") or the Hub password")
	}
}

func TestExitScriptsSecrets(t *testing.T) {
	exit := exitNodeSetupScript("203.0.113.7", 8443)
	if regexp.MustCompile(`echo "[^"\n]*\$SS_PASSWORD`).MatchString(exit) {
		t.Error("the exit script prints the SS password")
	}
	hub := hubHy2ClientScript("203.0.113.7", 5, "s3cret")
	u, p := strings.Index(hub, "umask 077\n"), strings.Index(hub, "put_file /opt/heyearth/hysteria/client-5.yaml 600 ")
	if u < 0 || p < 0 || u > p {
		t.Errorf("client-5.yaml must be written under umask 077 with mode 600 (umask %d, put_file %d)", u, p)
	}
	// the tunnel password only travels inside the base64 payload, never through the shell
	if strings.Contains(hub, "s3cret") {
		t.Error("the tunnel password appears in the shell script")
	}
	// the Hub client is exactly the template heyearth-api writes (hopping, hopInterval, ACL)
	if got := payloadFrom(t, hub, "/opt/heyearth/hysteria/client-5.yaml"); got != config.HubHysteriaClientYAML(5, "203.0.113.7", "s3cret", 1093) {
		t.Errorf("client yaml is not config.HubHysteriaClientYAML:\n%s", got)
	}
	if got := payloadFrom(t, hub, "/etc/systemd/system/hysteria2-client-5.service"); got != config.HubHysteriaClientUnit(5, "203.0.113.7") {
		t.Errorf("unit is not config.HubHysteriaClientUnit:\n%s", got)
	}
	for _, w := range []string{"server: 203.0.113.7:" + config.ExitHopRange + "\n", "    hopInterval: 30s\n", "acl:\n  inline:\n", "listen: 127.0.0.1:1093\n"} {
		if !strings.Contains(config.HubHysteriaClientYAML(5, "203.0.113.7", "s3cret", 1093), w) {
			t.Errorf("client template lacks %q", w)
		}
	}
}

// A re-run on an unchanged exit: the Hub client is restarted only when its files changed or it
// is down, and the tunnel row (and heyearth-api) only when the credentials changed.
func TestExitRerunDoesNotInterrupt(t *testing.T) {
	want := exitTunnelCreds{UUID: "u", PublicKey: "pk", ShortID: "s", SSPassword: "p", Protocol: "hysteria2", Port: 50443, SNI: config.ExitVRSNI}
	if a := decideExitDBAction(want, true, want); a != exitDBKeep || exitDBNeedsResync(a) {
		t.Errorf("unchanged credentials: action %d, resync %v", a, exitDBNeedsResync(a))
	}
	for _, mut := range []func(*exitTunnelCreds){
		func(c *exitTunnelCreds) { c.UUID = "x" }, func(c *exitTunnelCreds) { c.PublicKey = "x" }, func(c *exitTunnelCreds) { c.ShortID = "x" },
		func(c *exitTunnelCreds) { c.SSPassword = "x" }, func(c *exitTunnelCreds) { c.Protocol = "vless" }, func(c *exitTunnelCreds) { c.Port = 443 },
		func(c *exitTunnelCreds) { c.SNI = "x" },
	} {
		cur := want
		mut(&cur)
		if a := decideExitDBAction(cur, true, want); a != exitDBUpdate || !exitDBNeedsResync(a) {
			t.Errorf("changed credentials %+v: action %d", cur, a)
		}
	}
	if a := decideExitDBAction(exitTunnelCreds{}, false, want); a != exitDBInsert || !exitDBNeedsResync(a) {
		t.Errorf("new exit: action %d", a)
	}
	if !haveBash() {
		t.Skip("no working bash")
	}
	// the restart block of the Hub client script, with systemctl stubbed
	hub := hubHy2ClientScript("203.0.113.7", 5, "s3cret")
	i, j := strings.Index(hub, "systemctl daemon-reload\n"), strings.Index(hub, "systemctl status hysteria2-client-5")
	block := hub[i:j]
	for _, c := range []struct{ changed, active, wantRestart string }{{"0", "0", "1"}, {"0", "1", "0"}, {"1", "1", "1"}} {
		out, _ := runBash(t, `PUT_CHANGED=`+c.changed+`; ACTIVE=`+c.active+`
systemctl() { case "$1" in restart) echo RESTARTED ;; is-active) [ "$ACTIVE" = 1 ] ;; esac; }
sleep() { :; }
`+block)
		if got := strings.Contains(out, "RESTARTED"); got != (c.wantRestart == "1") {
			t.Errorf("changed=%s active=%s: restarted=%v\n%s", c.changed, c.active, got, out)
		}
	}
}

// The SNI --setup-exit-node writes to tunnel_sni (and the Hub's generator falls back to) is one the
// exit's Reality inbound serves: www.microsoft.com, not the apex that fails the handshake on prod.
func TestExitNodeSetupScriptServesExitVRSNI(t *testing.T) {
	if config.ExitVRSNI != "www.microsoft.com" {
		t.Fatalf("config.ExitVRSNI = %q, want www.microsoft.com", config.ExitVRSNI)
	}
	s := exitNodeSetupScript("203.0.113.7", 8443)
	m := regexp.MustCompile(`"serverNames": \[([^\]]*)\]`).FindStringSubmatch(s)
	if m == nil || !strings.Contains(m[1], `"`+config.ExitVRSNI+`"`) {
		t.Errorf("the exit's Reality serverNames %v lack %q", m, config.ExitVRSNI)
	}
	if !strings.Contains(s, `echo "SNI: `+config.ExitVRSNI+`"`) {
		t.Errorf("the exit script reports another SNI than %q", config.ExitVRSNI)
	}
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "tunnel_sni = 'microsoft.com'") || strings.Contains(string(src), `SNI: "microsoft.com"`) {
		t.Error("setupExitNodeDeploy writes the apex microsoft.com to tunnel_sni")
	}
}

// The exit accepts the Hub clients' hopping range: the shared nft hop script and the firewall.
func TestExitNodeSetupScriptPortHopping(t *testing.T) {
	s := exitNodeSetupScript("203.0.113.7", 8443)
	hop := strings.Index(s, strings.TrimSuffix(config.ExitPortHopScript, "\n"))
	if hop < 0 {
		t.Fatal("the exit script does not install config.ExitPortHopScript")
	}
	if hy := strings.Index(s, "systemctl restart hysteria2\n"); hy < 0 || hy > hop {
		t.Error("port hopping must follow the hysteria server setup")
	}
	for _, w := range []string{"ufw allow 20000:50000/udp", "iptables -I INPUT -p udp --dport 20000:50000 -j ACCEPT"} {
		if !strings.Contains(s, w) {
			t.Errorf("exit firewall lacks %q", w)
		}
	}
	if strings.Contains(s, "%!") {
		t.Error("format verb mismatch in the exit script")
	}
}
