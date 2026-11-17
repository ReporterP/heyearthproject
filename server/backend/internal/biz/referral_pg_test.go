package biz

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

func codeOf(t *testing.T, userDBID int) string {
	t.Helper()
	c, err := EnsureReferralCode(context.Background(), userDBID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// R2: a code is created once; binding refuses oneself, a circle, a second binding, the
// cabinet field after 7 days or after a payment.
func TestReferralBindRules(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	a := testpg.NewUser(t, conn, "ref-a")
	b := testpg.NewUser(t, conn, "ref-b")
	c := testpg.NewUser(t, conn, "ref-c")
	ca := codeOf(t, a)
	if again := codeOf(t, a); again != ca || len(ca) != 8 {
		t.Fatalf("code %q then %q", ca, again)
	}
	if err := BindReferral(ctx, a, ca, "", true); !errors.Is(err, ErrReferralSelf) {
		t.Errorf("self: %v", err)
	}
	if err := BindReferral(ctx, b, "nope1234", "", true); !errors.Is(err, ErrReferralNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := BindReferral(ctx, b, " "+ca+" ", "203.0.113.5", true); err != nil { // case/space-insensitive
		t.Fatal(err)
	}
	if err := BindReferral(ctx, b, codeOf(t, c), "", true); !errors.Is(err, ErrReferralBound) {
		t.Errorf("rebind: %v", err)
	}
	// a -> b exists: b's code for a is a circle.
	if err := BindReferral(ctx, a, codeOf(t, b), "", true); !errors.Is(err, ErrReferralCircle) {
		t.Errorf("circle: %v", err)
	}
	// The cabinet field: 7 days, and before the first payment.
	old := testpg.NewUser(t, conn, "ref-old")
	testpg.MustExec(t, conn, `UPDATE users SET created_at = NOW() - INTERVAL '8 days' WHERE id = $1`, old)
	if err := BindReferral(ctx, old, ca, "", true); !errors.Is(err, ErrReferralTooLate) {
		t.Errorf("late: %v", err)
	}
	if err := BindReferral(ctx, old, ca, "", false); err != nil { // registration by the link: no window
		t.Errorf("link: %v", err)
	}
	payer := testpg.NewUser(t, conn, "ref-payer")
	testpg.MustExec(t, conn, `INSERT INTO payments (user_id_text, amount, currency, status, provider) VALUES ('ref-payer', 300, 'RUB', 'succeeded', 'yookassa')`)
	if err := BindReferral(ctx, payer, ca, "", true); !errors.Is(err, ErrReferralAfterPaid) {
		t.Errorf("after paid: %v", err)
	}
	testpg.MustExec(t, conn, `UPDATE app_settings SET value = 'false' WHERE key = 'referral_enabled'`)
	if err := BindReferral(ctx, c, ca, "", true); !errors.Is(err, ErrReferralOff) {
		t.Errorf("off: %v", err)
	}
}

// R3: the friend's first purchase paid with real money rewards both, once; a purchase paid
// with bonus money does not count; the monthly cap holds.
func TestReferralReward(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	plan := newPlan(t, conn, "t-all", 300, 30, nil, true)
	a := testpg.NewUser(t, conn, "inviter")
	b := testpg.NewUser(t, conn, "friend")
	if err := BindReferral(ctx, b, codeOf(t, a), "", false); err != nil {
		t.Fatal(err)
	}

	// Paid only with an admin credit (no YooKassa money): no reward.
	if _, err := walletApply(t, conn, b, 30000, WalletAdminCredit, "gift", WalletRefs{Comment: "x"}); err != nil {
		t.Fatal(err)
	}
	res, err := purchase(t, conn, PurchaseRequest{UserID: "friend", PlanID: plan, Key: "p0", Kind: WalletPurchase})
	if err != nil || res.Referral != nil {
		t.Fatalf("bonus-money purchase: %+v %v", res, err)
	}
	// Real money: rewarded, both wallets credited.
	topupFor(t, conn, b, "friend", 30000, "yk-f1")
	res, err = purchase(t, conn, PurchaseRequest{UserID: "friend", PlanID: plan, Key: "p1", Kind: WalletPurchase})
	if err != nil || res.Referral == nil || res.Referral.ReferrerKop != 10000 {
		t.Fatalf("first real purchase: %+v %v", res, err)
	}
	if ba, bb := balanceOf(t, conn, a), balanceOf(t, conn, b); ba != 10000 || bb != 10000 {
		t.Errorf("balances inviter %d friend %d, want 10000 each", ba, bb)
	}
	// Second real purchase: nothing more.
	topupFor(t, conn, b, "friend", 30000, "yk-f2")
	if res, err := purchase(t, conn, PurchaseRequest{UserID: "friend", PlanID: plan, Key: "p2", Kind: WalletPurchase}); err != nil || res.Referral != nil {
		t.Fatalf("second purchase: %+v %v", res, err)
	}
	if ba := balanceOf(t, conn, a); ba != 10000 {
		t.Errorf("inviter balance %d after a second purchase, want 10000", ba)
	}

	// The cap: 1 per month -> the next friend's reward is recorded as capped, no money.
	testpg.MustExec(t, conn, `UPDATE app_settings SET value = '1' WHERE key = 'referral_monthly_cap'`)
	c := testpg.NewUser(t, conn, "friend2")
	if err := BindReferral(ctx, c, codeOf(t, a), "", false); err != nil {
		t.Fatal(err)
	}
	topupFor(t, conn, c, "friend2", 30000, "yk-g1")
	res, err = purchase(t, conn, PurchaseRequest{UserID: "friend2", PlanID: plan, Key: "q1", Kind: WalletPurchase})
	if err != nil || res.Referral == nil || !res.Referral.Capped {
		t.Fatalf("capped: %+v %v", res, err)
	}
	if ba := balanceOf(t, conn, a); ba != 10000 {
		t.Errorf("inviter balance %d over the cap, want 10000", ba)
	}
	var st sql.NullString
	_ = conn.QueryRow(`SELECT status FROM referral_rewards WHERE friend_id = $1`, c).Scan(&st)
	if st.String != "capped" {
		t.Errorf("status %q", st.String)
	}
}

// Review fix: a retry of a finished purchase gets its result even when the promocode it
// used is now exhausted.
func TestPurchaseReplayBeforePromoCheck(t *testing.T) {
	conn := testpg.Open(t)
	plan := newPlan(t, conn, "t-all", 300, 30, nil, true)
	u := testpg.NewUser(t, conn, "replayer")
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, discount_percent, max_uses) VALUES ('ONE', 30, 'discount_percent', 10, 1)`)
	topupFor(t, conn, u, "replayer", 30000, "yk-r")
	first, err := purchase(t, conn, PurchaseRequest{UserID: "replayer", PlanID: plan, Promocode: "ONE", Key: "k", Kind: WalletPurchase})
	if err != nil {
		t.Fatal(err)
	}
	again, err := purchase(t, conn, PurchaseRequest{UserID: "replayer", PlanID: plan, Promocode: "ONE", Key: "k", Kind: WalletPurchase})
	if err != nil || !again.Replayed || again.PriceKop != first.PriceKop || again.PlanName == "" {
		t.Fatalf("replay: %+v %v", again, err)
	}
}

// Review fixes: the friend's first real-money purchase below the minimum closes the
// chance; the inviter buying while a friend buys does not deadlock.
func TestReferralFirstPurchaseAndNoDeadlock(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	cheap := newPlan(t, conn, "t-cheap", 100, 30, nil, true)
	plan := newPlan(t, conn, "t-all", 300, 30, nil, true)
	a := testpg.NewUser(t, conn, "inv")
	b := testpg.NewUser(t, conn, "frd")
	if err := BindReferral(ctx, b, codeOf(t, a), "", false); err != nil {
		t.Fatal(err)
	}
	topupFor(t, conn, b, "frd", 10000, "yk-c1")
	if res, err := purchase(t, conn, PurchaseRequest{UserID: "frd", PlanID: cheap, Key: "c1", Kind: WalletPurchase}); err != nil || res.Referral != nil {
		t.Fatalf("cheap first purchase: %+v %v", res, err)
	}
	topupFor(t, conn, b, "frd", 30000, "yk-c2")
	if res, err := purchase(t, conn, PurchaseRequest{UserID: "frd", PlanID: plan, Key: "c2", Kind: WalletPurchase}); err != nil || res.Referral != nil {
		t.Fatalf("second purchase rewarded after a first one below the minimum: %+v %v", res, err)
	}

	// Concurrency: friends of A and A himself buy at the same time, repeatedly.
	for i := 0; i < 5; i++ {
		f := testpg.NewUser(t, conn, "f"+string(rune('a'+i)))
		if err := BindReferral(ctx, f, codeOf(t, a), "", false); err != nil {
			t.Fatal(err)
		}
		topupFor(t, conn, f, "f"+string(rune('a'+i)), 30000, "yk-f"+string(rune('a'+i)))
	}
	topupFor(t, conn, a, "inv", 150000, "yk-inv")
	errs := make(chan error, 10)
	for i := 0; i < 5; i++ {
		go func(i int) {
			_, err := purchase(t, conn, PurchaseRequest{UserID: "f" + string(rune('a'+i)), PlanID: plan, Key: "fp" + string(rune('a'+i)), Kind: WalletPurchase})
			errs <- err
		}(i)
		go func(i int) {
			_, err := purchase(t, conn, PurchaseRequest{UserID: "inv", PlanID: plan, Key: "ip" + string(rune('a'+i)), Kind: WalletPurchase})
			errs <- err
		}(i)
	}
	for i := 0; i < 10; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent purchase: %v", err)
		}
	}
}
