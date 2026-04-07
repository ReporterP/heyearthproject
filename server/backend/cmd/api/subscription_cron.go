package main

import (
	"fmt"
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/tbank"
)

// ─────────────────────── Subscription & Billing Cron ───────────────────────
//
// Фоновые задачи:
// 1. Проверка истекающих подписок (каждый час)
// 2. B2B tenant monthly billing (1-го числа каждого месяца)

// StartSubscriptionCron запускает фоновые задачи для подписок и биллинга.
func StartSubscriptionCron() {
	// Проверка истекающих подписок — каждый час
	go func() {
		time.Sleep(30 * time.Second)
		for {
			checkExpiringSubscriptions()
			time.Sleep(1 * time.Hour)
		}
	}()
	log.Println("[subscription-cron] Фоновая проверка подписок запущена (каждый час)")

	// B2B billing — каждый час проверяем, нужно ли запускать
	go func() {
		time.Sleep(1 * time.Minute)
		for {
			checkMonthlyBilling()
			time.Sleep(1 * time.Hour)
		}
	}()
	log.Println("[subscription-cron] B2B monthly billing cron запущен")
}

// checkExpiringSubscriptions находит подписки, истекающие в ближайшие 24 часа.
func checkExpiringSubscriptions() {
	// Логируем подписки, которые скоро истекут.
	// IMPORTANT: исключаем подписки, для которых уже есть pending/completed платёж
	// за последние 24 часа — чтобы не списывать повторно (cron каждый час).
	rows, err := db.DB.Query(`
		SELECT u.user_id, us.valid_until, COALESCE(p.name, us.plan, 'unknown'), us.auto_renew
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.valid_until > NOW()
		  AND us.valid_until < NOW() + INTERVAL '24 hours'
		  AND NOT EXISTS (
		    SELECT 1 FROM payments pay
		    WHERE pay.user_id_text = u.user_id
		      AND pay.description LIKE '%автопродление%'
		      AND pay.created_at > NOW() - INTERVAL '24 hours'
		  )
	`)
	if err != nil {
		log.Printf("[subscription-cron] Ошибка проверки истекающих подписок: %v", err)
		return
	}
	defer rows.Close()

	expiringCount := 0
	for rows.Next() {
		var userID, plan string
		var validUntil time.Time
		var autoRenew bool
		if err := rows.Scan(&userID, &validUntil, &plan, &autoRenew); err != nil {
			continue
		}
		expiringCount++

		if autoRenew {
			// Auto-renew: charge saved payment method
			if err := processAutoRenew(userID); err != nil {
				log.Printf("[subscription-cron] Auto-renew %s (%s): %v", userID, plan, err)
			} else {
				log.Printf("[subscription-cron] Auto-renew %s (%s): платёж создан", userID, plan)
			}
		} else {
			log.Printf("[subscription-cron] Подписка %s (%s) истекает %s",
				userID, plan, validUntil.Format("2006-01-02 15:04"))
		}
	}

	if expiringCount > 0 {
		log.Printf("[subscription-cron] Найдено %d подписок, истекающих в ближайшие 24 часа", expiringCount)
	}

	// Очистка: пересинхронизируем Xray если есть только что истёкшие подписки
	var justExpired int
	_ = db.DB.QueryRow(`
		SELECT COUNT(*)
		FROM user_subscriptions
		WHERE valid_until < NOW()
		  AND valid_until > NOW() - INTERVAL '2 hours'
	`).Scan(&justExpired)

	if justExpired > 0 {
		log.Printf("[subscription-cron] %d подписок только что истекли, пересинхронизация Xray...", justExpired)
		RequestXraySync()
	}
}

// checkMonthlyBilling проверяет нужно ли запускать месячный биллинг для B2B тенантов.
func checkMonthlyBilling() {
	now := time.Now()
	// Запускаем только 1-го числа, между 00:00 и 01:00
	if now.Day() != 1 || now.Hour() != 0 {
		return
	}

	// Проверяем что биллинг за этот месяц ещё не запускался
	var lastBillingMonth string
	_ = db.DB.QueryRow(`
		SELECT COALESCE(value, '') FROM app_settings WHERE key = 'last_billing_month'
	`).Scan(&lastBillingMonth)

	currentMonth := now.Format("2006-01")
	if lastBillingMonth == currentMonth {
		return // Уже обработано в этом месяце
	}

	log.Printf("[subscription-cron] Запуск месячного B2B биллинга за %s...", currentMonth)

	if err := biz.RunMonthlyTenantBilling(); err != nil {
		log.Printf("[subscription-cron] Ошибка B2B биллинга: %v", err)
		return
	}

	// Записываем что биллинг за этот месяц выполнен
	_ = biz.SetSetting("last_billing_month", currentMonth)
	log.Printf("[subscription-cron] B2B биллинг за %s завершён", currentMonth)
}

// processAutoRenew creates a recurring payment for a user with saved payment method.
func processAutoRenew(userID string) error {
	// Get user's saved payment method
	var savedPM string
	err := db.DB.QueryRow("SELECT COALESCE(saved_payment_method, '') FROM users WHERE user_id = $1", userID).Scan(&savedPM)
	if err != nil || savedPM == "" {
		return fmt.Errorf("no saved payment method for %s", userID)
	}

	// Get user's current plan
	plan, err := biz.GetUserPlan(userID)
	if err != nil || plan == nil {
		return fmt.Errorf("no active plan for %s", userID)
	}

	// Free plans don't need payment
	if plan.PriceRub <= 0 {
		// Just extend the subscription
		return biz.ActivateSubscriptionWithPlan(userID, plan.ID, plan.DurationDays, 0, "auto-renew")
	}

	// Create recurring payment via T-Bank (Charge with saved RebillID)
	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		return fmt.Errorf("tbank not configured: %w", err)
	}

	description := fmt.Sprintf("HiEarth %s — автопродление", plan.Name)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(plan.PriceRub), "RUB", "tbank", description)
	if err != nil {
		return fmt.Errorf("create payment record: %w", err)
	}

	// Init + Charge using saved RebillID
	orderID := fmt.Sprintf("renew-%d-%d", paymentDBID, time.Now().Unix())
	chargeRes, err := tbClient.ChargeRecurring(orderID, uint64(plan.PriceRub)*100, savedPM, description, userID)
	if err != nil {
		return fmt.Errorf("tbank charge: %w", err)
	}

	_ = biz.UpdatePaymentYookassa(paymentDBID, chargeRes.PaymentID, chargeRes.Status, "")

	// Activate subscription immediately after successful charge.
	// Webhook is idempotent fallback — don't rely on it arriving.
	if chargeRes.Status == "CONFIRMED" || chargeRes.Status == "AUTHORIZED" {
		if err := biz.ActivateSubscriptionWithPlan(userID, plan.ID, plan.DurationDays, plan.PriceRub, "auto-renew"); err != nil {
			log.Printf("[auto-renew] User %s: charge OK but activation failed: %v", userID, err)
		} else {
			log.Printf("[auto-renew] User %s: subscription renewed (plan: %s, %d₽)", userID, plan.Slug, plan.PriceRub)
			RequestXraySync()
		}
	} else {
		log.Printf("[auto-renew] User %s: T-Bank payment %s (status: %s, awaiting webhook)", userID, chargeRes.PaymentID, chargeRes.Status)
	}

	return nil
}
