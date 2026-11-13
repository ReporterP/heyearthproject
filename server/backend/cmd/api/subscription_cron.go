package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
			safely("subscription-cron", checkExpiringSubscriptions)
			time.Sleep(1 * time.Hour)
		}
	}()
	log.Println("[subscription-cron] Фоновая проверка подписок запущена (каждый час)")

	// B2B billing — каждый час проверяем, нужно ли запускать
	go func() {
		time.Sleep(1 * time.Minute)
		for {
			safely("b2b-billing", checkMonthlyBilling)
			safely("b2b-balance", checkTenantBalanceWarnings)
			time.Sleep(1 * time.Hour)
		}
	}()
	log.Println("[subscription-cron] B2B monthly billing + low-balance warnings cron запущен")
}

// checkExpiringSubscriptions находит подписки, истекающие в ближайшие 24 часа.
func checkExpiringSubscriptions() {
	// Логируем подписки, которые скоро истекут.
	// IMPORTANT: исключаем подписки, для которых уже есть pending/completed платёж
	// за последние 24 часа — чтобы не списывать повторно (cron каждый час).
	rows, err := db.DB.Query(`
		SELECT u.user_id, us.id, us.valid_until, COALESCE(p.name, us.plan, 'unknown'), us.auto_renew
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.valid_until > NOW()
		  AND us.valid_until < NOW() + INTERVAL '24 hours'
		  AND (us.paused_until IS NULL OR us.paused_until < NOW())
	`)
	if err != nil {
		log.Printf("[subscription-cron] Ошибка проверки истекающих подписок: %v", err)
		return
	}
	defer rows.Close()

	expiringCount := 0
	for rows.Next() {
		var userID, plan string
		var subID int
		var validUntil time.Time
		var autoRenew bool
		if err := rows.Scan(&userID, &subID, &validUntil, &plan, &autoRenew); err != nil {
			continue
		}
		expiringCount++

		if autoRenew {
			// Auto-renew: charge saved payment method
			if err := processAutoRenew(userID, subID); err != nil {
				log.Printf("[subscription-cron] Auto-renew %s (%s): %v", userID, plan, err)
			} else {
				log.Printf("[subscription-cron] Auto-renew %s (%s): обработано", userID, plan)
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

// checkTenantBalanceWarnings runs once per day; emails tenants whose balance
// is below ~50% of next month's expected debit. Dedup is internal to
// RunTenantBalanceWarnings (one notification per calendar month per tenant).
func checkTenantBalanceWarnings() {
	now := time.Now()
	// Run once per day around 9:00 local time so the email arrives during
	// working hours. Other ticks no-op.
	if now.Hour() != 9 {
		return
	}
	var lastDate string
	_ = db.DB.QueryRow(`SELECT COALESCE(value, '') FROM app_settings WHERE key = 'last_tenant_warning_date'`).Scan(&lastDate)
	today := now.Format("2006-01-02")
	if lastDate == today {
		return
	}
	log.Printf("[subscription-cron] Проверка низких балансов B2B-тенантов…")
	if err := biz.RunTenantBalanceWarnings(); err != nil {
		log.Printf("[subscription-cron] Ошибка проверки балансов: %v", err)
		return
	}
	_ = biz.SetSetting("last_tenant_warning_date", today)
	log.Printf("[subscription-cron] Проверка балансов завершена")
}

// processAutoRenew renews one subscription (user_subscriptions.id) that ends within 24 h
// and has auto_renew set.
//
// Free access (owner's decision 13.11): a subscription given by a promocode and paid
// nothing (price_paid = 0, promocode_used names an existing code) renews free, for the
// plan's duration, and keeps the code as its mark. A free plan WITHOUT a promocode (the
// trial taken from the site) never renews: its auto_renew is switched off. Paid plans
// are paid from the wallet (see below).
func processAutoRenew(userID string, subID int) error {
	var planID, pricePaid int
	var promo string
	err := db.DB.QueryRow(`
		SELECT COALESCE(plan_id, 0), COALESCE(price_paid, 0), COALESCE(promocode_used, '')
		FROM user_subscriptions WHERE id = $1 AND valid_until > NOW() AND valid_until < NOW() + INTERVAL '25 hours'`, subID).Scan(&planID, &pricePaid, &promo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // not due (already renewed, or ended)
	}
	if err != nil {
		return fmt.Errorf("subscription %d: %w", subID, err)
	}
	if planID == 0 {
		return fmt.Errorf("subscription %d has no plan", subID)
	}
	plan, err := biz.GetPlanByID(planID)
	if err != nil || plan == nil {
		return fmt.Errorf("plan %d of subscription %d: %w", planID, subID, err)
	}

	// Activation extends the user's latest active row; a row that is not that one (an
	// older trial next to a paid plan) is left to end on its own.
	var latest int
	if err := db.DB.QueryRow(`
		SELECT us.id FROM user_subscriptions us JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW() AND us.status = 'active' AND us.plan_id IS NOT NULL
		ORDER BY us.valid_until DESC LIMIT 1`, userID).Scan(&latest); err != nil {
		return fmt.Errorf("latest subscription of %s: %w", userID, err)
	}
	if latest != subID {
		log.Printf("[auto-renew] User %s: subscription %d is not the current one (%d) — not renewed", userID, subID, latest)
		return nil
	}

	freePromo := false
	if pricePaid == 0 {
		if freePromo, err = biz.IsFreeAccessPromocode(context.Background(), db.DB, promo); err != nil {
			return fmt.Errorf("check promocode of subscription %d: %w", subID, err)
		}
	}
	if freePromo {
		return biz.ActivateSubscriptionWithPlan(userID, plan.ID, plan.DurationDays, 0, biz.NormalizePromocode(promo))
	}
	if plan.PriceRub <= 0 {
		if _, err := db.DB.Exec(`UPDATE user_subscriptions SET auto_renew = FALSE WHERE id = $1`, subID); err != nil {
			return fmt.Errorf("switch off auto-renew of free subscription %d: %w", subID, err)
		}
		log.Printf("[auto-renew] User %s: free plan '%s' without a promocode is not renewed (auto_renew switched off)", userID, plan.Slug)
		return nil
	}

	// Paid plan: from the wallet (cleanup plan W4 — card recurring and dunning are
	// gone). The key is the subscription and its current end: the hourly cron, a retry
	// or a restart never charge one period twice. Not enough money: nothing happens —
	// Happ shows «Пополните кошелёк» and the reminder email asks to top up; the next
	// hourly run tries again until the subscription ends.
	var validUntil time.Time
	if err := db.DB.QueryRow(`SELECT valid_until FROM user_subscriptions WHERE id = $1`, subID).Scan(&validUntil); err != nil {
		return fmt.Errorf("subscription %d: %w", subID, err)
	}
	res, err := walletPurchaseNow(context.Background(), biz.PurchaseRequest{
		UserID: userID, PlanID: plan.ID,
		Key:  fmt.Sprintf("renew:%d:%d", subID, validUntil.Unix()),
		Kind: biz.WalletAutoRenew,
	})
	if errors.Is(err, biz.ErrInsufficientFunds) {
		log.Printf("[auto-renew] User %s: not enough on the wallet for '%s' (%d ₽) — waiting for a top-up", userID, plan.Slug, plan.PriceRub)
		return nil
	}
	if err != nil {
		return fmt.Errorf("renew from wallet: %w", err)
	}
	if res.Replayed {
		log.Printf("[auto-renew] User %s: period already renewed", userID)
	}
	return nil
}
