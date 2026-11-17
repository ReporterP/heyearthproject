package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSharedSourceCountsPerUser(t *testing.T) {
	SharedSource = func(ip string) bool { return ip == "198.51.100.7" }
	defer func() { SharedSource = nil }()
	rl := NewRateLimiter(2, time.Minute)
	h := rl.Middleware(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	do := func(ip, auth string) int {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r = r.WithContext(TrustProxyHeaders(r.Context()))
		r.Header.Set("X-Real-IP", ip)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	// An ordinary address: 2 requests, then 429.
	if do("203.0.113.5", "") != 200 || do("203.0.113.5", "") != 200 || do("203.0.113.5", "") != 429 {
		t.Fatal("plain address limit")
	}
	// Behind an exit: user A's limit does not touch user B's.
	if do("198.51.100.7", "Bearer a") != 200 || do("198.51.100.7", "Bearer a") != 200 || do("198.51.100.7", "Bearer a") != 429 {
		t.Fatal("per-user limit behind a shared address")
	}
	if do("198.51.100.7", "Bearer b") != 200 {
		t.Fatal("user B must have its own bucket")
	}
	// Anonymous requests from the exit: sharedAnonFactor times the limit.
	for i := 0; i < 2*sharedAnonFactor; i++ {
		if do("198.51.100.7", "") != 200 {
			t.Fatalf("anonymous request %d from a shared address refused", i)
		}
	}
	if do("198.51.100.7", "") != 429 {
		t.Fatal("anonymous shared limit must still apply")
	}
}
