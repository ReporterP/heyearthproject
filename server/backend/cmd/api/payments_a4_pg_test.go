package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// A4: a payment YooKassa never received (no yookassa_payment_id) is closed after a day;
// a fresh one, one with a YooKassa id and a paid one are left alone.
func TestCloseNeverCreatedPayments(t *testing.T) {
	conn := testpg.Open(t)
	ins := func(age string, ykID string, status string) int {
		var id int
		if err := conn.QueryRow(`
			INSERT INTO payments (user_id_text, amount, currency, status, provider, payment_method, yookassa_payment_id, created_at)
			VALUES ('u', 300, 'RUB', $1, 'yookassa', 'yookassa', NULLIF($2, ''), NOW() - $3::interval) RETURNING id`, status, ykID, age).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := ins("30 hours", "", "pending")
	fresh := ins("2 hours", "", "pending")
	withYK := ins("30 hours", "yk-1", "pending")
	paid := ins("30 hours", "", "succeeded")

	closeNeverCreatedPayments()

	want := map[int]string{old: "canceled", fresh: "pending", withYK: "pending", paid: "succeeded"}
	for id, w := range want {
		var st string
		if err := conn.QueryRow(`SELECT status FROM payments WHERE id = $1`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		if st != w {
			t.Errorf("payment %d: %s, want %s", id, st, w)
		}
	}
}

// A4: Happ's «Продлите» line leads to the existing /payment page (it led to /checkout,
// a 404 without a payment id).
func TestAnnounceLinksToPaymentPage(t *testing.T) {
	conn := testpg.Open(t)
	id := testpg.NewUser(t, conn, "expired-user")
	testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, valid_until, status) VALUES ($1, NOW() - INTERVAL '1 day', 'active')`, id)
	text, url, err := buildSubscriptionAnnounce("expired-user")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Продлите") || !strings.HasSuffix(url, "/payment") {
		t.Errorf("announce %q -> %q, want the /payment page", text, url)
	}
}

// A4: the refund request accepts YooKassa's paid status ("succeeded"), not only the
// legacy "completed".
func TestRefundRequestAcceptsSucceeded(t *testing.T) {
	conn := testpg.Open(t)
	uid := testpg.NewUser(t, conn, "payer")
	var pid int
	if err := conn.QueryRow(`INSERT INTO payments (user_id_text, amount, currency, status, provider) VALUES ('payer', 300, 'RUB', 'succeeded', 'yookassa') RETURNING id`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", "test-secret")
	token, err := biz.GenerateUserSessionToken("payer", uid)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"payment_id": pid, "reason": "не подошло"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/payment/refund", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handleRefundRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refund request for a succeeded payment: %d %s", rec.Code, rec.Body.String())
	}
}

// A5: a database failure while checking the session is a 503, not a 401 (the site logs
// the client out on 401); a bad token is a 401 with a Russian message.
func TestAuthErrorStatus(t *testing.T) {
	conn := testpg.Open(t)
	uid := testpg.NewUser(t, conn, "someone")
	t.Setenv("JWT_SECRET", "test-secret")
	token, err := biz.GenerateUserSessionToken("someone", uid)
	if err != nil {
		t.Fatal(err)
	}
	call := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
		req.Header.Set("Authorization", auth)
		rec := httptest.NewRecorder()
		handleUserProfile(rec, req)
		return rec
	}
	if rec := call("Bearer garbage"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "войдите") {
		t.Errorf("bad token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call("Bearer " + token); rec.Code != http.StatusOK {
		t.Errorf("good token: %d %s", rec.Code, rec.Body.String())
	}
	conn.Close() // the session lookup now fails
	if rec := call("Bearer " + token); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("database down: %d %s, want 503", rec.Code, rec.Body.String())
	}
}

// Review fix: a single-use 100% code cannot be used twice by parallel free purchases.
func TestFreePurchaseRevalidatesCode(t *testing.T) {
	conn := testpg.Open(t)
	plan := pgPlan(t, conn, "t-paid", 300, 30)
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, discount_percent, max_uses, per_user_limit) VALUES ('FULL', 30, 'discount_percent', 100, 1, 0)`)
	p, err := biz.GetPlanByID(plan)
	if err != nil {
		t.Fatal(err)
	}
	const n = 6
	for i := 0; i < n; i++ {
		testpg.NewUser(t, conn, "free-"+string(rune('a'+i)))
	}
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			errs <- activateFreePurchase(context.Background(), "free-"+string(rune('a'+i)), p, "FULL")
		}(i)
	}
	ok := 0
	for i := 0; i < n; i++ {
		if err := <-errs; err == nil {
			ok++
		} else if !biz.IsPromoError(err) {
			t.Errorf("unexpected: %v", err)
		}
	}
	var subs int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM user_subscriptions`).Scan(&subs)
	if ok != 1 || subs != 1 {
		t.Fatalf("%d free purchases went through (%d subscriptions), want 1", ok, subs)
	}
}
