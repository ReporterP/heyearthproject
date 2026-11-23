package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
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
			safely("payment-sync", syncPendingPayments)
			safely("payment-never-created", closeNeverCreatedPayments)
			safely("wallet-receipts", func() {
				if walletReceiptsMu.TryLock() {
					defer walletReceiptsMu.Unlock()
					sendPendingWalletReceipts()
				}
			})
			safely("refund-reconcile", reconcilePendingRefunds)
			safely("wallet-refund-release", releaseFailedWalletRefunds)
			safely("wallet-refund-external", holdExternalWalletRefunds)
			safely("payment-alerts", syncUnappliedPaymentAlerts)
			healthBoard.Set("payments", "Оплаты", "ok", "сверка платежей с ЮKassa прошла")
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
	// paymentNeverCreatedSQL closes our payments that never got a YooKassa id within a
	// day: the call to YooKassa failed, so there is nothing to pay or to check. A payment
	// created in YooKassa whose answer was lost is linked by its webhook (metadata
	// payment_db_id) long before that, so it is not touched.
	paymentNeverCreatedSQL = `
		UPDATE payments SET status = 'canceled', yookassa_status = 'canceled',
		       cancellation_reason = 'not created in YooKassa',
		       updated_at = CURRENT_TIMESTAMP
		WHERE provider = 'yookassa'
		  AND COALESCE(yookassa_payment_id, '') = ''
		  AND status IN ('pending', 'waiting_for_capture')
		  AND paid_at IS NULL
		  AND created_at < NOW() - INTERVAL '24 hours'`
	// paymentUnappliedSQL: paid payments still marked "paid but not applied". No LIMIT: the
	// set is what waits for a person, and an open alert missing from it gets resolved.
	paymentUnappliedSQL = `
		SELECT id, COALESCE(user_id_text, ''), COALESCE(metadata::text, '{}'), amount::text
		FROM payments
		WHERE status IN (` + biz.PaidPaymentStatusesSQL + `) AND metadata ? 'unapplied_reason'
		ORDER BY id`
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

// closeNeverCreatedPayments cancels payments YooKassa never received (see
// paymentNeverCreatedSQL).
func closeNeverCreatedPayments() {
	res, err := db.DB.Exec(paymentNeverCreatedSQL)
	if err != nil {
		log.Printf("[payment-sync] close never-created payments: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[payment-sync] closed %d payment(s) never created in YooKassa (older than 24 h)", n)
	}
}

// syncUnappliedPaymentAlerts keeps payment-unapplied-<id> in line with the payments'
// "paid but not applied" mark (see paymentUnapplied), every payment sync run:
//   - a paid payment that still carries the mark raises its alert again: a raise Telegram
//     never got is re-sent once the delivery backoff is over, a delivered one is not repeated;
//   - an open alert whose payment no longer carries the mark (applied, fully refunded, edited
//     by an admin) is resolved. The open keys are read BEFORE the payments, so an alert a
//     claim raises meanwhile is not resolved on a read that predates its mark.
func syncUnappliedPaymentAlerts() {
	open := openUnappliedAlerts()
	rows, err := db.DB.Query(paymentUnappliedSQL)
	if err != nil {
		log.Printf("[payment-sync] unapplied payments: %v", err)
		return
	}
	defer rows.Close()
	type marked struct{ key, details string }
	var list []marked
	for rows.Next() {
		var row paymentRow
		var metaText, amount string
		if err := rows.Scan(&row.ID, &row.UserID, &metaText, &amount); err != nil {
			log.Printf("[payment-sync] unapplied payments: %v", err)
			return // a partial list must not resolve anything
		}
		row.Meta = map[string]any{}
		_ = json.Unmarshal([]byte(metaText), &row.Meta)
		reason, _ := row.Meta[biz.UnappliedReasonKey].(string)
		list = append(list, marked{unappliedAlertKey(row.ID),
			unappliedAlertDetails(row.ID, row.kind(), row.UserID, row.metaInt("tenant_id"), amount, reason)})
	}
	if err := rows.Err(); err != nil {
		log.Printf("[payment-sync] unapplied payments: %v", err)
		return
	}
	still := make(map[string]bool, len(list))
	for _, m := range list {
		still[m.key] = true
		raiseUnappliedAlert(m.key, AlertCritical, unappliedAlertTitle, m.details)
	}
	for _, key := range open {
		if !still[key] {
			resolvePaymentAlert(key, fmt.Sprintf("Платёж #%s больше не ждёт решения: отметка «не применён» снята (платёж применён, возвращён или исправлен вручную).",
				strings.TrimPrefix(key, unappliedAlertPrefix)))
		}
	}
}
