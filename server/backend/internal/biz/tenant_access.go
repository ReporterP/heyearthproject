package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

// ── Company-paid access (QA 2026-11-18, jzd5.24 / jzd5.23) ─────────────────
//
// While a company (tenants) is 'active', every member (tenant_members, any role) has a
// user_subscriptions row marked with the company (tenant_id) on the member plan. The row is
// an ordinary subscription to every reader of access — Xray, Hysteria2, traffic, the Happ
// subscription, the cabinet — and is skipped by the paths that spend or remind about the
// member's own money (purchase, auto-renew, reminders, Stars). A member's own subscription
// (tenant_id IS NULL) wins over the company one (effectiveSubOrderBy).
//
// The row's valid_until is a technical horizon, not a paid-through date: the truth is the
// company's status, which SyncTenantAccessTx enforces wherever the status or the member list
// changes, and the hourly StartTenantAccessLoop re-checks. 35 days keep the row clear of every
// «ends soon» window (reminders, auto-renew, the Happ notice); the horizon moves once a day.

// TenantMemberPlanSetting names (by slug) the plan the members of an active company get.
const TenantMemberPlanSetting = "b2b_member_plan"

// DefaultTenantMemberPlan is the member plan when the setting is empty: «Полный» on prod.
const DefaultTenantMemberPlan = "all-inclusive"

const (
	tenantAccessDays        = 35
	tenantAccessRefreshDays = 34
)

// TenantMemberPlanSlug is the configured member plan slug (DefaultTenantMemberPlan when unset).
func TenantMemberPlanSlug() string {
	s := strings.ToLower(strings.TrimSpace(GetSettingOrDefault(TenantMemberPlanSetting, "")))
	if s == "" {
		return DefaultTenantMemberPlan
	}
	return s
}

const (
	// Every live company row ends (the company is not active).
	tenantEndAllQuery = `
		UPDATE user_subscriptions SET valid_until = NOW(), status = 'cancelled'
		 WHERE tenant_id = $1 AND valid_until > NOW()`
	// The rows of people who are no longer members end.
	tenantEndLeftQuery = `
		UPDATE user_subscriptions us SET valid_until = NOW(), status = 'cancelled'
		 WHERE us.tenant_id = $1 AND us.valid_until > NOW()
		   AND NOT EXISTS (SELECT 1 FROM tenant_members m WHERE m.tenant_id = us.tenant_id AND m.user_id = us.user_id)`
	// Members whose access the grant below changes: no live row yet, or another plan.
	tenantGainingQuery = `
		SELECT COUNT(*) FROM tenant_members m
		  LEFT JOIN user_subscriptions us ON us.user_id = m.user_id AND us.tenant_id = m.tenant_id
		 WHERE m.tenant_id = $1
		   AND (us.id IS NULL OR us.valid_until <= NOW() OR us.status <> 'active' OR us.plan_id IS DISTINCT FROM $2)`
	// Every member gets (or keeps) a live row on the member plan; the horizon moves only when
	// less than $4 days are left, so the hourly loop writes a row about once a day. A row that
	// had ended starts a new traffic cycle.
	tenantGrantQuery = `
		INSERT INTO user_subscriptions (user_id, tenant_id, plan_id, valid_until, auto_renew, price_paid, status, started_at)
		SELECT m.user_id, m.tenant_id, $2, NOW() + make_interval(days => $3), FALSE, 0, 'active', NOW()
		  FROM tenant_members m
		 WHERE m.tenant_id = $1
		ON CONFLICT (user_id, tenant_id) WHERE tenant_id IS NOT NULL DO UPDATE SET
		    started_at  = CASE WHEN user_subscriptions.valid_until <= NOW() OR user_subscriptions.status <> 'active'
		                       THEN NOW() ELSE user_subscriptions.started_at END,
		    plan_id     = EXCLUDED.plan_id,
		    valid_until = EXCLUDED.valid_until,
		    status      = 'active',
		    auto_renew  = FALSE
		 WHERE user_subscriptions.valid_until < NOW() + make_interval(days => $4)
		    OR user_subscriptions.status <> 'active'
		    OR user_subscriptions.plan_id IS DISTINCT FROM EXCLUDED.plan_id`
)

// SyncTenantAccessTx makes the company rows of tenantID match the company, inside the
// caller's transaction (it locks the tenant row; a caller already holding it loses nothing):
// active and the member plan found — every member has a live row on that plan; not active —
// every row ends; a member who left loses theirs. With the member plan missing nobody gains
// access, the rows already given stay until their horizon (logged). changed: someone gained
// or lost access, or the plan changed — the caller calls NotifySubscriptionChange after commit.
func SyncTenantAccessTx(ctx context.Context, tx *sql.Tx, tenantID int) (changed bool, err error) {
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // deleted: ON DELETE CASCADE took its rows
	}
	if err != nil {
		return false, fmt.Errorf("lock tenant %d: %w", tenantID, err)
	}

	if status != "active" {
		n, err := tenantExecCount(ctx, tx, tenantEndAllQuery, tenantID)
		if err != nil {
			return false, fmt.Errorf("end access of tenant %d: %w", tenantID, err)
		}
		return n > 0, nil
	}

	n, err := tenantExecCount(ctx, tx, tenantEndLeftQuery, tenantID)
	if err != nil {
		return false, fmt.Errorf("end access of former members of tenant %d: %w", tenantID, err)
	}
	changed = n > 0

	slug := TenantMemberPlanSlug()
	var planID int
	err = tx.QueryRowContext(ctx, `SELECT id FROM plans WHERE slug = $1`, slug).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		log.Printf("[tenant-access] tenant %d: member plan %q not found — no new access (Настройки → Подключение → %s)", tenantID, slug, TenantMemberPlanSetting)
		return changed, nil
	}
	if err != nil {
		return changed, fmt.Errorf("member plan %q: %w", slug, err)
	}

	var gaining int
	if err := tx.QueryRowContext(ctx, tenantGainingQuery, tenantID, planID).Scan(&gaining); err != nil {
		return changed, fmt.Errorf("access changes of tenant %d: %w", tenantID, err)
	}
	if _, err := tx.ExecContext(ctx, tenantGrantQuery, tenantID, planID, tenantAccessDays, tenantAccessRefreshDays); err != nil {
		return changed, fmt.Errorf("grant access of tenant %d: %w", tenantID, err)
	}
	return changed || gaining > 0, nil
}

func tenantExecCount(ctx context.Context, tx *sql.Tx, q string, args ...any) (int64, error) {
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SyncTenantAccess runs SyncTenantAccessTx in its own transaction and asks for an Xray /
// Hysteria2 sync when access changed. For callers that changed the status or the members
// outside a transaction of theirs (admin edit, role change, member removal).
func SyncTenantAccess(ctx context.Context, tenantID int) error {
	changed, err := syncTenantAccessOnce(ctx, tenantID)
	if changed {
		NotifySubscriptionChange()
	}
	return err
}

func syncTenantAccessOnce(ctx context.Context, tenantID int) (bool, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	changed, err := SyncTenantAccessTx(ctx, tx, tenantID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return changed, nil
}

// SyncAllTenantAccess re-checks every company (the hourly loop, a changed member plan). One
// company's error does not stop the others; the first error is returned. It returns how many
// companies' access changed and asks for one sync when any did.
func SyncAllTenantAccess(ctx context.Context) (int, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT id FROM tenants ORDER BY id`)
	if err != nil {
		return 0, fmt.Errorf("list tenants: %w", err)
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var firstErr error
	changedCount := 0
	for _, id := range ids {
		changed, err := syncTenantAccessOnce(ctx, id)
		if err != nil {
			log.Printf("[tenant-access] tenant %d: %v", id, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if changed {
			changedCount++
		}
	}
	if changedCount > 0 {
		NotifySubscriptionChange()
	}
	return changedCount, firstErr
}

// ── Monthly charge under the tenant row lock ───────────────────────────────

// tenantBillingTx locks the tenant row and reads what the monthly charge depends on.
func tenantBillingTx(ctx context.Context, tx *sql.Tx, tenantID int) (TenantBillingInfo, string, error) {
	var t TenantBillingInfo
	var status string
	err := tx.QueryRowContext(ctx, `
		SELECT id, name, plan, max_users, max_servers, use_provider_servers, balance::float8, status
		  FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).
		Scan(&t.TenantID, &t.Name, &t.Plan, &t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance, &status)
	return t, status, err
}

// debitTenantTx takes amount from the tenant wallet (row already locked) and returns the new
// balance. A zero amount writes nothing.
func debitTenantTx(ctx context.Context, tx *sql.Tx, tenantID int, amount float64, description string) (float64, error) {
	if amount <= 0 {
		var bal float64
		err := tx.QueryRowContext(ctx, `SELECT balance::float8 FROM tenants WHERE id = $1`, tenantID).Scan(&bal)
		return bal, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tenant_wallet_transactions (tenant_id, amount, direction, description)
		VALUES ($1, $2, 'debit', $3)`, tenantID, amount, description); err != nil {
		return 0, fmt.Errorf("insert debit: %w", err)
	}
	var bal float64
	if err := tx.QueryRowContext(ctx, `
		UPDATE tenants SET balance = balance - $1, updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 RETURNING balance::float8`, amount, tenantID).Scan(&bal); err != nil {
		return 0, fmt.Errorf("debit balance: %w", err)
	}
	return bal, nil
}

// TenantMonthResult is what one company's monthly billing did.
type TenantMonthResult struct {
	Skipped    bool // no longer active by the time its row was locked
	Charged    bool
	Suspended  bool
	Charge     float64
	Balance    float64 // after the charge, or what was there when suspended
	Name       string
	AccessDown bool // members lost access (NotifySubscriptionChange was asked)
}

// BillTenantMonth takes one month from an active company under its row lock: enough on the
// wallet — the debit; short — 'suspended', and the members' access ends in the same
// transaction (jzd5.23). Locking first means a top-up racing the billing is either counted or
// resumes the company afterwards.
func BillTenantMonth(ctx context.Context, tenantID int) (TenantMonthResult, error) {
	var res TenantMonthResult
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	info, status, err := tenantBillingTx(ctx, tx, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		res.Skipped = true
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("lock tenant %d: %w", tenantID, err)
	}
	res.Name, res.Charge, res.Balance = info.Name, CalculateTenantMonthlyCharge(info), info.Balance
	if status != "active" {
		res.Skipped = true
		return res, nil
	}
	if info.Balance >= res.Charge {
		bal, err := debitTenantTx(ctx, tx, tenantID, res.Charge, fmt.Sprintf("Списание за месяц: %s", info.Name))
		if err != nil {
			return res, err
		}
		if err := tx.Commit(); err != nil {
			return res, err
		}
		res.Charged, res.Balance = true, bal
		return res, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tenants SET status = 'suspended', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, tenantID); err != nil {
		return res, fmt.Errorf("suspend tenant %d: %w", tenantID, err)
	}
	changed, err := SyncTenantAccessTx(ctx, tx, tenantID)
	if err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.Suspended, res.AccessDown = true, changed
	if changed {
		NotifySubscriptionChange()
	}
	return res, nil
}
