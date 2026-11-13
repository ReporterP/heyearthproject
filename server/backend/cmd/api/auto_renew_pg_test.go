package main

import (
	"database/sql"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

func pgPlan(t *testing.T, conn *sql.DB, slug string, price, days int) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`
		INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active, auto_renew_default)
		VALUES ($1, $1, '', $2, $3, ARRAY['all'], TRUE, $2 > 0) RETURNING id`, slug, price, days).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// pgSub inserts a subscription ending in 12 h with auto-renew on and returns its id.
func pgSub(t *testing.T, conn *sql.DB, userDBID, planID, pricePaid int, promo string) int {
	t.Helper()
	var id int
	if err := conn.QueryRow(`
		INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, price_paid, promocode_used, status, started_at)
		VALUES ($1, $2, NOW() + INTERVAL '12 hours', TRUE, $3, NULLIF($4, ''), 'active', NOW() - INTERVAL '30 days') RETURNING id`,
		userDBID, planID, pricePaid, promo).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func subState(t *testing.T, conn *sql.DB, id int) (autoRenew bool, hoursLeft int, promo string) {
	t.Helper()
	if err := conn.QueryRow(`SELECT auto_renew, (EXTRACT(EPOCH FROM valid_until - NOW()) / 3600)::int, COALESCE(promocode_used, '') FROM user_subscriptions WHERE id = $1`, id).
		Scan(&autoRenew, &hoursLeft, &promo); err != nil {
		t.Fatal(err)
	}
	return
}

// A1: the free trial without a promocode is not renewed; free access by a promocode is,
// and keeps its code (so it is still recognised as promo access next time).
func TestAutoRenewFreeAccess(t *testing.T) {
	conn := testpg.Open(t)
	trial := pgPlan(t, conn, "t-trial", 0, 7)
	annual := pgPlan(t, conn, "t-annual", 3000, 365)
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, free_days, plan_id_granted) VALUES ('GIFT1', 30, 'free_trial', 30, $1)`, annual)

	// Trial from the site: no renewal, auto-renew switched off, end date untouched.
	u1 := testpg.NewUser(t, conn, "trial-user")
	s1 := pgSub(t, conn, u1, trial, 0, "")
	if err := processAutoRenew("trial-user", s1); err != nil {
		t.Fatal(err)
	}
	if ar, h, _ := subState(t, conn, s1); ar || h > 12 {
		t.Errorf("trial without promocode: auto_renew=%v hours left=%d, want off and unchanged", ar, h)
	}

	// The loophole's own mark ("auto-renew") is not a promocode.
	u2 := testpg.NewUser(t, conn, "loophole-user")
	s2 := pgSub(t, conn, u2, trial, 0, "auto-renew")
	if err := processAutoRenew("loophole-user", s2); err != nil {
		t.Fatal(err)
	}
	if ar, h, _ := subState(t, conn, s2); ar || h > 12 {
		t.Errorf("auto-renew mark: auto_renew=%v hours left=%d, want off and unchanged", ar, h)
	}

	// Free access by a promocode renews free for the plan's duration, twice in a row.
	u3 := testpg.NewUser(t, conn, "promo-user")
	s3 := pgSub(t, conn, u3, annual, 0, "gift1")
	for i := 1; i <= 2; i++ {
		if err := processAutoRenew("promo-user", s3); err != nil {
			t.Fatal(err)
		}
		ar, h, promo := subState(t, conn, s3)
		if !ar || promo != "GIFT1" || h < i*365*24 {
			t.Errorf("promo renewal %d: auto_renew=%v hours=%d promo=%q", i, ar, h, promo)
		}
	}
}

// Review fixes: a discount code, or a free code the owner switched off, does not make
// a zero-price subscription renew free; a row that is not the user's current one is
// left alone.
func TestAutoRenewOnlyActiveFreeCodes(t *testing.T) {
	conn := testpg.Open(t)
	trial := pgPlan(t, conn, "t-trial", 0, 7)
	annual := pgPlan(t, conn, "t-annual", 3000, 365)
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, discount_percent) VALUES ('TEN', 30, 'discount_percent', 10)`)
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, free_days, is_active) VALUES ('OFFGIFT', 30, 'free_trial', 30, FALSE)`)

	u1 := testpg.NewUser(t, conn, "disc-user")
	s1 := pgSub(t, conn, u1, trial, 0, "TEN")
	u2 := testpg.NewUser(t, conn, "off-user")
	s2 := pgSub(t, conn, u2, annual, 0, "OFFGIFT")
	for _, c := range []struct {
		user string
		sub  int
	}{{"disc-user", s1}, {"off-user", s2}} {
		if err := processAutoRenew(c.user, c.sub); err != nil {
			t.Fatal(err)
		}
		if _, h, _ := subState(t, conn, c.sub); h > 12 {
			t.Errorf("%s renewed free (%d hours left)", c.user, h)
		}
	}

	// Two rows: the older one ending soon is not the current subscription.
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, free_days) VALUES ('GIFT', 30, 'free_trial', 30)`)
	u3 := testpg.NewUser(t, conn, "two-rows")
	old := pgSub(t, conn, u3, trial, 0, "GIFT")
	testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, price_paid) VALUES ($1, $2, NOW() + INTERVAL '20 days', 'active', 3000)`, u3, annual)
	if err := processAutoRenew("two-rows", old); err != nil {
		t.Fatal(err)
	}
	if _, h, _ := subState(t, conn, old); h > 12 {
		t.Errorf("non-current row renewed (%d hours left)", h)
	}
}
