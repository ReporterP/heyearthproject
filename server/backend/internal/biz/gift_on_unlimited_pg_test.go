package biz

import (
	"errors"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A traffic gift is worthless on an unlimited plan: buying one with it is refused with the
// cabinet's text, and neither the code nor the money is spent.
func TestTrafficGiftIsRefusedOnAnUnlimitedPlan(t *testing.T) {
	conn := testpg.Open(t)
	plan := newPlan(t, conn, "t-unlim", 300, 30, nil, true)
	u := testpg.NewUser(t, conn, "gift-buyer")
	topupFor(t, conn, u, "gift-buyer", 30000, "yk-gift")
	testpg.MustExec(t, conn, `INSERT INTO promocodes (code, duration_days, promo_type, max_uses, per_user_limit, traffic_gift_bytes)
		VALUES ('GIFT5', 30, 'traffic_gift', 100, 1, 5368709120)`)

	_, err := purchase(t, conn, PurchaseRequest{UserID: "gift-buyer", PlanID: plan, Key: "purchase:gift", Kind: WalletPurchase, Promocode: "GIFT5"})
	if !errors.Is(err, ErrPromoUnlimited) {
		t.Fatalf("got %v, want ErrPromoUnlimited", err)
	}
	var used int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM promocode_redemptions WHERE code = 'GIFT5'`).Scan(&used)
	if used != 0 {
		t.Errorf("the code was spent (%d redemptions)", used)
	}
	if b := balanceOf(t, conn, u); b != 30000 {
		t.Errorf("balance %d, want 30000 (nothing charged)", b)
	}
	if err := PromoFitsPlan(&Promocode{Type: PromoTrafficGift}, 10<<30); err != nil {
		t.Errorf("a gift on a limited plan: %v, want nil", err)
	}
}
