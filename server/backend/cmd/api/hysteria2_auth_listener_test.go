package main

import (
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

// startAuthOnly serves the production auth-only server (serveHysteria2Auth) with handler h on
// a fresh loopback port and returns its base URL.
func startAuthOnly(t *testing.T, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := serveHysteria2Auth(ln, h)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

// startMain serves the production nginx-facing server (newMainHTTPServer) with handler h.
func startMain(t *testing.T, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newMainHTTPServer(ln.Addr().String(), h)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

func doReq(t *testing.T, method, url, body string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

const defaultMuxSentinel = "/__hysteria2_auth_listener_test_sentinel"

var registerSentinel sync.Once

func TestHysteria2AuthListenerServesOnlyAuth(t *testing.T) {
	// A route on http.DefaultServeMux (where main registers the whole API) must not be
	// reachable through the auth-only listener.
	registerSentinel.Do(func() {
		http.HandleFunc(defaultMuxSentinel, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "default-mux") })
	})

	var hits int
	var mu sync.Mutex
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		io.WriteString(w, "auth")
	})
	base := startAuthOnly(t, stub)

	if code, body := doReq(t, http.MethodPost, base+hysteria2AuthPath, `{}`, nil); code != 200 || body != "auth" {
		t.Fatalf("auth path: %d %q, want 200 \"auth\"", code, body)
	}
	for _, p := range []string{
		"/", defaultMuxSentinel, "/api/v1/health", "/api/admin/login", "/api/v1/auth/login",
		hysteria2AuthPath + "/", hysteria2AuthPath + "x", "/internal/hysteria2", "/internal/hysteria2/../hysteria2/auth",
		"//internal/hysteria2/auth", "/INTERNAL/hysteria2/auth",
	} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			code, body := doReq(t, m, base+p, `{}`, nil)
			if code != http.StatusNotFound || strings.Contains(body, "default-mux") {
				t.Errorf("%s %s: %d %q, want 404", m, p, code, body)
			}
		}
	}
	mu.Lock()
	if hits != 1 {
		t.Errorf("the auth handler ran %d times, want 1 (only for the auth path)", hits)
	}
	mu.Unlock()

	// The production handler behind the listener: a short password is refused without the
	// database, as Hysteria2 expects it (200, ok:false).
	realBase := startAuthOnly(t, handleHysteria2Auth)
	if code, body := doReq(t, http.MethodPost, realBase+hysteria2AuthPath, `{"auth":"x","addr":"192.0.2.10:1234","tx":0}`, nil); code != 200 || strings.TrimSpace(body) != `{"ok":false}` {
		t.Errorf("real handler on the auth listener: %d %q", code, body)
	}
	if code, _ := doReq(t, http.MethodPost, realBase+"/api/v1/health", `{}`, nil); code != http.StatusNotFound {
		t.Errorf("real auth listener served /api/v1/health: %d", code)
	}
}

// The auth listener never honours proxy headers (its only client is hysteria-server, which
// sends none); the nginx-facing main listener does.
func TestHysteria2AuthListenerNeverTrustsProxyHeaders(t *testing.T) {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, middleware.ExtractIP(r)) })
	spoofed := map[string]string{"X-Real-IP": "203.0.113.7", "X-Forwarded-For": "203.0.113.8"}

	if _, got := doReq(t, http.MethodPost, startAuthOnly(t, echo)+hysteria2AuthPath, `{}`, spoofed); got != "127.0.0.1" {
		t.Errorf(":8001-style auth listener: ExtractIP = %q, want the peer 127.0.0.1 — proxy headers must never be trusted there", got)
	}
	if _, got := doReq(t, http.MethodGet, startMain(t, echo)+"/", "", spoofed); got != "203.0.113.7" {
		t.Errorf(":8000-style main listener: ExtractIP = %q, want the nginx X-Real-IP 203.0.113.7", got)
	}
}

func TestHysteria2AuthServerConfig(t *testing.T) {
	srv := newHysteria2AuthServer(http.NotFoundHandler())
	if srv.Addr != "127.0.0.1:8001" {
		t.Errorf("auth listener address %q, want 127.0.0.1:8001 (loopback only)", srv.Addr)
	}
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.WriteTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 {
		t.Errorf("auth listener needs all timeouts and a header cap: %+v", srv)
	}
	if srv.BaseContext != nil {
		t.Errorf("auth listener must not set a BaseContext (that is how proxy-header trust is granted)")
	}
	if srv.Handler == nil {
		t.Errorf("a nil Handler would fall back to http.DefaultServeMux, i.e. the whole API")
	}
	if mainSrv := newMainHTTPServer("127.0.0.1:8000", nil); mainSrv.BaseContext == nil {
		t.Errorf("the main listener must grant proxy-header trust via BaseContext")
	}
}

func TestHysteria2AuthLimitKey(t *testing.T) {
	for addr, want := range map[string]string{
		"198.51.100.1:443":         "198.51.100.1",
		" 198.51.100.1:1 ":         "198.51.100.1",
		"[2001:DB8::1]:443":        "2001:db8::1",
		"[::ffff:198.51.100.1]:80": "198.51.100.1",
		"198.51.100.1":             hysteria2AuthNoAddrKey,
		"host.example:443":         hysteria2AuthNoAddrKey,
		"":                         hysteria2AuthNoAddrKey,
	} {
		if got := hysteria2AuthLimitKey(addr); got != want {
			t.Errorf("hysteria2AuthLimitKey(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestRenderHysteria2ConfigAuthURL(t *testing.T) {
	if hysteria2AuthURL != "http://127.0.0.1:8001/internal/hysteria2/auth" {
		t.Fatalf("hysteria2AuthURL = %q", hysteria2AuthURL)
	}
	yaml := renderHysteria2Config(50, 100, "obfs-pass", "stats-secret", 1090, []string{"138.16.181.225"})
	if !strings.Contains(yaml, "auth:\n  type: http\n  http:\n    url: http://127.0.0.1:8001/internal/hysteria2/auth\n") {
		t.Errorf("server.yaml must point the auth callback at the :8001 listener:\n%s", yaml)
	}
	if strings.Contains(yaml, ":8000") {
		t.Errorf("server.yaml still mentions the main API listener :8000:\n%s", yaml)
	}
}

func TestHysteria2ACLExplicitPrivateRanges(t *testing.T) {
	hub := []string{"138.16.181.225", "2001:db8::2"}
	rules := hysteria2ACLRules(hub)
	wantHead := []string{
		"reject(geoip:private)", "reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)",
		"reject(10.0.0.0/8)", "reject(172.16.0.0/12)", "reject(192.168.0.0/16)", "reject(100.64.0.0/10)",
		"reject(169.254.0.0/16)", "reject(fc00::/7)", "reject(fe80::/10)",
	}
	wantTail := []string{
		"via_exit(138.16.181.225, tcp/80)", "via_exit(138.16.181.225, tcp/443)", "reject(138.16.181.225)",
		"via_exit(2001:db8::2, tcp/80)", "via_exit(2001:db8::2, tcp/443)", "reject(2001:db8::2)",
		"direct(geoip:ru)", "via_exit(all)",
	}
	if want := append(append([]string{}, wantHead...), wantTail...); !reflect.DeepEqual(rules, want) {
		t.Fatalf("ACL rules:\n got %v\nwant %v", rules, want)
	}
	yaml := renderHysteria2Config(50, 100, "obfs-pass", "stats-secret", 1090, hub)
	if !strings.Contains(yaml, "  inline:\n    - "+strings.Join(append(append([]string{}, wantHead...), wantTail...), "\n    - ")+"\n") {
		t.Errorf("rendered ACL does not carry the rules in order:\n%s", yaml)
	}
}

// A listener that dies under the server (Serve returns anything but http.ErrServerClosed)
// raises the alert; a deliberate Close does not.
func TestHysteria2AuthListenerDeathAlerts(t *testing.T) {
	died := make(chan error, 4)
	orig := hysteria2AuthListenerDied
	hysteria2AuthListenerDied = func(_ net.Addr, err error) { died <- err }
	t.Cleanup(func() { hysteria2AuthListenerDied = orig })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := serveHysteria2Auth(ln, http.NotFoundHandler())
	defer srv.Close()
	ln.Close() // the socket goes away under a running server
	select {
	case err := <-died:
		if err == nil {
			t.Fatal("alert raised with a nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a dead auth listener raised no alert")
	}

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv2 := serveHysteria2Auth(ln2, http.NotFoundHandler())
	srv2.Close()
	select {
	case err := <-died:
		t.Fatalf("a deliberate Close raised the alert: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}
