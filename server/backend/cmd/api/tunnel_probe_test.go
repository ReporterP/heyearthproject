package main

import (
	"encoding/base64"
	"errors"
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
	return serveTestSOCKS5(t, ln)
}

// serveTestSOCKS5 runs testSOCKS5 on ln (closed at cleanup).
func serveTestSOCKS5(t *testing.T, ln net.Listener) *testSOCKS5 {
	t.Helper()
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

// step runs one monitor tick for one path the way exitPathState.fold does: evalExitHealth,
// then the counter reset after a status flip.
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

// ─────────────── Two paths per exit (phase 1) ───────────────

// «Exit мёртв, только если мертвы оба пути» and Review Focus 3, tick by tick.
func TestExitHealthTwoPaths(t *testing.T) {
	ok := func(ms int) pathProbe { return pathProbe{present: true, ok: true, rttMs: ms} }
	hyFail := pathProbe{present: true, err: "hysteria2 exit #2 via 127.0.0.1:1090: timeout after 5s"}
	vrFail := pathProbe{present: true, err: "tcp-reality exit #2 via 127.0.0.1:12002: EOF"}
	absent := pathProbe{}
	R, S, N := exitAlertRaise, exitAlertResolve, exitAlertNone
	type tick struct {
		p                           exitPathProbe
		status                      string
		exitAlert, hyAlert, vrAlert exitAlertAction
		hyChanged                   bool
	}
	cases := []struct {
		name  string
		db    string
		ticks []tick
	}{
		{"Review Focus 3 → UDP Россия→заграница задушен, TCP жив: exit жив, тревога про путь Hysteria", "healthy", []tick{
			{exitPathProbe{hyFail, ok(90)}, "healthy", S, S, S, false},
			{exitPathProbe{hyFail, ok(90)}, "healthy", S, S, S, false},
			{exitPathProbe{hyFail, ok(90)}, "healthy", S, R, S, true},
			{exitPathProbe{hyFail, ok(90)}, "healthy", S, R, S, false},
			{exitPathProbe{ok(40), ok(90)}, "healthy", S, R, S, false}, // one success is not a recovery
			{exitPathProbe{ok(40), ok(90)}, "healthy", S, S, S, true},
		}},
		{"оба пути мертвы: exit мёртв, говорит только тревога exit; вернулся по TCP", "healthy", []tick{
			{exitPathProbe{hyFail, vrFail}, "healthy", S, S, S, false},
			{exitPathProbe{hyFail, vrFail}, "healthy", S, S, S, false},
			{exitPathProbe{hyFail, vrFail}, "unhealthy", R, N, N, true},
			{exitPathProbe{hyFail, ok(90)}, "unhealthy", R, N, N, false},
			{exitPathProbe{hyFail, ok(90)}, "healthy", S, R, S, false},
		}},
		{"старый exit только с Hysteria: смерть пути — смерть exit", "healthy", []tick{
			{exitPathProbe{hyFail, absent}, "healthy", S, S, S, false},
			{exitPathProbe{hyFail, absent}, "healthy", S, S, S, false},
			{exitPathProbe{hyFail, absent}, "unhealthy", R, N, S, true},
		}},
		{"новый exit (unknown): первый успех любого пути делает его живым", "unknown", []tick{
			{exitPathProbe{hyFail, ok(80)}, "healthy", S, N, S, false},
			{exitPathProbe{ok(30), ok(80)}, "healthy", S, S, S, true},
		}},
		{"у exit нет ни одного пути", "healthy", []tick{
			{exitPathProbe{absent, absent}, "unhealthy", R, S, S, false},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h exitHealth
			db := tc.db
			for i, tk := range tc.ticks {
				v := h.tick(db, tk.p)
				if v.status != tk.status || v.exitAlert != tk.exitAlert || v.hyAlert != tk.hyAlert ||
					v.vrAlert != tk.vrAlert || v.hyChanged != tk.hyChanged {
					t.Fatalf("tick %d: got status=%s exit=%v hy=%v vr=%v hyChanged=%v, want %s %v %v %v %v",
						i+1, v.status, v.exitAlert, v.hyAlert, v.vrAlert, v.hyChanged,
						tk.status, tk.exitAlert, tk.hyAlert, tk.vrAlert, tk.hyChanged)
				}
				db = v.status // checkAllTunnels persists it; the next tick reads it back
			}
		})
	}
}

func TestExitHealthLatencyAndError(t *testing.T) {
	var h exitHealth
	hyFail := pathProbe{present: true, err: "hysteria2 exit #2 via 127.0.0.1:1090: timeout after 5s"}
	v := h.tick("healthy", exitPathProbe{hyFail, pathProbe{present: true, ok: true, rttMs: 90}})
	if v.latencyMs != 90 || !strings.Contains(v.errText, "timeout after 5s") {
		t.Fatalf("Hysteria down: latency %d (want the TCP RTT 90), error %q", v.latencyMs, v.errText)
	}
	both := exitPathProbe{pathProbe{present: true, ok: true, rttMs: 40}, pathProbe{present: true, ok: true, rttMs: 90}}
	if v = h.tick("healthy", both); v.latencyMs != 40 || v.errText != "" {
		t.Fatalf("both up: latency %d (want the Hysteria RTT 40), error %q", v.latencyMs, v.errText)
	}
	if snap := h.snapshot(both, time.Unix(0, 0)); snap.Hy != biz.PathHealthy || snap.HyRTTms != 40 || snap.VRRTTms != 90 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// probeExitPathsLive dials the exit's own loopback ports — Hysteria at 1088+id, TCP at xray's
// probe inbound 12000+id — and judges each path by itself; a path the exit lacks is not dialled.
func TestProbeExitPathsLive(t *testing.T) {
	const id = 497 // 127.0.0.1:1585 and :12497; skipped if something on this host holds them
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", exitSocksPort(id)))
	if err != nil {
		t.Skipf("Hysteria test port busy: %v", err)
	}
	serveTestSOCKS5(t, ln) // Hysteria path alive
	vrLn, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", exitProbePort(id)))
	if err != nil {
		t.Skipf("TCP probe test port busy: %v", err)
	}
	vrLn.Close() // TCP path dead: nothing listens on the probe inbound
	saved := exitProbeTargets
	exitProbeTargets = []exitProbeTarget{{"local", slowStatusServer(t, http.StatusNoContent, 20*time.Millisecond) + "/generate_204"}}
	t.Cleanup(func() { exitProbeTargets = saved })

	node := biz.ExitNodeTunnel{ID: id, TunnelProtocol: "hysteria2", TunnelUUID: "11111111-2222-3333-4444-555555555555",
		TunnelPubKey: base64.RawURLEncoding.EncodeToString(make([]byte, 32)), TunnelShortID: "abcd", FallbackEnabled: true}
	p := probeExitPathsLive(node)
	if !p.hy.present || !p.hy.ok || p.hy.rttMs < 1 || p.hy.err != "" {
		t.Errorf("Hysteria path = %+v, want present, ok, an RTT", p.hy)
	}
	if want := fmt.Sprintf("tcp-reality exit #%d via 127.0.0.1:%d: ", id, exitProbePort(id)); !p.vr.present || p.vr.ok || !strings.HasPrefix(p.vr.err, want) {
		t.Errorf("TCP path = %+v, want present, failed, error starting %q", p.vr, want)
	}

	// The owner switched the TCP fallback off: the path is absent (xray has no probe inbound for
	// it), not dead — never dialled, so the monitor resolves its alert as gone, not as recovered.
	node.FallbackEnabled = false
	if p = probeExitPathsLive(node); p.vr != (pathProbe{}) || !p.hy.ok {
		t.Errorf("switched-off TCP path: %+v, want it absent and not dialled", p)
	}
	node.FallbackEnabled = true

	node.TunnelUUID, node.TunnelPubKey = "", "" // no Reality credentials: no TCP path
	if p = probeExitPathsLive(node); p.vr != (pathProbe{}) || !p.hy.ok {
		t.Errorf("exit without a TCP path: %+v, want the TCP path absent and not dialled", p)
	}
}

// Someone else wrote tunnel_status: the node wizard activated a re-provisioned exit (online +
// healthy after checking both paths itself) whose paths the monitor last saw dead, or a re-setup
// reset it to unknown. The paths start over from the DB status, as after an API restart; reusing
// the old memory would flip the fresh exit back to unhealthy on its first tick (an xray restart
// and a false exit-down alert, against «one xray restart per added exit»).
func TestExitHealthReseedsOnStatusWrittenElsewhere(t *testing.T) {
	ok := pathProbe{present: true, ok: true, rttMs: 50}
	fail := pathProbe{present: true, err: "timeout after 5s"}
	for _, tc := range []struct {
		db        string
		hyChanged bool // the Hysteria path flips unless it was seeded healthy
	}{{"healthy", false}, {"unknown", true}, {"", true}} {
		t.Run("db="+tc.db, func(t *testing.T) {
			var h exitHealth
			st := "healthy"
			for range 3 {
				st = h.tick(st, exitPathProbe{fail, fail}).status
			}
			if st != biz.PathUnhealthy {
				t.Fatalf("setup: both paths dead for 3 ticks gave %q, want unhealthy", st)
			}
			v := h.tick(tc.db, exitPathProbe{ok, ok})
			if v.status != biz.PathHealthy || v.exitAlert != exitAlertResolve || v.hyChanged != tc.hyChanged {
				t.Fatalf("first tick after tunnel_status=%q: status=%s exit=%v hyChanged=%v, want healthy, resolve, hyChanged=%v",
					tc.db, v.status, v.exitAlert, v.hyChanged, tc.hyChanged)
			}
		})
	}
}

// A verdict that did not reach the DB is decided and written again on the next tick: the stale
// DB status is not taken for a write from elsewhere (no re-seed, the hysteresis is kept).
func TestExitHealthUnsavedVerdictIsRetried(t *testing.T) {
	fail := pathProbe{present: true, err: "timeout after 5s"}
	var h exitHealth
	var v exitVerdict
	for range 3 {
		v = h.tick("healthy", exitPathProbe{fail, fail})
	}
	if v.status != biz.PathUnhealthy {
		t.Fatalf("3 failures of both paths: %q, want unhealthy", v.status)
	}
	h.writeFailed("healthy") // UpdateTunnelStatusWithError failed: the DB still says healthy
	if v = h.tick("healthy", exitPathProbe{fail, fail}); v.status != biz.PathUnhealthy || v.exitAlert != exitAlertRaise {
		t.Fatalf("next tick after a failed write: status=%s exit=%v, want unhealthy + raise at once", v.status, v.exitAlert)
	}
}

// An exit that left the online list (deprovisioned, failed) is forgotten: its path view no longer
// stands for it in the admin and in Bamboo's choice, and when it comes back it starts afresh.
func TestForgetGoneExits(t *testing.T) {
	const gone, kept = 9001, 9002
	t.Cleanup(func() {
		for _, id := range []int{gone, kept} {
			exitHealthByID.Delete(id)
			biz.ForgetExitPaths(id)
		}
	})
	for _, id := range []int{gone, kept} {
		exitHealthFor(id).tick("healthy", exitPathProbe{})
		biz.SetExitPaths(id, biz.ExitPaths{Hy: biz.PathHealthy, VR: biz.PathHealthy})
	}
	forgetGoneExits([]biz.ExitNodeTunnel{{ID: kept}})
	if _, ok := exitHealthByID.Load(gone); ok {
		t.Error("monitor memory of a gone exit kept")
	}
	if _, ok := biz.GetExitPaths(gone); ok {
		t.Error("path view of a gone exit kept")
	}
	if _, ok := exitHealthByID.Load(kept); !ok {
		t.Error("monitor memory of an online exit dropped")
	}
	if _, ok := biz.GetExitPaths(kept); !ok {
		t.Error("path view of an online exit dropped")
	}
}

// withExitAlertHarness routes the test's RaiseAlert/ResolveAlert/ClearAlert to a synchronous
// harness (fake clock, fake sender, in-memory journal) and restores the global manager after.
func withExitAlertHarness(t *testing.T) *alertHarness {
	t.Helper()
	old := alerts
	t.Cleanup(func() { alerts = old })
	h := newAlertHarness()
	alerts = h.m
	return h
}

// alertLog is the harness journal as "kind key" entries.
func alertLog(h *alertHarness) string {
	var out []string
	for _, ev := range h.events {
		out = append(out, ev.Kind+" "+ev.Key)
	}
	return strings.Join(out, ", ")
}

// exitMonitorRun is one exit through tick + applyExitAlerts, persisting the status as
// checkAllTunnels does.
type exitMonitorRun struct {
	node biz.ExitNodeTunnel
	h    exitHealth
	db   string
}

func (r *exitMonitorRun) tick(p exitPathProbe) {
	v := r.h.tick(r.db, p)
	applyExitAlerts(r.node, v, p)
	r.db = v.status
}

// Review Focus 3 at the alert manager: the Hysteria path's own key is raised once, never the
// exit's or the TCP path's, and resolved after the hysteresis with the path's recovery text.
func TestApplyExitAlertsHysteriaPathDown(t *testing.T) {
	ah := withExitAlertHarness(t)
	ok := func(ms int) pathProbe { return pathProbe{present: true, ok: true, rttMs: ms} }
	hyFail := pathProbe{present: true, err: "hysteria2 exit #2 via 127.0.0.1:1090: timeout after 5s"}
	r := &exitMonitorRun{node: biz.ExitNodeTunnel{ID: 2, Name: "Амстердам", IPAddress: "89.124.89.126"}, db: "healthy"}
	for range 3 {
		r.tick(exitPathProbe{hyFail, ok(90)})
	}
	if got := alertLog(ah); got != "raise exit-path-down-2-hy" {
		t.Fatalf("after 3 ticks with Hysteria dead, TCP alive: journal %q, want only the raise of exit-path-down-2-hy", got)
	}
	if len(ah.sent) != 1 || !strings.Contains(ah.sent[0], "запасному TCP-пути") || !strings.Contains(ah.sent[0], "timeout after 5s") {
		t.Fatalf("sent %q, want the Hysteria path message with the probe error", ah.sent)
	}
	for range 2 {
		r.tick(exitPathProbe{ok(40), ok(90)})
	}
	if got, want := alertLog(ah), "raise exit-path-down-2-hy, resolve exit-path-down-2-hy"; got != want {
		t.Fatalf("after 2 OK ticks: journal %q, want %q", got, want)
	}
	if len(ah.sent) != 2 || !strings.Contains(ah.sent[1], "путь Hysteria снова работает") {
		t.Fatalf("sent %q, want the Hysteria path recovery", ah.sent)
	}
}

// A path that goes away (protocol / credentials changed; in phase 2 the TCP-fallback switch) closes
// its alert with a text that does not claim the path works again.
func TestApplyExitAlertsPathRemoved(t *testing.T) {
	ok := pathProbe{present: true, ok: true, rttMs: 40}
	fail := pathProbe{present: true, err: "EOF"}
	for _, tc := range []struct {
		path               string
		down, gone         exitPathProbe
		wantText, notThere string
	}{
		{"hy", exitPathProbe{fail, ok}, exitPathProbe{pathProbe{}, ok}, "путь Hysteria отключён — тревога снята", "снова работает"},
		{"vr", exitPathProbe{ok, fail}, exitPathProbe{ok, pathProbe{}}, "запасной TCP-путь отключён — тревога снята", "снова работает"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			ah := withExitAlertHarness(t)
			r := &exitMonitorRun{node: biz.ExitNodeTunnel{ID: 2, Name: "Амстердам"}, db: "healthy"}
			for range 3 {
				r.tick(tc.down)
			}
			r.tick(tc.gone)
			key := exitPathAlertKey(2, tc.path)
			if got, want := alertLog(ah), "raise "+key+", resolve "+key; got != want {
				t.Fatalf("journal %q, want %q", got, want)
			}
			if len(ah.sent) != 2 || !strings.Contains(ah.sent[1], tc.wantText) || strings.Contains(ah.sent[1], tc.notThere) {
				t.Fatalf("closing message %q, want %q and no %q", ah.sent[len(ah.sent)-1], tc.wantText, tc.notThere)
			}
		})
	}
}

// A forgotten exit's open alerts are closed without a message: it left the pool, it did not recover.
func TestForgetGoneExitsClearsAlertsSilently(t *testing.T) {
	ah := withExitAlertHarness(t)
	const id = 9003
	t.Cleanup(func() { exitHealthByID.Delete(id); biz.ForgetExitPaths(id) })
	exitHealthFor(id)
	RaiseAlert(exitDownAlertKey(id), AlertWarn, "Exit-нода недоступна", "d")
	RaiseAlert(exitPathAlertKey(id, "hy"), AlertWarn, "путь Hysteria недоступен", "d")
	forgetGoneExits(nil)
	want := "raise exit-down-9003, raise exit-path-down-9003-hy, cleared exit-down-9003, cleared exit-path-down-9003-hy"
	if got := alertLog(ah); got != want {
		t.Fatalf("journal %q, want %q", got, want)
	}
	if len(ah.sent) != 2 {
		t.Fatalf("sent %q, want only the two raises (no recovery message)", ah.sent)
	}
}

// The status write is conditional on the status read at the start of the tick (saveExitStatus →
// biz.UpdateTunnelStatusIfUnchanged): only a written flip resyncs xray; a write from elsewhere
// stands and the next tick starts over from it; a DB error is retried without a re-seed.
func TestPersistExitVerdict(t *testing.T) {
	saved := saveExitStatus
	t.Cleanup(func() { saveExitStatus = saved })
	fail := pathProbe{present: true, err: "timeout after 5s"}
	ok := pathProbe{present: true, ok: true, rttMs: 50}
	node := biz.ExitNodeTunnel{ID: 2, Name: "Амстердам", TunnelStatus: "healthy"}
	dead := func() (*exitHealth, exitVerdict) { // both paths dead 3 ticks: verdict unhealthy, DB still healthy
		var h exitHealth
		var v exitVerdict
		for range 3 {
			v = h.tick("healthy", exitPathProbe{fail, fail})
		}
		return &h, v
	}
	type call struct {
		id                int
		old, status, errT string
	}
	var calls []call
	answer := func(written bool, err error) {
		calls = nil
		saveExitStatus = func(id int, old, status string, _ int, errText string) (bool, error) {
			calls = append(calls, call{id, old, status, errText})
			return written, err
		}
	}

	answer(true, nil)
	h, v := dead()
	if !persistExitVerdict(h, node, v) {
		t.Error("written flip healthy → unhealthy not reported")
	}
	if len(calls) != 1 || calls[0].id != 2 || calls[0].old != "healthy" || calls[0].status != "unhealthy" || !strings.Contains(calls[0].errT, "timeout") {
		t.Errorf("write calls %+v, want one conditional write of unhealthy over healthy", calls)
	}
	if persistExitVerdict(h, node, exitVerdict{status: "healthy"}) {
		t.Error("written, unchanged status reported as a flip")
	}

	t.Run("someone else wrote the status meanwhile", func(t *testing.T) {
		answer(false, nil)
		h, v := dead()
		if persistExitVerdict(h, node, v) {
			t.Fatal("an unwritten verdict reported as a flip (would resync xray over the other write)")
		}
		// the re-setup's unknown stands: the next tick starts from it (first success → healthy)
		if v := h.tick("unknown", exitPathProbe{ok, ok}); v.status != biz.PathHealthy {
			t.Fatalf("next tick after the other write: %q, want healthy", v.status)
		}
	})
	t.Run("DB error", func(t *testing.T) {
		answer(false, errors.New("connection refused"))
		h, v := dead()
		if persistExitVerdict(h, node, v) {
			t.Fatal("a failed write reported as a flip")
		}
		// the DB still says healthy: the next tick decides unhealthy again at once and writes again
		if v := h.tick("healthy", exitPathProbe{fail, fail}); v.status != biz.PathUnhealthy {
			t.Fatalf("next tick after a failed write: %q, want unhealthy at once", v.status)
		}
	})
}

func TestExitPathAlertTexts(t *testing.T) {
	node := biz.ExitNodeTunnel{ID: 2, Name: "Амстердам", IPAddress: "89.124.89.126"}
	if exitPathAlertKey(2, "hy") != "exit-path-down-2-hy" || exitPathAlertKey(20, "vr") != "exit-path-down-20-vr" {
		t.Fatal("unexpected path alert keys")
	}
	title, details := exitPathDownText(node, "hy", "hysteria2 exit #2 via 127.0.0.1:1090: timeout after 5s")
	for _, want := range []string{"Hysteria", "запасному TCP-пути", "Бамбук", "hysteria2-client-2", "timeout after 5s"} {
		if !strings.Contains(title+details, want) {
			t.Errorf("Hysteria path alert lacks %q: %s / %s", want, title, details)
		}
	}
	title, details = exitPathDownText(node, "vr", "")
	for _, want := range []string{"TCP", "10443", "ждём подтверждения"} {
		if !strings.Contains(title+details, want) {
			t.Errorf("TCP path alert lacks %q: %s / %s", want, title, details)
		}
	}
	if !strings.Contains(exitPathUpText(node, "vr"), "снова работает") {
		t.Error("recovery text")
	}
}

// MTProxy the owner switched off (mtproto_status = 'disabled') is not probed: a health verdict
// would turn it back into healthy/unhealthy, i.e. hand it out to users again.
func TestMTProtoHealthSkipsDisabled(t *testing.T) {
	q := strings.Join(strings.Fields(mtprotoHealthQuery), " ")
	for _, want := range []string{"mtproto_status <> 'disabled'", "mtproto_installed_at IS NOT NULL", "lifecycle_state = 'online'"} {
		if !strings.Contains(q, want) {
			t.Errorf("query lacks %q: %s", want, q)
		}
	}
}
