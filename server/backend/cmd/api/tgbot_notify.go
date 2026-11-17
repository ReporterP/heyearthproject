package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
	tg "github.com/heyearth/backend/internal/telegram"
)

// ─────────────────────── Bot notifications (plan TG4) ───────────────────────
//
// The same events as the e-mails (E8) and Happ's announce line, no second copy of the logic:
// the reminder window and kind come from email_cron.go (pickRemindersFrom, reminder.kind), the
// purchase / top-up / bonus notices hang on the hooks that send the letters. Each notice is one
// tg_outbox row per (user, kind, ref), so a cron that runs again never repeats it.
// Reminders, the «закончилась» notice and «не хватает на продление» obey notify_reminders;
// money notices (purchase, top-up, bonus) and support answers always go.

const (
	tgKindReminder3d    = "reminder_3d"
	tgKindReminderToday = "reminder_today" // also «не хватило на автопродление»: one notice per period
	tgKindExpired       = "expired"
	tgKindPurchase      = "purchase"
	tgKindTopup         = "topup"
	tgKindReferral      = "referral"
)

func cabinetRow(path string) []tg.InlineKeyboardButton {
	return tg.Row(tg.WebAppButton("Открыть кабинет", miniAppURL(path)))
}

// tgReminderMessage is the bot's text for a reminder (05 §8a).
func tgReminderMessage(r reminder) tgOut {
	plan := "«" + html.EscapeString(r.PlanName) + "»"
	end := ruDate(r.ValidUntil)
	priceKop := int64(r.PriceRub) * 100
	var text string
	var kb *tg.InlineKeyboardMarkup
	switch r.kind() {
	case email.ReminderRenewsFine:
		text = fmt.Sprintf("🔁 Подписка %s продлится %s: спишем %s с кошелька, на балансе хватает.", plan, end, rub(priceKop))
		kb = tg.Keyboard(cabinetRow(""))
	case email.ReminderTopUp:
		head := fmt.Sprintf("⏳ Подписка %s заканчивается %s.", plan, end)
		if r.Today {
			head = fmt.Sprintf("⏰ Сегодня последний день подписки %s.", plan)
		}
		text = fmt.Sprintf("%s Для автопродления на кошельке не хватает %s (на балансе %s).", head, rub(priceKop-r.BalanceKop), rub(r.BalanceKop))
		if starsRate() > 0 {
			text += " Продлить можно звёздами прямо здесь."
		}
		kb = tg.Keyboard(starsChooseButton("Продлить звёздами"), cabinetRow(""))
	default: // auto-renew off
		head := fmt.Sprintf("⏳ Подписка %s закончится %s — автопродление выключено.", plan, end)
		if r.Today {
			head = fmt.Sprintf("⏰ Сегодня последний день подписки %s — автопродление выключено.", plan)
		}
		text = head + " Продлите её или включите автопродление в кабинете."
		kb = tg.Keyboard(starsChooseButton("Продлить звёздами"),
			tg.Row(tg.WebAppButton("Включить автопродление", miniAppURL("payments"))))
	}
	return tgOut{Text: text, ParseMode: "HTML", Keyboard: kb}
}

func subRef(subID int, until time.Time) string { return fmt.Sprintf("%d:%d", subID, until.Unix()) }

// sendTGReminders runs with the e-mail reminders (every 15 minutes).
func sendTGReminders() {
	if userBotToken() == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	now := time.Now()
	due, err := pickRemindersFrom(ctx, now, reminderCandidatesQueryTG)
	if err != nil {
		log.Printf("[tg-notify] reminders: %v", err)
		return
	}
	queued := 0
	for _, r := range due {
		kind := tgKindReminder3d
		if r.Today {
			kind = tgKindReminderToday
		}
		if ok, err := enqueueTG(ctx, r.UserDBID, kind, subRef(r.SubID, r.ValidUntil), tgReminderMessage(r)); err != nil {
			log.Printf("[tg-notify] reminder %s: %v", shortHiEarthID(r.UserIDText), err)
		} else if ok {
			queued++
		}
	}
	n, err := queueTGExpired(ctx, now)
	if err != nil {
		log.Printf("[tg-notify] expired: %v", err)
	}
	if queued+n > 0 {
		log.Printf("[tg-notify] reminders queued: %d, ended: %d", queued, n)
	}
}

// tgExpiredQuery: a user's latest subscription ended in ($1, $2] and nothing newer exists.
const tgExpiredQuery = `
SELECT u.id, us.id, us.valid_until, COALESCE(p.name, us.plan, '')
  FROM user_subscriptions us
  JOIN users u ON u.id = us.user_id
  LEFT JOIN plans p ON p.id = us.plan_id
 WHERE u.telegram_id IS NOT NULL AND NOT u.tg_blocked AND u.notify_reminders AND NOT COALESCE(u.is_blocked, FALSE)
   AND us.valid_until > $1 AND us.valid_until <= $2
   AND NOT EXISTS (SELECT 1 FROM user_subscriptions n WHERE n.user_id = us.user_id AND n.valid_until > us.valid_until)`

// queueTGExpired queues «подписка закончилась» for subscriptions that ended in the last 3 hours.
func queueTGExpired(ctx context.Context, now time.Time) (int, error) {
	rows, err := db.DB.QueryContext(ctx, tgExpiredQuery, now.Add(-3*time.Hour), now)
	if err != nil {
		return 0, err
	}
	type ended struct {
		user, sub int
		until     time.Time
		plan      string
	}
	var list []ended
	for rows.Next() {
		var e ended
		if err := rows.Scan(&e.user, &e.sub, &e.until, &e.plan); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, e := range list {
		m := tgOut{ParseMode: "HTML",
			Text:     fmt.Sprintf("⚠️ Подписка «%s» закончилась — подключение на паузе. Подписка в Happ остаётся: после оплаты потяните список вниз в Happ, чтобы обновить её.", html.EscapeString(e.plan)),
			Keyboard: tg.Keyboard(starsChooseButton("Продлить звёздами"), cabinetRow(""))}
		if ok, err := enqueueTG(ctx, e.user, tgKindExpired, subRef(e.sub, e.until), m); err == nil && ok {
			n++
		}
	}
	return n, nil
}

// notifyTGRenewShort: the hourly auto-renew found the wallet short (once per period; shares
// its key with the last-day reminder so the person gets one of the two).
func notifyTGRenewShort(userID string, subID int, until time.Time, planName string, priceRub int) {
	if userBotToken() == "" {
		return
	}
	go func() {
		defer recoverBG("tg-renew-short")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var userDBID int
		var reminders bool
		if err := db.DB.QueryRowContext(ctx, `SELECT id, notify_reminders FROM users WHERE user_id = $1`, userID).Scan(&userDBID, &reminders); err != nil || !reminders {
			return
		}
		bal, err := biz.WalletBalance(ctx, userDBID)
		if err != nil {
			return
		}
		price := int64(priceRub) * 100
		m := tgOut{ParseMode: "HTML",
			Text: fmt.Sprintf("⚠️ Не получилось продлить подписку «%s»: на кошельке %s, нужно %s — не хватает %s. Автопродление попробует снова до конца подписки (%s).",
				html.EscapeString(planName), rub(bal), rub(price), rub(price-bal), ruDate(until)),
			Keyboard: tg.Keyboard(starsChooseButton("Продлить звёздами"))}
		if _, err := enqueueTG(ctx, userDBID, tgKindReminderToday, subRef(subID, until), m); err != nil {
			log.Printf("[tg-notify] renew short %s: %v", shortHiEarthID(userID), err)
		}
	}()
}

// notifyTGPurchase: a plan bought or renewed from the wallet (after COMMIT).
func notifyTGPurchase(userID string, res *biz.PurchaseResult, renewal bool) {
	if userBotToken() == "" {
		return
	}
	go func() {
		defer recoverBG("tg-purchase")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var userDBID int
		if err := db.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
			return
		}
		plan := html.EscapeString(res.PlanName)
		var text string
		switch {
		case renewal:
			text = fmt.Sprintf("✅ Подписка «%s» продлена до %s: списали %s с кошелька, на балансе %s.", plan, ruDate(res.ValidUntil), rub(res.PriceKop), rub(res.BalanceAfterKop))
		case res.PriceKop > 0:
			text = fmt.Sprintf("🎉 Подписка «%s» включена до %s. С кошелька списано %s, на балансе %s.", plan, ruDate(res.ValidUntil), rub(res.PriceKop), rub(res.BalanceAfterKop))
		default:
			text = fmt.Sprintf("🎉 Подписка «%s» включена до %s.", plan, ruDate(res.ValidUntil))
		}
		kb := tg.Keyboard(cabinetRow(""))
		if !renewal {
			text += " 🔌 Добавьте её в Happ — кнопка ниже."
			kb = tg.Keyboard(tg.Row(tg.CallbackButton("🔌 Подключить", "m:con").Styled(tg.StyleSuccess)))
		}
		ref := fmt.Sprintf("%d:%d", res.SubscriptionID, res.ValidUntil.Unix())
		if _, err := enqueueTG(ctx, userDBID, tgKindPurchase, ref, tgOut{Text: text, ParseMode: "HTML", Keyboard: kb}); err != nil {
			log.Printf("[tg-notify] purchase %s: %v", shortHiEarthID(userID), err)
		}
	}()
}

// notifyTGTopup: a top-up was credited and nothing was bought with it.
func notifyTGTopup(userDBID int, amountKop, balanceKop int64) {
	notifyTG(userDBID, tgKindTopup, "", tgOut{
		Text:     fmt.Sprintf("💳 Кошелёк пополнен на %s. Баланс — %s.", rub(amountKop), rub(balanceKop)),
		Keyboard: tg.Keyboard(cabinetRow("wallet")),
	})
}

// notifyTGReferral: both sides of a «приведи друга» reward.
func notifyTGReferral(rw *biz.ReferralReward) {
	if rw.ReferrerKop > 0 {
		notifyTG(rw.ReferrerID, tgKindReferral, fmt.Sprintf("%d:inviter", rw.ID), tgOut{
			Text:     fmt.Sprintf("🎁 Друг оплатил подписку — на ваш кошелёк начислено %s.", rub(rw.ReferrerKop)),
			Keyboard: tg.Keyboard(cabinetRow("wallet"))})
	}
	if rw.FriendKop > 0 {
		notifyTG(rw.FriendID, tgKindReferral, fmt.Sprintf("%d:friend", rw.ID), tgOut{
			Text:     fmt.Sprintf("🎁 Бонус за приглашение — на кошелёк начислено %s.", rub(rw.FriendKop)),
			Keyboard: tg.Keyboard(cabinetRow("wallet"))})
	}
}
