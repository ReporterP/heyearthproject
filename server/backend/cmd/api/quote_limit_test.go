package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

// The promo-code limiter is about guessing codes: a price quote without a code never spends it
// (the payment page asks for one on every open), a quote with a code does.
func TestQuoteWithoutPromoDoesNotSpendTheCodeLimit(t *testing.T) {
	rl := middleware.NewRateLimiter(2, 10*time.Minute)
	h := codeLimitedWhenPromo(rl, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	call := func(query string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/quote?"+query, nil)
		req.RemoteAddr = "198.51.100.30:4000"
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec.Code
	}
	for i := 0; i < 5; i++ {
		if code := call("plan_id=5"); code != http.StatusOK {
			t.Fatalf("plain quote %d: %d", i+1, code)
		}
	}
	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		if code := call("plan_id=5&promocode=GUESS"); code != want {
			t.Fatalf("quote with a code %d: %d, want %d", i+1, code, want)
		}
	}
	if code := call("plan_id=5"); code != http.StatusOK {
		t.Fatalf("a plain quote after the code limit: %d, want 200", code)
	}
}
