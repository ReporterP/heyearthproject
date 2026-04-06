package main

import (
	"fmt"
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
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
	// Логируем подписки, которые скоро истекут
	rows, err := db.DB.Query(`
		SELECT u.user_id, us.valid_until, us.plan, us.auto_renew
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE us.valid_until > NOW()
		  AND us.valid_until < NOW() + INTERVAL '24 hours'
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

	// Create recurring payment via YooKassa
	ykClient, err := yookassa.NewClientFromSettings()
	if err != nil {
		return fmt.Errorf("yookassa not configured: %w", err)
	}

	description := fmt.Sprintf("HiEarth %s — автопродление", plan.Name)
	paymentDBID, err := biz.CreatePaymentRecord(userID, float64(plan.PriceRub), "RUB", "yookassa", description)
	if err != nil {
		return fmt.Errorf("create payment record: %w", err)
	}

	ykResp, err := ykClient.CreateRecurringPayment(savedPM, plan.PriceRub, "RUB", description, map[string]string{
		"payment_db_id": fmt.Sprintf("%d", paymentDBID),
		"user_id":       userID,
		"plan_id":       fmt.Sprintf("%d", plan.ID),
		"plan_slug":     plan.Slug,
		"type":          "auto_renew",
	})
	if err != nil {
		return fmt.Errorf("yookassa recurring: %w", err)
	}

	// Update payment record with YooKassa ID
	_ = biz.UpdatePaymentYookassa(paymentDBID, ykResp.ID, ykResp.Status, "")
	log.Printf("[auto-renew] User %s: YooKassa payment %s created (status: %s, plan: %s, %d₽)",
		userID, ykResp.ID, ykResp.Status, plan.Slug, plan.PriceRub)

	return nil
}
