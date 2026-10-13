package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
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
