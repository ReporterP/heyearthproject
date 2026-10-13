package admin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// Review Focus 5 → a refund (and the other money actions) without payments.refund is a 403 and
// never reaches YooKassa; payments.write alone is not enough.
func TestPaymentMoneyActionsRequireRefundPermission(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-jwt-secret")
	oldActive, oldPA := adminIsActive, PaymentActionHandler
	t.Cleanup(func() { adminIsActive, PaymentActionHandler = oldActive, oldPA })
	adminIsActive = func(int) (bool, error) { return true, nil }
	called := 0
	PaymentActionHandler = func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(http.StatusOK) }
	token := func(perms ...string) string {
		tok, err := GenerateToken(AdminUser{ID: 7, Username: "operator", Permissions: perms})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	do := func(path, tok string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"amount":249}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		paymentsItemRouter(rec, req)
		return rec.Code
	}
	for _, path := range []string{"/api/admin/payments/12/cancel", "/api/admin/payments/12/check-status", "/api/admin/payments/resend"} {
		called = 0
		if code := do(path, token("payments.read", "payments.write")); code != http.StatusForbidden || called != 0 {
			t.Fatalf("%s without payments.refund: status %d, YooKassa called %d times", path, code, called)
		}
		if code := do(path, token("payments.refund")); code != http.StatusOK || called != 1 {
			t.Fatalf("%s with payments.refund: status %d, called %d", path, code, called)
		}
	}
}

func TestPaymentsRefundPermissionSeeded(t *testing.T) {
	if !slices.Contains(allPermissionKeys(), "payments.refund") {
		t.Fatal("payments.refund is missing from permissionCatalog: ensureSuperAdminRole would strip it from super_admin on every login")
	}
	up, err := os.ReadFile("../../migrations/000059_payments_refund_permission.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"'payments.refund'", "ON CONFLICT (permission_key) DO NOTHING", "r.role_key = 'super_admin'", "ON CONFLICT DO NOTHING"} {
		if !strings.Contains(string(up), want) {
			t.Errorf("000059 up lacks %q", want)
		}
	}
	down, err := os.ReadFile("../../migrations/000059_payments_refund_permission.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DELETE FROM admin_role_permissions", "DELETE FROM admin_permissions", "'payments.refund'"} {
		if !strings.Contains(string(down), want) {
			t.Errorf("000059 down lacks %q", want)
		}
	}
}

// listDB answers the three queries of ListPaymentsHandler; the list query gets one row whose
// refundable column (right after refunded_amount) is 150.5.
type listDB struct{ listQuery string }

func (d *listDB) Connect(context.Context) (driver.Conn, error) { return &listConn{d}, nil }
func (d *listDB) Driver() driver.Driver                        { return listDriver{} }

type listDriver struct{}

func (listDriver) Open(string) (driver.Conn, error) { return nil, fmt.Errorf("list driver: no DSN") }

type listConn struct{ d *listDB }

func (c *listConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("list driver: no prepare")
}
func (c *listConn) Close() error              { return nil }
func (c *listConn) Begin() (driver.Tx, error) { return nil, fmt.Errorf("list driver: no tx") }

func (c *listConn) QueryContext(_ context.Context, q string, _ []driver.NamedValue) (driver.Rows, error) {
	q = strings.Join(strings.Fields(q), " ")
	switch {
	case strings.HasPrefix(q, "SELECT COUNT(*) FROM payments"):
		return &listRows{cols: []string{"count"}, rows: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(q, "SUM(p.amount)"):
		return &listRows{cols: []string{"sum"}, rows: [][]driver.Value{{float64(300)}}}, nil
	case strings.Contains(q, "ORDER BY"):
		c.d.listQuery = q
		now := time.Now()
		return &listRows{cols: make([]string, 24), rows: [][]driver.Value{{
			int64(12), int64(0), "user-1", float64(300), "RUB", "succeeded", "yookassa",
			"pay-1", "", []byte(`{}`), now, now, nil,
			"pay-1", "succeeded", "bank_card", []byte(`{}`),
			float64(100), float64(150.5), true, false,
			"Подписка", "", "",
		}}}, nil
	}
	return nil, fmt.Errorf("list driver: unscripted %s", q)
}

type listRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *listRows) Columns() []string { return r.cols }
func (*listRows) Close() error        { return nil }
func (r *listRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

// The admin list carries refundable_amount (rubles) from the shared SQL rule: amount minus
// pending and succeeded refunds, only for paid payments with a YooKassa id.
func TestListPaymentsRefundableAmount(t *testing.T) {
	d := &listDB{}
	fake := sql.OpenDB(d)
	prev := db.DB
	db.DB = fake
	t.Cleanup(func() { db.DB = prev; _ = fake.Close() })

	rec := httptest.NewRecorder()
	ListPaymentsHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/payments", nil))
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK || len(out.Items) != 1 {
		t.Fatalf("%d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	item := out.Items[0]
	if item["refundable_amount"] != 150.5 || item["refunded_amount"] != float64(100) || item["receipt_registered"] != true {
		t.Fatalf("item %v: want refundable_amount 150.5, refunded_amount 100, receipt_registered true", item)
	}
	for _, want := range []string{
		"WHEN p.status IN ('succeeded', 'completed') AND COALESCE(p.yookassa_payment_id, '') <> ''",
		"p.amount - COALESCE(( SELECT SUM(r.amount) FROM payment_refunds r WHERE r.payment_id = p.id AND r.status IN ('pending', 'succeeded')), 0)",
	} {
		if !strings.Contains(d.listQuery, want) {
			t.Errorf("list query lacks %q:\n%s", want, d.listQuery)
		}
	}
}
