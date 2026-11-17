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

// Promocodes have ONE way to be used. Whoever takes a code — the cabinet field
// («Активировать промокод»), the payment form, the wallet purchase, the app — goes
// through LoadPromocode (validation) and consumePromocodeTx (usage), so every path
// checks the same things:
//
//   - the code is case-insensitive;
//   - is_active, expires_at, max_uses (all users), per_user_limit (this user) and
//     applies_to_plans (the plan being bought) are checked;
//   - the type decides the effect: a discount only lowers the price of a purchase,
//     free_trial grants free_days on plan_id_granted, traffic_gift adds traffic to
//     the current subscription;
//   - current_uses grows and a promocode_redemptions row is written only together
//     with the effect (the free grant, or the paid purchase), never when a payment
//     is merely started.

// Promocode types.
const (
	PromoDiscountPercent = "discount_percent"
	PromoDiscountFixed   = "discount_fixed"
	PromoFreeTrial       = "free_trial"
	PromoTrafficGift     = "traffic_gift"
)

// Promocode is a validated code, ready to apply.
type Promocode struct {
	Code             string
	Type             string
	DiscountPercent  int
	DiscountFixedRub int
	FreeDays         int
	TrafficGiftBytes int64
	PlanIDGranted    int // 0: none
	AppliesToPlans   []int64
}

// IsDiscount reports a code that lowers the price of a purchase.
func (p *Promocode) IsDiscount() bool {
	return p.Type == PromoDiscountPercent || p.Type == PromoDiscountFixed
}

// PriceFor is the price of a purchase of basePriceRub with this code. Only discount
// codes change it; it never goes below zero.
func (p *Promocode) PriceFor(basePriceRub int) int {
	price := basePriceRub
	switch p.Type {
	case PromoDiscountPercent:
		price = basePriceRub - basePriceRub*p.DiscountPercent/100
	case PromoDiscountFixed:
		price = basePriceRub - p.DiscountFixedRub
	}
	if price < 0 {
		price = 0
	}
	return price
}

// PromoError is a refusal the client may see as is (Russian, no internals).
type PromoError struct{ Msg string }

func (e *PromoError) Error() string { return e.Msg }

var (
	ErrPromoNotFound    = &PromoError{"Промокод не найден"}
	ErrPromoInactive    = &PromoError{"Промокод больше не действует"}
	ErrPromoExpired     = &PromoError{"Срок действия промокода истёк"}
	ErrPromoExhausted   = &PromoError{"Промокод уже использован максимальное число раз"}
	ErrPromoUsedByUser  = &PromoError{"Вы уже использовали этот промокод"}
	ErrPromoWrongPlan   = &PromoError{"Промокод не действует на этот тариф"}
	ErrPromoNeedsPlan   = &PromoError{"Это промокод на скидку — введите его при оплате тарифа"}
	ErrPromoUnlimited   = &PromoError{"У вас безлимитный трафик — подарок трафика не нужен"}
	ErrPromoNoSub       = &PromoError{"Подарок трафика добавляется к действующей подписке — сначала оформите подписку"}
	ErrPromoHigherPlan  = &PromoError{"У вас уже есть подписка дороже — промокод можно будет применить после её окончания"}
	ErrPromoUnknownType = &PromoError{"Промокод настроен неверно — напишите в поддержку"}
)

// IsPromoError reports a client-facing promocode refusal.
func IsPromoError(err error) bool {
	var pe *PromoError
	return errors.As(err, &pe)
}

// NormalizePromocode is the stored form of a code: trimmed, upper case.
func NormalizePromocode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// queryer is *sql.DB or *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

const promocodeSelect = `
	SELECT code, promo_type, COALESCE(discount_percent, 0), COALESCE(discount_fixed_rub, 0),
	       COALESCE(NULLIF(free_days, 0), duration_days, 0), COALESCE(traffic_gift_bytes, 0),
	       COALESCE(plan_id_granted, 0), COALESCE(applies_to_plans, '{}'),
	       is_active, COALESCE(max_uses, 0), COALESCE(current_uses, 0), expires_at,
	       COALESCE(per_user_limit, 1)
	FROM promocodes WHERE code = $1`

// LoadPromocode validates code for the user (users.id) and, when planID > 0, for that
// plan. With lock it takes FOR UPDATE on the promocode row (inside a transaction that
// will consume it), so two concurrent uses of a last code serialize.
func LoadPromocode(ctx context.Context, q queryer, userDBID int, code string, planID int, lock bool) (*Promocode, error) {
	norm := NormalizePromocode(code)
	if norm == "" {
		return nil, ErrPromoNotFound
	}
	query := promocodeSelect
	if lock {
		query += ` FOR UPDATE`
	}
	var p Promocode
	var plans pq.Int64Array
	var active bool
	var maxUses, curUses, perUser int
	var expires sql.NullTime
	err := q.QueryRowContext(ctx, query, norm).Scan(&p.Code, &p.Type, &p.DiscountPercent, &p.DiscountFixedRub,
		&p.FreeDays, &p.TrafficGiftBytes, &p.PlanIDGranted, &plans, &active, &maxUses, &curUses, &expires, &perUser)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPromoNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load promocode: %w", err)
	}
	p.AppliesToPlans = plans
	switch {
	case !active:
		return nil, ErrPromoInactive
	case expires.Valid && !expires.Time.After(time.Now()):
		return nil, ErrPromoExpired
	case maxUses > 0 && curUses >= maxUses:
		return nil, ErrPromoExhausted
	}
	switch p.Type {
	case PromoDiscountPercent, PromoDiscountFixed, PromoFreeTrial, PromoTrafficGift:
	default:
		return nil, ErrPromoUnknownType
	}
	if perUser > 0 && userDBID > 0 {
		var used int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM promocode_redemptions WHERE code = $1 AND user_id = $2`, norm, userDBID).Scan(&used); err != nil {
			return nil, fmt.Errorf("count promocode redemptions: %w", err)
		}
		if used >= perUser {
			return nil, ErrPromoUsedByUser
		}
	}
	if planID > 0 && len(p.AppliesToPlans) > 0 {
		ok := false
		for _, id := range p.AppliesToPlans {
			if int(id) == planID {
				ok = true
				break
			}
		}
		if !ok {
			return nil, ErrPromoWrongPlan
		}
	}
	return &p, nil
}

// Redemption sources (promocode_redemptions.source).
const (
	RedeemSourceCabinet = "cabinet"  // «Активировать промокод», free grant
	RedeemSourcePayment = "payment"  // a YooKassa payment with the code
	RedeemSourceWallet  = "wallet"   // a purchase from the wallet with the code
	RedeemSourceFree    = "free"     // a purchase that cost 0 with the code
	RedeemSourceApp     = "app_grpc" // the frozen app's ApplyPromocode
)

// ConsumePromocodeTx records one use of code by the user in tx: current_uses + 1 and a
// redemptions row. Call it in the transaction that applies the effect. paymentID may
// be 0.
func ConsumePromocodeTx(ctx context.Context, tx *sql.Tx, code string, userDBID int, source string, paymentID int) error {
	norm := NormalizePromocode(code)
	res, err := tx.ExecContext(ctx, `UPDATE promocodes SET current_uses = COALESCE(current_uses, 0) + 1, updated_at = NOW() WHERE code = $1`, norm)
	if err != nil {
		return fmt.Errorf("count promocode use: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPromoGone
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO promocode_redemptions (code, user_id, source, payment_id)
		VALUES ($1, $2, $3, NULLIF($4, 0))`, norm, userDBID, source, paymentID); err != nil {
		return fmt.Errorf("record promocode redemption: %w", err)
	}
	return nil
}

// ErrPromoGone: the code was deleted after a payment with it was started. The payment
// still applies; there is nothing to count.
var ErrPromoGone = errors.New("promocode no longer exists")

// ConsumePaidPromocodeTx records the code a paid (or zero-price) purchase was made
// with and applies its after-purchase effect (traffic_gift). A code deleted meanwhile
// is skipped: the money is in and the purchase must go through.
func ConsumePaidPromocodeTx(ctx context.Context, tx *sql.Tx, code string, userDBID int, source string, paymentID int) error {
	norm := NormalizePromocode(code)
	if norm == "" {
		return nil
	}
	var promoType string
	var gift int64
	err := tx.QueryRowContext(ctx, `SELECT promo_type, COALESCE(traffic_gift_bytes, 0) FROM promocodes WHERE code = $1 FOR UPDATE`, norm).Scan(&promoType, &gift)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load promocode: %w", err)
	}
	if err := ConsumePromocodeTx(ctx, tx, norm, userDBID, source, paymentID); err != nil && !errors.Is(err, ErrPromoGone) {
		return err
	}
	if promoType == PromoTrafficGift && gift > 0 {
		return ApplyTrafficGiftTx(ctx, tx, userDBID, gift)
	}
	return nil
}

// RedeemResult describes what a cabinet redemption did.
type RedeemResult struct {
	Type         string
	PlanID       int
	PlanName     string
	Days         int
	TrafficBytes int64
	Message      string
}

// RedeemPromocode is the cabinet / app path («Активировать промокод»): a free_trial
// code grants its days, a traffic_gift code adds traffic to the current subscription,
// a discount code is refused (it only works when paying). fallbackPlanID is the plan a
// free_trial code without plan_id_granted grants (the free-trial plan when 0); a
// planID > 0 is also checked against applies_to_plans.
func RedeemPromocode(ctx context.Context, userID, code string, fallbackPlanID int, source string) (*RedeemResult, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	res, err := RedeemPromocodeTx(ctx, tx, userID, code, fallbackPlanID, source, 0)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	NotifySubscriptionChange()
	return res, nil
}

// RedeemPromocodeTx is RedeemPromocode inside the caller's transaction (no commit, no
// notify).
func RedeemPromocodeTx(ctx context.Context, tx *sql.Tx, userID, code string, fallbackPlanID int, source string, paymentID int) (*RedeemResult, error) {
	var userDBID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
		return nil, fmt.Errorf("resolve user: %w", err)
	}
	p, err := LoadPromocode(ctx, tx, userDBID, code, fallbackPlanID, true)
	if err != nil {
		return nil, err
	}
	var out *RedeemResult
	switch p.Type {
	case PromoFreeTrial:
		out, err = grantFreeDaysTx(ctx, tx, userID, p, fallbackPlanID)
	case PromoTrafficGift:
		out, err = addTrafficGiftTx(ctx, tx, userDBID, p.TrafficGiftBytes)
	default:
		return nil, ErrPromoNeedsPlan
	}
	if err != nil {
		return nil, err
	}
	if err := ConsumePromocodeTx(ctx, tx, p.Code, userDBID, source, paymentID); err != nil {
		return nil, err
	}
	return out, nil
}

// grantFreeDaysTx gives a free_trial code's days on its plan. The subscription is
// marked with the code (promocode_used), has no traffic limit (traffic_limit = 0, the
// owner's rule for promo access) and is not set to auto-renew when it is created.
func grantFreeDaysTx(ctx context.Context, tx *sql.Tx, userID string, p *Promocode, fallbackPlanID int) (*RedeemResult, error) {
	planID := p.PlanIDGranted
	if planID == 0 {
		planID = fallbackPlanID
	}
	if planID == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM plans WHERE slug = 'free-trial' LIMIT 1`).Scan(&planID); err != nil {
			return nil, fmt.Errorf("resolve free-trial plan: %w", err)
		}
	}
	plan, err := GetPlanByID(planID)
	if err != nil || plan == nil {
		return nil, fmt.Errorf("resolve plan %d: %w", planID, err)
	}
	days := p.FreeDays
	if days <= 0 {
		days = plan.DurationDays
	}
	noLimit := int64(0)
	noRenew := false
	_, err = ActivateSubscriptionTx(ctx, tx, userID, planID, days, 0, p.Code, ActivateOpts{InsertAutoRenew: &noRenew, TrafficLimit: &noLimit})
	if errors.Is(err, ErrDowngradeNotAllowed) {
		return nil, ErrPromoHigherPlan
	}
	if err != nil {
		return nil, err
	}
	return &RedeemResult{Type: PromoFreeTrial, PlanID: planID, PlanName: plan.Name, Days: days,
		Message: fmt.Sprintf("Промокод активирован: тариф «%s» на %d дн.", plan.Name, days)}, nil
}

// addTrafficGiftTx raises the traffic limit of the user's effective subscription by
// bytes. An unlimited subscription needs no gift.
func addTrafficGiftTx(ctx context.Context, tx *sql.Tx, userDBID int, bytes int64) (*RedeemResult, error) {
	var subID int
	var limit int64
	err := tx.QueryRowContext(ctx, `
		SELECT us.id, COALESCE(us.traffic_limit, p.traffic_limit_bytes, 0)
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY `+effectiveSubOrderBy("us")+`
		LIMIT 1
		FOR UPDATE OF us`, userDBID).Scan(&subID, &limit)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPromoNoSub
	}
	if err != nil {
		return nil, fmt.Errorf("load subscription: %w", err)
	}
	if limit <= 0 {
		return nil, ErrPromoUnlimited
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_subscriptions SET traffic_limit = $1 WHERE id = $2`, limit+bytes, subID); err != nil {
		return nil, fmt.Errorf("add traffic gift: %w", err)
	}
	return &RedeemResult{Type: PromoTrafficGift, TrafficBytes: bytes,
		Message: fmt.Sprintf("Промокод активирован: +%s трафика", FormatGB(bytes))}, nil
}

// ApplyTrafficGiftTx adds a traffic_gift code's bytes after a paid purchase made with
// it; an unlimited subscription keeps no gift (the code is still used).
func ApplyTrafficGiftTx(ctx context.Context, tx *sql.Tx, userDBID int, bytes int64) error {
	_, err := addTrafficGiftTx(ctx, tx, userDBID, bytes)
	if errors.Is(err, ErrPromoUnlimited) || errors.Is(err, ErrPromoNoSub) {
		return nil
	}
	return err
}

// FormatGB is bytes as «N ГБ» (one decimal below 10 GB).
func FormatGB(b int64) string {
	gb := float64(b) / (1 << 30)
	if gb < 10 {
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", gb), "0"), ".") + " ГБ"
	}
	return fmt.Sprintf("%.0f ГБ", gb)
}

// IsFreeAccessPromocode reports whether label (user_subscriptions.promocode_used) names a
// free-access code that still renews free: an existing free_trial code the owner has not
// switched off. Discount codes, the cron's old "auto-renew" marks and empty values do not.
func IsFreeAccessPromocode(ctx context.Context, q queryer, label string) (bool, error) {
	norm := NormalizePromocode(label)
	if norm == "" {
		return false, nil
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM promocodes WHERE code = $1 AND promo_type = 'free_trial' AND is_active`, norm).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}
