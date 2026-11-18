package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	tgapi "github.com/heyearth/backend/internal/telegram"
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

// A news post carries a scene or its own picture, never both; the caption limit applies to a scene
// too; the site feed never shows the scene (one hedgehog per screen).
func TestValidateNewsScene(t *testing.T) {
	base := biz.NewsInput{Title: "Новое", Body: "Коротко.", Targets: []string{"channel"}}
	with := func(f func(*biz.NewsInput)) biz.NewsInput { in := base; f(&in); return in }
	cases := []struct {
		name string
		in   biz.NewsInput
		want string
	}{
		{"scene alone", with(func(in *biz.NewsInput) { in.Scene = "news" }), ""},
		{"scene and picture", with(func(in *biz.NewsInput) { in.Scene = "news"; in.Image = "a1b2c3d4e5f6a7b8.png" }), "Выберите что-то одно: ёжик или своя картинка."},
		{"unknown scene", with(func(in *biz.NewsInput) { in.Scene = "nope" }), "Неизвестная сценка."},
		{"scene with a long text", with(func(in *biz.NewsInput) { in.Scene = "news"; in.Body = strings.Repeat("я", 1100) }), "С картинкой текст не длиннее 1024"},
		// Telegram counts UTF-16 units: 600 emoji are 1200 of them (review: validation and delivery must agree)
		{"scene with many emoji", with(func(in *biz.NewsInput) { in.Scene = "news"; in.Body = strings.Repeat("🦔", 600) }), "С картинкой текст не длиннее 1024"},
		{"text-only post with many emoji", with(func(in *biz.NewsInput) { in.Body = strings.Repeat("🦔", 600) }), ""},
	}
	for _, c := range cases {
		got := validateNews(c.in, true)
		if (c.want == "" && got != "") || (c.want != "" && !strings.Contains(got, c.want)) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	raw, _ := json.Marshal(toNewsItem(biz.NewsPost{ID: 1, Title: "x", Scene: "news"}))
	if strings.Contains(string(raw), "scene") || strings.Contains(string(raw), "image_url\":\"/") {
		t.Errorf("the site feed must not show the scene: %s", raw)
	}
}

// An edit of a sent copy tries the other kind when the first fails: a scene post that went out as
// text (caption too long, unknown scene) still gets its edit (review).
func TestEditNewsCopyFallsBack(t *testing.T) {
	f := &fakeSceneBot{fail: func(method, _ string) error {
		if method == "editMessageCaption" {
			return &tgapi.APIError{Status: 400, Description: "Bad Request: there is no caption in the message to edit", FromTelegram: true}
		}
		return nil
	}}
	if err := editNewsCopy(context.Background(), &tgapi.Bot{Token: fakeBotToken, Doer: f}, "5", 10, "Исправлено", nil, true); err != nil {
		t.Fatal(err)
	}
	if strings.Join(heads(f.calls), ",") != "editMessageCaption,editMessageText" {
		t.Fatalf("calls: %v", heads(f.calls))
	}
}
