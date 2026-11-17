package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	tgapi "github.com/heyearth/backend/internal/telegram"
	"github.com/heyearth/backend/internal/testpg"
)

func TestStarsPrice(t *testing.T) {
	for _, c := range []struct {
		rub  int
		rate float64
		want int
	}{{300, 1.3, 231}, {250, 1.3, 193}, {3000, 1.3, 2308}, {300, 1, 300}, {300, 0, 0}, {0, 1.3, 0}, {1, 100, 1}} {
		if got := starsPrice(c.rub, c.rate); got != c.want {
			t.Errorf("starsPrice(%d, %v) = %d, want %d", c.rub, c.rate, got, c.want)
		}
	}
	if p, u, s, ok := parseStarsPayload(starsPayload(4, 17, 231)); !ok || p != 4 || u != 17 || s != 231 {
		t.Fatalf("payload round trip: %d %d %d %v", p, u, s, ok)
	}
	if _, _, _, ok := parseStarsPayload("plan:x:1:2"); ok {
		t.Fatal("a broken payload must not parse")
	}
}

// fakeStarsBot records Bot API calls and answers them like Telegram.
type fakeStarsBot struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeStarsBot) Do(_ context.Context, req *tgapi.Request) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req.Method+" "+string(req.Body))
	switch req.Method {
	case "answerPreCheckoutQuery", "refundStarPayment":
		return []byte("true"), nil
	}
	out, _ := json.Marshal(map[string]any{"message_id": len(f.calls), "chat": map[string]any{"id": 1}})
	return out, nil
}

func (f *fakeStarsBot) last(method string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if strings.HasPrefix(f.calls[i], method+" ") {
			return f.calls[i]
		}
	}
	return ""
}

// Stars: the pre-checkout accepts only the linked account at the shown price; a successful
// payment activates the plan once per charge id; a cheaper plan than the current one is refused.
func TestStarsPreCheckoutAndActivation(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	tgCfg.forget(tgBotTokenSetting, tgStarsRateSetting)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('tg_bot_token', $1), ('tg_stars_rub', '1.3')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, fakeBotToken)
	t.Cleanup(func() { tgCfg.forget(tgBotTokenSetting, tgStarsRateSetting) })

	var full, start int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-full', 'Полный', '', 300, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&full); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-start', 'Старт', '', 250, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&start); err != nil {
		t.Fatal(err)
	}
	u := testpg.NewUser(t, conn, "stars-user")
	testpg.MustExec(t, conn, `UPDATE users SET telegram_id = 9100 WHERE id = $1`, u)

	fake := &fakeStarsBot{}
	bot := &tgapi.Bot{Token: fakeBotToken, Doer: fake}
	inv, _, err := starsInvoice(ctx, u, full)
	if err != nil || inv.Stars != 231 {
		t.Fatalf("invoice: %+v %v", inv, err)
	}

	// another Telegram account cannot pay this invoice
	handleStarsPreCheckout(ctx, bot, &tgapi.PreCheckoutQuery{ID: "q1", From: tgapi.User{ID: 9999}, Currency: "XTR", TotalAmount: 231, InvoicePayload: inv.Payload})
	if !strings.Contains(fake.last("answerPreCheckoutQuery"), `"ok":false`) {
		t.Fatalf("a stranger's pre-checkout must be refused: %s", fake.last("answerPreCheckoutQuery"))
	}
	// the right account at the right price
	handleStarsPreCheckout(ctx, bot, &tgapi.PreCheckoutQuery{ID: "q2", From: tgapi.User{ID: 9100}, Currency: "XTR", TotalAmount: 231, InvoicePayload: inv.Payload})
	if !strings.Contains(fake.last("answerPreCheckoutQuery"), `"ok":true`) {
		t.Fatalf("pre-checkout must pass: %s", fake.last("answerPreCheckoutQuery"))
	}
	// the price changed since the invoice
	testpg.MustExec(t, conn, `UPDATE app_settings SET value = '1.5' WHERE key = 'tg_stars_rub'`)
	tgCfg.forget(tgStarsRateSetting)
	handleStarsPreCheckout(ctx, bot, &tgapi.PreCheckoutQuery{ID: "q3", From: tgapi.User{ID: 9100}, Currency: "XTR", TotalAmount: 231, InvoicePayload: inv.Payload})
	if !strings.Contains(fake.last("answerPreCheckoutQuery"), "Цена изменилась") {
		t.Fatalf("a changed price must be refused: %s", fake.last("answerPreCheckoutQuery"))
	}

	// the payment activates the plan once
	c, err := newTGCtx(ctx, bot, tgapi.User{ID: 9100})
	if err != nil {
		t.Fatal(err)
	}
	paid := &tgapi.Message{MessageID: 5, From: &tgapi.User{ID: 9100}, Chat: tgapi.Chat{ID: 9100, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 231, InvoicePayload: inv.Payload, TelegramPaymentChargeID: "charge-1"}}
	handleStarsPaid(c, paid)
	handleStarsPaid(c, paid)
	var subs, rows int
	var status string
	_ = conn.QueryRow(`SELECT COUNT(*) FROM user_subscriptions WHERE user_id = $1 AND valid_until > NOW()`, u).Scan(&subs)
	_ = conn.QueryRow(`SELECT COUNT(*), MIN(status) FROM stars_payments WHERE charge_id = 'charge-1'`).Scan(&rows, &status)
	if subs != 1 || rows != 1 || status != "activated" {
		t.Fatalf("activation: subs=%d rows=%d status=%q", subs, rows, status)
	}
	var until time.Time
	_ = conn.QueryRow(`SELECT valid_until FROM user_subscriptions WHERE user_id = $1`, u).Scan(&until)
	if d := time.Until(until); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("a 30-day plan, got %v", d)
	}
	if strings.Contains(fake.last("refundStarPayment"), "charge-1") {
		t.Fatal("a successful activation must not refund")
	}

	// with «Полный» active the cheaper «Старт» is not offered for Stars
	if _, _, err := starsInvoice(ctx, u, start); err == nil || !strings.Contains(err.Error(), "дороже") {
		t.Fatalf("a cheaper plan must be refused while the dearer one runs: %v", err)
	}
	// a payload for another account is refunded, nothing activates
	other := testpg.NewUser(t, conn, "stars-other")
	bad := &tgapi.Message{MessageID: 6, From: &tgapi.User{ID: 9100}, Chat: tgapi.Chat{ID: 9100, Type: "private"},
		SuccessfulPayment: &tgapi.SuccessfulPayment{Currency: "XTR", TotalAmount: 231, InvoicePayload: starsPayload(full, other, 231), TelegramPaymentChargeID: "charge-2"}}
	handleStarsPaid(c, bad)
	if !strings.Contains(fake.last("refundStarPayment"), "charge-2") {
		t.Fatal("a mismatched payment must be refunded")
	}
	_ = conn.QueryRow(`SELECT status FROM stars_payments WHERE charge_id = 'charge-2'`).Scan(&status)
	if status != "refunded" {
		t.Fatalf("mismatched payment status %q", status)
	}
}
