package main

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/email"
	"github.com/heyearth/backend/internal/testpg"
)

// E8: an address is confirmed by its link (once, in time); reminders go only to confirmed
// addresses with reminders on, to the right subscriptions, once per window; the one-click link
// only works with its signature.
func TestEmailConfirmAndReminders(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-e8")
	ctx := context.Background()

	// ── the address and its link ──
	u := testpg.NewUser(t, conn, "mail-user")
	if _, err := biz.RequestEmailConfirm(ctx, u, "not an address"); err == nil {
		t.Fatal("a bad address was accepted")
	}
	token, err := biz.RequestEmailConfirm(ctx, u, " Client@Example.TEST ")
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := biz.LoadEmailPrefs(ctx, u); p.Email != "" || p.Pending != "client@example.test" {
		t.Fatalf("before the link: %+v", p)
	}
	if biz.ConfirmedEmail(ctx, u) != "" {
		t.Fatal("a pending address counts as confirmed")
	}
	if _, _, err := biz.ConfirmEmail(ctx, token+"x"); err != biz.ErrEmailToken {
		t.Fatalf("a wrong token: %v", err)
	}
	id, addr, err := biz.ConfirmEmail(ctx, token)
	if err != nil || id != u || addr != "client@example.test" {
		t.Fatalf("confirm: %d %q %v", id, addr, err)
	}
	if _, _, err := biz.ConfirmEmail(ctx, token); err != biz.ErrEmailToken {
		t.Fatalf("the link worked twice: %v", err)
	}
	if biz.ConfirmedEmail(ctx, u) != "client@example.test" {
		t.Fatal("not confirmed")
	}
	stale, _ := biz.RequestEmailConfirm(ctx, u, "new@example.test")
	testpg.MustExec(t, conn, `UPDATE users SET email_token_expires_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, u)
	if _, _, err := biz.ConfirmEmail(ctx, stale); err != biz.ErrEmailToken {
		t.Fatalf("an expired link worked: %v", err)
	}
	if biz.ConfirmedEmail(ctx, u) != "client@example.test" {
		t.Fatal("an unconfirmed new address replaced the confirmed one")
	}

	// ── one-click unsubscribe ──
	q := biz.UnsubscribeQuery("mail-user")
	sig := q[strings.Index(q, "&s=")+3:]
	if err := biz.UnsubscribeByLink(ctx, "mail-user", sig+"x"); err != biz.ErrEmailToken {
		t.Fatalf("a forged link: %v", err)
	}
	if err := biz.UnsubscribeByLink(ctx, "mail-user", sig); err != nil {
		t.Fatal(err)
	}
	if p, _ := biz.LoadEmailPrefs(ctx, u); !p.RemindersOff {
		t.Fatal("the link did not switch reminders off")
	}
	if err := biz.SetEmailReminders(ctx, u, true); err != nil {
		t.Fatal(err)
	}

	// ── who gets a reminder ──
	var paid, free int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-paid', 'Всё включено', '', 300, 30, ARRAY['all'], TRUE) RETURNING id`).Scan(&paid); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, description, price_rub, duration_days, allowed_protocols, is_active)
		VALUES ('t-free', 'Пробный', '', 0, 7, ARRAY['all'], TRUE) RETURNING id`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	confirmed := func(name string) int {
		id := testpg.NewUser(t, conn, name)
		testpg.MustExec(t, conn, `UPDATE users SET email = $2, email_verified_at = NOW() WHERE id = $1`, id, name+"@example.test")
		return id
	}
	sub := func(user, plan int, ends time.Duration, autoRenew bool) {
		testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, auto_renew)
			VALUES ($1, $2, $3, 'active', NOW() - INTERVAL '27 days', $4)`, user, plan, now.Add(ends), autoRenew)
	}
	wallet := func(user int, kop int64) {
		testpg.MustExec(t, conn, `INSERT INTO user_wallets (user_id, balance_kop) VALUES ($1, $2)
			ON CONFLICT (user_id) DO UPDATE SET balance_kop = EXCLUDED.balance_kop`, user, kop)
	}
	sub(u, paid, 60*time.Hour, true) // mail-user: renews fine (3-day letter)
	wallet(u, 50000)
	short := confirmed("short")
	sub(short, paid, 60*time.Hour, true) // the wallet is short
	off := confirmed("off")
	sub(off, paid, 60*time.Hour, false) // auto-renew off
	unconfirmed := testpg.NewUser(t, conn, "unconfirmed")
	testpg.MustExec(t, conn, `UPDATE users SET email_pending = 'p@example.test' WHERE id = $1`, unconfirmed)
	sub(unconfirmed, paid, 60*time.Hour, false)
	muted := confirmed("muted")
	testpg.MustExec(t, conn, `UPDATE users SET email_reminders_off = TRUE WHERE id = $1`, muted)
	sub(muted, paid, 60*time.Hour, false)
	blocked := confirmed("blocked")
	testpg.MustExec(t, conn, `UPDATE users SET is_blocked = TRUE WHERE id = $1`, blocked)
	sub(blocked, paid, 60*time.Hour, false)
	freeUser := confirmed("freeuser")
	sub(freeUser, free, 60*time.Hour, true)
	renewed := confirmed("renewed")
	sub(renewed, paid, 60*time.Hour, false)
	sub(renewed, paid, 30*24*time.Hour, false) // a later period exists: no reminder for the old one
	far := confirmed("far")
	sub(far, paid, 10*24*time.Hour, false)
	todayFine := confirmed("todayfine")
	sub(todayFine, paid, time.Minute, true)
	wallet(todayFine, 50000)
	todayShort := confirmed("todayshort")
	sub(todayShort, paid, time.Minute, true)

	due, err := pickReminders(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	kinds := map[string]email.ReminderKind{}
	for _, r := range due {
		key := r.UserIDText
		if r.Today {
			key += "@today"
		}
		got = append(got, key)
		kinds[key] = r.kind()
	}
	sort.Strings(got)
	want := []string{"mail-user", "off", "short", "todayshort@today"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("recipients %v, want %v", got, want)
	}
	if kinds["mail-user"] != email.ReminderRenewsFine || kinds["short"] != email.ReminderTopUp || kinds["off"] != email.ReminderEnds ||
		kinds["todayshort@today"] != email.ReminderTopUp {
		t.Fatalf("kinds %v", kinds)
	}

	// ── each letter once: the second run sends nothing ──
	var sent []email.Message
	prev := sendReminderMail
	t.Cleanup(func() { sendReminderMail = prev })
	sendReminderMail = func(m email.Message) error {
		sent = append(sent, m)
		testpg.MustExec(t, conn, `INSERT INTO email_log (user_id, user_id_text, email, template, subject, status) VALUES ($1, $2, $3, $4, $5, 'sent')`,
			m.UserID, m.UserIDText, m.To, m.Template, m.Subject)
		return nil
	}
	sendReminders()
	if len(sent) != 4 {
		t.Fatalf("first run sent %d letters", len(sent))
	}
	for _, m := range sent {
		if !strings.Contains(m.UnsubscribeURL, "/api/v1/email/unsubscribe?u="+m.UserIDText+"&s=") || !strings.Contains(m.HTMLBody, "/email/unsubscribe?u=") {
			t.Errorf("%s: no one-click unsubscribe: %q", m.UserIDText, m.UnsubscribeURL)
		}
	}
	sendReminders()
	if len(sent) != 4 {
		t.Fatalf("second run sent %d more letters", len(sent)-4)
	}
}
