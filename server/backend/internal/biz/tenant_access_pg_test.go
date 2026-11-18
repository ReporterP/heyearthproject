package biz

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

// Company-paid access (QA 2026-11-18, jzd5.24 / jzd5.23): members of an active company have
// a subscription row marked with it; suspension, leaving and deletion end it; a top-up that
// covers the month resumes the company.

const (
	accAdmin   = "a1000000-0000-4000-8000-000000000001"
	accBilling = "a1000000-0000-4000-8000-000000000002"
	accViewer  = "a1000000-0000-4000-8000-000000000003"
	accUser    = "a1000000-0000-4000-8000-000000000004"
	accNewbie  = "a1000000-0000-4000-8000-000000000005"
)

// accessCompany: the member plan (all-inclusive), an active ten-seat company of four members
// (one per role), and their users.id by text id.
func accessCompany(t *testing.T, conn *sql.DB) (tenantID, planID int, members map[string]int) {
	t.Helper()
	// The migrations seed all-inclusive (000025, Express in 000048), as on prod.
	if err := conn.QueryRow(`SELECT id FROM plans WHERE slug = $1`, DefaultTenantMemberPlan).Scan(&planID); err != nil {
		t.Fatalf("member plan %s: %v", DefaultTenantMemberPlan, err)
	}
	tenantID = newTestTenant(t, conn, 10)
	members = map[string]int{}
	for id, role := range map[string]string{accAdmin: "admin", accBilling: "billing", accViewer: "viewer", accUser: "user"} {
		members[id] = testpg.NewUser(t, conn, id)
		testpg.MustExec(t, conn, `INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, $3)`, tenantID, members[id], role)
	}
	return tenantID, planID, members
}

type companyRowState struct {
	ID        int
	Live      bool
	PlanID    int
	Status    string
	AutoRenew bool
	Started   time.Time
}

// companyRow: the user's company row of tenantID (ok=false: none).
func companyRow(t *testing.T, conn *sql.DB, userDBID, tenantID int) (companyRowState, bool) {
	t.Helper()
	var r companyRowState
	err := conn.QueryRow(`SELECT id, valid_until > NOW(), COALESCE(plan_id, 0), status, auto_renew, started_at
		FROM user_subscriptions WHERE user_id = $1 AND tenant_id = $2`, userDBID, tenantID).
		Scan(&r.ID, &r.Live, &r.PlanID, &r.Status, &r.AutoRenew, &r.Started)
	if err == sql.ErrNoRows {
		return r, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return r, true
}

// xrayPlans: the users Xray lets in, by text id, with the plan they are routed by.
func xrayPlans(t *testing.T) map[string]int {
	t.Helper()
	clients, err := ListActiveVlessClientsContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, c := range clients {
		out[c.UserID] = c.PlanID
	}
	return out
}

func setTenantStatus(t *testing.T, conn *sql.DB, tenantID int, status string) {
	t.Helper()
	testpg.MustExec(t, conn, `UPDATE tenants SET status = $2 WHERE id = $1`, tenantID, status)
}

func syncAccess(t *testing.T, tenantID int) {
	t.Helper()
	if err := SyncTenantAccess(context.Background(), tenantID); err != nil {
		t.Fatal(err)
	}
}

// Every member of an active company, whatever the role, gets the member plan; suspension
// takes it away from all of them; resuming gives the same rows back.
func TestTenantAccessFollowsTheCompanyStatus(t *testing.T) {
	conn := testpg.Open(t)
	tenant, plan, members := accessCompany(t, conn)

	syncAccess(t, tenant)
	xray := xrayPlans(t)
	first := map[string]companyRowState{}
	for id, dbID := range members {
		row, ok := companyRow(t, conn, dbID, tenant)
		if !ok || !row.Live || row.PlanID != plan || row.Status != "active" || row.AutoRenew {
			t.Fatalf("%s: company row %+v ok=%v, want a live active row on plan %d without auto-renew", id, row, ok, plan)
		}
		if xray[id] != plan {
			t.Errorf("%s: Xray routes by plan %d, want %d (in Xray: %v)", id, xray[id], plan, xray[id] != 0)
		}
		if ok, err := Hysteria2StillAllowed(id); err != nil || !ok {
			t.Errorf("%s: Hysteria2 refuses a member of an active company (%v, %v)", id, ok, err)
		}
		first[id] = row
	}

	setTenantStatus(t, conn, tenant, "suspended")
	syncAccess(t, tenant)
	xray = xrayPlans(t)
	for id, dbID := range members {
		if row, _ := companyRow(t, conn, dbID, tenant); row.Live || row.Status != "cancelled" {
			t.Errorf("%s: suspended company still gives access: %+v", id, row)
		}
		if _, in := xray[id]; in {
			t.Errorf("%s: still in Xray after the company was suspended", id)
		}
		if ok, err := Hysteria2StillAllowed(id); err != nil || ok {
			t.Errorf("%s: Hysteria2 still allowed (%v, %v)", id, ok, err)
		}
	}

	time.Sleep(10 * time.Millisecond) // a resumed row starts a new cycle: started_at moves
	setTenantStatus(t, conn, tenant, "active")
	syncAccess(t, tenant)
	for id, dbID := range members {
		row, _ := companyRow(t, conn, dbID, tenant)
		if !row.Live || row.ID != first[id].ID || !row.Started.After(first[id].Started) {
			t.Errorf("%s: after resume %+v, want the same row (%d) live with a new start", id, row, first[id].ID)
		}
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM user_subscriptions WHERE tenant_id = $1`, tenant).Scan(&n)
	if n != len(members) {
		t.Errorf("%d company rows, want one per member (%d)", n, len(members))
	}
}

// Removing a member ends their access at once; the others keep theirs; a new member gets it.
func TestTenantAccessFollowsTheMemberList(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, members := accessCompany(t, conn)
	syncAccess(t, tenant)

	// users.tenant_id is the legacy mirror a claim writes and nothing clears.
	testpg.MustExec(t, conn, `UPDATE users SET tenant_id = $1 WHERE id = $2`, tenant, members[accUser])
	if st, err := GetUserAccessState(accUser, ""); err != nil || !st.CanConnect {
		t.Fatalf("member: can_connect %+v %v, want true", st, err)
	}
	if err := RemoveTenantMember(tenant, members[accUser]); err != nil {
		t.Fatal(err)
	}
	if row, _ := companyRow(t, conn, members[accUser], tenant); row.Live {
		t.Errorf("a removed member keeps the company access: %+v", row)
	}
	if st, err := GetUserAccessState(accUser, ""); err != nil || st.CanConnect {
		t.Errorf("a removed member (users.tenant_id still set) may connect: %+v %v", st, err)
	}
	if row, _ := companyRow(t, conn, members[accViewer], tenant); !row.Live {
		t.Errorf("another member lost access with the removal: %+v", row)
	}

	newbie := testpg.NewUser(t, conn, accNewbie)
	if err := AddTenantMember(tenant, newbie, "user"); err != nil {
		t.Fatal(err)
	}
	if row, ok := companyRow(t, conn, newbie, tenant); !ok || !row.Live {
		t.Errorf("an added member has no access: %+v ok=%v", row, ok)
	}
}

// The company's deletion takes its rows with it; the members' own subscriptions stay.
func TestTenantAccessGoesWithTheCompany(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, members := accessCompany(t, conn)
	syncAccess(t, tenant)
	own := newPlan(t, conn, "own-plan", 250, 30, nil, true)
	activate(t, conn, accUser, own, 250)

	testpg.MustExec(t, conn, `DELETE FROM tenants WHERE id = $1`, tenant)
	var company, personal int
	_ = conn.QueryRow(`SELECT COUNT(*) FILTER (WHERE tenant_id IS NOT NULL), COUNT(*) FILTER (WHERE tenant_id IS NULL)
		FROM user_subscriptions WHERE user_id = $1`, members[accUser]).Scan(&company, &personal)
	if company != 0 || personal != 1 {
		t.Errorf("after deleting the company: %d company rows, %d own, want 0 and 1", company, personal)
	}
}

func activate(t *testing.T, conn *sql.DB, userID string, planID, price int) {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := ActivateSubscriptionWithPlanTx(context.Background(), tx, userID, planID, 30, price, ""); err != nil {
		tx.Rollback()
		t.Fatalf("activate plan %d for %s: %v", planID, userID, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// A purchase never touches the company row: a cheaper own plan is not a «downgrade» of the
// company plan, it is a separate row, and it is the one the member is routed and shown by.
func TestOwnPurchaseLeavesTheCompanyRow(t *testing.T) {
	conn := testpg.Open(t)
	tenant, plan, members := accessCompany(t, conn)
	syncAccess(t, tenant)
	before, _ := companyRow(t, conn, members[accUser], tenant)
	own := newPlan(t, conn, "own-plan", 250, 30, nil, true)

	activate(t, conn, accUser, own, 250)

	after, _ := companyRow(t, conn, members[accUser], tenant)
	if after != before {
		t.Errorf("the purchase changed the company row: %+v → %+v", before, after)
	}
	var personal int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM user_subscriptions WHERE user_id = $1 AND tenant_id IS NULL AND plan_id = $2 AND valid_until > NOW()`,
		members[accUser], own).Scan(&personal)
	if personal != 1 {
		t.Fatalf("%d own rows on the bought plan, want 1", personal)
	}
	if got := xrayPlans(t)[accUser]; got != own {
		t.Errorf("Xray routes by plan %d, want the own plan %d (company plan %d)", got, own, plan)
	}
	items, err := ListUserSubscriptions(accUser)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Company != nil || items[0].PlanID != own {
		t.Fatalf("subscriptions %+v: want the own one first", items)
	}
	if c := items[1].Company; c == nil || c.Name != "QA Co" || c.Status != "active" {
		t.Errorf("company row shows company %+v, want QA Co / active", c)
	}
	if n, err := activationCountOK(conn, accUser, plan); err != nil || !n {
		t.Errorf("the company row counts as an activation of the member plan (%v, %v)", n, err)
	}
}

// activationCountOK: the user may still activate a once-only plan (the company row does not
// use up its activations).
func activationCountOK(conn *sql.DB, userID string, planID int) (bool, error) {
	if _, err := conn.Exec(`UPDATE plans SET max_activations_per_user = 1 WHERE id = $1`, planID); err != nil {
		return false, err
	}
	return CheckPlanActivationLimit(userID, planID) == nil, nil
}

// The subscription list names the paying company and why its access ended.
func TestSubscriptionListNamesTheCompany(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, members := accessCompany(t, conn)
	syncAccess(t, tenant)
	items, err := ListUserSubscriptions(accViewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !items[0].Active || items[0].Company == nil || items[0].Company.Status != "active" {
		t.Fatalf("%+v: want one live company row", items)
	}

	setTenantStatus(t, conn, tenant, "suspended")
	syncAccess(t, tenant)
	if items, _ = ListUserSubscriptions(accViewer); items[0].Active || items[0].Company.Status != "suspended" {
		t.Errorf("suspended: %+v %+v", items[0], items[0].Company)
	}
	if err := RemoveTenantMember(tenant, members[accViewer]); err != nil {
		t.Fatal(err)
	}
	if items, _ = ListUserSubscriptions(accViewer); items[0].Company.Status != "left" {
		t.Errorf("after leaving: %+v", items[0].Company)
	}
}

// A claim code gives the access in the same transaction and tells the site where to go.
func TestClaimGivesTheAccessAtOnce(t *testing.T) {
	conn := testpg.Open(t)
	tenant, plan, _ := accessCompany(t, conn)
	syncAccess(t, tenant)
	newbie := testpg.NewUser(t, conn, accNewbie)
	addClaimCode(t, conn, tenant, "tn-r-user-access")

	res, err := RedeemTenantClaimCode("tn-r-user-access", accNewbie, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.TenantID != tenant || res.Role != "user" || res.TenantName != "QA Co" || res.TenantStatus != "active" {
		t.Errorf("claim result %+v", res)
	}
	if row, ok := companyRow(t, conn, newbie, tenant); !ok || !row.Live || row.PlanID != plan {
		t.Errorf("after the claim: %+v ok=%v, want a live row on %d", row, ok, plan)
	}

	// A code for someone already in the company reports the role they keep.
	addClaimCode(t, conn, tenant, "tn-r-user-again")
	if res, err = RedeemTenantClaimCode("tn-r-user-again", accAdmin, ""); err != nil || res.Role != "admin" {
		t.Errorf("an admin redeeming a user code: %+v %v, want role admin", res, err)
	}
}

// Joining a suspended company makes you a member without access.
func TestClaimIntoASuspendedCompanyGivesNoAccess(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, _ := accessCompany(t, conn)
	setTenantStatus(t, conn, tenant, "suspended")
	newbie := testpg.NewUser(t, conn, accNewbie)
	addClaimCode(t, conn, tenant, "tn-r-user-susp")
	if _, err := RedeemTenantClaimCode("tn-r-user-susp", accNewbie, ""); err != nil {
		t.Fatal(err)
	}
	if role := memberRole(t, tenant, newbie); role != "user" {
		t.Fatalf("role %q, want user", role)
	}
	if row, ok := companyRow(t, conn, newbie, tenant); ok && row.Live {
		t.Errorf("a suspended company gave access: %+v", row)
	}
}

// Without the member plan nobody gains access and nothing fails; a changed plan moves every
// member to it.
func TestTenantAccessFollowsTheMemberPlan(t *testing.T) {
	conn := testpg.Open(t)
	tenant, plan, members := accessCompany(t, conn)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ($1, 'no-such-plan')`, TenantMemberPlanSetting)
	syncAccess(t, tenant)
	if row, ok := companyRow(t, conn, members[accUser], tenant); ok {
		t.Fatalf("access given without the member plan: %+v", row)
	}

	testpg.MustExec(t, conn, `UPDATE app_settings SET value = $2 WHERE key = $1`, TenantMemberPlanSetting, DefaultTenantMemberPlan)
	syncAccess(t, tenant)
	other := newPlan(t, conn, "team-plan", 200, 30, nil, false)
	testpg.MustExec(t, conn, `UPDATE app_settings SET value = 'team-plan' WHERE key = $1`, TenantMemberPlanSetting)
	n, err := SyncAllTenantAccess(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("SyncAllTenantAccess: %d changed, %v; want 1", n, err)
	}
	if row, _ := companyRow(t, conn, members[accUser], tenant); !row.Live || row.PlanID != other {
		t.Errorf("after the plan change: %+v, want plan %d (was %d)", row, other, plan)
	}
	// Nothing to change: the loop's next run writes nothing.
	if n, err := SyncAllTenantAccess(context.Background()); err != nil || n != 0 {
		t.Errorf("a second run changed %d companies (%v)", n, err)
	}
}

func creditTenant(t *testing.T, conn *sql.DB, tenantID int, amount float64) CreditTenantWalletResult {
	t.Helper()
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	res, err := CreditTenantWalletTx(context.Background(), tx, tenantID, amount, "test top-up", nil)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return res
}

func tenantState(t *testing.T, conn *sql.DB, tenantID int) (status string, balance float64) {
	t.Helper()
	if err := conn.QueryRow(`SELECT status, balance::float8 FROM tenants WHERE id = $1`, tenantID).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	return
}

// jzd5.23: a top-up that covers the month takes it at once and brings back the company and
// the access; a smaller one is credited and changes nothing else.
func TestTopUpResumesASuspendedCompany(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, members := accessCompany(t, conn)
	// 2 seats × 250 ₽ = 500 ₽ a month.
	testpg.MustExec(t, conn, `UPDATE tenants SET status = 'suspended', max_users = 2, max_servers = 5, use_provider_servers = FALSE, balance = 0 WHERE id = $1`, tenant)

	res := creditTenant(t, conn, tenant, 400)
	if status, bal := tenantState(t, conn, tenant); status != "suspended" || bal != 400 || res.Resumed || res.Charged != 0 || res.MonthlyCharge != 500 {
		t.Fatalf("400 ₽ of 500: %s %.2f %+v, want suspended with 400 on the wallet", status, bal, res)
	}
	if row, ok := companyRow(t, conn, members[accUser], tenant); ok && row.Live {
		t.Fatalf("a top-up short of the month gave access: %+v", row)
	}

	res = creditTenant(t, conn, tenant, 200)
	status, bal := tenantState(t, conn, tenant)
	if status != "active" || bal != 100 || !res.Resumed || res.Activated || res.Charged != 500 || res.NewBalance != 100 || !res.AccessChanged {
		t.Fatalf("600 ₽ for a 500 ₽ month: %s %.2f %+v, want active with 100 left", status, bal, res)
	}
	for id, dbID := range members {
		if row, ok := companyRow(t, conn, dbID, tenant); !ok || !row.Live {
			t.Errorf("%s: no access after the resume: %+v", id, row)
		}
	}
	var desc string
	_ = conn.QueryRow(`SELECT description FROM tenant_wallet_transactions WHERE tenant_id = $1 AND direction = 'debit'`, tenant).Scan(&desc)
	if !strings.Contains(desc, "возобновлении") {
		t.Errorf("debit %q, want the month taken at the resume", desc)
	}

	// An active company's top-up is only a credit.
	res = creditTenant(t, conn, tenant, 1000)
	if _, bal := tenantState(t, conn, tenant); bal != 1100 || res.Charged != 0 {
		t.Errorf("active company: balance %.2f, %+v; want 1100 and no charge", bal, res)
	}
}

// An inactive company activates the same way: only a top-up that covers the month.
func TestTopUpActivatesAnInactiveCompanyForAMonth(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, members := accessCompany(t, conn)
	testpg.MustExec(t, conn, `UPDATE tenants SET status = 'inactive', max_users = 2, max_servers = 5, use_provider_servers = FALSE, balance = 0 WHERE id = $1`, tenant)

	if res := creditTenant(t, conn, tenant, 100); res.Activated {
		t.Fatalf("100 ₽ activated a 500 ₽ company: %+v", res)
	}
	res := creditTenant(t, conn, tenant, 400)
	if status, bal := tenantState(t, conn, tenant); status != "active" || bal != 0 || !res.Activated || res.Charged != 500 {
		t.Fatalf("500 ₽ in total: %s %.2f %+v, want active, the month taken", status, bal, res)
	}
	if row, ok := companyRow(t, conn, members[accUser], tenant); !ok || !row.Live {
		t.Errorf("no access after the activation: %+v", row)
	}
}

// The monthly billing suspends a company short of money and ends the access at once; one
// with enough money is charged and keeps it.
func TestMonthlyBillingSuspendsAndEndsTheAccess(t *testing.T) {
	conn := testpg.Open(t)
	tenant, _, members := accessCompany(t, conn)
	testpg.MustExec(t, conn, `UPDATE tenants SET max_users = 2, max_servers = 5, use_provider_servers = FALSE, balance = 100 WHERE id = $1`, tenant)
	syncAccess(t, tenant)

	res, err := BillTenantMonth(context.Background(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	if status, bal := tenantState(t, conn, tenant); !res.Suspended || !res.AccessDown || status != "suspended" || bal != 100 {
		t.Fatalf("100 ₽ for a 500 ₽ month: %+v, %s %.2f; want suspended, the money kept", res, status, bal)
	}
	if row, _ := companyRow(t, conn, members[accBilling], tenant); row.Live {
		t.Errorf("a suspended company's member keeps access: %+v", row)
	}

	setTenantStatus(t, conn, tenant, "active")
	testpg.MustExec(t, conn, `UPDATE tenants SET balance = 800 WHERE id = $1`, tenant)
	syncAccess(t, tenant)
	if res, err = BillTenantMonth(context.Background(), tenant); err != nil || !res.Charged || res.Balance != 300 {
		t.Fatalf("800 ₽ for 500: %+v %v; want charged, 300 left", res, err)
	}
	if row, _ := companyRow(t, conn, members[accBilling], tenant); !row.Live {
		t.Errorf("a charged company's member lost access: %+v", row)
	}
	// Billing skips a company that is not active.
	setTenantStatus(t, conn, tenant, "suspended")
	if res, err = BillTenantMonth(context.Background(), tenant); err != nil || !res.Skipped {
		t.Errorf("suspended company billed: %+v %v", res, err)
	}
}
