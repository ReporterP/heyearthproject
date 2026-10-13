package yookassa

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
)

// The refund Idempotence-Key is the caller's when given (a repeat of the same attempt is
// deduplicated by YooKassa), every other POST keeps a fresh random key per call.
func TestIdempotenceKeyHeader(t *testing.T) {
	var (
		mu   sync.Mutex
		keys []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotence-Key"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"rf-1","status":"succeeded","amount":{"value":"10.00","currency":"RUB"},"payment_id":"pay-1"}`)
	}))
	defer srv.Close()
	c := NewClient("shop", "secret", true)
	c.baseURL = srv.URL
	random := regexp.MustCompile(`^[0-9a-f]{32}$`)
	refund := CreateRefundRequest{PaymentID: "pay-1", Amount: Amount{Value: "10.00", Currency: "RUB"}}

	tests := []struct {
		name string
		call func() error
		want func(first, second string) bool // keys of two identical calls
	}{
		{"refund with a key sends that key every time", func() error {
			_, err := c.CreateRefundWithKey(refund, "refund-7-3")
			return err
		}, func(a, b string) bool { return a == "refund-7-3" && b == "refund-7-3" }},
		{"refund without a key gets a fresh random key", func() error {
			_, err := c.CreateRefund(refund)
			return err
		}, func(a, b string) bool { return random.MatchString(a) && random.MatchString(b) && a != b }},
		{"empty key falls back to a random one", func() error {
			_, err := c.CreateRefundWithKey(refund, "")
			return err
		}, func(a, b string) bool { return random.MatchString(a) && random.MatchString(b) && a != b }},
		{"payments keep a random key per call", func() error {
			_, err := c.CreatePayment(CreatePaymentRequest{Amount: Amount{Value: "10.00", Currency: "RUB"}})
			return err
		}, func(a, b string) bool { return random.MatchString(a) && random.MatchString(b) && a != b }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mu.Lock()
			keys = nil
			mu.Unlock()
			for i := 0; i < 2; i++ {
				if err := tt.call(); err != nil {
					t.Fatalf("call %d: %v", i+1, err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(keys) != 2 || !tt.want(keys[0], keys[1]) {
				t.Fatalf("Idempotence-Key headers %q", keys)
			}
		})
	}
}
