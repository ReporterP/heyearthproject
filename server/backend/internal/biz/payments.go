package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

func CreatePaymentRecord(userID string, amount float64, currency, provider, description string) (int, error) {
	var id int
	err := db.DB.QueryRow(`
		INSERT INTO payments (user_id_text, amount, currency, status, provider, payment_method, description, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, userID, amount, currency, provider, provider, description).Scan(&id)
	return id, err
}

func HasActiveSubscription(userID string) (bool, error) {
	var count int
	err := db.DB.QueryRow(`
		SELECT COUNT(*) FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > CURRENT_TIMESTAMP
	`, userID).Scan(&count)
	return count > 0, err
}

// Tenant — minimal B2B tenant record. Mirrors the columns used by the
// public-API/webhook code paths; admin handlers have their own response
// type with additional fields.
type Tenant struct {
	ID                 int
	Name               string
	Slug               string
	Status             string
	Plan               string
	ContactEmail       string
	BillingNotes       string
	MaxUsers           int
	MaxServers         int
	UseProviderServers bool
	Balance            float64
	INN                string
	CreatedAt          time.Time
}

// GetTenantByID returns a tenant by primary key (or nil if not found).
func GetTenantByID(id int) (*Tenant, error) {
	t := &Tenant{}
	var inn sql.NullString
	err := db.DB.QueryRow(`
		SELECT id, name, slug, status, plan, COALESCE(contact_email,''), COALESCE(billing_notes,''),
		       max_users, max_servers, use_provider_servers, balance::float8, COALESCE(inn,''), created_at
		FROM tenants WHERE id = $1
	`, id).Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.Plan, &t.ContactEmail, &t.BillingNotes,
		&t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance, &inn, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inn.Valid {
		t.INN = inn.String
	}
	return t, nil
}

// Payment kinds (payments.metadata.kind) that credit a tenant wallet, not a subscription.
const (
	PaymentKindTenantTopup = "tenant_topup"
	PaymentKindB2BInvoice  = "b2b_invoice"
)

// IsTenantPaymentKind reports whether a payment of this kind is credited to a tenant wallet.
func IsTenantPaymentKind(kind string) bool {
	return kind == PaymentKindTenantTopup || kind == PaymentKindB2BInvoice
}

// UnappliedReasonKey is the payments.metadata key that marks a paid payment which changed
// nothing (no plan, downgrade refused, no tenant…): it holds the reason, in Russian, until
// the payment is applied by hand or fully refunded. The payment sync cron keeps the
// «Оплата получена, но не применена» alert open while the mark is there.
const UnappliedReasonKey = "unapplied_reason"

// Tenant wallet

// CreditTenantWalletResult tells the caller what happened so they can fire
// downstream side-effects (welcome email, log entry, access sync) idempotently.
type CreditTenantWalletResult struct {
	NewBalance float64 // after the credit and the month taken, if one was
	Activated  bool    // inactive → active by this credit (the first month taken)
	Resumed    bool    // suspended → active by this credit (the month taken)
	Charged    float64 // the month taken at activation or resume, 0 otherwise
	// MonthlyCharge is what a month costs the company; when the company stays inactive or
	// suspended the wallet had less than this.
	MonthlyCharge float64
	// AccessChanged: the members' access changed — call NotifySubscriptionChange after commit.
	AccessChanged bool
}

// ErrPaymentAlreadyCredited: the payment was credited to a wallet before
// (tenant_wallet_transactions(payment_id) is unique); nothing was credited again.
var ErrPaymentAlreadyCredited = errors.New("payment already credited to a tenant wallet")

// CreditTenantWalletTx credits a tenant wallet inside the caller's transaction (the
// paid-payment claim, the admin attach); the caller commits. Every statement runs with
// ctx. A second credit for the same payment returns ErrPaymentAlreadyCredited instead of
// doubling the balance — the failed INSERT leaves the transaction unusable, so the
// caller must roll back.
//
// An inactive or suspended company whose wallet now covers the monthly charge is charged
// for the month at once and becomes active, and its members get access in the same
// transaction (jzd5.23; the owner's decision of 2026-11-18 for suspended — inactive follows
// the same rule, otherwise any top-up would open a month of access for every seat). Short of
// the month, the money is credited and the status stays.
func CreditTenantWalletTx(ctx context.Context, tx *sql.Tx, tenantID int, amount float64, description string, paymentID *int) (CreditTenantWalletResult, error) {
	var res CreditTenantWalletResult

	// Lock the row first: it serializes concurrent credits (two webhooks racing) and the
	// monthly billing, and the pre-credit status tells whether this credit may activate.
	info, oldStatus, err := tenantBillingTx(ctx, tx, tenantID)
	if err != nil {
		return res, fmt.Errorf("lock tenant %d: %w", tenantID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tenant_wallet_transactions (tenant_id, amount, direction, description, payment_id)
		VALUES ($1, $2, 'credit', $3, $4)
	`, tenantID, amount, description, paymentID); err != nil {
		var pqErr *pq.Error
		if paymentID != nil && errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return res, ErrPaymentAlreadyCredited
		}
		return res, fmt.Errorf("insert wallet txn: %w", err)
	}

	if err := tx.QueryRowContext(ctx, `
		UPDATE tenants
		SET balance = balance + $1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
		RETURNING balance::float8
	`, amount, tenantID).Scan(&res.NewBalance); err != nil {
		return res, fmt.Errorf("update tenant balance: %w", err)
	}

	res.MonthlyCharge = CalculateTenantMonthlyCharge(info)
	if (oldStatus != "inactive" && oldStatus != "suspended") || res.NewBalance < res.MonthlyCharge {
		return res, nil
	}
	what := "при активации"
	if oldStatus == "suspended" {
		what = "при возобновлении"
	}
	bal, err := debitTenantTx(ctx, tx, tenantID, res.MonthlyCharge, fmt.Sprintf("Списание за месяц %s: %s", what, info.Name))
	if err != nil {
		return res, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tenants SET status = 'active', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, tenantID); err != nil {
		return res, fmt.Errorf("activate tenant %d: %w", tenantID, err)
	}
	res.NewBalance, res.Charged = bal, res.MonthlyCharge
	res.Activated, res.Resumed = oldStatus == "inactive", oldStatus == "suspended"
	if res.AccessChanged, err = SyncTenantAccessTx(ctx, tx, tenantID); err != nil {
		return res, err
	}
	return res, nil
}

// ── Tenant members (B2B many-to-many) ──────────────────────────────────────
//
// tenant_members holds the source-of-truth for "which users have what role
// inside which tenant". A user can belong to multiple tenants. Roles:
//   admin   — full control, including billing/topup and managing members
//   billing — can top up the wallet, view transactions, but not invite users
//   viewer  — read-only access to tenant dashboard
//   user    — proxy access only, no dashboard rights
//
// Note: users.tenant_id is preserved as the "primary tenant" for legacy queries
// that filter on it. New code should consult this table.

type TenantMember struct {
	TenantID int
	UserID   int
	UserGUID string
	Username string
	Email    string
	Role     string
	JoinedAt time.Time
}

// AddTenantMember adds a member (or changes the role of one) and gives them the company-paid
// access in the same transaction while the company is active.
func AddTenantMember(tenantID, userID int, role string) error {
	if role == "" {
		role = "user"
	}
	return changeTenantMembers(tenantID, `
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = $3
	`, tenantID, userID, role)
}

// RemoveTenantMember removes a member; their company-paid access ends in the same transaction.
func RemoveTenantMember(tenantID, userID int) error {
	return changeTenantMembers(tenantID, `DELETE FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
}

// changeTenantMembers runs one change of tenant_members and SyncTenantAccessTx together. The
// tenant row is locked first, as the claim and the billing lock it: the member row's foreign
// key takes a share lock on it, and two changes upgrading it afterwards would deadlock.
func changeTenantMembers(tenantID int, q string, args ...any) error {
	ctx := context.Background()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&locked); err != nil {
		return fmt.Errorf("lock tenant %d: %w", tenantID, err)
	}
	if _, err := tx.ExecContext(ctx, q, args...); err != nil {
		return err
	}
	changed, err := SyncTenantAccessTx(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if changed {
		NotifySubscriptionChange()
	}
	return nil
}

// GetTenantMember returns the role for (tenantID, userID), or ok=false if no
// membership exists. Callers use this for permission checks.
func GetTenantMember(tenantID, userID int) (role string, ok bool, err error) {
	err = db.DB.QueryRow(`SELECT role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

func ListTenantMembers(tenantID int) ([]TenantMember, error) {
	rows, err := db.DB.Query(`
		SELECT m.tenant_id, m.user_id, u.user_id, COALESCE(u.username, ''), COALESCE(u.email, ''), m.role, m.joined_at
		FROM tenant_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.tenant_id = $1
		ORDER BY m.role = 'admin' DESC, m.joined_at ASC
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []TenantMember
	for rows.Next() {
		var m TenantMember
		if err := rows.Scan(&m.TenantID, &m.UserID, &m.UserGUID, &m.Username, &m.Email, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// PrimaryTenantForUser returns the tenant_id the user is a member of with the
// highest-priority role (admin > billing > viewer > user), or 0 if none.
// Used by /api/v1/business/me to pick which tenant to show by default.
func PrimaryTenantForUser(userID int) (tenantID int, role string, err error) {
	err = db.DB.QueryRow(`
		SELECT tenant_id, role FROM tenant_members
		WHERE user_id = $1
		ORDER BY CASE role
		  WHEN 'admin'   THEN 1
		  WHEN 'billing' THEN 2
		  WHEN 'viewer'  THEN 3
		  ELSE                4
		END
		LIMIT 1
	`, userID).Scan(&tenantID, &role)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	return tenantID, role, err
}

// ListTenantTransactions returns the most recent N wallet entries (debit+credit
// mixed, newest first). Used by /api/v1/business/me and admin TenantsPage.
type TenantTransaction struct {
	ID          int
	TenantID    int
	Amount      float64
	Direction   string // "credit" or "debit"
	Description string
	PaymentID   *int
	CreatedAt   time.Time
}

func ListTenantTransactions(tenantID, limit int) ([]TenantTransaction, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	rows, err := db.DB.Query(`
		SELECT id, tenant_id, amount::float8, direction, COALESCE(description, ''), payment_id, created_at
		FROM tenant_wallet_transactions
		WHERE tenant_id = $1
		ORDER BY id DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []TenantTransaction
	for rows.Next() {
		var t TenantTransaction
		var paymentID sql.NullInt64
		if err := rows.Scan(&t.ID, &t.TenantID, &t.Amount, &t.Direction, &t.Description, &paymentID, &t.CreatedAt); err != nil {
			return nil, err
		}
		if paymentID.Valid {
			pid := int(paymentID.Int64)
			t.PaymentID = &pid
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// FindTenantByINN returns the tenant matching the given INN (or nil if absent).
// Used by handleCreateInvoice to route public invoices to existing tenants.
func FindTenantByINN(inn string) (*Tenant, error) {
	inn = strings.TrimSpace(inn)
	if inn == "" {
		return nil, nil
	}
	t := &Tenant{}
	var dbINN sql.NullString
	err := db.DB.QueryRow(`
		SELECT id, name, slug, status, plan, COALESCE(contact_email,''), COALESCE(billing_notes,''),
		       max_users, max_servers, use_provider_servers, balance::float8, COALESCE(inn,''), created_at
		FROM tenants
		WHERE inn = $1
	`, inn).Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.Plan, &t.ContactEmail, &t.BillingNotes,
		&t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance, &dbINN, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if dbINN.Valid {
		t.INN = dbINN.String
	}
	return t, nil
}

// CreateInactiveTenantFromInvoice creates a tenant in 'inactive' status when
// someone pays a B2B invoice for a company we haven't seen before. The tenant
// becomes 'active' on the first successful payment credit (see CreditTenantWalletTx).
// If a tenant with the same INN already exists, that one is returned (idempotent).
func CreateInactiveTenantFromInvoice(inn, legalName, email string) (*Tenant, error) {
	if found, err := FindTenantByINN(inn); err != nil {
		return nil, err
	} else if found != nil {
		return found, nil
	}

	// Slug is a non-conflicting-by-construction value; we don't care about
	// human readability for inactive auto-created tenants.
	slug := slugifyINN(inn)
	t := &Tenant{}
	err := db.DB.QueryRow(`
		INSERT INTO tenants (name, slug, status, plan, contact_email, inn, max_users, max_servers, use_provider_servers, balance)
		VALUES ($1, $2, 'inactive', 'standard', $3, $4, 10, 0, TRUE, 0)
		RETURNING id, name, slug, status, plan, COALESCE(contact_email,''), COALESCE(billing_notes,''),
		         max_users, max_servers, use_provider_servers, balance::float8, created_at
	`, legalName, slug, email, inn).Scan(
		&t.ID, &t.Name, &t.Slug, &t.Status, &t.Plan, &t.ContactEmail, &t.BillingNotes,
		&t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance, &t.CreatedAt,
	)
	if err != nil {
		// Race: a concurrent caller may have inserted between our lookup and
		// our insert. Fall back to fetching their row.
		if found, _ := FindTenantByINN(inn); found != nil {
			return found, nil
		}
		return nil, fmt.Errorf("create inactive tenant: %w", err)
	}
	t.INN = inn
	return t, nil
}

func slugifyINN(inn string) string {
	return "inn-" + strings.TrimSpace(inn)
}

// Tenant claim codes

// RedeemTenantClaimCode binds the calling user to a tenant via a one-shot
// claim code (created by an admin or by the public signup flow). It writes
// both `users.tenant_id` (the legacy primary-tenant field) AND a row in
// `tenant_members` with a role parsed from the code's prefix.
//
// Code formats:
//
//	tn-r-admin-XXX    → admin role
//	tn-r-billing-XXX  → billing role
//	tn-r-viewer-XXX   → viewer role
//	tn-r-user-XXX     → user role (default)
//	anything else     → user role
//
// A code never lowers the role of someone already in the company (that would bypass the
// last-admin guard of the role change), and a newcomer needs a free seat (max_users).
// A member of an active company gets the company-paid access in the same transaction
// (SyncTenantAccessTx); the result names the company and the role the caller now has, so the
// site can send an employee to their own cabinet (jzd5.24).
func RedeemTenantClaimCode(code, userID, userName string) (res TenantClaimResult, err error) {
	ctx := context.Background()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	var tenantID int

	var codeID, maxUses, usedCount int
	err = tx.QueryRowContext(ctx, `
		SELECT id, tenant_id, max_uses, used_count FROM tenant_claim_codes
		WHERE code = $1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		FOR UPDATE
	`, code).Scan(&codeID, &tenantID, &maxUses, &usedCount)
	if err == sql.ErrNoRows {
		return res, fmt.Errorf("invalid or expired code")
	}
	if err != nil {
		return res, err
	}
	if maxUses > 0 && usedCount >= maxUses {
		return res, fmt.Errorf("code usage limit reached")
	}

	// Resolve user numeric id — needed for tenant_members FK.
	var userDBID int
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
		return res, fmt.Errorf("resolve user id: %w", err)
	}

	// The tenant row is locked so two newcomers can't both take the last seat.
	var maxUsers int
	if err = tx.QueryRowContext(ctx, `SELECT max_users FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&maxUsers); err != nil {
		return res, fmt.Errorf("lock tenant: %w", err)
	}
	role := parseRoleFromCode(code)
	var current string
	err = tx.QueryRowContext(ctx, `SELECT role FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`, tenantID, userDBID).Scan(&current)
	switch {
	case err == sql.ErrNoRows:
		var members int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tenant_members WHERE tenant_id = $1`, tenantID).Scan(&members); err != nil {
			return res, err
		}
		if maxUsers > 0 && members >= maxUsers {
			return res, ErrTenantNoSeats
		}
	case err != nil:
		return res, err
	case tenantRoleRank(current) >= tenantRoleRank(role):
		// Already a member with this role or a higher one: the code changes nothing.
		return claimResult(ctx, tx, tenantID, current)
	}

	_, err = tx.ExecContext(ctx, `UPDATE tenant_claim_codes SET used_count = used_count + 1 WHERE id = $1`, codeID)
	if err != nil {
		return res, err
	}

	// Mirror primary tenant on users for legacy code paths that still filter on it.
	if _, err = tx.ExecContext(ctx, `UPDATE users SET tenant_id = $1 WHERE id = $2`, tenantID, userDBID); err != nil {
		return res, err
	}

	// Add the membership, or raise the role a higher code grants.
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`, tenantID, userDBID, role); err != nil {
		return res, fmt.Errorf("add membership: %w", err)
	}
	changed, err := SyncTenantAccessTx(ctx, tx, tenantID)
	if err != nil {
		return res, err
	}
	if res, err = claimResult(ctx, tx, tenantID, role); err != nil {
		return res, err
	}
	if changed {
		NotifySubscriptionChange()
	}
	return res, nil
}

// TenantClaimResult: the company a claim code joined, its status (active: the member has the
// company-paid access now) and the caller's role in it.
type TenantClaimResult struct {
	TenantID     int
	TenantName   string
	TenantStatus string
	Role         string
}

// claimResult reads the company and commits the claim transaction.
func claimResult(ctx context.Context, tx *sql.Tx, tenantID int, role string) (TenantClaimResult, error) {
	res := TenantClaimResult{TenantID: tenantID, Role: role}
	if err := tx.QueryRowContext(ctx, `SELECT name, status FROM tenants WHERE id = $1`, tenantID).Scan(&res.TenantName, &res.TenantStatus); err != nil {
		return TenantClaimResult{}, fmt.Errorf("tenant name: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TenantClaimResult{}, err
	}
	return res, nil
}

// ErrTenantNoSeats: the company has as many members as max_users allows.
var ErrTenantNoSeats = errors.New("no seats left")

// tenantRoleRank orders the roles as PrimaryTenantForUser does: admin > billing > viewer > user.
func tenantRoleRank(role string) int {
	switch role {
	case "admin":
		return 4
	case "billing":
		return 3
	case "viewer":
		return 2
	}
	return 1
}

// parseRoleFromCode extracts the role hint embedded in the claim code by
// b2b_handlers.go and PublicCreateTenantHandler. Falls back to 'user' on
// unrecognized formats.
func parseRoleFromCode(code string) string {
	if !strings.HasPrefix(code, "tn-r-") {
		return "user"
	}
	rest := strings.TrimPrefix(code, "tn-r-")
	for _, role := range []string{"admin", "billing", "viewer", "user"} {
		if strings.HasPrefix(rest, role+"-") {
			return role
		}
	}
	return "user"
}

// Multiple accounts per telegram user
