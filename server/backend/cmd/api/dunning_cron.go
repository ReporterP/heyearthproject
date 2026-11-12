package main

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── Dunning Cron ───────────────────────
//
// Каждые 30 минут обрабатывает очередь failed recurring платежей,
// ретраит через YooKassa ChargeRecurring. Расписание попыток:
// +24ч → +72ч → +168ч, после 3х провалов — exhausted + grace period 3 дня.
// Каждая попытка триггерит email юзеру.

const dunningInterval = 30 * time.Minute

func StartDunningCron() {
	go func() {
		time.Sleep(3 * time.Minute)
		for {
			safely("dunning", processRetryQueue)
			time.Sleep(dunningInterval)
		}
	}()
	log.Println("[dunning] Cron запущен (каждые 30 мин)")
}

func processRetryQueue() {
	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		return // Ключи не заданы
	}

	processed := 0
	for i := 0; i < 20; i++ { // макс 20 retries за цикл
		retry, err := biz.ClaimRetry()
		if err != nil {
			log.Printf("[dunning] ClaimRetry error: %v", err)
			return
		}
		if retry == nil {
			break
		}
		if err := attemptRetry(client, retry); err != nil {
			log.Printf("[dunning] retry id=%d failed: %v", retry.ID, err)
		}
		processed++
	}
	if processed > 0 {
		log.Printf("[dunning] Обработано retries: %d", processed)
	}
}

// attemptRetry — одна попытка retry для конкретного юзера+плана.
func attemptRetry(client *yookassa.Client, retry *biz.RetryRow) error {
	plan, err := biz.GetPlanByID(retry.PlanID)
	if err != nil || plan == nil {
		_ = biz.MarkRetryFailed(retry.ID, "plan not found")
		return err
	}

	var savedPM string
	_ = db.DB.QueryRow(`SELECT COALESCE(saved_payment_method, '') FROM users WHERE id = $1`, retry.UserID).Scan(&savedPM)
	if savedPM == "" {
		_ = biz.MarkRetryFailed(retry.ID, "no saved payment method")
		sendDunningEmail(retry, plan, fmt.Errorf("карта не привязана"))
		return nil
	}

	description := fmt.Sprintf("HiEarth %s — автопродление (попытка %d)", plan.Name, retry.Attempt+1)
	paymentDBID, err := biz.CreatePaymentRecord(retry.UserIDText, float64(plan.PriceRub), "RUB", "yookassa", description)
	if err != nil {
		_ = biz.MarkRetryFailed(retry.ID, err.Error())
		return err
	}

	email := email.GetUserEmail(retry.UserIDText)
	receipt := buildReceipt(description, float64(plan.PriceRub), email, "full_payment")
	metadata := map[string]string{
		"payment_db_id": fmt.Sprintf("%d", paymentDBID),
		"plan_id":       fmt.Sprintf("%d", plan.ID),
		"plan_slug":     plan.Slug,
		"user_id":       retry.UserIDText,
		"kind":          "subscription",
		"auto_renew":    "true",
		"retry_id":      fmt.Sprintf("%d", retry.ID),
	}

	payment, err := client.ChargeRecurring(savedPM,
		yookassa.Amount{Value: fmt.Sprintf("%.2f", float64(plan.PriceRub)), Currency: "RUB"},
		description, receipt, metadata)

	if err != nil {
		_ = biz.MarkRetryFailed(retry.ID, err.Error())
		sendDunningEmailByPlan(retry, plan, err)
		return err
	}

	// Запоминаем yookassa_id в нашем payment и применяем списание через paid claim
	// (как и webhook этого списания: применит тот, кто первый).
	if err := recordRecurringCharge(paymentDBID, payment,
		map[string]any{"plan_id": plan.ID, "retry_id": retry.ID, "kind": "subscription", "auto_renew": true}); err != nil {
		log.Printf("[dunning] retry %d: charge %s: %v (activation left to the webhook/sync)", retry.ID, payment.ID, err)
	}

	if payment.Status == yookassa.PaymentStatusSucceeded {
		_ = biz.MarkRetrySucceeded(retry.ID, paymentDBID)
		log.Printf("[dunning] ✓ Retry %d succeeded for user %s (plan %s)", retry.ID, retry.UserIDText, plan.Slug)
		RequestXraySync()
		return nil
	}

	// Status pending/canceled — считаем как failed для этой попытки
	reason := "status=" + payment.Status
	if payment.CancellationDetails != nil && payment.CancellationDetails.Reason != "" {
		reason = payment.CancellationDetails.Reason
	}
	_ = biz.MarkRetryFailed(retry.ID, reason)
	sendDunningEmailByPlan(retry, plan, errors.New(reason))
	return nil
}

func sendDunningEmailByPlan(retry *biz.RetryRow, plan *biz.Plan, retryErr error) {
	sendDunningEmail(retry, plan, retryErr)
}

func sendDunningEmail(retry *biz.RetryRow, plan *biz.Plan, retryErr error) {
	to := email.GetUserEmail(retry.UserIDText)
	if to == "" {
		return
	}

	// Определяем время след. попытки
	var nextRetry time.Time
	_ = db.DB.QueryRow(`SELECT next_retry_at FROM payment_retry_queue WHERE id = $1`, retry.ID).Scan(&nextRetry)

	subj, html, text := email.PaymentFailedRetry(plan.Name, plan.PriceRub, retry.Attempt+1, nextRetry, retry.GraceUntil)

	// Если exhausted → шлём expired template
	var status string
	_ = db.DB.QueryRow(`SELECT status FROM payment_retry_queue WHERE id = $1`, retry.ID).Scan(&status)
	if status == "exhausted" {
		subj, html, text = email.SubscriptionExpired(plan.Name)
	}

	_ = email.Send(email.Message{
		To:         to,
		Subject:    subj,
		HTMLBody:   html,
		TextBody:   text,
		Template:   email.TemplatePaymentFailedRetry,
		UserID:     retry.UserID,
		UserIDText: retry.UserIDText,
	})
}
