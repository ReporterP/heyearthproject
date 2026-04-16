package biz

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// ScheduleRetry планирует первый retry failed recurring платежа.
// Если для юзера+плана уже есть active retry — не создаёт дубль.
// Возвращает ID записи в payment_retry_queue.
func ScheduleRetry(userDBID, planID, originalPaymentID int, errorMsg string) (int, error) {
	// Проверка — не создан ли уже активный retry для этого юзера+плана
	var existingID int
	err := db.DB.QueryRow(`
		SELECT id FROM payment_retry_queue
		WHERE user_id = $1 AND plan_id = $2 AND status IN ('pending', 'in_progress')
		LIMIT 1
	`, userDBID, planID).Scan(&existingID)
	if err == nil {
		log.Printf("[dunning] Retry уже запланирован для user=%d plan=%d (id=%d)", userDBID, planID, existingID)
		return existingID, nil
	}

	// Первая попытка через 24 часа
	nextRetry := time.Now().Add(24 * time.Hour)
	// Grace period: +3 дня от момента попытки 3 (= 24+72+168+72 = 336 ч ≈ 14 дней с failed_at)
	// Но для UX удобнее — grace_until = last_retry + 72h, это будет определено при последнем attempt
	graceUntil := time.Now().Add(14 * 24 * time.Hour) // upper bound

	var newID int
	err = db.DB.QueryRow(`
		INSERT INTO payment_retry_queue (
			user_id, plan_id, original_payment_id,
			failed_at, next_retry_at, attempt, max_attempts,
			last_error, status, grace_until
		) VALUES (
			$1, $2, $3,
			CURRENT_TIMESTAMP, $4, 0, 3,
			$5, 'pending', $6
		)
		RETURNING id
	`, userDBID, planID, originalPaymentID, nextRetry, errorMsg, graceUntil).Scan(&newID)
	if err != nil {
		return 0, fmt.Errorf("insert retry: %w", err)
	}
	log.Printf("[dunning] ✓ Запланирован retry id=%d для user=%d plan=%d (через 24ч)", newID, userDBID, planID)
	return newID, nil
}

// RetryRow — одна запись очереди retry.
type RetryRow struct {
	ID                int
	UserID            int
	UserIDText        string
	PlanID            int
	OriginalPaymentID sql.NullInt64
	Attempt           int
	MaxAttempts       int
	LastError         string
	GraceUntil        time.Time
}

// ClaimRetry атомарно берёт следующий pending retry у которого next_retry_at < NOW(),
// помечает in_progress. Возвращает nil если нет работы.
func ClaimRetry() (*RetryRow, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var r RetryRow
	err = tx.QueryRow(`
		SELECT rq.id, rq.user_id, u.user_id, rq.plan_id, rq.original_payment_id,
		       rq.attempt, rq.max_attempts, COALESCE(rq.last_error, ''), rq.grace_until
		FROM payment_retry_queue rq
		JOIN users u ON u.id = rq.user_id
		WHERE rq.status = 'pending'
		  AND rq.next_retry_at < NOW()
		ORDER BY rq.next_retry_at ASC
		LIMIT 1
		FOR UPDATE SKIP LOCKED
	`).Scan(&r.ID, &r.UserID, &r.UserIDText, &r.PlanID, &r.OriginalPaymentID,
		&r.Attempt, &r.MaxAttempts, &r.LastError, &r.GraceUntil)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(`
		UPDATE payment_retry_queue SET status = 'in_progress', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, r.ID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &r, nil
}

// MarkRetrySucceeded помечает retry как succeeded.
func MarkRetrySucceeded(retryID, paymentID int) error {
	_, err := db.DB.Exec(`
		UPDATE payment_retry_queue SET
			status = 'succeeded',
			succeeded_payment_id = $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, paymentID, retryID)
	return err
}

// MarkRetryFailed увеличивает attempt, планирует next_retry_at по schedule.
// Schedule: после attempt=1 → +72h, после attempt=2 → +168h (неделя),
// после attempt=3 → status=exhausted, grace_until = NOW() + 72h.
func MarkRetryFailed(retryID int, errorMsg string) error {
	var attempt, maxAttempts int
	_ = db.DB.QueryRow(`SELECT attempt, max_attempts FROM payment_retry_queue WHERE id = $1`, retryID).Scan(&attempt, &maxAttempts)
	attempt++

	if attempt >= maxAttempts {
		// Exhausted — grace period 3 дня
		_, err := db.DB.Exec(`
			UPDATE payment_retry_queue SET
				attempt = $1,
				status = 'exhausted',
				last_error = $2,
				grace_until = NOW() + INTERVAL '3 days',
				updated_at = CURRENT_TIMESTAMP
			WHERE id = $3
		`, attempt, errorMsg, retryID)
		return err
	}

	// Next attempt: 72ч после 1-й попытки, 168ч после 2-й
	var nextDelay time.Duration
	switch attempt {
	case 1:
		nextDelay = 72 * time.Hour
	case 2:
		nextDelay = 168 * time.Hour
	default:
		nextDelay = 24 * time.Hour
	}
	nextRetry := time.Now().Add(nextDelay)

	_, err := db.DB.Exec(`
		UPDATE payment_retry_queue SET
			attempt = $1,
			next_retry_at = $2,
			status = 'pending',
			last_error = $3,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, attempt, nextRetry, errorMsg, retryID)
	return err
}

// CancelActiveRetries отменяет все active retries юзера (например когда он вручную оплатил).
func CancelActiveRetries(userDBID int, reason string) error {
	_, err := db.DB.Exec(`
		UPDATE payment_retry_queue SET
			status = 'canceled',
			last_error = COALESCE(last_error || ' | ', '') || $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $2 AND status IN ('pending', 'in_progress')
	`, reason, userDBID)
	return err
}

// GetActiveRetryForUser возвращает active retry (pending/in_progress/exhausted с активным grace).
func GetActiveRetryForUser(userDBID int) (*RetryRow, error) {
	var r RetryRow
	err := db.DB.QueryRow(`
		SELECT id, user_id, '', plan_id, original_payment_id,
		       attempt, max_attempts, COALESCE(last_error, ''), grace_until
		FROM payment_retry_queue
		WHERE user_id = $1
		  AND (
		    status IN ('pending', 'in_progress')
		    OR (status = 'exhausted' AND grace_until > NOW())
		  )
		ORDER BY created_at DESC
		LIMIT 1
	`, userDBID).Scan(&r.ID, &r.UserID, &r.UserIDText, &r.PlanID, &r.OriginalPaymentID,
		&r.Attempt, &r.MaxAttempts, &r.LastError, &r.GraceUntil)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// HasActiveGracePeriod возвращает true если у юзера есть retry в grace period
// (подписка истекла, но retry или exhausted-grace ещё не закончились).
func HasActiveGracePeriod(userIDText string) bool {
	var dbUserID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userIDText).Scan(&dbUserID); err != nil {
		return false
	}
	r, err := GetActiveRetryForUser(dbUserID)
	if err != nil || r == nil {
		return false
	}
	return time.Until(r.GraceUntil) > 0
}
