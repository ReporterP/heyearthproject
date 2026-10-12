package middleware

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractIP(t *testing.T) {
	const spoof = "203.0.113.7"
	tests := []struct {
		name    string
		trusted bool
		remote  string
		headers map[string][]string
		want    string
	}{
		{"untrusted listener ignores X-Real-IP", false, "127.0.0.1:5000", map[string][]string{"X-Real-IP": {spoof}}, "127.0.0.1"},
		{"untrusted listener ignores X-Forwarded-For", false, "127.0.0.1:5000", map[string][]string{"X-Forwarded-For": {spoof}}, "127.0.0.1"},
		{"untrusted listener, remote peer", false, "198.51.100.4:5000", map[string][]string{"X-Real-IP": {spoof}}, "198.51.100.4"},
		{"trusted listener, loopback peer: X-Real-IP", true, "127.0.0.1:5000", map[string][]string{"X-Real-IP": {spoof}}, spoof},
		{"trusted listener, IPv6 loopback peer", true, "[::1]:5000", map[string][]string{"X-Real-IP": {" " + spoof + " "}}, spoof},
		{"trusted listener, non-loopback peer: headers ignored", true, "198.51.100.4:5000", map[string][]string{"X-Real-IP": {spoof}, "X-Forwarded-For": {spoof}}, "198.51.100.4"},
		{"X-Real-IP wins over X-Forwarded-For", true, "127.0.0.1:5000", map[string][]string{"X-Real-IP": {spoof}, "X-Forwarded-For": {"192.0.2.1, 192.0.2.2"}}, spoof},
		{"X-Forwarded-For: right-most entry (nginx-appended), not the client's left-most", true, "127.0.0.1:5000", map[string][]string{"X-Forwarded-For": {"10.9.9.9, 192.0.2.2"}}, "192.0.2.2"},
		{"X-Forwarded-For over several header lines", true, "127.0.0.1:5000", map[string][]string{"X-Forwarded-For": {"10.9.9.9", "192.0.2.3 ,"}}, "192.0.2.3"},
		{"garbage X-Real-IP falls through to X-Forwarded-For", true, "127.0.0.1:5000", map[string][]string{"X-Real-IP": {"evil"}, "X-Forwarded-For": {"192.0.2.4"}}, "192.0.2.4"},
		{"garbage headers fall back to the peer", true, "127.0.0.1:5000", map[string][]string{"X-Real-IP": {"evil"}, "X-Forwarded-For": {"1.2.3.4, nope"}}, "127.0.0.1"},
		{"IPv6 client canonicalised", true, "127.0.0.1:5000", map[string][]string{"X-Real-IP": {"2001:DB8:0::1"}}, "2001:db8::1"},
		{"no headers", true, "127.0.0.1:5000", nil, "127.0.0.1"},
		{"RemoteAddr without port is kept as is", false, "not-an-addr", map[string][]string{"X-Real-IP": {spoof}}, "not-an-addr"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			for k, vs := range tc.headers {
				for _, v := range vs {
					r.Header.Add(k, v)
				}
			}
			if tc.trusted {
				r = r.WithContext(TrustProxyHeaders(r.Context()))
			}
			if got := ExtractIP(r); got != tc.want {
				t.Fatalf("ExtractIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// serve starts a real HTTP server on a fresh loopback port and returns its base URL.
func serve(t *testing.T, base func(net.Listener) context.Context, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, BaseContext: base, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

func get(t *testing.T, url string, header map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
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

// The same handler behind the two kinds of listener: the nginx-facing one (BaseContext =
// TrustedProxyBaseContext) honours the proxy headers, an unmarked one never does.
func TestExtractIPPerListener(t *testing.T) {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, ExtractIP(r)) })
	nginxFacing := serve(t, TrustedProxyBaseContext, echo)
	authOnly := serve(t, nil, echo)

	spoofed := map[string]string{"X-Real-IP": "203.0.113.7", "X-Forwarded-For": "203.0.113.8"}
	if _, got := get(t, nginxFacing, spoofed); got != "203.0.113.7" {
		t.Errorf("nginx-facing listener: ExtractIP = %q, want the X-Real-IP 203.0.113.7", got)
	}
	if _, got := get(t, nginxFacing, map[string]string{"X-Forwarded-For": "10.0.0.1, 203.0.113.8"}); got != "203.0.113.8" {
		t.Errorf("nginx-facing listener: ExtractIP = %q, want the right-most X-Forwarded-For entry", got)
	}
	if _, got := get(t, authOnly, spoofed); got != "127.0.0.1" {
		t.Errorf("unmarked listener: ExtractIP = %q, want the peer 127.0.0.1 (headers must be ignored)", got)
	}
}

// A client that rotates X-Real-IP must not get a fresh bucket per request on an unmarked
// listener; behind the nginx-facing one, distinct client IPs do get distinct buckets.
func TestMiddlewareHeaderRotation(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })

	untrusted := serve(t, nil, NewRateLimiter(2, time.Minute).Middleware(ok))
	codes := []int{}
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		code, _ := get(t, untrusted, map[string]string{"X-Real-IP": ip})
		codes = append(codes, code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Errorf("unmarked listener, rotating X-Real-IP: codes %v, want [200 200 429]", codes)
	}

	trusted := serve(t, TrustedProxyBaseContext, NewRateLimiter(2, time.Minute).Middleware(ok))
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if code, _ := get(t, trusted, map[string]string{"X-Real-IP": ip}); code != 200 {
			t.Errorf("nginx-facing listener: client %s got %d, want 200 (own bucket)", ip, code)
		}
	}
	for i := 0; i < 2; i++ {
		get(t, trusted, map[string]string{"X-Real-IP": "192.0.2.9"})
	}
	if code, _ := get(t, trusted, map[string]string{"X-Real-IP": "192.0.2.9"}); code != http.StatusTooManyRequests {
		t.Errorf("nginx-facing listener: third request of one client got %d, want 429", code)
	}
}
