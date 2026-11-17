package main

import (
	"context"
	"strings"
	"testing"
	"time"

	tgapi "github.com/heyearth/backend/internal/telegram"
	"github.com/heyearth/backend/internal/testpg"
)

// The chat menu without the Mini App: sections for a linked person, auto-renew from the chat,
// signing in by HiEarth ID in the chat (the ID message is deleted), creating an account, and the
// inline invitation card.
func TestTGChatMenu(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	tgCfg.forget(tgBotTokenSetting, tgStarsRateSetting, tgInlineSetting)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('tg_bot_token', $1), ('tg_stars_rub', '1.3'), ('tg_bot_inline', '1'), ('tg_bot_username', 'hiearth_test_bot')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, fakeBotToken)
	t.Cleanup(func() { tgCfg.forget(tgBotTokenSetting, tgStarsRateSetting, tgInlineSetting, tgBotUsernameSetting) })

	var plan int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-menu', 'Полный', '', 300, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	u := testpg.NewUser(t, conn, "menu-user-0000-1111")
	testpg.MustExec(t, conn, `UPDATE users SET telegram_id = 8100 WHERE id = $1`, u)
	testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew)
		VALUES ($1, $2, $3, 'active', NOW() - INTERVAL '5 days', TRUE)`, u, plan, time.Now().Add(20*24*time.Hour))
	testpg.MustExec(t, conn, `INSERT INTO user_wallets (user_id, balance_kop) VALUES ($1, 12000)`, u)

	fake := &fakeStarsBot{}
	bot := &tgapi.Bot{Token: fakeBotToken, Doer: fake}
	c, err := newTGCtx(ctx, bot, tgapi.User{ID: 8100, FirstName: "Иван"})
	if err != nil || c.user == nil {
		t.Fatalf("linked ctx: %v", err)
	}

	home, kb := tgRender(c, "home")
	if !strings.Contains(home, "Иван") || !strings.Contains(home, "«Полный»") || !strings.Contains(home, "120 ₽") || !strings.Contains(home, "не хватает 180") {
		t.Fatalf("home: %q", home)
	}
	var data []string
	for _, r := range kb.InlineKeyboard {
		for _, b := range r {
			data = append(data, b.CallbackData)
		}
	}
	for _, want := range []string{"m:bal", "m:sub", "m:con", "m:ref", "m:pay", "m:sup", "m:news", "m:set"} {
		if !strings.Contains(strings.Join(data, " "), want) {
			t.Errorf("home menu lacks %s: %v", want, data)
		}
	}
	if bal, _ := tgRender(c, "bal"); !strings.Contains(bal, "На балансе: <b>120 ₽</b>") {
		t.Errorf("balance: %q", bal)
	}
	// auto-renew off from the chat
	if msg := tgSetAutoRenew(c, false); !strings.Contains(msg, "выключено") {
		t.Fatalf("auto-renew: %q", msg)
	}
	if sub, kb := tgRender(c, "sub"); !strings.Contains(sub, "Автопродление выключено") || kb.InlineKeyboard[0][0].CallbackData != "ar:on" {
		t.Errorf("subscription after switching off: %q", sub)
	}
	// the referral section shares through inline mode when it is on
	ref, kb := tgRender(c, "ref")
	if !strings.Contains(ref, "/r/") || kb.InlineKeyboard[0][0].SwitchInlineQuery == nil {
		t.Errorf("referral: %q %+v", ref, kb.InlineKeyboard[0][0])
	}
	// the inline card carries the person's link and opens the bot with ref_
	handleInlineQuery(ctx, bot, &tgapi.InlineQuery{ID: "iq", From: tgapi.User{ID: 8100}})
	call := fake.last("answerInlineQuery")
	if !strings.Contains(call, "/r/") || !strings.Contains(call, "start=ref_") || !strings.Contains(call, `"is_personal":true`) {
		t.Errorf("inline answer: %s", call)
	}

	// a stranger signs in by HiEarth ID in the chat
	other := testpg.NewUser(t, conn, "chat-login-2222-3333")
	s, _ := newTGCtx(ctx, bot, tgapi.User{ID: 8200, FirstName: "Пётр"})
	if s.user != nil {
		t.Fatal("not linked yet")
	}
	if w, _ := tgRender(s, "home"); !strings.Contains(w, "Войдите или создайте") {
		t.Fatalf("welcome: %q", w)
	}
	tgAskLogin(s)
	handleTGMessage(s, &tgapi.Message{MessageID: 41, From: &tgapi.User{ID: 8200}, Chat: tgapi.Chat{ID: 8200, Type: "private"}, Text: "chat-login-2222-3333"})
	if !strings.Contains(fake.last("deleteMessage"), `"message_id":41`) {
		t.Errorf("the message with the ID must be deleted: %s", fake.last("deleteMessage"))
	}
	var linked int64
	_ = conn.QueryRow(`SELECT COALESCE(telegram_id, 0) FROM users WHERE id = $1`, other).Scan(&linked)
	if linked != 8200 {
		t.Fatalf("sign-in by ID did not link: %d", linked)
	}

	// a newcomer creates an account in the chat
	n, _ := newTGCtx(ctx, bot, tgapi.User{ID: 8300, FirstName: "Анна"})
	tgCreateAccount(n, 0)
	var created int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM users WHERE telegram_id = 8300`).Scan(&created)
	if created != 1 || !strings.Contains(fake.last("sendMessage"), "Аккаунт создан") {
		t.Fatalf("account in the chat: %d, %s", created, fake.last("sendMessage"))
	}
}
