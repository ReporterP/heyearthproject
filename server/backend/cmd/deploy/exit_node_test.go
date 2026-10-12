package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"

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
	hub := hubHy2ClientScript("203.0.113.7", "5", "s3cret")
	u, c, m := strings.Index(hub, "umask 077\n"), strings.Index(hub, "cat > /opt/heyearth/hysteria/client-5.yaml"), strings.Index(hub, "chmod 600 /opt/heyearth/hysteria/client-5.yaml\n")
	if u < 0 || c < 0 || m < 0 || u > c || c > m {
		t.Errorf("client-5.yaml must be created under umask 077 and chmod 600 (umask %d, cat %d, chmod %d)", u, c, m)
	}
	// the unit and the rest still render with the right ids
	for _, w := range []string{"/etc/systemd/system/hysteria2-client-5.service", "ExecStart=/usr/local/bin/hysteria client -c /opt/heyearth/hysteria/client-5.yaml", "PORT=$((1088 + 5))"} {
		if !strings.Contains(hub, w) {
			t.Errorf("hub script lacks %q", w)
		}
	}
}
