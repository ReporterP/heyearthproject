package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/middleware"
)

const authLoopback = "127.0.0.1:40000"

// testAuthHandler builds the callback handler with the given limits and a fake validator that
// accepts every password starting with "good". validated records what reached the "database".
func testAuthHandler(perCredential, failures int) (http.HandlerFunc, *[]string) {
	validated := &[]string{}
	validate := func(pw string) biz.Hysteria2AuthResult {
		*validated = append(*validated, pw)
		return biz.Hysteria2AuthResult{OK: strings.HasPrefix(pw, "good"), UserID: "u-" + pw}
	}
	h := newHysteria2AuthHandler(
		middleware.NewRateLimiter(perCredential, time.Minute),
		middleware.NewRateLimiter(failures, time.Minute),
		validate)
	return h, validated
}

func callAuth(h http.HandlerFunc, remote, auth, addr string) (int, string) {
	body, _ := json.Marshal(map[string]any{"auth": auth, "addr": addr, "tx": 0})
	r := httptest.NewRequest(http.MethodPost, hysteria2AuthPath, strings.NewReader(string(body)))
	r.RemoteAddr = remote
	w := httptest.NewRecorder()
	h(w, r)
	return w.Code, strings.TrimSpace(w.Body.String())
}

func countOf(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// I-1: a client with a stale password retrying on every dial must not lock out a valid user
// behind the same address (home Wi-Fi, CGNAT). Production limits.
func TestHysteria2AuthStalePasswordDoesNotLockOutNeighbours(t *testing.T) {
	h, validated := testAuthHandler(hysteria2AuthRateLimit, hysteria2AuthFailLimit)
	const ip = "198.51.100.1"

	limited := 0
	for i := 0; i < 2*hysteria2AuthRateLimit; i++ {
		code, body := callAuth(h, authLoopback, "stale-password", ip+":"+itoa(20000+i))
		switch {
		case code == http.StatusTooManyRequests && body == `{"ok":false}`:
			limited++
		case code != 200 || body != `{"ok":false}`:
			t.Fatalf("stale callback %d: %d %s", i, code, body)
		}
		// the neighbour on the same address logs in every few dials of the stale client
		if i%10 == 0 {
			if code, body := callAuth(h, authLoopback, "good-neighbour", ip+":"+itoa(40000+i)); code != 200 || body != `{"id":"u-good-neighbour","ok":true}` {
				t.Fatalf("valid user behind the same IP after %d stale dials: %d %s", i, code, body)
			}
		}
	}
	if limited != hysteria2AuthRateLimit {
		t.Errorf("stale client limited %d times, want %d (its own bucket only)", limited, hysteria2AuthRateLimit)
	}
	if n := countOf(*validated, "stale-password"); n != hysteria2AuthRateLimit {
		t.Errorf("stale password reached the database %d times, want %d", n, hysteria2AuthRateLimit)
	}
	// the same password from another address has its own bucket
	if code, _ := callAuth(h, authLoopback, "stale-password", "198.51.100.2:1"); code != 200 {
		t.Errorf("same credential from another IP: %d, want 200", code)
	}
}

// The per-IP cap counts failed validations only; successes never consume it. Once spent, every
// callback from that IP is refused without touching the database; other IPs are unaffected.
func TestHysteria2AuthFailureCapCountsOnlyFailures(t *testing.T) {
	h, validated := testAuthHandler(1000, 3)
	const a, b = "198.51.100.10:1", "198.51.100.11:1"

	for i := 0; i < 20; i++ {
		if code, body := callAuth(h, authLoopback, "good-"+itoa(i), a); code != 200 || !strings.Contains(body, `"ok":true`) {
			t.Fatalf("success %d: %d %s", i, code, body)
		}
	}
	for i := 0; i < 3; i++ {
		if code, body := callAuth(h, authLoopback, "bad-"+itoa(i), a); code != 200 || body != `{"ok":false}` {
			t.Fatalf("failure %d after 20 successes must still be validated (successes consume nothing): %d %s", i, code, body)
		}
	}
	before := len(*validated)
	for _, pw := range []string{"good-again", "bad-again"} {
		if code, body := callAuth(h, authLoopback, pw, a); code != http.StatusTooManyRequests || body != `{"ok":false}` {
			t.Errorf("after 3 failures from the IP, %q: %d %s, want 429", pw, code, body)
		}
	}
	if len(*validated) != before {
		t.Errorf("a capped IP reached the database: %v", (*validated)[before:])
	}
	// another IP: unaffected, and its successes still do not count
	for i := 0; i < 5; i++ {
		if code, _ := callAuth(h, authLoopback, "good-b", b); code != 200 {
			t.Errorf("other IP, success %d: %d", i, code)
		}
	}
	for i := 0; i < 3; i++ {
		if code, _ := callAuth(h, authLoopback, "bad-b"+itoa(i), b); code != 200 {
			t.Errorf("other IP, failure %d: %d", i, code)
		}
	}
}

func TestHysteria2AuthCredKey(t *testing.T) {
	k := hysteria2AuthCredKey("198.51.100.1", "secret-password")
	ip, hash, ok := strings.Cut(k, "|")
	if !ok || ip != "198.51.100.1" || len(hash) != 16 || strings.Trim(hash, "0123456789abcdef") != "" {
		t.Fatalf("key %q, want 198.51.100.1|<16 hex>", k)
	}
	if strings.Contains(k, "secret") {
		t.Errorf("the password leaked into the key: %q", k)
	}
	if hysteria2AuthCredKey("198.51.100.1", "secret-password") != k {
		t.Errorf("not deterministic")
	}
	if hysteria2AuthCredKey("198.51.100.1", "other-password") == k || hysteria2AuthCredKey("198.51.100.2", "secret-password") == k {
		t.Errorf("another password or another IP must give another bucket")
	}
}

// Refusals before any limiter: non-loopback peer, wrong method, oversized body.
func TestHysteria2AuthHandlerRefusals(t *testing.T) {
	h, validated := testAuthHandler(1, 1)
	if code, _ := callAuth(h, "192.0.2.1:5555", "good", "198.51.100.3:1"); code != http.StatusForbidden {
		t.Errorf("non-loopback peer: %d, want 403", code)
	}
	r := httptest.NewRequest(http.MethodGet, hysteria2AuthPath, nil)
	r.RemoteAddr = authLoopback
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d, want 405", w.Code)
	}
	if code, body := callAuth(h, authLoopback, strings.Repeat("a", hysteria2AuthMaxBody), "198.51.100.4:1"); code != 200 || body != `{"ok":false}` {
		t.Errorf("oversized body: %d %s, want 200 {\"ok\":false}", code, body)
	}
	if len(*validated) != 0 {
		t.Errorf("refused requests reached validation: %v", *validated)
	}
	// none of them consumed a bucket: the one allowed callback of the IP still goes through
	if code, _ := callAuth(h, authLoopback, "good", "198.51.100.3:1"); code != 200 {
		t.Errorf("first real callback after refusals: %d, want 200", code)
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
