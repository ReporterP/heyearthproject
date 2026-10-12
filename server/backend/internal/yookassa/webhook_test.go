package yookassa

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

const (
	ykIP     = "185.71.76.5"  // inside 185.71.76.0/27
	ykIPv6   = "2a02:5180::1" // inside 2a02:5180::/32
	attacker = "203.0.113.9"
)

func TestIsFromYooKassa(t *testing.T) {
	tests := []struct {
		name       string
		nginx      bool // request accepted by the nginx-facing (TrustProxyHeaders) listener
		remote     string
		headers    map[string]string
		allowLocal bool
		want       bool
	}{
		// nginx forwarding a known client address (X-Real-IP / appended to XFF): what it does on
		// port 80, and on 443 once the stream SNI hop to :8443 carries the client address
		{"YooKassa via nginx (X-Real-IP)", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": ykIP, "X-Forwarded-For": ykIP}, false, true},
		{"YooKassa IPv6 via nginx", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": ykIPv6}, false, true},
		{"YooKassa via nginx, XFF only (right-most entry)", true, "127.0.0.1:41000", map[string]string{"X-Forwarded-For": attacker + ", " + ykIP}, false, true},
		{"attacker via nginx", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": attacker, "X-Forwarded-For": attacker}, false, false},
		// the bypass: client-written left-most XFF entry carrying a YooKassa address, nginx
		// appends the attacker's real address and sets X-Real-IP to it
		{"spoofed left-most XFF via nginx", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": attacker, "X-Forwarded-For": ykIP + ", " + attacker}, false, false},
		{"spoofed left-most XFF via nginx, no X-Real-IP", true, "127.0.0.1:41000", map[string]string{"X-Forwarded-For": ykIP + ", " + attacker}, false, false},
		// a non-loopback peer cannot be nginx: its headers are ignored even on the trusted listener
		{"spoofed headers from a non-loopback peer", true, attacker + ":5000", map[string]string{"X-Real-IP": ykIP, "X-Forwarded-For": ykIP}, false, false},
		{"spoofed headers from a non-loopback peer, untrusted listener", false, attacker + ":5000", map[string]string{"X-Real-IP": ykIP, "X-Forwarded-For": ykIP}, false, false},
		// a loopback peer on any other listener is not nginx either
		{"spoofed headers on a non-nginx listener", false, "127.0.0.1:41000", map[string]string{"X-Real-IP": ykIP, "X-Forwarded-For": ykIP}, false, false},
		// a direct connection from YooKassa's own address
		{"direct peer in the whitelist", false, ykIP + ":443", nil, false, true},
		// Production topology today: 443 → nginx stream SNI router → 127.0.0.1:8443 without the
		// client address, so nginx reports EVERY client as 127.0.0.1. Such a request is never
		// "local", whatever the flag says — and it is not in the whitelist either.
		{"prod topology, allow-local on: any client via nginx", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": "127.0.0.1", "X-Forwarded-For": "127.0.0.1"}, true, false},
		{"prod topology, allow-local on: X-Real-IP 127.0.0.1 only", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": "127.0.0.1"}, true, false},
		{"prod topology, allow-local on: XFF 127.0.0.1 only", true, "127.0.0.1:41000", map[string]string{"X-Forwarded-For": "127.0.0.1"}, true, false},
		{"prod topology, allow-local on, unmarked listener", false, "127.0.0.1:41000", map[string]string{"X-Real-IP": "127.0.0.1"}, true, false},
		{"prod topology, allow-local off", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": "127.0.0.1", "X-Forwarded-For": "127.0.0.1"}, false, false},
		{"allow-local on: empty proxy header still means proxied", true, "127.0.0.1:41000", map[string]string{"X-Forwarded-For": ""}, true, false},
		{"allow-local on: internet client via nginx", true, "127.0.0.1:41000", map[string]string{"X-Real-IP": attacker, "X-Forwarded-For": attacker}, true, false},
		// YOOKASSA_WEBHOOK_ALLOW_LOCAL: only a direct curl on the Hub (loopback peer, no proxy headers)
		{"allow-local: direct local curl", false, "127.0.0.1:41000", nil, true, true},
		{"allow-local: direct local curl over IPv6", false, "[::1]:41000", nil, true, true},
		{"allow-local: direct local curl to the nginx-facing listener", true, "127.0.0.1:41000", nil, true, true},
		{"allow-local: non-loopback peer without headers", false, attacker + ":5000", nil, true, false},
		{"allow-local off: direct local curl", false, "127.0.0.1:41000", nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.allowLocal {
				t.Setenv("YOOKASSA_WEBHOOK_ALLOW_LOCAL", "true")
			} else {
				t.Setenv("YOOKASSA_WEBHOOK_ALLOW_LOCAL", "")
			}
			r := httptest.NewRequest(http.MethodPost, "/api/webhooks/yookassa", nil)
			r.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if tc.nginx {
				r = r.WithContext(middleware.TrustProxyHeaders(r.Context()))
			}
			if got := IsFromYooKassa(r); got != tc.want {
				t.Fatalf("IsFromYooKassa = %v, want %v", got, tc.want)
			}
		})
	}
}

// The same check behind real servers: the nginx-facing one (BaseContext =
// middleware.TrustedProxyBaseContext, as the :8000 listener) and an unmarked one.
func TestIsFromYooKassaPerListener(t *testing.T) {
	t.Setenv("YOOKASSA_WEBHOOK_ALLOW_LOCAL", "")
	check := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strconv.FormatBool(IsFromYooKassa(r))))
	})
	serve := func(base func(net.Listener) context.Context) string {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		srv := &http.Server{Handler: check, BaseContext: base, ReadHeaderTimeout: 5 * time.Second}
		go srv.Serve(ln)
		t.Cleanup(func() { srv.Close() })
		return "http://" + ln.Addr().String()
	}
	post := func(url string, h map[string]string) string {
		req, _ := http.NewRequest(http.MethodPost, url, nil)
		for k, v := range h {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	nginxFacing, other := serve(middleware.TrustedProxyBaseContext), serve(nil)

	if got := post(nginxFacing, map[string]string{"X-Real-IP": ykIP, "X-Forwarded-For": ykIP}); got != "true" {
		t.Errorf("YooKassa via the nginx-facing listener: %s, want true", got)
	}
	if got := post(nginxFacing, map[string]string{"X-Real-IP": attacker, "X-Forwarded-For": ykIP + ", " + attacker}); got != "false" {
		t.Errorf("spoofed left-most XFF via the nginx-facing listener: %s, want false", got)
	}
	if got := post(other, map[string]string{"X-Real-IP": ykIP, "X-Forwarded-For": ykIP}); got != "false" {
		t.Errorf("spoofed headers on an unmarked listener: %s, want false", got)
	}
}
