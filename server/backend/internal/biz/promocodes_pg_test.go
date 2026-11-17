package biz

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// newPlan inserts a plan and returns its id.
func newPlan(t *testing.T, conn *sql.DB, slug string, price, days int, limit any, autoRenewDefault bool) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`
		INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes, allowed_protocols, is_active, auto_renew_default)
		VALUES ($1, $1, '', $2, $3, $4, ARRAY['all'], TRUE, $5) RETURNING id`, slug, price, days, limit, autoRenewDefault).Scan(&id); err != nil {
		t.Fatalf("insert plan %s: %v", slug, err)
	}
	return id
}

type subRow struct {
	PlanID       int
	AutoRenew    bool
	Promo        string
	TrafficLimit sql.NullInt64
	DaysLeft     int
}

func activeSub(t *testing.T, conn *sql.DB, userDBID int) subRow {
	t.Helper()
	var s subRow
	if err := conn.QueryRow(`
		SELECT plan_id, auto_renew, COALESCE(promocode_used, ''), traffic_limit,
		       ROUND(EXTRACT(EPOCH FROM valid_until - NOW()) / 86400)::int
		FROM user_subscriptions WHERE user_id = $1 AND valid_until > NOW()
		ORDER BY valid_until DESC LIMIT 1`, userDBID).Scan(&s.PlanID, &s.AutoRenew, &s.Promo, &s.TrafficLimit, &s.DaysLeft); err != nil {
		t.Fatalf("active subscription of %d: %v", userDBID, err)
	}
	return s
}

func promoUses(t *testing.T, conn *sql.DB, code string) (current, redemptions int) {
	t.Helper()
	if err := conn.QueryRow(`SELECT current_uses, (SELECT COUNT(*) FROM promocode_redemptions WHERE code = $1) FROM promocodes WHERE code = $1`, code).Scan(&current, &redemptions); err != nil {
		t.Fatal(err)
	}
	return
}

// A1: a new subscription takes auto_renew from the plan (false for the free trial).
func TestActivateTakesAutoRenewFromPlan(t *testing.T) {
	conn := testpg.Open(t)
	trial := newPlan(t, conn, "t-trial", 0, 7, int64(10<<30), false)
	paid := newPlan(t, conn, "t-paid", 300, 30, nil, true)

	u1 := testpg.NewUser(t, conn, "user-trial")
	if err := ActivateSubscriptionWithPlan("user-trial", trial, 7, 0, ""); err != nil {
		t.Fatal(err)
	}
	if s := activeSub(t, conn, u1); s.AutoRenew {
		t.Error("new trial subscription must not auto-renew")
	}
	// Trial -> paid switch takes the paid plan's default.
	if err := ActivateSubscriptionWithPlan("user-trial", paid, 30, 300, ""); err != nil {
		t.Fatal(err)
	}
	if s := activeSub(t, conn, u1); !s.AutoRenew || s.PlanID != paid {
		t.Errorf("after paying: %+v, want paid plan with auto-renew", s)
	}

	// A user who switched auto-renew off keeps it off when extending the same plan.
	u2 := testpg.NewUser(t, conn, "user-paid")
	if err := ActivateSubscriptionWithPlan("user-paid", paid, 30, 300, ""); err != nil {
		t.Fatal(err)
	}
	testpg.MustExec(t, conn, `UPDATE user_subscriptions SET auto_renew = FALSE WHERE user_id = $1`, u2)
	if err := ActivateSubscriptionWithPlan("user-paid", paid, 30, 300, ""); err != nil {
		t.Fatal(err)
	}
	if s := activeSub(t, conn, u2); s.AutoRenew || s.DaysLeft != 60 {
		t.Errorf("extension: %+v, want auto-renew off and 60 days", s)
	}
}

func TestRedeemPromocodeRules(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	trial := newPlan(t, conn, "t-trial", 0, 7, int64(10<<30), false)
	paid := newPlan(t, conn, "t-paid", 300, 30, nil, true)
	other := newPlan(t, conn, "t-other", 250, 30, nil, true)
	_ = trial
	testpg.MustExec(t, conn, `UPDATE plans SET slug = 'free-trial-old' WHERE slug = 'free-trial'`)
	testpg.MustExec(t, conn, `UPDATE plans SET slug = 'free-trial' WHERE id = $1`, trial)

	ins := func(code, typ string, extra string, args ...any) {
		t.Helper()
		testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, max_uses, per_user_limit) VALUES ($1, 30, $2, 100, 1)`, code, typ)
		if extra != "" {
			testpg.MustExec(t, conn, `UPDATE promocodes SET `+extra+` WHERE code = $1`, append([]any{code}, args...)...)
		}
	}
	ins("FREE30", PromoFreeTrial, "free_days = 30, plan_id_granted = $2", paid)
	ins("OFF", PromoFreeTrial, "is_active = FALSE")
	ins("OLD", PromoFreeTrial, "expires_at = NOW() - INTERVAL '1 day'")
	ins("ONCE", PromoFreeTrial, "max_uses = 1, current_uses = 1")
	ins("DISC", PromoDiscountPercent, "discount_percent = 20")
	ins("GIFT", PromoTrafficGift, "traffic_gift_bytes = $2", int64(5<<30))
	ins("ONLYOTHER", PromoDiscountFixed, "discount_fixed_rub = 50, applies_to_plans = ARRAY[$2::int]", other)
	ins("WEIRD", "mystery", "")

	testpg.NewUser(t, conn, "u-a")
	uB := testpg.NewUser(t, conn, "u-b")

	for _, tc := range []struct {
		code string
		want error
	}{
		{"nope", ErrPromoNotFound},
		{"off", ErrPromoInactive},
		{"old", ErrPromoExpired},
		{"once", ErrPromoExhausted},
		{"disc", ErrPromoNeedsPlan},
		{"weird", ErrPromoUnknownType},
		{"gift", ErrPromoNoSub},
	} {
		if _, err := RedeemPromocode(ctx, "u-a", tc.code, 0, RedeemSourceCabinet); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.code, err, tc.want)
		}
	}
	// Refusals use nothing.
	if cur, red := promoUses(t, conn, "DISC"); cur != 0 || red != 0 {
		t.Errorf("refused code was counted: %d/%d", cur, red)
	}

	// free_trial: its days on its plan, unlimited, no auto-renew, counted once.
	res, err := RedeemPromocode(ctx, "u-b", " free30 ", 0, RedeemSourceCabinet)
	if err != nil {
		t.Fatal(err)
	}
	if res.Days != 30 || res.PlanID != paid {
		t.Errorf("result %+v", res)
	}
	s := activeSub(t, conn, uB)
	if s.PlanID != paid || s.AutoRenew || s.Promo != "FREE30" || !s.TrafficLimit.Valid || s.TrafficLimit.Int64 != 0 || s.DaysLeft != 30 {
		t.Errorf("granted subscription %+v", s)
	}
	if cur, red := promoUses(t, conn, "FREE30"); cur != 1 || red != 1 {
		t.Errorf("FREE30 uses %d/%d, want 1/1", cur, red)
	}
	// per_user_limit = 1.
	if _, err := RedeemPromocode(ctx, "u-b", "FREE30", 0, RedeemSourceCabinet); !errors.Is(err, ErrPromoUsedByUser) {
		t.Errorf("second use: %v", err)
	}
	// Unlimited subscription: a traffic gift is refused.
	if _, err := RedeemPromocode(ctx, "u-b", "GIFT", 0, RedeemSourceCabinet); !errors.Is(err, ErrPromoUnlimited) {
		t.Errorf("gift on unlimited: %v", err)
	}

	// traffic_gift on a limited subscription raises the limit.
	uC := testpg.NewUser(t, conn, "u-c")
	if err := ActivateSubscriptionWithPlan("u-c", trial, 7, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RedeemPromocode(ctx, "u-c", "gift", 0, RedeemSourceCabinet); err != nil {
		t.Fatal(err)
	}
	if s := activeSub(t, conn, uC); s.TrafficLimit.Int64 != 15<<30 {
		t.Errorf("limit after gift %d, want 15 GB", s.TrafficLimit.Int64)
	}

	// Payment-side validation: plan restriction and the discount math.
	if _, err := LoadPromocode(ctx, conn, uC, "onlyother", paid, false); !errors.Is(err, ErrPromoWrongPlan) {
		t.Errorf("applies_to_plans: %v", err)
	}
	p, err := LoadPromocode(ctx, conn, uC, "OnlyOther", other, false)
	if err != nil || p.PriceFor(250) != 200 {
		t.Errorf("fixed discount: %v %v", p, err)
	}
	p, _ = LoadPromocode(ctx, conn, uC, "disc", paid, false)
	if p.PriceFor(300) != 240 {
		t.Errorf("20%% of 300 -> %d", p.PriceFor(300))
	}
}

// Two users race for the last use of a code: exactly one gets it.
func TestRedeemPromocodeLastUseRace(t *testing.T) {
	conn := testpg.Open(t)
	paid := newPlan(t, conn, "t-paid", 300, 30, nil, true)
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, max_uses, free_days, plan_id_granted) VALUES ('LAST', 30, 'free_trial', 1, 10, $1)`, paid)
	const n = 8
	for i := 0; i < n; i++ {
		testpg.NewUser(t, conn, "racer-"+string(rune('a'+i)))
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = RedeemPromocode(context.Background(), "racer-"+string(rune('a'+i)), "LAST", 0, RedeemSourceCabinet)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrPromoExhausted):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d users got the last use, want 1", ok)
	}
	if cur, red := promoUses(t, conn, "LAST"); cur != 1 || red != 1 {
		t.Errorf("uses %d/%d, want 1/1", cur, red)
	}
}

// Review fix: free days over a PAID subscription of the same plan only add days — the row
// stays paid (price, mark, traffic limit), so it never becomes free-forever access.
func TestFreeDaysOverPaidKeepPaidMarks(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	paid := newPlan(t, conn, "t-paid", 300, 30, int64(50<<30), true)
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, free_days, plan_id_granted) VALUES ('GIFT7', 7, 'free_trial', 7, $1)`, paid)
	uid := testpg.NewUser(t, conn, "payer")
	if err := ActivateSubscriptionWithPlan("payer", paid, 30, 300, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := RedeemPromocode(ctx, "payer", "GIFT7", 0, RedeemSourceCabinet); err != nil {
		t.Fatal(err)
	}
	var pricePaid int
	var promo string
	var limit sql.NullInt64
	var days int
	if err := conn.QueryRow(`SELECT price_paid, COALESCE(promocode_used, ''), traffic_limit, ROUND(EXTRACT(EPOCH FROM valid_until - NOW()) / 86400)::int
		FROM user_subscriptions WHERE user_id = $1`, uid).Scan(&pricePaid, &promo, &limit, &days); err != nil {
		t.Fatal(err)
	}
	if pricePaid != 300 || promo != "" || limit.Valid || days != 37 {
		t.Errorf("paid row after free days: price=%d promo=%q limit=%v days=%d; want 300, no mark, plan limit, 37 days", pricePaid, promo, limit, days)
	}
}
