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

type PaymentRecord struct {
	ID              int
	UserID          string
	Amount          float64
	Currency        string
	Status          string
	Provider        string
	YookassaID      string
	YookassaStatus  string
	Description     string
	ConfirmationURL string
	PaidAt          *time.Time
	CreatedAt       time.Time
}

func CreatePaymentRecord(userID string, amount float64, currency, provider, description string) (int, error) {
	var id int
	err := db.DB.QueryRow(`
		INSERT INTO payments (user_id_text, amount, currency, status, provider, payment_method, description, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, userID, amount, currency, provider, provider, description).Scan(&id)
	return id, err
}

func UpdatePaymentYookassa(paymentDBID int, yookassaID, yookassaStatus, confirmationURL string) error {
	_, err := db.DB.Exec(`
		UPDATE payments
		SET yookassa_payment_id = $1, yookassa_status = $2, confirmation_url = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, yookassaID, yookassaStatus, confirmationURL, paymentDBID)
	return err
}

func GetPaymentByYookassaID(yookassaID string) (*PaymentRecord, error) {
	var p PaymentRecord
	err := db.DB.QueryRow(`
		SELECT id, COALESCE(user_id_text,''), amount, COALESCE(currency,'RUB'), status, COALESCE(provider,'yookassa'),
		       COALESCE(yookassa_payment_id,''), COALESCE(yookassa_status,''), COALESCE(description,''),
		       COALESCE(confirmation_url,''), paid_at, created_at
		FROM payments WHERE yookassa_payment_id = $1
	`, yookassaID).Scan(
		&p.ID, &p.UserID, &p.Amount, &p.Currency, &p.Status, &p.Provider,
		&p.YookassaID, &p.YookassaStatus, &p.Description,
		&p.ConfirmationURL, &p.PaidAt, &p.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &p, err
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

// Tenant wallet

// CreditTenantWalletResult tells the caller what happened so they can fire
// downstream side-effects (welcome email, log entry) idempotently. Activated
// is true iff this credit transitioned the tenant from 'inactive' to 'active'.
type CreditTenantWalletResult struct {
	NewBalance float64
	Activated  bool
}

// ErrPaymentAlreadyCredited: the payment was credited to a wallet before
// (tenant_wallet_transactions(payment_id) is unique); nothing was credited again.
var ErrPaymentAlreadyCredited = errors.New("payment already credited to a tenant wallet")

// CreditTenantWalletTx credits a tenant wallet inside the caller's transaction (the
// paid-payment claim, the admin attach); the caller commits. Every statement runs with
// ctx. A second credit for the same payment returns ErrPaymentAlreadyCredited instead of
// doubling the balance — the failed INSERT leaves the transaction unusable, so the
// caller must roll back.
func CreditTenantWalletTx(ctx context.Context, tx *sql.Tx, tenantID int, amount float64, description string, paymentID *int) (CreditTenantWalletResult, error) {
	var res CreditTenantWalletResult

	// Capture the pre-credit status under FOR UPDATE so we can detect the
	// inactive→active transition. Locking the row also serializes concurrent
	// credits against the same tenant (e.g. two webhooks racing).
	var oldStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&oldStatus); err != nil {
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

	// Apply credit; auto-activate if tenant was inactive (first payment unlocks
	// the account). Status stays 'suspended' or 'active' otherwise.
	if err := tx.QueryRowContext(ctx, `
		UPDATE tenants
		SET balance = balance + $1,
		    status = CASE WHEN status = 'inactive' THEN 'active' ELSE status END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
		RETURNING balance::float8
	`, amount, tenantID).Scan(&res.NewBalance); err != nil {
		return res, fmt.Errorf("update tenant balance: %w", err)
	}

	if oldStatus == "inactive" {
		res.Activated = true
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

func AddTenantMember(tenantID, userID int, role string) error {
	if role == "" {
		role = "user"
	}
	_, err := db.DB.Exec(`
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = $3
	`, tenantID, userID, role)
	return err
}

func RemoveTenantMember(tenantID, userID int) error {
	_, err := db.DB.Exec(`DELETE FROM tenant_members WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	return err
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

func DebitTenantWallet(tenantID int, amount float64, description string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var balance float64
	if err := tx.QueryRow(`SELECT balance FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&balance); err != nil {
		return err
	}
	if balance < amount {
		return fmt.Errorf("insufficient balance: have %.2f, need %.2f", balance, amount)
	}

	_, err = tx.Exec(`
		INSERT INTO tenant_wallet_transactions (tenant_id, amount, direction, description)
		VALUES ($1, $2, 'debit', $3)
	`, tenantID, amount, description)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE tenants SET balance = balance - $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, amount, tenantID)
	if err != nil {
		return err
	}

	return tx.Commit()
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
func RedeemTenantClaimCode(code, userID, userName string) (tenantID int, err error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var codeID, maxUses, usedCount int
	err = tx.QueryRow(`
		SELECT id, tenant_id, max_uses, used_count FROM tenant_claim_codes
		WHERE code = $1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
	`, code).Scan(&codeID, &tenantID, &maxUses, &usedCount)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("invalid or expired code")
	}
	if err != nil {
		return 0, err
	}
	if maxUses > 0 && usedCount >= maxUses {
		return 0, fmt.Errorf("code usage limit reached")
	}

	_, err = tx.Exec(`UPDATE tenant_claim_codes SET used_count = used_count + 1 WHERE id = $1`, codeID)
	if err != nil {
		return 0, err
	}

	// Resolve user numeric id — needed for tenant_members FK.
	var userDBID int
	if err = tx.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
		return 0, fmt.Errorf("resolve user id: %w", err)
	}

	// Mirror primary tenant on users for legacy code paths that still filter on it.
	if _, err = tx.Exec(`UPDATE users SET tenant_id = $1 WHERE id = $2`, tenantID, userDBID); err != nil {
		return 0, err
	}

	// Add membership with role parsed from code prefix.
	role := parseRoleFromCode(code)
	if _, err = tx.Exec(`
		INSERT INTO tenant_members (tenant_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`, tenantID, userDBID, role); err != nil {
		return 0, fmt.Errorf("add membership: %w", err)
	}

	return tenantID, tx.Commit()
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

func LinkTelegramUser(telegramID int64, userID, label string) error {
	_, err := db.DB.Exec(`
		INSERT INTO telegram_linked_users (telegram_id, user_id, label)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id, user_id) DO UPDATE SET label = $3
	`, telegramID, userID, label)
	return err
}

type LinkedAccount struct {
	UserID   string
	Label    string
	IsActive bool
}

func GetLinkedAccounts(telegramID int64) ([]LinkedAccount, error) {
	rows, err := db.DB.Query(`
		SELECT user_id, label, is_active FROM telegram_linked_users
		WHERE telegram_id = $1 ORDER BY created_at
	`, telegramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []LinkedAccount
	for rows.Next() {
		var a LinkedAccount
		if err := rows.Scan(&a.UserID, &a.Label, &a.IsActive); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

func SetActiveLinkedAccount(telegramID int64, userID string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE telegram_linked_users SET is_active = FALSE WHERE telegram_id = $1`, telegramID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE telegram_linked_users SET is_active = TRUE WHERE telegram_id = $1 AND user_id = $2`, telegramID, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
