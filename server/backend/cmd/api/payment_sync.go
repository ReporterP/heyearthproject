package main

import (
	"database/sql"
	"log"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── Payment Sync Cron ───────────────────────
//
// Периодически опрашивает YooKassa по всем "застрявшим" платежам
// (pending или waiting_for_capture старше N минут) и синхронизирует
// статус локально. Это подстраховка на случай когда webhook не пришёл
// (сеть упала, YooKassa сбой, наш backend был недоступен).
//
// Без этого cron'а платёж может висеть в pending вечно, даже если
// по факту оплачен или отменён на стороне YooKassa.

const (
	// Проверяем каждые 5 минут
	paymentSyncInterval = 5 * time.Minute

	// Синхронизируем платежи старше 3 минут (чтобы webhook успел прийти
	// первым при нормальной работе)
	paymentSyncMinAge = 3 * time.Minute

	// За один прогон обрабатываем максимум N платежей чтобы не перегружать
	// YooKassa API (rate limit: 50 req/sec, но дергать лишнее незачем).
	paymentSyncBatchSize = 50

	// Платежи старше этого возраста считаем "мёртвыми" — автоматически
	// переводим в canceled если всё ещё pending. YooKassa сам отменяет
	// неоплаченные через 10 минут (checkout timeout), 1 час — с запасом.
	paymentStaleThreshold = 1 * time.Hour
)

// StartPaymentSyncCron запускает фоновый cron синхронизации статусов.
func StartPaymentSyncCron() {
	go func() {
		// Первый прогон через минуту после старта
		time.Sleep(1 * time.Minute)
		for {
			syncPendingPayments()
			reconcilePendingRefunds()
			time.Sleep(paymentSyncInterval)
		}
	}()
	log.Println("[payment-sync] Фоновый cron синхронизации pending платежей запущен (каждые 5 мин)")
}

const (
	// paymentSyncSelectSQL: unpaid payments to re-check. A payment whose activation failed
	// paymentActivationMaxFailures times is left alone (an alert went out), and the ones
	// that failed before go after the fresh ones so they cannot starve new payments.
	paymentSyncSelectSQL = `
		SELECT id, yookassa_payment_id, created_at
		FROM payments
		WHERE yookassa_payment_id IS NOT NULL
		  AND yookassa_payment_id != ''
		  AND status IN ('pending', 'waiting_for_capture')
		  AND created_at < NOW() - ($1::interval)
		  AND activation_failures < $3
		ORDER BY activation_failures ASC, created_at ASC
		LIMIT $2`
	// paymentStaleCancelSQL cancels a payment YooKassa no longer knows — never a paid one.
	paymentStaleCancelSQL = `
		UPDATE payments SET status = 'canceled', yookassa_status = 'canceled',
		       cancellation_reason = 'auto-canceled: stale payment not found in YooKassa',
		       updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status IN ('pending', 'waiting_for_capture') AND paid_at IS NULL`
)

// paymentGetter is the part of the YooKassa client the sync needs (a test seam).
type paymentGetter interface {
	GetPayment(paymentID string) (*yookassa.Payment, error)
}

// Test seams.
var (
	newPaymentSyncClient = func() (paymentGetter, error) {
		c, err := yookassa.NewClientFromSettings()
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	syncProcessPayment = processPaymentEvent
)

// syncPendingPayments опрашивает все pending платежи старше 3 минут.
func syncPendingPayments() {
	client, err := newPaymentSyncClient()
	if err != nil {
		// Ключи не заданы — молча выходим (настройки ещё не готовы)
		return
	}

	rows, err := db.DB.Query(paymentSyncSelectSQL, paymentSyncMinAge.String(), paymentSyncBatchSize, paymentActivationMaxFailures)
	if err != nil {
		log.Printf("[payment-sync] Ошибка выборки pending: %v", err)
		return
	}
	defer rows.Close()

	type pendingPayment struct {
		ID         int
		YookassaID string
		CreatedAt  time.Time
	}
	var pendings []pendingPayment
	for rows.Next() {
		var p pendingPayment
		if err := rows.Scan(&p.ID, &p.YookassaID, &p.CreatedAt); err != nil {
			continue
		}
		pendings = append(pendings, p)
	}

	if len(pendings) == 0 {
		return
	}
	log.Printf("[payment-sync] Синхронизация %d pending платежей...", len(pendings))

	synced := 0
	stale := 0
	for _, p := range pendings {
		payment, err := client.GetPayment(p.YookassaID)
		if err != nil {
			// YooKassa вернула 404 или ошибку — если платёж очень старый, автоматически
			// помечаем canceled (скорее всего он удалён или никогда не существовал).
			if time.Since(p.CreatedAt) > paymentStaleThreshold {
				if _, err := db.DB.Exec(paymentStaleCancelSQL, p.ID); err != nil {
					log.Printf("[payment-sync] stale cancel of payment %d: %v", p.ID, err)
				}
				stale++
			}
			continue
		}

		// Если статус изменился — processPaymentEvent обновит запись и
		// активирует подписку если нужно.
		if payment.Status != "pending" && payment.Status != "waiting_for_capture" {
			if err := syncProcessPayment(payment); err != nil {
				log.Printf("[payment-sync] Ошибка обработки payment %d (yk=%s): %v", p.ID, p.YookassaID, err)
				continue
			}
			synced++
		} else if time.Since(p.CreatedAt) > paymentStaleThreshold {
			// Всё ещё pending после часа — YooKassa сама должна отменить
			// через checkout timeout (10 мин), но на всякий случай.
			log.Printf("[payment-sync] Payment %d (yk=%s) всё ещё pending после %s, пропускаем",
				p.ID, p.YookassaID, time.Since(p.CreatedAt).Round(time.Minute))
		}
	}

	if synced > 0 || stale > 0 {
		log.Printf("[payment-sync] ✓ Синхронизировано: %d, помечено stale: %d", synced, stale)
	}
}

// SyncSinglePayment — ручная синхронизация одного платежа (для админского endpoint).
// Возвращает актуальный payment и ошибку.
func SyncSinglePayment(paymentID int) (*yookassa.Payment, error) {
	var yookassaID string
	err := db.DB.QueryRow(`SELECT COALESCE(yookassa_payment_id, '') FROM payments WHERE id = $1`, paymentID).Scan(&yookassaID)
	if err == sql.ErrNoRows {
		return nil, err
	}
	if yookassaID == "" {
		return nil, sql.ErrNoRows
	}
	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, err
	}
	payment, err := client.GetPayment(yookassaID)
	if err != nil {
		return nil, err
	}
	if err := processPaymentEvent(payment); err != nil {
		log.Printf("[payment-sync] process single payment %d: %v", paymentID, err)
	}
	return payment, nil
}
