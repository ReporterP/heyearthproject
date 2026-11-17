package admin

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// C7: only manual records are created and edited by hand (and audited); a YooKassa payment is
// never touched — its status, amount and metadata stay.
func TestManualPayments(t *testing.T) {
	conn := testpg.Open(t)
	var audits []string
	prev := auditLogExec
	t.Cleanup(func() { auditLogExec = prev })
	auditLogExec = func(_ string, args ...any) (sql.Result, error) {
		audits = append(audits, args[2].(string))
		return driver.RowsAffected(1), nil
	}
	uid := testpg.NewUser(t, conn, "manual-user")
	rec := callAs(CreatePaymentHandler, "POST", "/api/admin/payments", `{"user_db_id": `+strconv.Itoa(uid)+`, "amount": 300, "status": "succeeded", "notes": "наличные"}`, "payments.write")
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created struct{ ID int }
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	var provider, currency, userText string
	_ = conn.QueryRow(`SELECT provider, currency, user_id_text FROM payments WHERE id = $1`, created.ID).Scan(&provider, &currency, &userText)
	if provider != "manual" || currency != "RUB" || userText != "manual-user" {
		t.Errorf("created %s %s %s", provider, currency, userText)
	}
	for _, bad := range []string{`{"user_db_id": 0, "amount": 1, "status": "succeeded"}`, `{"user_db_id": 1, "amount": 0, "status": "succeeded"}`, `{"user_db_id": 1, "amount": 1, "status": "refunded"}`} {
		if rec := callAs(CreatePaymentHandler, "POST", "/api/admin/payments", bad, "payments.write"); rec.Code != 400 {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if rec := callAs(UpdatePaymentHandler, "PATCH", "/api/admin/payments/"+strconv.Itoa(created.ID), `{"amount": 350, "status": "canceled", "notes": "ошибка"}`, "payments.write"); rec.Code != 200 {
		t.Fatalf("update manual: %d %s", rec.Code, rec.Body)
	}

	var yk int
	_ = conn.QueryRow(`INSERT INTO payments (user_id, user_id_text, amount, currency, status, provider, yookassa_payment_id, metadata)
		VALUES ($1, 'manual-user', 250, 'RUB', 'succeeded', 'yookassa', 'yk-1', '{"kind": "wallet_topup"}') RETURNING id`, uid).Scan(&yk)
	if rec := callAs(UpdatePaymentHandler, "PATCH", "/api/admin/payments/"+strconv.Itoa(yk), `{"amount": 1, "status": "canceled"}`, "payments.write"); rec.Code != 409 {
		t.Errorf("a YooKassa payment edited: %d %s", rec.Code, rec.Body)
	}
	var st, kind string
	var amount float64
	_ = conn.QueryRow(`SELECT status, amount::float8, metadata->>'kind' FROM payments WHERE id = $1`, yk).Scan(&st, &amount, &kind)
	if st != "succeeded" || amount != 250 || kind != "wallet_topup" {
		t.Errorf("YooKassa payment changed: %s %v %s", st, amount, kind)
	}
	if len(audits) != 2 || audits[0] != "payment.manual_create" || audits[1] != "payment.manual_update" {
		t.Errorf("audit %v", audits)
	}
}
