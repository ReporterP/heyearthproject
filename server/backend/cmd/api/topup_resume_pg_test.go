package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
	"github.com/heyearth/backend/internal/yookassa"
)

type resumeGetter map[string]*yookassa.Payment

func (g resumeGetter) GetPayment(id string) (*yookassa.Payment, error) {
	if p, ok := g[id]; ok {
		return p, nil
	}
	return nil, &yookassa.APIError{HTTPStatus: 404, Code: "not_found"}
}

// E7: a pending embedded top-up can be continued after a refresh; a paid, canceled or foreign
// one cannot.
func TestWalletTopupResume(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-resume")
	uid := testpg.NewUser(t, conn, "resume-user")
	testpg.NewUser(t, conn, "resume-other")
	pay := func(user, status, yk string) (id int) {
		if err := conn.QueryRow(`INSERT INTO payments (user_id_text, amount, currency, status, provider, yookassa_payment_id)
			VALUES ($1, 300, 'RUB', $2, 'yookassa', NULLIF($3, '')) RETURNING id`, user, status, yk).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return
	}
	pending := pay("resume-user", "pending", "yk-pending")
	paid := pay("resume-user", "succeeded", "yk-paid")
	expired := pay("resume-user", "pending", "yk-expired")
	foreign := pay("resume-other", "pending", "yk-foreign")

	prev := newTopupResumeClient
	t.Cleanup(func() { newTopupResumeClient = prev })
	newTopupResumeClient = func() (paymentGetter, error) {
		return resumeGetter{
			"yk-pending": {ID: "yk-pending", Status: "pending", Confirmation: &yookassa.Confirmation{Type: "embedded", ConfirmationToken: "ct-123"}},
			"yk-expired": {ID: "yk-expired", Status: "canceled"},
			"yk-foreign": {ID: "yk-foreign", Status: "pending", Confirmation: &yookassa.Confirmation{Type: "embedded", ConfirmationToken: "ct-foreign"}},
		}, nil
	}
	tok, err := issueUserJWT("resume-user", uid)
	if err != nil {
		t.Fatal(err)
	}
	call := func(id int) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/topup/"+strconv.Itoa(id)+"/resume", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		handleWalletTopupStatus(rec, req)
		return rec.Code, rec.Body.String()
	}
	if code, body := call(pending); code != 200 || !strings.Contains(body, `"confirmation_token":"ct-123"`) {
		t.Fatalf("pending: %d %s", code, body)
	}
	if code, _ := call(paid); code != http.StatusConflict {
		t.Errorf("paid: %d", code)
	}
	if code, _ := call(expired); code != http.StatusConflict {
		t.Errorf("canceled at YooKassa: %d", code)
	}
	if code, body := call(foreign); code != http.StatusNotFound || strings.Contains(body, "ct-foreign") {
		t.Errorf("another user's payment: %d %s", code, body)
	}
	// GET on the same path is still the status poll.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wallet/topup/"+strconv.Itoa(pending), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	handleWalletTopupStatus(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"pending"`) {
		t.Errorf("status poll: %d %s", rec.Code, rec.Body.String())
	}
}

// A resumed checkout (new tab, refresh in another session) gets back what the payment is for:
// the top-up amount, and for a top-up with a purchase the plan and the price to charge.
func TestWalletTopupResumeCarriesAmountAndPlan(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-resume-meta")
	uid := testpg.NewUser(t, conn, "resume-meta-user")
	var planID, payID int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('full', 'Полный', '', 300, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`INSERT INTO payments (user_id_text, amount, currency, status, provider, yookassa_payment_id, metadata)
		VALUES ('resume-meta-user', 180, 'RUB', 'pending', 'yookassa', 'yk-meta', jsonb_build_object('purchase_plan_id', $1::int, 'purchase_price_kop', 30000))
		RETURNING id`, planID).Scan(&payID); err != nil {
		t.Fatal(err)
	}
	prev := newTopupResumeClient
	t.Cleanup(func() { newTopupResumeClient = prev })
	newTopupResumeClient = func() (paymentGetter, error) {
		return resumeGetter{"yk-meta": {ID: "yk-meta", Status: "pending", Confirmation: &yookassa.Confirmation{Type: "embedded", ConfirmationToken: "ct-meta"}}}, nil
	}
	tok, err := issueUserJWT("resume-meta-user", uid)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wallet/topup/"+strconv.Itoa(payID)+"/resume", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	handleWalletTopupStatus(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`"confirmation_token":"ct-meta"`, `"amount_kop":18000`, `"plan_name":"Полный"`, `"plan_slug":"full"`, `"price_kop":30000`} {
		if !strings.Contains(body, want) {
			t.Errorf("resume answer %d %s: no %s", rec.Code, body, want)
		}
	}
}
