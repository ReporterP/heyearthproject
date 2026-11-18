package main

import (
	"testing"
	"time"

	"github.com/heyearth/backend/internal/middleware"
)

// The golden tests fetch one subscription many times; the per-token limit has its own test.
func init() { subTokenLimiter = middleware.NewRateLimiter(1<<30, time.Minute) }

func TestValidSubscribeToken(t *testing.T) {
	for tok, want := range map[string]bool{
		"0b5f7c2e-9a1d-4c3b-8e2f-1a2b3c4d5e6f": true, "0B5F7C2E-9A1D-4C3B-8E2F-1A2B3C4D5E6F": true,
		"": false, "abc": false, "0b5f7c2e9a1d4c3b8e2f1a2b3c4d5e6f": false, "0b5f7c2e-9a1d-4c3b-8e2f-1a2b3c4d5e6f'--": false,
	} {
		if validSubscribeToken(tok) != want {
			t.Errorf("%q: want %v", tok, want)
		}
	}
}

func TestPerTokenLimit(t *testing.T) {
	l := middleware.NewRateLimiter(subTokenPerMin, time.Minute)
	for i := 0; i < subTokenPerMin; i++ {
		if !l.Allow("t") {
			t.Fatalf("request %d refused", i)
		}
	}
	if l.Allow("t") || !l.Allow("other") {
		t.Fatal("the limit is per token")
	}
}

func TestSameSiteURL(t *testing.T) {
	t.Setenv("PUBLIC_BASE_URL", "https://example.org")
	for raw, want := range map[string]bool{
		"https://example.org/wallet?topup=5": true, "https://go.example.org/x": true,
		"https://evil.org/": false, "http://example.org/": false, "https://example.org.evil.org/": false,
		"https://user@example.org/": false, "javascript:alert(1)": false, "": false, "//evil.org/": false,
	} {
		if sameSiteURL(raw) != want {
			t.Errorf("%q: want %v", raw, want)
		}
	}
}
