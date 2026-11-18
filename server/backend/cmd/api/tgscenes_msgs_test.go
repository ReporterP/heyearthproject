package main

import (
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// Every event notice carries its scene (spec 12 §12.6).
func TestTGNoticeScenes(t *testing.T) {
	until := time.Date(2026, 12, 18, 12, 0, 0, 0, time.UTC)
	res := &biz.PurchaseResult{PlanName: "Полный", ValidUntil: until, PriceKop: 30000, BalanceAfterKop: 1000, SubscriptionID: 1}
	rem := func(autoRenew bool, balanceKop int64) tgOut {
		return tgReminderMessage(reminder{PlanName: "Полный", ValidUntil: until, PriceRub: 300, AutoRenew: autoRenew, BalanceKop: balanceKop})
	}
	cases := []struct {
		name  string
		m     tgOut
		scene string
		text  string
	}{
		{"purchase", tgPurchaseOut(res, false), "coin", "включена до"},
		{"renewal", tgPurchaseOut(res, true), "coin", "продлена до"},
		{"topup", tgTopupOut(50000, 60000), "piggy", "Кошелёк пополнен"},
		{"referral inviter", tgReferralOut(10000, true), "gift", "Друг оплатил"},
		{"referral friend", tgReferralOut(10000, false), "gift", "Бонус за приглашение"},
		{"renew short", tgRenewShortOut("Полный", 1000, 30000, until), "wallet", "Не получилось продлить"},
		{"expired", tgExpiredOut("Полный"), "sleep", "закончилась"},
		{"reminder, auto-renew off", rem(false, 0), "alarm", "автопродление выключено"},
		{"reminder, renews fine", rem(true, 50000), "alarm", "продлится"},
		{"reminder, top-up needed", rem(true, 1000), "wallet", "не хватает"},
	}
	for _, c := range cases {
		if c.m.Scene != c.scene || !strings.Contains(c.m.Text, c.text) {
			t.Errorf("%s: scene %q (want %q), text %q", c.name, c.m.Scene, c.scene, c.m.Text)
		}
	}
}
