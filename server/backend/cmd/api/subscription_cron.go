package main

import (
	"log"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
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
			log.Printf("[subscription-cron] Подписка %s (%s) истекает %s — auto_renew=true (TODO: автоплатёж)",
				userID, plan, validUntil.Format("2006-01-02 15:04"))
			// TODO: создать рекуррентный платёж YooKassa когда будет настроен saved_payment_method
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
