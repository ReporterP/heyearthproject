package hubnet

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestOutboundVerdict(t *testing.T) {
	for _, tc := range []struct {
		cur, new int
		want     string
	}{
		{24, 24, VerdictOK},
		{14, 10, VerdictOK}, // entry 6: ~40 % loss on both addresses — the path, not the address
		{14, 9, VerdictFail},
		{24, 0, VerdictFail},
		{0, 0, VerdictNotIndicative},
		{0, 5, VerdictOK},
	} {
		if got := OutboundVerdict(tc.cur, tc.new, 24); got != tc.want {
			t.Errorf("OutboundVerdict(%d, %d, 24) = %q, want %q", tc.cur, tc.new, got, tc.want)
		}
	}
}

func TestProbeTCPCountsSuccesses(t *testing.T) {
	n := 0
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		n++
		if n%3 == 0 {
			return nil, errors.New("timeout")
		}
		c1, c2 := net.Pipe()
		c2.Close()
		return c1, nil
	}
	if got := ProbeTCP(context.Background(), dial, "198.51.100.5:443", 24); got != 16 {
		t.Fatalf("ProbeTCP = %d, want 16", got)
	}
}

// fakeSMTP answers one session: greeting, EHLO with AUTH PLAIN, the AUTH verdict, QUIT.
func fakeSMTP(t *testing.T, accept bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		c.Write([]byte("220 mail.example.test ESMTP\r\n"))
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch cmd := strings.ToUpper(strings.Fields(line + " x")[0]); cmd {
			case "EHLO":
				c.Write([]byte("250-mail.example.test\r\n250 AUTH PLAIN LOGIN\r\n"))
			case "AUTH":
				if accept {
					c.Write([]byte("235 2.7.0 Authentication successful\r\n"))
				} else {
					c.Write([]byte("535 5.7.8 Error: authentication failed\r\n"))
				}
			case "QUIT":
				c.Write([]byte("221 bye\r\n"))
				return
			default:
				c.Write([]byte("502 unknown\r\n"))
			}
		}
	}()
	return ln.Addr().String()
}

func TestProbeSMTPAuth(t *testing.T) {
	d := &net.Dialer{Timeout: 2 * time.Second}
	addr := fakeSMTP(t, true)
	host, port, _ := net.SplitHostPort(addr)
	if err := ProbeSMTPAuth(context.Background(), d.DialContext, host, atoi(port), "u", "p"); err != nil {
		t.Fatalf("accepted AUTH: %v", err)
	}
	addr = fakeSMTP(t, false)
	host, port, _ = net.SplitHostPort(addr)
	err := ProbeSMTPAuth(context.Background(), d.DialContext, host, atoi(port), "u", "p")
	if err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("refused AUTH: want an error with the server's reply, got %v", err)
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestCheckDNS(t *testing.T) {
	oldIP, nIP, entry := netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("198.51.100.20"), "203.0.113.50"
	servers := []string{"1.1.1.1", "8.8.8.8", "77.88.8.8"}
	state := map[string]map[string]string{ // server → name → answer
		"1.1.1.1": {"hiearth.ru": "198.51.100.20", "go.hiearth.ru": entry},
		"8.8.8.8": {"hiearth.ru": "198.51.100.20", "go.hiearth.ru": entry},
	}
	before := map[string]string{"hiearth.ru": "192.0.2.10", "go.hiearth.ru": entry}
	resolve := func(ctx context.Context, server, name string) ([]netip.Addr, error) {
		m, ok := state[server]
		if !ok {
			return nil, errors.New("no answer")
		}
		return []netip.Addr{netip.MustParseAddr(m[name])}, nil
	}
	rows, answered := CheckDNS(context.Background(), resolve, []string{"hiearth.ru", "go.hiearth.ru"}, servers, oldIP, nIP, before)
	if answered != 2 {
		t.Fatalf("answered = %d, want 2", answered)
	}
	if !rows[0].PointsAtOld || !rows[0].Converged || rows[1].PointsAtOld {
		t.Fatalf("rows: %+v", rows)
	}
	if err := DNSGate(rows, answered); err != nil {
		t.Fatalf("gate: %v", err)
	}

	state["8.8.8.8"]["hiearth.ru"] = "192.0.2.10"
	rows, answered = CheckDNS(context.Background(), resolve, []string{"hiearth.ru", "go.hiearth.ru"}, servers, oldIP, nIP, before)
	err := DNSGate(rows, answered)
	if err == nil || !strings.Contains(err.Error(), "hiearth.ru") || !strings.Contains(err.Error(), "8.8.8.8") {
		t.Fatalf("one server still on the old address: want an error naming it, got %v", err)
	}

	delete(state, "8.8.8.8")
	rows, answered = CheckDNS(context.Background(), resolve, []string{"hiearth.ru"}, servers, oldIP, nIP, before)
	if err := DNSGate(rows, answered); err == nil {
		t.Fatal("one resolver answering: want a refusal")
	}
}

func TestRolesAndValidateNew(t *testing.T) {
	route := DefaultRoute{Gateway: netip.MustParseAddr("203.0.113.1"), Dev: "ens3", Src: netip.MustParseAddr("192.0.2.10")}
	ifs := []netip.Prefix{netip.MustParsePrefix("203.0.113.5/24"), netip.MustParsePrefix("192.0.2.10/32"), netip.MustParsePrefix("192.0.2.99/24")}
	roles := Roles(ifs, route, "192.0.2.10", "", "")
	byAddr := map[string][]string{}
	for _, r := range roles {
		byAddr[r.Addr.String()] = r.Roles
	}
	if strings.Join(byAddr["203.0.113.5"], ",") != RoleGateway || strings.Join(byAddr["192.0.2.10"], ",") != RoleUsers+","+RoleSource ||
		strings.Join(byAddr["192.0.2.99"], ",") != RoleOther {
		t.Fatalf("roles: %v", byAddr)
	}

	nodes := []string{"203.0.113.50"}
	for in, want := range map[string]string{
		"10.0.0.5":     "частный",
		"2001:db8::1":  "IPv4",
		"203.0.113.50": "сервер",
		"192.0.2.10":   "уже",
		"203.0.113.5":  "шлюз",
		"not-an-ip":    "IPv4",
		"127.0.0.1":    "частный",
	} {
		_, err := ValidateNew(in, ifs, nodes, route, "192.0.2.10")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateNew(%q): want an error with %q, got %v", in, want, err)
		}
	}
	if a, err := ValidateNew(" 198.51.100.20 ", ifs, nodes, route, "192.0.2.10"); err != nil || a.String() != "198.51.100.20" {
		t.Fatalf("valid address: %v %v", a, err)
	}
}
