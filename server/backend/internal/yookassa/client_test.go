package yookassa

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"
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

// ListRefunds asks for the payment's refunds 100 per page and follows next_cursor.
func TestListRefundsFollowsCursor(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"type":"list","items":[{"id":"rf-1","status":"succeeded","amount":{"value":"10.00","currency":"RUB"},"payment_id":"pay-1"}],"next_cursor":"c2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"type":"list","items":[{"id":"rf-2","status":"pending","amount":{"value":"5.00","currency":"RUB"},"payment_id":"pay-1"}]}`)
	}))
	defer srv.Close()
	c := NewClient("shop", "secret", true)
	c.baseURL = srv.URL
	got, err := c.ListRefunds("pay-1", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "rf-1" || got[1].ID != "rf-2" || got[1].Amount.Value != "5.00" {
		t.Fatalf("refunds %+v", got)
	}
	want := []string{"GET /refunds?limit=100&payment_id=pay-1", "GET /refunds?cursor=c2&limit=100&payment_id=pay-1"}
	if len(queries) != 2 || queries[0] != want[0] || queries[1] != want[1] {
		t.Fatalf("queries %q, want %q", queries, want)
	}
}

// The deadline is checked before every page: a page already running finishes, the next one
// is not requested, and a partial list is an error (never a short answer).
func TestListRefundsDeadlinePerPage(t *testing.T) {
	var (
		mu    sync.Mutex
		pages int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		pages++
		mu.Unlock()
		time.Sleep(60 * time.Millisecond)
		_, _ = io.WriteString(w, `{"type":"list","items":[{"id":"rf-1","amount":{"value":"1.00","currency":"RUB"}}],"next_cursor":"more"}`)
	}))
	defer srv.Close()
	c := NewClient("shop", "secret", true)
	c.baseURL = srv.URL

	if got, err := c.ListRefunds("pay-1", time.Now().Add(-time.Second)); err == nil || got != nil {
		t.Fatalf("spent budget: %v, %v; want an error and no list", got, err)
	}
	mu.Lock()
	if pages != 0 {
		t.Fatalf("spent budget still requested %d pages", pages)
	}
	mu.Unlock()

	got, err := c.ListRefunds("pay-1", time.Now().Add(30*time.Millisecond))
	mu.Lock()
	defer mu.Unlock()
	if err == nil || got != nil || pages != 1 {
		t.Fatalf("budget ends during page 1: %d pages, %v, %v; want 1 page and an error", pages, got, err)
	}
}

// WithTimeout gives the copy its own timeout and leaves the original client alone.
func TestWithTimeoutCopies(t *testing.T) {
	c := NewClient("shop", "secret", true)
	short := c.WithTimeout(25 * time.Second)
	if short.http.Timeout != 25*time.Second || c.http.Timeout != httpTimeout || short.shopID != "shop" || short.baseURL != c.baseURL {
		t.Fatalf("copy timeout %v, original %v", short.http.Timeout, c.http.Timeout)
	}
}
