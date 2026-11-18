package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// Retry-After tells the real wait: the rest of the fixed window, not a constant minute
// (the promo-code limiter's window is 10 minutes).
func TestRetryAfterIsTheRestOfTheWindow(t *testing.T) {
	rl := NewRateLimiter(2, 10*time.Minute)
	h := rl.Middleware(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	var last *httptest.ResponseRecorder
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "198.51.100.20:4000"
		last = httptest.NewRecorder()
		h(last, req)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: %d, want 429", last.Code)
	}
	sec, err := strconv.Atoi(last.Header().Get("Retry-After"))
	if err != nil || sec < 590 || sec > 600 {
		t.Fatalf("Retry-After = %q, want ~600", last.Header().Get("Retry-After"))
	}
}
