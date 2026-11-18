package main

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
)

// ─────────────────────── E-mail reminders (cleanup plan E8) ───────────────────────
//
// Every 15 minutes: a paid subscription ending in 2–3 days gets one «скоро продление /
// пополните кошелёк / заканчивается» letter, and on its last day one more when it will not
// renew by itself. Only to an address confirmed in the cabinet, never to a blocked user or one
// who switched reminders off; email_log keeps a letter from going twice.

const emailReminderInterval = 15 * time.Minute

func StartEmailReminderCron() {
	go func() {
		time.Sleep(2 * time.Minute)
		for {
			safely("email-cron", sendReminders)
			safely("tg-reminders", sendTGReminders)
			time.Sleep(emailReminderInterval)
		}
	}()
	log.Println("[email-cron] reminders every 15 min")
}

// reminder is one letter to send.
type reminder struct {
	UserDBID   int
	UserIDText string
	To         string
	SubID      int
	ValidUntil time.Time
	PlanName   string
	PriceRub   int
	AutoRenew  bool
	BalanceKop int64
	Today      bool
}

func (r reminder) kind() email.ReminderKind {
	switch {
	case !r.AutoRenew:
		return email.ReminderEnds
	case r.BalanceKop >= int64(r.PriceRub)*100:
		return email.ReminderRenewsFine
	default:
		return email.ReminderTopUp
	}
}

func (r reminder) template() string {
	if r.Today {
		return email.TemplateReminderToday
	}
	return email.TemplateReminder3d
}

// reminderCandidatesQuery: the latest paid subscription of a user with a confirmed address and
// reminders on, ending inside the window ($1, $2]; with the wallet balance.
const reminderCandidatesQuery = `
SELECT u.id, u.user_id, u.email, us.id, us.valid_until, COALESCE(p.name, ''), p.price_rub, us.auto_renew,
       COALESCE((SELECT w.balance_kop FROM user_wallets w WHERE w.user_id = u.id), 0)
  FROM user_subscriptions us
  JOIN users u ON u.id = us.user_id
  JOIN plans p ON p.id = us.plan_id
 WHERE u.email_verified_at IS NOT NULL AND COALESCE(u.email, '') <> '' AND NOT u.email_reminders_off
   AND NOT COALESCE(u.is_blocked, FALSE)
   AND p.price_rub > 0
   AND us.valid_until > $1 AND us.valid_until <= $2
   AND NOT EXISTS (SELECT 1 FROM user_subscriptions n WHERE n.user_id = us.user_id AND n.valid_until > us.valid_until)`

// pickReminders: the letters due at now. The 3-day letter goes for a subscription ending in
// 2–3 days; the last-day one for a subscription ending before the next Moscow midnight that will
// not renew by itself (auto-renew off or the wallet short) — one that renews gets the renewal
// letter instead.
func pickReminders(ctx context.Context, now time.Time) ([]reminder, error) {
	return pickRemindersFrom(ctx, now, reminderCandidatesQuery)
}

// reminderCandidatesQueryTG is the same choice for the Telegram bot: a linked account that did
// not block the bot and keeps the bot's reminders on; To is the Telegram chat id.
var reminderCandidatesQueryTG = strings.Replace(strings.Replace(reminderCandidatesQuery,
	"SELECT u.id, u.user_id, u.email,", "SELECT u.id, u.user_id, u.telegram_id::text,", 1),
	"WHERE u.email_verified_at IS NOT NULL AND COALESCE(u.email, '') <> '' AND NOT u.email_reminders_off",
	"WHERE u.telegram_id IS NOT NULL AND NOT u.tg_blocked AND u.notify_reminders", 1)

// pickRemindersFrom runs the window logic of pickReminders over one candidate query.
func pickRemindersFrom(ctx context.Context, now time.Time, query string) ([]reminder, error) {
	var out []reminder
	scan := func(from, to time.Time, today bool) error {
		rows, err := db.DB.QueryContext(ctx, query, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			r := reminder{Today: today}
			if err := rows.Scan(&r.UserDBID, &r.UserIDText, &r.To, &r.SubID, &r.ValidUntil, &r.PlanName, &r.PriceRub, &r.AutoRenew, &r.BalanceKop); err != nil {
				return err
			}
			if today && r.kind() == email.ReminderRenewsFine {
				continue
			}
			out = append(out, r)
		}
		return rows.Err()
	}
	if err := scan(now.Add(48*time.Hour), now.Add(72*time.Hour), false); err != nil {
		return nil, err
	}
	msk := now.In(moscowTZ)
	midnight := time.Date(msk.Year(), msk.Month(), msk.Day()+1, 0, 0, 0, 0, moscowTZ)
	if err := scan(now, midnight, true); err != nil {
		return nil, err
	}
	return out, nil
}

// sendReminderMail is email.Send (a test seam).
var sendReminderMail = email.Send

// reminderDedup: a user gets one letter of a kind per this window (a 30-day period renews
// long after it ends).
var reminderDedup = map[string]time.Duration{
	email.TemplateReminder3d:    5 * 24 * time.Hour,
	email.TemplateReminderToday: 36 * time.Hour,
}

func sendReminders() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	due, err := pickReminders(ctx, time.Now())
	if err != nil {
		log.Printf("[email-cron] reminders: %v", err)
		return
	}
	sent := 0
	for _, r := range due {
		tpl := r.template()
		if email.AlreadySent(r.UserDBID, tpl, reminderDedup[tpl]) {
			continue
		}
		q := biz.UnsubscribeQueryID(r.UserDBID)
		subj, html, text := email.Reminder(r.kind(), r.PlanName, r.PriceRub, r.ValidUntil, r.BalanceKop, r.Today, email.SiteURL+"/email/unsubscribe?"+q)
		if err := sendReminderMail(email.Message{
			To: r.To, Subject: subj, HTMLBody: html, TextBody: text, Template: tpl,
			UserID: r.UserDBID, UserIDText: r.UserIDText, SubscriptionID: r.SubID,
			UnsubscribeURL: email.SiteURL + "/api/v1/email/unsubscribe?" + q,
		}); err != nil {
			log.Printf("[email-cron] reminder to user %d: %v", r.UserDBID, err)
			continue
		}
		sent++
	}
	if sent > 0 {
		log.Printf("[email-cron] reminders sent: %d", sent)
	}
}

// ── Wallet notices (E8): to the confirmed address only ──

// notifyWalletTopup — a top-up was credited and nothing was bought with it.
func notifyWalletTopup(userDBID int, userIDText string, amountKop, balanceKop int64) {
	to := biz.ConfirmedEmail(context.Background(), userDBID)
	if to == "" {
		return
	}
	subj, html, text := email.WalletToppedUp(amountKop, balanceKop)
	_ = email.Send(email.Message{To: to, Subject: subj, HTMLBody: html, TextBody: text,
		Template: email.TemplateWalletTopup, UserID: userDBID, UserIDText: userIDText})
}

// mailReferralReward — both sides of a «приведи друга» reward get a letter.
func mailReferralReward(rw *biz.ReferralReward) {
	for _, side := range []struct {
		id      int
		kop     int64
		inviter bool
	}{{rw.ReferrerID, rw.ReferrerKop, true}, {rw.FriendID, rw.FriendKop, false}} {
		if side.kop <= 0 {
			continue
		}
		to := biz.ConfirmedEmail(context.Background(), side.id)
		if to == "" {
			continue
		}
		subj, html, text := email.ReferralBonus(side.kop, side.inviter)
		_ = email.Send(email.Message{To: to, Subject: subj, HTMLBody: html, TextBody: text,
			Template: email.TemplateReferralBonus, UserID: side.id})
	}
}
