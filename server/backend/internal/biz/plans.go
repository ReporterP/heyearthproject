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
	DirectRoutingEnabled bool   `json:"direct_routing_enabled"`
	SortOrder        int      `json:"sort_order"`
	AutoRenewDefault      bool   `json:"auto_renew_default"`
	MaxActivationsPerUser *int  `json:"max_activations_per_user,omitempty"`
	// Express (Hysteria2) — UDP/QUIC inbound on the Hub for low-latency traffic.
	// Brutal congestion control needs explicit up/down targets in Mbps per plan.
	ExpressEnabled  bool `json:"express_enabled"`
	ExpressUpMbps   int  `json:"express_up_mbps"`
	ExpressDownMbps int  `json:"express_down_mbps"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
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
		       allowed_protocols, COALESCE(features::text, '{}'), is_active, direct_routing_enabled,
		       sort_order, auto_renew_default,
		       max_activations_per_user,
		       express_enabled, express_up_mbps, express_down_mbps,
		       created_at, updated_at
		FROM plans WHERE slug = $1
	`, slug).Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
		&p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features, &p.IsActive,
		&p.DirectRoutingEnabled, &p.SortOrder,
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
		       direct_routing_enabled=$10, sort_order=$11, auto_renew_default=$12,
		       max_activations_per_user=$13,
		       express_enabled=$14, express_up_mbps=$15, express_down_mbps=$16,
		       updated_at=CURRENT_TIMESTAMP
		WHERE id = $17
	`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
		pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.DirectRoutingEnabled,
		p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser,
		p.ExpressEnabled, p.ExpressUpMbps, p.ExpressDownMbps, id)
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
// If the user already has an active subscription on the same plan, valid_until is extended;
// otherwise a new subscription row is inserted (multiple plans per user allowed).
func ActivateSubscriptionWithPlan(userID string, planID int, durationDays int, pricePaid int, promocodeUsed string) error {
	var dbUserID int
	err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID)
	if err != nil {
		return fmt.Errorf("resolve user %s: %w", userID, err)
	}

	validUntilExpr := fmt.Sprintf("NOW() + INTERVAL '%d days'", durationDays)

	// Try to extend an existing active subscription on the same plan first.
	res, err := db.DB.Exec(fmt.Sprintf(`
		UPDATE user_subscriptions
		SET valid_until = GREATEST(valid_until, %s),
		    price_paid = $1, promocode_used = $2,
		    status = 'active', auto_renew = true
		WHERE user_id = $3 AND plan_id = $4 AND valid_until > NOW()
	`, validUntilExpr), pricePaid, promocodeUsed, dbUserID, planID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected > 0 {
		NotifySubscriptionChange()
		return nil
	}

	// No active sub for this plan — insert a new row.
	_, err = db.DB.Exec(fmt.Sprintf(`
		INSERT INTO user_subscriptions (user_id, valid_until, auto_renew, plan_id, price_paid, promocode_used, status, started_at)
		VALUES ($1, %s, true, $2, $3, $4, 'active', NOW())
	`, validUntilExpr), dbUserID, planID, pricePaid, promocodeUsed)
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

// ProrationResult — расчёт остатка текущей подписки в рублях.
type ProrationResult struct {
	CreditRub       int     // Кредит от текущей подписки (руб, округлён вниз)
	DaysRemaining   int     // Дней до конца текущей подписки
	CurrentPlanID   int     // Текущий план
	CurrentPriceRub int     // Цена текущего плана
	NewPlanPriceRub int     // Цена нового плана (для UI)
	IsDowngrade     bool    // true если новый план дешевле текущего
	Applied         bool    // Применимо ли для нового plan (не даунгрейд с credit > new price)
}

// CalculateProrationCredit вычисляет credit для юзера при смене тарифа.
// Формула: credit = current_plan_price * (days_remaining / duration_days)
//
// Правила:
//   - Apply только если current_price > 0 (с free нет proration)
//   - Credit не превышает 90% от new_plan цены (защита от zero-value оплат)
//   - Для downgrade (new < current) — credit 0 (просто новая цена, но valid_until продлевается на больший из двух)
func CalculateProrationCredit(userID string, newPlanID int) (*ProrationResult, error) {
	res := &ProrationResult{}

	// Текущая активная подписка
	var currentPlanID int
	var currentPrice int
	var currentDuration int
	var validUntil time.Time
	err := db.DB.QueryRow(`
		SELECT us.plan_id, p.price_rub, p.duration_days, us.valid_until
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&currentPlanID, &currentPrice, &currentDuration, &validUntil)
	if err != nil {
		return res, nil // нет активной — нет credit
	}

	if currentPrice <= 0 || currentDuration <= 0 {
		return res, nil
	}

	daysRemaining := int(time.Until(validUntil).Hours() / 24)
	if daysRemaining <= 0 {
		return res, nil
	}

	// Pro-rata credit
	credit := int(float64(currentPrice) * float64(daysRemaining) / float64(currentDuration))
	if credit <= 0 {
		return res, nil
	}

	res.CreditRub = credit
	res.DaysRemaining = daysRemaining
	res.CurrentPlanID = currentPlanID
	res.CurrentPriceRub = currentPrice

	// Ограничение: credit не больше 90% от new plan price (чтобы всегда был оплачиваемый остаток)
	newPlan, err := GetPlanByID(newPlanID)
	if err != nil || newPlan == nil {
		res.Applied = false
		return res, nil
	}

	res.NewPlanPriceRub = newPlan.PriceRub
	res.IsDowngrade = newPlan.PriceRub < currentPrice

	// Free newPlan — credit не применяем (платить нечего)
	if newPlan.PriceRub <= 0 {
		res.Applied = false
		return res, nil
	}

	// Ограничение: credit не больше 90% от new plan price (чтобы всегда был оплачиваемый
	// остаток больше 0 — иначе ЮKassa может не принять платёж на 0₽).
	// При downgrade credit обычно больше new price — будет обрезан до 90%,
	// а valid_until продлится через GREATEST в ActivateSubscriptionWithPlan.
	maxCredit := int(float64(newPlan.PriceRub) * 0.9)
	if credit > maxCredit {
		res.CreditRub = maxCredit
	}

	res.Applied = true
	return res, nil
}
