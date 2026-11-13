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

// Plan represents a subscription plan configured by admin.
type Plan struct {
	ID                    int      `json:"id"`
	Slug                  string   `json:"slug"`
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	PriceRub              int      `json:"price_rub"`
	DurationDays          int      `json:"duration_days"`
	TrafficLimitBytes     *int64   `json:"traffic_limit_bytes"`
	AllowedProtocols      []string `json:"allowed_protocols"`
	Features              string   `json:"features"` // JSON string
	IsActive              bool     `json:"is_active"`
	DirectRoutingEnabled  bool     `json:"direct_routing_enabled"`
	SortOrder             int      `json:"sort_order"`
	AutoRenewDefault      bool     `json:"auto_renew_default"`
	MaxActivationsPerUser *int     `json:"max_activations_per_user,omitempty"`
	// Express (Hysteria2) — UDP/QUIC inbound on the Hub for low-latency traffic.
	// Brutal congestion control needs explicit up/down targets in Mbps per plan.
	ExpressEnabled  bool   `json:"express_enabled"`
	ExpressUpMbps   int    `json:"express_up_mbps"`
	ExpressDownMbps int    `json:"express_down_mbps"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
	// Computed
	ServerIDs []int `json:"server_ids,omitempty"`
}

// ListActivePlans returns all active plans for display on the site.
func ListActivePlans() ([]Plan, error) {
	rows, err := db.DB.Query(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, direct_routing_enabled,
		       sort_order, auto_renew_default,
		       max_activations_per_user,
		       express_enabled, express_up_mbps, express_down_mbps,
		       created_at, updated_at
		FROM plans WHERE is_active = TRUE
		ORDER BY sort_order, price_rub
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
			&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive,
			&p.DirectRoutingEnabled, &p.SortOrder,
			&p.AutoRenewDefault, &p.MaxActivationsPerUser,
			&p.ExpressEnabled, &p.ExpressUpMbps, &p.ExpressDownMbps,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		p.ServerIDs = getPlanServerIDs(p.ID)
		plans = append(plans, p)
	}
	return plans, nil
}

// ListAllPlans returns all plans (for admin).
func ListAllPlans() ([]Plan, error) {
	rows, err := db.DB.Query(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, direct_routing_enabled,
		       sort_order, auto_renew_default,
		       max_activations_per_user,
		       express_enabled, express_up_mbps, express_down_mbps,
		       created_at, updated_at
		FROM plans ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
			&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive,
			&p.DirectRoutingEnabled, &p.SortOrder,
			&p.AutoRenewDefault, &p.MaxActivationsPerUser,
			&p.ExpressEnabled, &p.ExpressUpMbps, &p.ExpressDownMbps,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		p.ServerIDs = getPlanServerIDs(p.ID)
		plans = append(plans, p)
	}
	return plans, nil
}

// GetPlanByID returns a single plan.
func GetPlanByID(id int) (*Plan, error) {
	p, err := loadPlan(id)
	if err != nil {
		return nil, err
	}
	p.ServerIDs = getPlanServerIDs(p.ID)
	return p, nil
}

// loadPlan is GetPlanByID without ServerIDs. sql.ErrNoRows for an unknown id.
func loadPlan(id int) (*Plan, error) {
	var p Plan
	err := db.DB.QueryRow(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, direct_routing_enabled,
		       sort_order, auto_renew_default,
		       max_activations_per_user,
		       express_enabled, express_up_mbps, express_down_mbps,
		       created_at, updated_at
		FROM plans WHERE id = $1
	`, id).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
		&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive,
		&p.DirectRoutingEnabled, &p.SortOrder,
		&p.AutoRenewDefault, &p.MaxActivationsPerUser,
		&p.ExpressEnabled, &p.ExpressUpMbps, &p.ExpressDownMbps,
		&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPlanBySlug returns plan by slug.
func GetPlanBySlug(slug string) (*Plan, error) {
	var p Plan
	err := db.DB.QueryRow(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, direct_routing_enabled,
		       sort_order, auto_renew_default,
		       max_activations_per_user,
		       express_enabled, express_up_mbps, express_down_mbps,
		       created_at, updated_at
		FROM plans WHERE slug = $1
	`, slug).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
		&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive,
		&p.DirectRoutingEnabled, &p.SortOrder,
		&p.AutoRenewDefault, &p.MaxActivationsPerUser,
		&p.ExpressEnabled, &p.ExpressUpMbps, &p.ExpressDownMbps,
		&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.ServerIDs = getPlanServerIDs(p.ID)
	return &p, nil
}

// ListPlanServersContext returns all of plan_servers as plan id → server ids
// (ascending), in one query. Every plan is included, also one hidden from the
// site (is_active = FALSE): its existing subscribers are still routed by it.
// The xray sync builds the per-plan balancers from it.
func ListPlanServersContext(ctx context.Context) (map[int][]int, error) {
	rows, err := db.DB.QueryContext(ctx, `SELECT plan_id, server_id FROM plan_servers ORDER BY plan_id, server_id`)
	if err != nil {
		return nil, fmt.Errorf("plan servers: %w", err)
	}
	defer rows.Close()
	out := map[int][]int{}
	for rows.Next() {
		var planID, serverID int
		if err := rows.Scan(&planID, &serverID); err != nil {
			return nil, fmt.Errorf("plan servers scan: %w", err)
		}
		out[planID] = append(out[planID], serverID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plan servers rows: %w", err)
	}
	return out, nil
}

// PlanServerIDs is the strict form of Plan.ServerIDs: the plan's servers
// (ascending, nil for none), or an error when the read failed.
func PlanServerIDs(planID int) ([]int, error) {
	rows, err := db.DB.Query("SELECT server_id FROM plan_servers WHERE plan_id = $1 ORDER BY server_id", planID)
	if err != nil {
		return nil, fmt.Errorf("plan %d servers: %w", planID, err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("plan %d servers scan: %w", planID, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("plan %d servers: %w", planID, err)
	}
	return ids, nil
}

func getPlanServerIDs(planID int) []int {
	rows, err := db.DB.Query("SELECT server_id FROM plan_servers WHERE plan_id = $1 ORDER BY server_id", planID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// planWriteTimeout bounds one plan write (the plan row and its plan_servers in one transaction).
const planWriteTimeout = 15 * time.Second

const (
	deletePlanServersSQL = `DELETE FROM plan_servers WHERE plan_id = $1`
	insertPlanServersSQL = `INSERT INTO plan_servers (plan_id, server_id) SELECT $1, unnest($2::int[]) ON CONFLICT DO NOTHING`
)

// replacePlanServersTx replaces a plan's exits inside the caller's transaction, so the xray sync
// never sees a plan whose old rows are gone and the new ones not yet in (its users would fall to
// the catch-all balancer = other plans' exits).
func replacePlanServersTx(ctx context.Context, tx *sql.Tx, planID int, serverIDs []int) error {
	if _, err := tx.ExecContext(ctx, deletePlanServersSQL, planID); err != nil {
		return fmt.Errorf("delete plan_servers: %w", err)
	}
	if len(serverIDs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(serverIDs))
	for _, id := range serverIDs {
		ids = append(ids, int64(id))
	}
	if _, err := tx.ExecContext(ctx, insertPlanServersSQL, planID, pq.Array(ids)); err != nil {
		return fmt.Errorf("insert plan_servers: %w", err)
	}
	return nil
}

// CreatePlan creates a new plan with its exits in one transaction.
func CreatePlan(p *Plan) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), planWriteTimeout)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	var id int
	err = tx.QueryRowContext(ctx, `
		INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		                   allowed_protocols, features, is_active, direct_routing_enabled, sort_order,
		                   auto_renew_default, max_activations_per_user,
		                   express_enabled, express_up_mbps, express_down_mbps)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13, $14, $15, $16)
		RETURNING id
	`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
		pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.DirectRoutingEnabled,
		p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser,
		p.ExpressEnabled, p.ExpressUpMbps, p.ExpressDownMbps).Scan(&id)
	if err != nil {
		return 0, err
	}
	if err := replacePlanServersTx(ctx, tx, id, p.ServerIDs); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return id, nil
}

// UpdatePlan updates a plan and replaces its exits (p.ServerIDs) in one transaction.
func UpdatePlan(id int, p *Plan) error {
	ctx, cancel := context.WithTimeout(context.Background(), planWriteTimeout)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE plans SET slug=$1, name=$2, description=$3, price_rub=$4, duration_days=$5,
		       traffic_limit_bytes=$6, allowed_protocols=$7, features=$8::jsonb, is_active=$9,
		       direct_routing_enabled=$10, sort_order=$11, auto_renew_default=$12,
		       max_activations_per_user=$13,
		       express_enabled=$14, express_up_mbps=$15, express_down_mbps=$16,
		       updated_at=CURRENT_TIMESTAMP
		WHERE id = $17
	`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
		pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.DirectRoutingEnabled,
		p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser,
		p.ExpressEnabled, p.ExpressUpMbps, p.ExpressDownMbps, id); err != nil {
		return err
	}
	if err := replacePlanServersTx(ctx, tx, id, p.ServerIDs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// DeletePlan removes a plan.
func DeletePlan(id int) error {
	_, err := db.DB.Exec("DELETE FROM plans WHERE id = $1", id)
	return err
}

// GetUserPlan returns the active plan for a user: the plan of their latest
// status = 'active' subscription, ordered by valid_until alone. It is
// deliberately NOT the effective subscription of effectiveSubOrderBy: plan-less
// legacy rows are not demoted, so a later plan-less subscription yields "no
// active plan". Switching it would change the protocol gate (IsProtocolAllowed),
// the AmneziaWG gate, auto-renew and the Express speeds for such users, not just
// a tie-break.
func GetUserPlan(userID string) (*Plan, error) {
	var planID sql.NullInt64
	err := db.DB.QueryRow(userPlanQuery, userID).Scan(&planID)
	if err != nil || !planID.Valid {
		return nil, fmt.Errorf("no active plan")
	}
	return GetPlanByID(int(planID.Int64))
}

// userPlanQuery is the plan id of GetUserPlan / ActiveUserPlan (u.user_id = $1).
var userPlanQuery = `
		SELECT us.plan_id FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
	`

// ActiveUserPlan is GetUserPlan with a strict error contract, for callers that
// must not mistake a failed read for "no plan" (the subscription builders):
// (nil, nil) when the user has no active subscription with a plan (or the plan
// row is gone), an error only when a read failed. GetUserPlan reports every
// failure as "no active plan", which IsProtocolAllowed reads as "free tier:
// allow everything" and the Clash builder as "default Express speeds".
// ServerIDs is not loaded.
func ActiveUserPlan(userID string) (*Plan, error) {
	var planID sql.NullInt64
	err := db.DB.QueryRow(userPlanQuery, userID).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active plan: %w", err)
	}
	if !planID.Valid {
		return nil, nil
	}
	p, err := loadPlan(int(planID.Int64))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("plan %d: %w", planID.Int64, err)
	}
	return p, nil
}

// IsProtocolAllowed checks if a protocol is available in the user's plan.
func IsProtocolAllowed(userID, protocol string) bool {
	plan, err := GetUserPlan(userID)
	if err != nil {
		return true // no plan = free tier = allow all (backward compat)
	}
	return PlanAllowsProtocol(plan, protocol)
}

// PlanAllowsProtocol is the protocol gate of IsProtocolAllowed for an already
// loaded plan; a nil plan (no active plan) is the free tier and allows all
// (backward compat).
func PlanAllowsProtocol(plan *Plan, protocol string) bool {
	if plan == nil {
		return true
	}
	for _, p := range plan.AllowedProtocols {
		if p == "all" || strings.EqualFold(p, protocol) {
			return true
		}
	}
	return false
}

// computeBonusDays — общая математика для конвертации остатка старого плана
// в бонус-дни нового. Используется и в ActivateSubscriptionWithPlan, и в
// CalculateProrationCredit чтобы числа в UI preview и в финальной активации
// были идентичны.
//
// Формула:
//
//	leftover_money = currentPriceRub × leftoverDays / currentDurationDays
//	bonus_days     = floor(leftover_money × newDurationDays / newPriceRub)
//	bonus_days     = min(bonus_days, newDurationDays × 10)  // защита
//
// Edge cases:
//
//	currentPriceRub == 0      → leftover_money = 0       → bonus = 0
//	newPriceRub == 0          → деление guarded           → bonus = 0
//	leftoverDays <= 0         → leftover_money = 0        → bonus = 0
//	currentDurationDays <= 0  → бессмысленный план       → bonus = 0
func computeBonusDays(currentPriceRub, currentDurationDays, leftoverDays, newPriceRub, newDurationDays int) int {
	if currentPriceRub <= 0 || currentDurationDays <= 0 || leftoverDays <= 0 || newPriceRub <= 0 || newDurationDays <= 0 {
		return 0
	}
	leftoverMoney := float64(currentPriceRub) * float64(leftoverDays) / float64(currentDurationDays)
	bonus := int(leftoverMoney * float64(newDurationDays) / float64(newPriceRub))
	maxBonus := newDurationDays * 10
	if bonus > maxBonus {
		bonus = maxBonus
	}
	return bonus
}

// ErrDowngradeNotAllowed возвращается из ActivateSubscriptionWithPlan когда
// активная подписка дороже нового плана. После истечения валиdity (current.valid_until ≤ NOW)
// эта проверка не срабатывает — юзер свободен.
var ErrDowngradeNotAllowed = errors.New("downgrade_not_allowed")

// ActivateSubscriptionWithPlan creates or extends a user's subscription based on a plan.
//
// Behaviour by case:
//  1. No active subscription (or current expired):  INSERT new row, valid_until = NOW + durationDays.
//  2. Active subscription, same plan:               UPDATE valid_until = current + durationDays (simple stacking).
//  3. Active subscription, different plan, new cheaper:  ErrDowngradeNotAllowed — caller must surface as HTTP 400.
//  4. Active subscription, different plan, new ≥ current price:
//     leftover_money = current.price × leftover_days / current.duration
//     bonus_days     = floor(leftover_money × durationDays / pricePaid)  (capped at durationDays × 10)
//     Update row to new plan_id, valid_until = NOW + durationDays + bonus_days, price_paid = pricePaid.
//
// pricePaid is the *new* plan's price (not the amount after discount) — it's what
// determines bonus_days. Promocode discounts already adjusted the YooKassa charge
// upstream; here we always use new_plan.price_rub for honest conversion.
func ActivateSubscriptionWithPlan(userID string, planID int, durationDays int, pricePaid int, promocodeUsed string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if err := ActivateSubscriptionWithPlanTx(context.Background(), tx, userID, planID, durationDays, pricePaid, promocodeUsed); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	NotifySubscriptionChange()
	return nil
}

// ActivateSubscriptionWithPlanTx is ActivateSubscriptionWithPlan inside the caller's
// transaction (the paid-payment claim): it neither commits nor notifies — the caller
// commits and then calls NotifySubscriptionChange. Every statement runs with ctx, so
// the caller's timeout also bounds lock waits. ErrDowngradeNotAllowed comes back before
// any write, so the transaction stays usable.
func ActivateSubscriptionWithPlanTx(ctx context.Context, tx *sql.Tx, userID string, planID int, durationDays int, pricePaid int, promocodeUsed string) error {
	_, err := ActivateSubscriptionTx(ctx, tx, userID, planID, durationDays, pricePaid, promocodeUsed, ActivateOpts{})
	return err
}

// ActivateOpts adjusts ActivateSubscriptionTx.
type ActivateOpts struct {
	// InsertAutoRenew is auto_renew of a NEW subscription row; nil = the plan's
	// auto_renew_default (false for the free trial). An extended or switched row keeps
	// the user's own choice, except that leaving a free plan for a paid one takes the
	// paid plan's default.
	InsertAutoRenew *bool
	// TrafficLimit, when set, is written to the row in every branch (0 = unlimited:
	// promocode access).
	TrafficLimit *int64
}

// ActivateSubscriptionTx is ActivateSubscriptionWithPlanTx with options; it returns the
// id of the subscription row it created or changed.
//
// A free grant (pricePaid 0, a promocode's days) over a PAID row only adds days (and
// switches the plan when it is a better one): the row keeps its price_paid, promocode
// mark, traffic limit and auto-renew, so free days never turn paid access into
// free-forever renewals.
func ActivateSubscriptionTx(ctx context.Context, tx *sql.Tx, userID string, planID int, durationDays int, pricePaid int, promocodeUsed string, opts ActivateOpts) (int, error) {
	var dbUserID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID); err != nil {
		return 0, fmt.Errorf("resolve user %s: %w", userID, err)
	}

	// Fetch new plan's LIST price (not pricePaid, which may be discounted by
	// promocode). Downgrade comparison and bonus_days math must use list prices
	// to be semantically honest — see code-quality review of 8197e40.
	newPlan, err := GetPlanByID(planID)
	if err != nil || newPlan == nil {
		return 0, fmt.Errorf("resolve new plan %d: %w", planID, err)
	}
	newPlanListPrice := newPlan.PriceRub

	// Fetch current active subscription (any plan) with FOR UPDATE so concurrent
	// payments serialize on this user's most-recent active row instead of racing
	// SELECT-then-UPDATE.
	var currentSubID int
	var currentPlanID int
	var currentPriceRub int
	var currentDurationDays int
	var currentValidUntil time.Time
	var currentPricePaid int
	hasActive := true
	scanErr := tx.QueryRowContext(ctx, `
		SELECT us.id, us.plan_id, p.price_rub, p.duration_days, us.valid_until, COALESCE(us.price_paid, 0)
		FROM user_subscriptions us
		JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
		FOR UPDATE OF us
	`, dbUserID).Scan(&currentSubID, &currentPlanID, &currentPriceRub, &currentDurationDays, &currentValidUntil, &currentPricePaid)
	if scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			hasActive = false // нет активной подписки → ветка (1)
		} else {
			return 0, fmt.Errorf("scan current subscription: %w", scanErr)
		}
	}
	freeOverPaid := hasActive && pricePaid == 0 && currentPricePaid > 0

	// traffic_limit: written only when the caller sets it ($n IS NULL keeps the row's).
	var trafficLimit sql.NullInt64
	if opts.TrafficLimit != nil {
		trafficLimit = sql.NullInt64{Int64: *opts.TrafficLimit, Valid: true}
	}

	// (1) No active subscription — fresh INSERT.
	if !hasActive {
		autoRenew := newPlan.AutoRenewDefault
		if opts.InsertAutoRenew != nil {
			autoRenew = *opts.InsertAutoRenew
		}
		var newID int
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO user_subscriptions (user_id, valid_until, auto_renew, plan_id, price_paid, promocode_used, status, started_at, traffic_limit)
			VALUES ($1, NOW() + make_interval(days => $2), $3, $4, $5, $6, 'active', NOW(), $7)
			RETURNING id
		`, dbUserID, durationDays, autoRenew, planID, pricePaid, promocodeUsed, trafficLimit).Scan(&newID); err != nil {
			return 0, err
		}
		return newID, nil
	}

	// (2) Same plan — extend valid_until in place; auto_renew stays the user's choice.
	if currentPlanID == planID && freeOverPaid {
		if _, err := tx.ExecContext(ctx, `UPDATE user_subscriptions SET valid_until = valid_until + make_interval(days => $1), status = 'active' WHERE id = $2`,
			durationDays, currentSubID); err != nil {
			return 0, err
		}
		return currentSubID, nil
	}
	if currentPlanID == planID {
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_subscriptions
			SET valid_until = valid_until + make_interval(days => $1),
			    price_paid = $2, promocode_used = $3,
			    status = 'active',
			    traffic_limit = CASE WHEN $4::bigint IS NULL THEN traffic_limit ELSE $4::bigint END
			WHERE id = $5
		`, durationDays, pricePaid, promocodeUsed, trafficLimit, currentSubID); err != nil {
			return 0, err
		}
		return currentSubID, nil
	}

	// (3) Different plan, downgrade — REJECT.
	// Use list price (not pricePaid) so promocode-discounted upgrades aren't
	// falsely blocked: a user paying 150₽ with promo for a 300₽ plan over
	// a 250₽ active sub is upgrading, not downgrading.
	if newPlanListPrice < currentPriceRub {
		return 0, ErrDowngradeNotAllowed
	}

	// (4) Cross-plan upgrade or equal-price switch — convert leftover to bonus days.
	// Use list price (newPlanListPrice), not pricePaid, so promocode discounts
	// don't artificially inflate bonus_days. The user paid what the promo said;
	// the bonus is computed against the plan's true value.
	leftoverDays := int(time.Until(currentValidUntil).Hours() / 24)
	if leftoverDays < 0 {
		leftoverDays = 0
	}
	bonusDays := computeBonusDays(currentPriceRub, currentDurationDays, leftoverDays, newPlanListPrice, durationDays)
	if freeOverPaid {
		if _, err := tx.ExecContext(ctx, `UPDATE user_subscriptions SET plan_id = $1, valid_until = NOW() + make_interval(days => $2), status = 'active' WHERE id = $3`,
			planID, durationDays+bonusDays, currentSubID); err != nil {
			return 0, err
		}
		return currentSubID, nil
	}
	// Leaving a free plan (the trial) takes the new plan's auto-renew default; between
	// paid plans the user's own choice stays.
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_subscriptions
		SET plan_id = $1,
		    valid_until = NOW() + make_interval(days => $2),
		    price_paid = $3,
		    promocode_used = $4,
		    status = 'active',
		    auto_renew = CASE WHEN $5 THEN $6 ELSE auto_renew END,
		    traffic_limit = CASE WHEN $7::bigint IS NULL THEN traffic_limit ELSE $7::bigint END
		WHERE id = $8
	`, planID, durationDays+bonusDays, pricePaid, promocodeUsed, currentPriceRub <= 0, newPlan.AutoRenewDefault, trafficLimit, currentSubID); err != nil {
		return 0, err
	}
	return currentSubID, nil
}

// CheckPlanActivationLimit checks if a plan has max_activations_per_user and if the user has exceeded it.
// Used for trial plans that can only be activated once per user lifetime.
func CheckPlanActivationLimit(userID string, planID int) error {
	var maxActivations sql.NullInt64
	err := db.DB.QueryRow("SELECT max_activations_per_user FROM plans WHERE id = $1", planID).Scan(&maxActivations)
	if err != nil || !maxActivations.Valid {
		return nil // no limit or plan not found
	}
	limit := int(maxActivations.Int64)
	if limit <= 0 {
		return nil // unlimited
	}

	// Count ALL historical activations (including expired) for this plan+user
	var count int
	db.DB.QueryRow(`
		SELECT COUNT(*) FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.plan_id = $2
	`, userID, planID).Scan(&count)

	if count >= limit {
		return fmt.Errorf("этот тариф можно активировать только %d раз", limit)
	}
	return nil
}

// ProrationResult — расчёт остатка текущей подписки.
//
// Legacy fields (CreditRub / Applied / IsDowngrade) сохранены для backward-compat
// и продолжают питать ProrationPreview frontend, но больше НЕ используются для
// вычитания из цены платежа в /payment/create. Юзер платит full new_plan.price_rub;
// остаток конвертируется в bonus_days (см. ниже) и продлевает valid_until.
type ProrationResult struct {
	CreditRub          int       // [legacy] Кредит от текущей подписки (руб, округлён вниз)
	DaysRemaining      int       // Дней до конца текущей подписки
	CurrentPlanID      int       // Текущий план (0 = нет активной подписки)
	CurrentPriceRub    int       // Цена текущего плана
	NewPlanPriceRub    int       // Цена нового плана (для UI)
	IsDowngrade        bool      // [legacy] true если новый план дешевле
	Applied            bool      // [legacy] true если CreditRub можно применить
	BonusDays          int       // Сколько бонус-дней получит юзер при покупке (>0 только для cross-plan upgrade/equal)
	NewValidUntil      time.Time // Итоговая дата окончания после покупки (NOW + new_duration + BonusDays, либо valid_until + new_duration для same-plan)
	IsDowngradeBlocked bool      // true если покупка должна быть отклонена 400 downgrade_not_allowed
}

// CalculateProrationCredit вычисляет credit + bonus_days для юзера при смене тарифа.
//
// Возвращает ОБА расчёта (legacy money-credit и новый days-bonus) в одном
// ProrationResult, чтобы handleProrationPreview мог выдать backward-compat ответ
// и новые поля одновременно.
//
// Логика is_downgrade_blocked: true только при наличии активной подписки и
// new_plan.price_rub < current.plan.price_rub. После истечения подписки (нет
// активной строки) — false, downgrade разрешён.
func CalculateProrationCredit(userID string, newPlanID int) (*ProrationResult, error) {
	res := &ProrationResult{}

	// Fetch new plan first (needed even when no active sub, for the response).
	newPlan, err := GetPlanByID(newPlanID)
	if err != nil || newPlan == nil {
		return res, nil
	}
	res.NewPlanPriceRub = newPlan.PriceRub

	// Текущая активная подписка
	var currentPlanID int
	var currentPrice int
	var currentDuration int
	var validUntil time.Time
	scanErr := db.DB.QueryRow(`
		SELECT us.plan_id, p.price_rub, p.duration_days, us.valid_until
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&currentPlanID, &currentPrice, &currentDuration, &validUntil)
	if scanErr != nil {
		// Нет активной — bonus 0, downgrade не блокируется, valid_until = NOW + new.duration.
		res.NewValidUntil = time.Now().Add(time.Duration(newPlan.DurationDays) * 24 * time.Hour)
		return res, nil
	}

	daysRemaining := int(time.Until(validUntil).Hours() / 24)
	if daysRemaining < 0 {
		daysRemaining = 0
	}
	res.DaysRemaining = daysRemaining
	res.CurrentPlanID = currentPlanID
	res.CurrentPriceRub = currentPrice
	res.IsDowngrade = newPlan.PriceRub < currentPrice
	res.IsDowngradeBlocked = currentPlanID != newPlanID && newPlan.PriceRub < currentPrice

	// Same plan — extension. valid_until = current + new.duration. bonus = 0.
	if currentPlanID == newPlanID {
		res.NewValidUntil = validUntil.Add(time.Duration(newPlan.DurationDays) * 24 * time.Hour)
		return res, nil
	}

	// Downgrade blocked — bonus = 0, valid_until не меняется (мы и не будем активировать).
	if res.IsDowngradeBlocked {
		res.NewValidUntil = validUntil
		return res, nil
	}

	// Cross-plan upgrade/equal — bonus_days conversion.
	res.BonusDays = computeBonusDays(currentPrice, currentDuration, daysRemaining, newPlan.PriceRub, newPlan.DurationDays)
	res.NewValidUntil = time.Now().Add(time.Duration(newPlan.DurationDays+res.BonusDays) * 24 * time.Hour)

	// Legacy money-credit поля (для backward-compat UI которое их читает).
	// Сейчас payment_handlers НЕ применяет CreditRub к priceRub — юзер платит full.
	if currentPrice > 0 && currentDuration > 0 && daysRemaining > 0 {
		credit := int(float64(currentPrice) * float64(daysRemaining) / float64(currentDuration))
		if credit > 0 && newPlan.PriceRub > 0 {
			maxCredit := int(float64(newPlan.PriceRub) * 0.9)
			if credit > maxCredit {
				credit = maxCredit
			}
			res.CreditRub = credit
			res.Applied = true
		}
	}
	return res, nil
}
