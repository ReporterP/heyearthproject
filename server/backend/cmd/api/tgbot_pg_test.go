package main

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// TG4: the bot's reminders reach linked accounts that keep reminders on and did not block the
// bot — the same window and kinds as the e-mails; a second run queues nothing; «закончилась»
// goes once; enqueueTG drops a repeated (kind, ref).
func TestTGReminderRecipientsAndDedup(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	tgCfg.forget(tgBotTokenSetting)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('tg_bot_token', '123456789:AAFakeTokenFakeTokenFakeTokenFake12')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	t.Cleanup(func() { tgCfg.forget(tgBotTokenSetting) })

	var paid int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-paid', 'Полный', '', 300, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var tgID int64 = 5000
	linked := func(name string) int {
		id := testpg.NewUser(t, conn, name)
		tgID++
		testpg.MustExec(t, conn, `UPDATE users SET telegram_id = $2, telegram_linked_at = NOW() WHERE id = $1`, id, tgID)
		return id
	}
	sub := func(user int, ends time.Duration, autoRenew bool) {
		testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew)
			VALUES ($1, $2, $3, 'active', NOW() - INTERVAL '27 days', $4)`, user, paid, now.Add(ends), autoRenew)
	}
	a := linked("tg-fine")
	sub(a, 60*time.Hour, false)
	muted := linked("tg-muted")
	testpg.MustExec(t, conn, `UPDATE users SET notify_reminders = FALSE WHERE id = $1`, muted)
	sub(muted, 60*time.Hour, false)
	blockedBot := linked("tg-blocked-bot")
	testpg.MustExec(t, conn, `UPDATE users SET tg_blocked = TRUE WHERE id = $1`, blockedBot)
	sub(blockedBot, 60*time.Hour, false)
	plain := testpg.NewUser(t, conn, "tg-unlinked")
	sub(plain, 60*time.Hour, false)
	ended := linked("tg-ended")
	sub(ended, -time.Hour, true)

	due, err := pickRemindersFrom(ctx, now, reminderCandidatesQueryTG)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range due {
		got = append(got, r.UserIDText)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "tg-fine" {
		t.Fatalf("bot reminder recipients %v, want [tg-fine]", got)
	}
	if !strings.Contains(tgReminderMessage(due[0]).Text, "автопродление выключено") {
		t.Fatalf("auto-renew off text: %q", tgReminderMessage(due[0]).Text)
	}

	count := func(kind string) int {
		var n int
		_ = conn.QueryRow(`SELECT COUNT(*) FROM tg_outbox WHERE kind = $1`, kind).Scan(&n)
		return n
	}
	sendTGReminders()
	sendTGReminders()
	if n := count(tgKindReminder3d); n != 1 {
		t.Fatalf("3-day reminders queued %d times, want 1", n)
	}
	if n := count(tgKindExpired); n != 1 {
		t.Fatalf("«закончилась» queued %d times, want 1", n)
	}

	// a repeated event is dropped; an unlinked user gets nothing
	m := tgOut{Text: "x"}
	if ok, err := enqueueTG(ctx, a, "purchase", "7:1", m); err != nil || !ok {
		t.Fatalf("first enqueue: %v %v", ok, err)
	}
	if ok, _ := enqueueTG(ctx, a, "purchase", "7:1", m); ok {
		t.Fatal("the same (kind, ref) must be queued once")
	}
	if ok, _ := enqueueTG(ctx, plain, "purchase", "8:1", m); ok {
		t.Fatal("an unlinked user must get nothing")
	}
	if ok, _ := enqueueTG(ctx, blockedBot, "purchase", "9:1", m); ok {
		t.Fatal("a person who blocked the bot must get nothing")
	}
	// unlinking drops what was still waiting
	if _, err := biz.UnlinkTelegram(ctx, a); err != nil {
		t.Fatal(err)
	}
	var pending int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM tg_outbox WHERE user_id = $1 AND status = 'pending'`, a).Scan(&pending)
	if pending != 0 {
		t.Fatalf("pending messages after unlink: %d", pending)
	}
}
