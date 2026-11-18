package biz

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

func newTestTenant(t *testing.T, conn *sql.DB, maxUsers int) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`INSERT INTO tenants (name, slug, status, plan, contact_email, max_users)
		VALUES ('QA Co', 'qa-co', 'active', 'basic', 'co@example.test', $1) RETURNING id`, maxUsers).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func addClaimCode(t *testing.T, conn *sql.DB, tenantID int, code string) {
	t.Helper()
	testpg.MustExec(t, conn, `INSERT INTO tenant_claim_codes (tenant_id, code, max_uses, used_count) VALUES ($1, $2, 0, 0)`, tenantID, code)
}

func memberRole(t *testing.T, tenantID, userDBID int) string {
	t.Helper()
	role, ok, err := GetTenantMember(tenantID, userDBID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return ""
	}
	return role
}

// A full company takes no new members: max_users is the seat limit the cabinet promises.
func TestRedeemRefusesBeyondTheSeatLimit(t *testing.T) {
	conn := testpg.Open(t)
	tenant := newTestTenant(t, conn, 1)
	admin := testpg.NewUser(t, conn, "c1c1c1c1-1111-4111-8111-111111111111")
	testpg.NewUser(t, conn, "d2d2d2d2-2222-4222-8222-222222222222")
	testpg.MustExec(t, conn, `INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`, tenant, admin)
	addClaimCode(t, conn, tenant, "tn-r-user-seat1")

	_, err := RedeemTenantClaimCode("tn-r-user-seat1", "d2d2d2d2-2222-4222-8222-222222222222", "")
	if !errors.Is(err, ErrTenantNoSeats) {
		t.Fatalf("got %v, want ErrTenantNoSeats", err)
	}
	var used int
	_ = conn.QueryRow(`SELECT used_count FROM tenant_claim_codes WHERE code = 'tn-r-user-seat1'`).Scan(&used)
	if used != 0 {
		t.Errorf("a refused redeem used the code (used_count=%d)", used)
	}
}

// A member redeeming a code for a lower role keeps the role they have: a code never demotes
// (it would bypass the last-admin guard of the role change).
func TestRedeemNeverLowersAnExistingRole(t *testing.T) {
	conn := testpg.Open(t)
	tenant := newTestTenant(t, conn, 10)
	admin := testpg.NewUser(t, conn, "e3e3e3e3-3333-4333-8333-333333333333")
	testpg.MustExec(t, conn, `INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`, tenant, admin)
	addClaimCode(t, conn, tenant, "tn-r-viewer-demote")

	if _, err := RedeemTenantClaimCode("tn-r-viewer-demote", "e3e3e3e3-3333-4333-8333-333333333333", ""); err != nil {
		t.Fatal(err)
	}
	if got := memberRole(t, tenant, admin); got != "admin" {
		t.Errorf("role after a viewer code: %q, want admin", got)
	}
}
