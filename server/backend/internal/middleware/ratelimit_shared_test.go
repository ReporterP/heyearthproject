package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSharedSourceCountsPerUser(t *testing.T) {
	SharedSource = func(ip string) bool { return ip == "198.51.100.7" }
	ValidBearer = func(a string) bool { return a == "Bearer a" || a == "Bearer b" }
	defer func() { SharedSource, ValidBearer = nil, nil }()
	rl := NewRateLimiter(2, time.Minute).PerUserBehindShared()
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
	// A forged token is not a bucket of its own: it counts with the anonymous ones (F17).
	if do("198.51.100.7", "Bearer forged") != 200 {
		t.Fatal("forged token goes to the shared bucket, which still has room")
	}
	// Anonymous requests from the exit: sharedAnonFactor times the limit (one used by the forged one).
	for i := 0; i < 2*sharedAnonFactor-1; i++ {
		if do("198.51.100.7", "") != 200 {
			t.Fatalf("anonymous request %d from a shared address refused", i)
		}
	}
	if do("198.51.100.7", "") != 429 {
		t.Fatal("anonymous shared limit must still apply")
	}
}

func TestSignInLimitStaysPerAddress(t *testing.T) {
	SharedSource = func(ip string) bool { return ip == "198.51.100.7" }
	ValidBearer = func(string) bool { return true }
	defer func() { SharedSource, ValidBearer = nil, nil }()
	rl := NewRateLimiter(1, time.Minute) // not PerUserBehindShared: like the sign-in / code limits
	h := rl.Middleware(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	codes := 0
	for i := 0; i < sharedAnonFactor+3; i++ {
		r := httptest.NewRequest("POST", "/login", nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r = r.WithContext(TrustProxyHeaders(r.Context()))
		r.Header.Set("X-Real-IP", "198.51.100.7")
		r.Header.Set("Authorization", "Bearer t"+string(rune('a'+i)))
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code == 200 {
			codes++
		}
	}
	if codes != sharedAnonFactor {
		t.Fatalf("a new token per request must not open new buckets: %d passed", codes)
	}
}
