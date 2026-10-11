package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// testSOCKS5 is a minimal no-auth SOCKS5 CONNECT proxy on loopback. It accepts IPv4
// (ATYP 0x01) and domain-name (ATYP 0x03) targets. A domain name is recorded and NOT
// resolved: the "far side" simply connects to 127.0.0.1:<port>, so tests can use
// fake hostnames exactly like production does through the exit.
type testSOCKS5 struct {
	addr string

	mu    sync.Mutex
	hosts []string // domain names received via ATYP 0x03
}

func newTestSOCKS5(t *testing.T) *testSOCKS5 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &testSOCKS5{addr: ln.Addr().String()}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *testSOCKS5) receivedHosts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hosts...)
}

func (s *testSOCKS5) serve(c net.Conn) {
	defer c.Close()
	b := make([]byte, 256)
	if _, err := io.ReadFull(c, b[:2]); err != nil { // VER NMETHODS
		return
	}
	if _, err := io.ReadFull(c, b[:b[1]]); err != nil { // METHODS
		return
	}
	c.Write([]byte{5, 0}) // method: no auth
	// request: VER CMD RSV ATYP; only CONNECT (1) is supported
	if _, err := io.ReadFull(c, b[:4]); err != nil || b[1] != 1 {
		return
	}
	var host string
	switch b[3] {
	case 1: // IPv4
		if _, err := io.ReadFull(c, b[:4]); err != nil {
			return
		}
		host = net.IP(b[:4]).String()
	case 3: // domain name: LEN NAME
		if _, err := io.ReadFull(c, b[:1]); err != nil {
			return
		}
		n := int(b[0])
		if _, err := io.ReadFull(c, b[:n]); err != nil {
			return
		}
		name := string(b[:n])
		s.mu.Lock()
		s.hosts = append(s.hosts, name)
		s.mu.Unlock()
		host = "127.0.0.1" // the far side "resolves" every name to loopback
	default:
		c.Write([]byte{5, 8, 0, 1, 0, 0, 0, 0, 0, 0}) // address type not supported
		return
	}
	if _, err := io.ReadFull(c, b[:2]); err != nil { // port
		return
	}
	up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(b[0])<<8|int(b[1]))))
	if err != nil {
		c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	go io.Copy(up, c)
	io.Copy(c, up)
}

func startTestSOCKS5(t *testing.T) string { return newTestSOCKS5(t).addr }

// startBlackholeSOCKS accepts TCP and never speaks SOCKS: a local client port whose tunnel is dead.
func startBlackholeSOCKS(t *testing.T, hold time.Duration) string {
	t.Helper()
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
			go func() { time.Sleep(hold); c.Close() }()
		}
	}()
	return ln.Addr().String()
}

// closedLoopbackAddr returns a loopback host:port with no listener behind it.
func closedLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func statusServer(t *testing.T, code int) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

// slowStatusServer answers with code after delay, so the measured RTT is distinguishable
// from zero even on a coarse (Windows) clock.
func slowStatusServer(t *testing.T, code int, delay time.Duration) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestProbeViaSOCKS5OK(t *testing.T) {
	lat, err := probeViaSOCKS5(startTestSOCKS5(t), slowStatusServer(t, http.StatusNoContent, 60*time.Millisecond)+"/generate_204", 2*time.Second)
	if err != nil || lat < 40*time.Millisecond {
		t.Fatalf("probe = (%v, %v), want (>=40ms, nil)", lat, err)
	}
}

func TestProbeViaSOCKS5WrongStatus(t *testing.T) {
	// An error page / captive portal behind the tunnel is not a healthy exit.
	if _, err := probeViaSOCKS5(startTestSOCKS5(t), statusServer(t, http.StatusOK), 2*time.Second); err == nil {
		t.Fatal("status 200 accepted, want error")
	}
}

// Production dials hostnames through the exit (socks5h style): the name must reach
// the proxy as ATYP 0x03 and must not be resolved on the Hub.
func TestProbeViaSOCKS5SendsHostnameToProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newTestSOCKS5(t)

	// "probe.invalid" cannot be resolved locally (RFC 6761), so success proves the far side did it.
	target := "http://probe.invalid:" + u.Port() + "/generate_204"
	if _, err := probeViaSOCKS5(proxy.addr, target, 2*time.Second); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := proxy.receivedHosts(); len(got) != 1 || got[0] != "probe.invalid" {
		t.Fatalf("proxy received hosts %q, want [probe.invalid]", got)
	}
}

// C4: the local SOCKS port accepts TCP, but the tunnel behind it is dead.
func TestProbeViaSOCKS5DeadTunnel(t *testing.T) {
	addr := startBlackholeSOCKS(t, 3*time.Second)
	start := time.Now()
	if _, err := probeViaSOCKS5(addr, "https://www.gstatic.com/generate_204", time.Second); err == nil {
		t.Fatal("dead tunnel reported healthy")
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("probe took %v, timeout not enforced", d)
	}
}

// The hysteria client is not running at all: nothing listens on the local SOCKS port.
func TestProbeViaSOCKS5ClosedLocalPort(t *testing.T) {
	start := time.Now()
	_, err := probeViaSOCKS5(closedLoopbackAddr(t), "https://www.gstatic.com/generate_204", 5*time.Second)
	if err == nil {
		t.Fatal("closed SOCKS port reported healthy")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("closed port took %v to fail, want a fast refusal", d)
	}
}

func TestProbeViaSOCKS5ErrorDoesNotRepeatAddressOrURL(t *testing.T) {
	addr := closedLoopbackAddr(t)
	target := "https://www.gstatic.com/generate_204"
	_, err := probeViaSOCKS5(addr, target, time.Second)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), target) || strings.Contains(err.Error(), "generate_204") ||
		strings.Contains(err.Error(), addr) || strings.Contains(err.Error(), "gstatic") {
		t.Fatalf("error repeats the target URL or SOCKS address: %q", err)
	}
	// An unanswered SOCKS handshake collapses to one timeout phrase, without addresses.
	_, err = probeViaSOCKS5(startBlackholeSOCKS(t, 2*time.Second), target, 300*time.Millisecond)
	if err == nil || err.Error() != "timeout after 300ms" {
		t.Fatalf("timeout error = %v, want %q", err, "timeout after 300ms")
	}
}

func TestProbeExitFallsBackToSecondTarget(t *testing.T) {
	cases := []struct {
		name  string
		first string
	}{
		{"first returns 500", slowStatusServer(t, http.StatusInternalServerError, 300*time.Millisecond)},
		{"first refuses connection", "http://" + closedLoopbackAddr(t) + "/generate_204"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			second := slowStatusServer(t, http.StatusNoContent, 60*time.Millisecond) + "/generate_204"
			targets := []exitProbeTarget{{"first", tc.first}, {"second", second}}
			rtt, err := probeExitViaSOCKS5(startTestSOCKS5(t), targets, 2*time.Second)
			if err != nil {
				t.Fatalf("probe: %v (second target answers 204, exit must be healthy)", err)
			}
			// The RTT is the successful probe's own, not the failed first attempt's (300ms in the 500 case).
			if rtt < 40*time.Millisecond || rtt >= 250*time.Millisecond {
				t.Fatalf("rtt = %v, want the second probe's ~60ms", rtt)
			}
		})
	}
}

func TestProbeExitFirstTargetWinsWithoutTouchingSecond(t *testing.T) {
	var hits int
	var mu sync.Mutex
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(second.Close)
	targets := []exitProbeTarget{{"first", statusServer(t, http.StatusNoContent)}, {"second", second.URL}}
	if _, err := probeExitViaSOCKS5(startTestSOCKS5(t), targets, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("second target hit %d times, want 0 (sequential, stop at first 204)", hits)
	}
}

func TestProbeExitAllTargetsFailOneConciseLine(t *testing.T) {
	addr := startTestSOCKS5(t)
	targets := []exitProbeTarget{
		{"gstatic", statusServer(t, http.StatusInternalServerError)},
		{"cloudflare", statusServer(t, http.StatusOK)},
	}
	_, err := probeExitViaSOCKS5(addr, targets, 2*time.Second)
	if err == nil {
		t.Fatal("all targets failed but probe reported healthy")
	}
	msg := err.Error()
	want := "GET generate_204 (gstatic, cloudflare) failed: gstatic: status 500, want 204; cloudflare: status 200, want 204"
	if msg != want {
		t.Fatalf("error = %q\nwant    %q", msg, want)
	}
	if strings.Contains(msg, "\n") || strings.Contains(msg, addr) {
		t.Fatalf("error must be one line without the SOCKS address (the caller adds it once): %q", msg)
	}
}

// A dead exit must not cost more than len(targets) * perTarget per probe.
func TestProbeExitDeadTunnelIsBounded(t *testing.T) {
	addr := startBlackholeSOCKS(t, 3*time.Second)
	targets := []exitProbeTarget{{"a", "https://a.invalid/generate_204"}, {"b", "https://b.invalid/generate_204"}}
	start := time.Now()
	if _, err := probeExitViaSOCKS5(addr, targets, 400*time.Millisecond); err == nil {
		t.Fatal("dead tunnel reported healthy")
	}
	d := time.Since(start)
	if d < 700*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("two timed-out targets took %v, want ~800ms (2 x 400ms, sequential)", d)
	}
}

func TestProbeExitNoTargets(t *testing.T) {
	if _, err := probeExitViaSOCKS5(startTestSOCKS5(t), nil, time.Second); err == nil {
		t.Fatal("empty target list reported healthy")
	}
}

// step runs one monitor tick for a node the way checkAllTunnels does: evalExitHealth,
// then the counter reset the caller performs after a persisted status flip.
func step(status string, hc *nodeHealthCounter, probe string) (string, exitAlertAction) {
	newStatus, act := evalExitHealth(status, probe, hc)
	if newStatus != status {
		*hc = nodeHealthCounter{}
	}
	return newStatus, act
}

func TestEvalExitHealthHysteresisAndAlertActions(t *testing.T) {
	type tick struct {
		probe      string
		wantStatus string
		wantAction exitAlertAction
	}
	cases := []struct {
		name  string
		start string
		ticks []tick
	}{
		{
			name:  "healthy tolerates two failures then flips on the third, alert follows status every tick",
			start: "healthy",
			ticks: []tick{
				{"unhealthy", "healthy", exitAlertResolve},
				{"unhealthy", "healthy", exitAlertResolve},
				{"unhealthy", "unhealthy", exitAlertRaise}, // 3rd consecutive failure
				{"unhealthy", "unhealthy", exitAlertRaise}, // persists: raised AGAIN (manager dedups)
				{"unhealthy", "unhealthy", exitAlertRaise},
			},
		},
		{
			name:  "unhealthy needs two successes; one success keeps raising",
			start: "unhealthy",
			ticks: []tick{
				{"unhealthy", "unhealthy", exitAlertRaise},
				{"healthy", "unhealthy", exitAlertRaise}, // 1st ok: still down
				{"unhealthy", "unhealthy", exitAlertRaise},
				{"healthy", "unhealthy", exitAlertRaise}, // ok counter was reset by the failure
				{"healthy", "healthy", exitAlertResolve}, // 2 consecutive ok
				{"healthy", "healthy", exitAlertResolve}, // healthy: resolve every tick
			},
		},
		{
			name:  "flap: recovery then a new outage raises again",
			start: "unhealthy",
			ticks: []tick{
				{"healthy", "unhealthy", exitAlertRaise},
				{"healthy", "healthy", exitAlertResolve},
				{"unhealthy", "healthy", exitAlertResolve},
				{"unhealthy", "healthy", exitAlertResolve},
				{"unhealthy", "unhealthy", exitAlertRaise},
			},
		},
		{
			name:  "failures that are not consecutive never flip a healthy node",
			start: "healthy",
			ticks: []tick{
				{"unhealthy", "healthy", exitAlertResolve},
				{"unhealthy", "healthy", exitAlertResolve},
				{"healthy", "healthy", exitAlertResolve},
				{"unhealthy", "healthy", exitAlertResolve},
				{"unhealthy", "healthy", exitAlertResolve},
				{"healthy", "healthy", exitAlertResolve},
			},
		},
		{
			name:  "unknown: one failed probe does not alert or flip",
			start: "unknown",
			ticks: []tick{
				{"unhealthy", "unknown", exitAlertNone},
				{"unhealthy", "unknown", exitAlertNone},
				{"unhealthy", "unhealthy", exitAlertRaise}, // same 3-fail threshold
				{"unhealthy", "unhealthy", exitAlertRaise},
			},
		},
		{
			name:  "unknown becomes healthy on the first success",
			start: "unknown",
			ticks: []tick{
				{"unhealthy", "unknown", exitAlertNone},
				{"healthy", "healthy", exitAlertResolve},
			},
		},
		{
			name:  "empty status behaves like unknown",
			start: "",
			ticks: []tick{
				{"unhealthy", "unknown", exitAlertNone},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, hc := tc.start, &nodeHealthCounter{}
			for i, tk := range tc.ticks {
				gotStatus, gotAction := step(status, hc, tk.probe)
				if gotStatus != tk.wantStatus || gotAction != tk.wantAction {
					t.Fatalf("tick %d (probe %s from %q): got (%s, action %d), want (%s, action %d)",
						i+1, tk.probe, status, gotStatus, gotAction, tk.wantStatus, tk.wantAction)
				}
				// Mirrors the DB: "unknown" is stored for an empty status too.
				status = gotStatus
			}
		})
	}
}

func TestExitAlertTexts(t *testing.T) {
	node := biz.ExitNodeTunnel{ID: 2, Name: "NL-1", IPAddress: "89.124.89.126", TunnelProtocol: "hysteria2"}
	errLine := fmt.Sprintf("hysteria2 exit #%d via 127.0.0.1:1090: GET generate_204 (gstatic, cloudflare) failed: gstatic: timeout after 5s; cloudflare: timeout after 5s", node.ID)

	if got, want := exitDownDetails(node, errLine), "NL-1 (89.124.89.126): "+errLine; got != want {
		t.Fatalf("exitDownDetails = %q, want %q", got, want)
	}
	// Status unhealthy while this tick's probe passed (waiting for the 2nd success): no empty error.
	if got := exitDownDetails(node, ""); strings.HasSuffix(got, ": ") || !strings.Contains(got, "NL-1") {
		t.Fatalf("exitDownDetails with empty error = %q", got)
	}
	if got := exitUpMessage(node, 412); got != "Exit-нода NL-1 (#2) снова доступна, RTT 412 мс" {
		t.Fatalf("exitUpMessage = %q", got)
	}
	if got := exitUpMessage(node, 0); strings.Contains(got, "RTT") {
		t.Fatalf("exitUpMessage without a measurement must omit RTT, got %q", got)
	}
	if exitDownAlertKey(2) != "exit-down-2" {
		t.Fatalf("exitDownAlertKey(2) = %q", exitDownAlertKey(2))
	}
}
