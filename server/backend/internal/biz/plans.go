package biz

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// Plan represents a subscription plan configured by admin.
type Plan struct {
	ID               int      `json:"id"`
	Slug             string   `json:"slug"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	PriceRub         int      `json:"price_rub"`
	DurationDays     int      `json:"duration_days"`
	TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
	AllowedProtocols []string `json:"allowed_protocols"`
	Features         string   `json:"features"` // JSON string
	IsActive         bool     `json:"is_active"`
	SortOrder        int      `json:"sort_order"`
	AutoRenewDefault      bool   `json:"auto_renew_default"`
	MaxActivationsPerUser *int  `json:"max_activations_per_user,omitempty"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
	// Computed
	ServerIDs []int `json:"server_ids,omitempty"`
}

// ListActivePlans returns all active plans for display on the site.
func ListActivePlans() ([]Plan, error) {
	rows, err := db.DB.Query(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, sort_order, auto_renew_default,
		       max_activations_per_user, created_at, updated_at
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
			&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive, &p.SortOrder,
			&p.AutoRenewDefault, &p.MaxActivationsPerUser, &p.CreatedAt, &p.UpdatedAt); err != nil {
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
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, sort_order, auto_renew_default,
		       max_activations_per_user, created_at, updated_at
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
			&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive, &p.SortOrder,
			&p.AutoRenewDefault, &p.MaxActivationsPerUser, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		p.ServerIDs = getPlanServerIDs(p.ID)
		plans = append(plans, p)
	}
	return plans, nil
}

// GetPlanByID returns a single plan.
func GetPlanByID(id int) (*Plan, error) {
	var p Plan
	err := db.DB.QueryRow(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, sort_order, auto_renew_default,
		       max_activations_per_user, created_at, updated_at
		FROM plans WHERE id = $1
	`, id).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
		&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive, &p.SortOrder,
		&p.AutoRenewDefault, &p.MaxActivationsPerUser, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.ServerIDs = getPlanServerIDs(p.ID)
	return &p, nil
}

// GetPlanBySlug returns plan by slug.
func GetPlanBySlug(slug string) (*Plan, error) {
	var p Plan
	err := db.DB.QueryRow(`
		SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, sort_order, auto_renew_default,
		       max_activations_per_user, created_at, updated_at
		FROM plans WHERE slug = $1
	`, slug).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
		&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive, &p.SortOrder,
		&p.AutoRenewDefault, &p.MaxActivationsPerUser, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.ServerIDs = getPlanServerIDs(p.ID)
	return &p, nil
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

// CreatePlan creates a new plan.
func CreatePlan(p *Plan) (int, error) {
	var id int
	err := db.DB.QueryRow(`
		INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes,
		                   allowed_protocols, features, is_active, sort_order, auto_renew_default, max_activations_per_user)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12)
		RETURNING id
	`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
		pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser).Scan(&id)
	if err != nil {
		return 0, err
	}
	if len(p.ServerIDs) > 0 {
		replacePlanServers(id, p.ServerIDs)
	}
	return id, nil
}

// UpdatePlan updates an existing plan.
func UpdatePlan(id int, p *Plan) error {
	_, err := db.DB.Exec(`
		UPDATE plans SET slug=$1, name=$2, description=$3, price_rub=$4, duration_days=$5,
		       traffic_limit_bytes=$6, allowed_protocols=$7, features=$8::jsonb, is_active=$9,
		       sort_order=$10, auto_renew_default=$11, max_activations_per_user=$12, updated_at=CURRENT_TIMESTAMP
		WHERE id = $13
	`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
		pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser, id)
	if err != nil {
		return err
	}
	replacePlanServers(id, p.ServerIDs)
	return nil
}

// DeletePlan removes a plan.
func DeletePlan(id int) error {
	_, err := db.DB.Exec("DELETE FROM plans WHERE id = $1", id)
	return err
}

func replacePlanServers(planID int, serverIDs []int) {
	db.DB.Exec("DELETE FROM plan_servers WHERE plan_id = $1", planID)
	for _, sid := range serverIDs {
		db.DB.Exec("INSERT INTO plan_servers (plan_id, server_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", planID, sid)
	}
}

// GetUserPlan returns the active plan for a user.
func GetUserPlan(userID string) (*Plan, error) {
	var planID sql.NullInt64
	err := db.DB.QueryRow(`
		SELECT us.plan_id FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active'
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&planID)
	if err != nil || !planID.Valid {
		return nil, fmt.Errorf("no active plan")
	}
	return GetPlanByID(int(planID.Int64))
}

// IsProtocolAllowed checks if a protocol is available in the user's plan.
func IsProtocolAllowed(userID, protocol string) bool {
	plan, err := GetUserPlan(userID)
	if err != nil {
		return true // no plan = free tier = allow all (backward compat)
	}
	for _, p := range plan.AllowedProtocols {
		if p == "all" || strings.EqualFold(p, protocol) {
			return true
		}
	}
	return false
}

// ActivateSubscriptionWithPlan creates or extends a user's subscription based on a plan.
// Uses group_id=1 as the single-subscription slot (one active plan per user).
func ActivateSubscriptionWithPlan(userID string, planID int, durationDays int, pricePaid int, promocodeUsed string) error {
	var dbUserID int
	err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID)
	if err != nil {
		return fmt.Errorf("resolve user %s: %w", userID, err)
	}

	// Ensure legacy group 1 exists (required for FK constraint).
	// All plan-based subscriptions use group_id=1 as a single-slot key.
	db.DB.Exec(`INSERT INTO user_groups (id, name, is_premium) VALUES (1, 'default', false) ON CONFLICT DO NOTHING`)

	validUntil := fmt.Sprintf("NOW() + INTERVAL '%d days'", durationDays)

	_, err = db.DB.Exec(fmt.Sprintf(`
		INSERT INTO user_subscriptions (user_id, group_id, valid_until, auto_renew, plan_id, price_paid, promocode_used, status, started_at)
		VALUES ($1, 1, %s, true, $2, $3, $4, 'active', NOW())
		ON CONFLICT (user_id, group_id) DO UPDATE
		SET valid_until = GREATEST(user_subscriptions.valid_until, %s),
		    plan_id = $2, price_paid = $3, promocode_used = $4, status = 'active', auto_renew = true
	`, validUntil, validUntil), dbUserID, planID, pricePaid, promocodeUsed)
	if err == nil {
		NotifySubscriptionChange()
	}
	return err
}

// ApplyPromoToPrice validates and applies a promocode for a specific plan and user.
// Uses FOR UPDATE lock to prevent race conditions on usage counter.
func ApplyPromoToPrice(basePrice int, code string, planID ...int) (finalPrice int, freeTrialDays int, trafficGiftBytes int64, err error) {
	codeNorm := strings.ToUpper(strings.TrimSpace(code))

	// Start transaction for atomic read-validate-increment
	tx, txErr := db.DB.Begin()
	if txErr != nil {
		return basePrice, 0, 0, fmt.Errorf("db error")
	}
	defer tx.Rollback()

	var promoType string
	var discountPercent, discountFixed, freeDays, maxUses, currentUses int
	var trafficGift int64
	var isActive bool
	var expiresAt sql.NullTime
	var appliesToPlans pq.Int64Array

	err = tx.QueryRow(`
		SELECT promo_type, COALESCE(discount_percent,0), COALESCE(discount_fixed_rub,0),
		       COALESCE(free_days,0), COALESCE(traffic_gift_bytes,0), is_active,
		       max_uses, current_uses, expires_at, applies_to_plans
		FROM promocodes WHERE code = $1 FOR UPDATE
	`, codeNorm).Scan(&promoType, &discountPercent, &discountFixed, &freeDays, &trafficGift,
		&isActive, &maxUses, &currentUses, &expiresAt, &appliesToPlans)
	if err != nil {
		return basePrice, 0, 0, fmt.Errorf("промокод не найден")
	}
	if !isActive {
		return basePrice, 0, 0, fmt.Errorf("промокод деактивирован")
	}
	if maxUses > 0 && currentUses >= maxUses {
		return basePrice, 0, 0, fmt.Errorf("промокод исчерпан")
	}
	if expiresAt.Valid && expiresAt.Time.Before(time.Now()) {
		return basePrice, 0, 0, fmt.Errorf("промокод истёк")
	}

	// Check applies_to_plans
	if len(appliesToPlans) > 0 && len(planID) > 0 {
		found := false
		for _, pid := range appliesToPlans {
			if int(pid) == planID[0] {
				found = true
				break
			}
		}
		if !found {
			return basePrice, 0, 0, fmt.Errorf("промокод не действует на этот план")
		}
	}

	switch promoType {
	case "discount_percent":
		finalPrice = basePrice - (basePrice * discountPercent / 100)
		if finalPrice < 0 {
			finalPrice = 0
		}
	case "discount_fixed":
		finalPrice = basePrice - discountFixed
		if finalPrice < 0 {
			finalPrice = 0
		}
	case "free_trial":
		finalPrice = 0
		freeTrialDays = freeDays
	case "traffic_gift":
		finalPrice = basePrice
		trafficGiftBytes = trafficGift
	default:
		finalPrice = basePrice
	}

	// Increment usage counter INSIDE transaction (atomic with validation)
	if _, txErr := tx.Exec("UPDATE promocodes SET current_uses = current_uses + 1, updated_at = NOW() WHERE code = $1", codeNorm); txErr != nil {
		return basePrice, 0, 0, fmt.Errorf("ошибка обновления промокода")
	}

	if txErr := tx.Commit(); txErr != nil {
		return basePrice, 0, 0, fmt.Errorf("ошибка применения промокода")
	}

	return
}

// CheckPromoPerUserLimit checks if user has already used this promo.
func CheckPromoPerUserLimit(userID, code string) error {
	codeNorm := strings.ToUpper(strings.TrimSpace(code))
	var perUserLimit int
	err := db.DB.QueryRow("SELECT COALESCE(per_user_limit, 1) FROM promocodes WHERE code = $1", codeNorm).Scan(&perUserLimit)
	if err != nil {
		return nil // no promo or no limit
	}
	if perUserLimit <= 0 {
		return nil // unlimited
	}
	var usedCount int
	db.DB.QueryRow("SELECT COUNT(*) FROM user_subscriptions us JOIN users u ON u.id = us.user_id WHERE u.user_id = $1 AND us.promocode_used = $2", userID, codeNorm).Scan(&usedCount)
	if usedCount >= perUserLimit {
		return fmt.Errorf("промокод уже использован (лимит %d на пользователя)", perUserLimit)
	}
	return nil
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
