package main

import (
	"log"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
)

// ─────────────────────── Email Reminder Cron ───────────────────────
//
// Каждые 15 минут проверяет подписки, истекающие через 2-4 дня, и шлёт
// превью-email ("Через 3 дня спишется XXX ₽") если ещё не слали в этом
// окне (дедупликация через email_log).

const emailReminderInterval = 15 * time.Minute

func StartEmailReminderCron() {
	go func() {
		time.Sleep(2 * time.Minute)
		for {
			sendRenewalPreviews()
			time.Sleep(emailReminderInterval)
		}
	}()
	log.Println("[email-cron] Reminder cron запущен (каждые 15 мин)")
}

// sendRenewalPreviews — ищет подписки с auto_renew=true, истекающие через 2-4 дня,
// и шлёт preview-email если ещё не слали в последние 7 дней.
func sendRenewalPreviews() {
	rows, err := db.DB.Query(`
		SELECT u.id, u.user_id, COALESCE(u.email, '') AS email,
		       us.id AS sub_id, us.valid_until, p.name AS plan_name, p.price_rub
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.auto_renew = true
		  AND us.valid_until BETWEEN NOW() + INTERVAL '2 days' AND NOW() + INTERVAL '4 days'
		  AND us.paused_until IS NULL
		  AND p.price_rub > 0
		  AND u.saved_payment_method IS NOT NULL
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	type candidate struct {
		DBUserID   int
		UserIDText string
		Email      string
		SubID      int
		ValidUntil time.Time
		PlanName   string
		PriceRub   int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.DBUserID, &c.UserIDText, &c.Email, &c.SubID, &c.ValidUntil, &c.PlanName, &c.PriceRub); err != nil {
			continue
		}
		candidates = append(candidates, c)
	}

	sent := 0
	for _, c := range candidates {
		// Email либо из users.email, либо fallback на payer_email
		to := c.Email
		if to == "" {
			to = email.GetUserEmail(c.UserIDText)
		}
		if to == "" {
			continue
		}
		// Дедуп: не слали preview за последние 7 дней
		if email.AlreadySent(c.DBUserID, email.TemplateRenewalPreview, 7*24*time.Hour) {
			continue
		}

		subj, html, text := email.RenewalPreview(c.PlanName, c.PriceRub, c.ValidUntil)
		if err := email.Send(email.Message{
			To:             to,
			Subject:        subj,
			HTMLBody:       html,
			TextBody:       text,
			Template:       email.TemplateRenewalPreview,
			UserID:         c.DBUserID,
			UserIDText:     c.UserIDText,
			SubscriptionID: c.SubID,
		}); err != nil {
			log.Printf("[email-cron] preview send failed for %s: %v", c.UserIDText, err)
		} else {
			sent++
		}
	}
	if sent > 0 {
		log.Printf("[email-cron] Отправлено preview-уведомлений: %d", sent)
	}
}
