package admin

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// B2: refused = the port is open at the hoster (nothing listens yet); a timeout = closed.
func TestHubReachChecks(t *testing.T) {
	prev := dialTCP
	t.Cleanup(func() { dialTCP = prev })
	dialTCP = func(ctx context.Context, addr string) error {
		if strings.HasSuffix(addr, ":50052") {
			return &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
		}
		return context.DeadlineExceeded
	}
	cs := hubReachChecks(context.Background(), "exit", "203.0.113.5")
	if len(cs) != 2 || !cs[0].OK || cs[1].OK || !strings.Contains(cs[1].Detail, "откройте входящий TCP 10443") {
		t.Fatalf("%+v", cs)
	}
	if cs := hubReachChecks(context.Background(), "relay", "203.0.113.5"); len(cs) != 3 || cs[0].Name != "TCP 443 с Hub" {
		t.Fatalf("relay %+v", cs)
	}
}

func TestParseCymruCountry(t *testing.T) {
	out := "Bulk mode; whois.cymru.com [2026-11-13 10:00:00 +0000]\n" +
		"64500   | 203.0.113.9      | 203.0.113.0/24      | nl | ripencc  | 2015-01-01 | EXAMPLE-AS, NL\n"
	if cc := parseCymruCountry(strings.NewReader(out), "203.0.113.9"); cc != "NL" {
		t.Errorf("cc %q", cc)
	}
	if cc := parseCymruCountry(strings.NewReader(out), "203.0.113.10"); cc != "" {
		t.Errorf("another ip: %q", cc)
	}
	if cc := parseCymruCountry(strings.NewReader("NA | 203.0.113.9 | NA | | other | | NA\n"), "203.0.113.9"); cc != "" {
		t.Errorf("no country: %q", cc)
	}
}

// C5: a journal is read only for a unit the server's role runs.
func TestNodeLogService(t *testing.T) {
	for _, c := range []struct{ role, asked, want string }{
		{"exit", "", "node-agent"},
		{"exit", "xray", "xray"},
		{"exit", "mtproxy", "mtproxy"},
		{"exit", "nginx", ""},
		{"relay", "", "nginx"},
		{"relay", "heyearth-relay", "heyearth-relay"},
		{"relay", "node-agent", ""},
		{"exit", "sshd; rm -rf /", ""},
		{"", "hysteria2", "hysteria2"},
	} {
		if got := nodeLogService(c.role, c.asked); got != c.want {
			t.Errorf("%s/%q: %q, want %q", c.role, c.asked, got, c.want)
		}
	}
}
