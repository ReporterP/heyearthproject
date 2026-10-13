package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// Admin refunds through YooKassa without double refunds.
//
//  1. reserveRefund — one transaction with the payment row locked FOR UPDATE: the payment
//     must be paid, the amount must fit into what is left after every pending or succeeded
//     refund, and a 'pending' payment_refunds row (the reservation) is inserted. Two
//     concurrent requests serialize on the lock; the second one sees the first reservation.
//  2. executeRefund — a second transaction that locks the payment row again for the whole
//     YooKassa call: POST /v3/refunds with Idempotence-Key refund-<payment id>-<row id>,
//     then the row is linked to the refund, or marked 'failed' when YooKassa rejected it.
//
// When YooKassa does not answer (network error, 5xx, 202) nobody knows whether the refund
// exists, so the reservation stays 'pending' and keeps holding its amount. The next request
// for exactly that amount repeats the attempt with the same key and body, and YooKassa
// returns the refund the first request created instead of making a second one.

// refundCreator is the part of the YooKassa client the refund flow needs (a test seam).
type refundCreator interface {
	CreateRefundWithKey(req yookassa.CreateRefundRequest, idempotenceKey string) (*yookassa.Refund, error)
}

var newRefundClient = func() (refundCreator, error) {
	c, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, err
	}
	return c, nil
}

const (
	// refundCallAttempts: an unanswered call is repeated once right away, with the same key.
	refundCallAttempts = 2
	// refundTimeout bounds the whole flow (two YooKassa calls of up to 30 s each).
	refundTimeout = 2 * time.Minute
)

// refundRetryDelay is the pause before repeating an unanswered call (0 in tests).
var refundRetryDelay = time.Second

// refundResumeWindow: YooKassa keeps an Idempotence-Key for 24 hours, an unanswered attempt
// is repeated with its key only well inside that window. Older ones stay reserved.
const refundResumeWindow = `INTERVAL '23 hours'`

const (
	refundLockPaymentSQL = `SELECT COALESCE(yookassa_payment_id, ''), COALESCE(status, ''), ROUND(amount * 100)::bigint
		FROM payments WHERE id = $1 FOR UPDATE`
	refundHeldSQL = `SELECT COALESCE(SUM(ROUND(amount * 100)), 0)::bigint FROM payment_refunds
		WHERE payment_id = $1 AND status IN (` + biz.RefundHoldStatusesSQL + `)`
	refundUnansweredSQL = `SELECT id, ROUND(amount * 100)::bigint, COALESCE(description, '') FROM payment_refunds
		WHERE payment_id = $1 AND status = 'pending' AND yookassa_refund_id IS NULL
		  AND created_at > NOW() - ` + refundResumeWindow + `
		ORDER BY id LIMIT 1`
	refundReserveSQL = `INSERT INTO payment_refunds (payment_id, amount, currency, status, description)
		VALUES ($1, $2::numeric, 'RUB', 'pending', $3) RETURNING id`
	refundRowStateSQL     = `SELECT status, COALESCE(yookassa_refund_id, '') FROM payment_refunds WHERE id = $1`
	refundByYookassaIDSQL = `SELECT id FROM payment_refunds WHERE yookassa_refund_id = $1`
	refundLinkSQL         = `UPDATE payment_refunds SET yookassa_refund_id = $2, amount = $3::numeric, status = $4,
		updated_at = CURRENT_TIMESTAMP WHERE id = $1`
	refundDropReservationSQL = `DELETE FROM payment_refunds WHERE id = $1 AND yookassa_refund_id IS NULL`
	refundMarkFailedSQL      = `UPDATE payment_refunds SET status = 'failed', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'pending' AND yookassa_refund_id IS NULL`
)

// refundError is a refusal with an HTTP status and a Russian message for the admin.
type refundError struct {
	status int
	msg    string
}

func (e *refundError) Error() string { return e.msg }

func refundRefusal(status int, format string, args ...any) *refundError {
	return &refundError{status: status, msg: fmt.Sprintf(format, args...)}
}

// rubles formats kopecks for the admin: 24950 -> "249,50".
func rubles(kop int64) string {
	if kop < 0 {
		kop = 0
	}
	return fmt.Sprintf("%d,%02d", kop/100, kop%100)
}

// ykAmount formats kopecks for YooKassa and NUMERIC columns: 24950 -> "249.50".
func ykAmount(kop int64) string {
	return fmt.Sprintf("%d.%02d", kop/100, kop%100)
}

// parseRefundAmount turns the requested rubles into kopecks; 0 means "everything that is left".
func parseRefundAmount(rub float64) (int64, *refundError) {
	if math.IsNaN(rub) || rub < 0 {
		return 0, refundRefusal(http.StatusBadRequest, "Сумма возврата не может быть отрицательной")
	}
	x := rub * 100
	if x > 1e13 { // far above any payment; keeps the int64 conversion defined
		return math.MaxInt64, nil
	}
	k := math.Round(x)
	if math.Abs(x-k) > 1e-6 {
		return 0, refundRefusal(http.StatusBadRequest, "Сумма возврата указывается с точностью до копейки")
	}
	return int64(k), nil
}

// refundReservation is a payment_refunds row reserved for an attempt.
type refundReservation struct {
	ID        int64
	AmountKop int64
	Reason    string // sent to YooKassa as the description; a repeat must send the same body
}

// refundState is what the refund decision reads under the payment row lock.
type refundState struct {
	YookassaID string // payments.yookassa_payment_id
	Status     string // payments.status
	AmountKop  int64  // payments.amount
	HeldKop    int64  // pending + succeeded refunds, unanswered reservations included
	Unanswered *refundReservation
}

// decideRefund returns the kopecks to reserve for a new attempt, or the unanswered attempt to
// repeat, or the refusal. requestedKop 0 means "everything that is left".
func decideRefund(st refundState, requestedKop int64) (int64, *refundReservation, *refundError) {
	if st.YookassaID == "" {
		return 0, nil, refundRefusal(http.StatusBadRequest, "У платежа нет идентификатора ЮKassa — вернуть его через ЮKassa нельзя")
	}
	if !biz.IsPaidPaymentStatus(st.Status) {
		return 0, nil, refundRefusal(http.StatusBadRequest, "Вернуть можно только оплаченный платёж (статус платежа: %s)", st.Status)
	}
	if u := st.Unanswered; u != nil {
		if requestedKop == u.AmountKop {
			return 0, u, nil
		}
		return 0, nil, refundRefusal(http.StatusConflict,
			"Предыдущий возврат на %s ₽ остался без ответа ЮKassa. Повторите его на ту же сумму — повтор безопасен, второй возврат не создастся", rubles(u.AmountKop))
	}
	remaining := st.AmountKop - st.HeldKop
	amount := requestedKop
	if amount == 0 {
		amount = remaining
	}
	if remaining <= 0 || amount > remaining {
		return 0, nil, refundRefusal(http.StatusBadRequest, "Можно вернуть не больше %s ₽ (остаток с учётом незавершённых возвратов)", rubles(remaining))
	}
	return amount, nil, nil
}

// refundAttempt is one refund attempt: a reservation row and the YooKassa request it sends.
type refundAttempt struct {
	PaymentID  int
	RowID      int64
	YookassaID string
	AmountKop  int64
	Reason     string
	Resumed    bool // repeating an attempt YooKassa did not answer
}

// refundIdempotenceKey is the YooKassa Idempotence-Key of a refund attempt: stable for the
// attempt (its reservation row), different for every new attempt.
func refundIdempotenceKey(paymentID int, rowID int64) string {
	return fmt.Sprintf("refund-%d-%d", paymentID, rowID)
}

func (a refundAttempt) request() yookassa.CreateRefundRequest {
	return yookassa.CreateRefundRequest{
		PaymentID:   a.YookassaID,
		Amount:      yookassa.Amount{Value: ykAmount(a.AmountKop), Currency: "RUB"},
		Description: a.Reason,
	}
}

// reserveRefund checks the payment and reserves the amount in one transaction under the
// payment row lock, or picks the unanswered attempt that this request repeats.
func reserveRefund(ctx context.Context, paymentID int, requestedKop int64, reason string) (refundAttempt, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return refundAttempt{}, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	var st refundState
	if err := tx.QueryRowContext(ctx, refundLockPaymentSQL, paymentID).Scan(&st.YookassaID, &st.Status, &st.AmountKop); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return refundAttempt{}, refundRefusal(http.StatusNotFound, "Платёж не найден")
		}
		return refundAttempt{}, fmt.Errorf("lock payment: %w", err)
	}
	if err := tx.QueryRowContext(ctx, refundHeldSQL, paymentID).Scan(&st.HeldKop); err != nil {
		return refundAttempt{}, fmt.Errorf("sum refunds: %w", err)
	}
	var u refundReservation
	switch err := tx.QueryRowContext(ctx, refundUnansweredSQL, paymentID).Scan(&u.ID, &u.AmountKop, &u.Reason); {
	case err == nil:
		st.Unanswered = &u
	case !errors.Is(err, sql.ErrNoRows):
		return refundAttempt{}, fmt.Errorf("find unanswered refund: %w", err)
	}

	amountKop, resume, refusal := decideRefund(st, requestedKop)
	if refusal != nil {
		return refundAttempt{}, refusal
	}
	a := refundAttempt{PaymentID: paymentID, YookassaID: st.YookassaID}
	if resume != nil {
		a.RowID, a.AmountKop, a.Reason, a.Resumed = resume.ID, resume.AmountKop, resume.Reason, true
	} else {
		if err := tx.QueryRowContext(ctx, refundReserveSQL, paymentID, ykAmount(amountKop), reason).Scan(&a.RowID); err != nil {
			return refundAttempt{}, fmt.Errorf("reserve refund: %w", err)
		}
		a.AmountKop, a.Reason = amountKop, reason
	}
	if err := tx.Commit(); err != nil {
		return refundAttempt{}, fmt.Errorf("commit reservation: %w", err)
	}
	return a, nil
}

type refundCallOutcome int

const (
	refundUnknown  refundCallOutcome = iota // no answer: the refund may or may not exist
	refundCreated                           // YooKassa returned the refund
	refundRejected                          // YooKassa answered 4xx: nothing was created
)

func classifyRefundCall(ref *yookassa.Refund, err error) refundCallOutcome {
	if err == nil {
		if ref != nil && ref.ID != "" {
			return refundCreated
		}
		return refundUnknown // HTTP 202: YooKassa is still processing this key
	}
	var apiErr *yookassa.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		return refundRejected
	}
	return refundUnknown // transport error, 5xx, unreadable answer
}

// callRefund sends the attempt, repeating an unanswered call with the same key.
func callRefund(client refundCreator, req yookassa.CreateRefundRequest, key string) (*yookassa.Refund, refundCallOutcome, error) {
	var (
		ref     *yookassa.Refund
		err     error
		outcome refundCallOutcome
	)
	for try := 1; try <= refundCallAttempts; try++ {
		if try > 1 {
			time.Sleep(refundRetryDelay)
		}
		ref, err = client.CreateRefundWithKey(req, key)
		if outcome = classifyRefundCall(ref, err); outcome != refundUnknown {
			break
		}
		log.Printf("[admin-refund] no answer from YooKassa for %s (try %d/%d): %v", key, try, refundCallAttempts, err)
	}
	if outcome == refundUnknown && err == nil {
		err = errors.New("ЮKassa ещё обрабатывает запрос")
	}
	return ref, outcome, err
}

// executeRefund sends the attempt to YooKassa under the payment row lock and records the answer.
func executeRefund(ctx context.Context, a refundAttempt, client refundCreator) (*yookassa.Refund, error) {
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	var ykID, status string
	var amountKop int64
	if err := tx.QueryRowContext(ctx, refundLockPaymentSQL, a.PaymentID).Scan(&ykID, &status, &amountKop); err != nil {
		return nil, fmt.Errorf("lock payment: %w", err)
	}
	var rowStatus, rowRefundID string
	switch err := tx.QueryRowContext(ctx, refundRowStateSQL, a.RowID).Scan(&rowStatus, &rowRefundID); {
	case errors.Is(err, sql.ErrNoRows), err == nil && (rowStatus != "pending" || rowRefundID != ""):
		return nil, refundRefusal(http.StatusConflict, "Этот возврат уже обработан параллельным запросом — обновите список платежей")
	case err != nil:
		return nil, fmt.Errorf("read reservation: %w", err)
	}

	key := refundIdempotenceKey(a.PaymentID, a.RowID)
	refund, outcome, callErr := callRefund(client, a.request(), key)
	switch outcome {
	case refundCreated:
		if err := linkRefund(ctx, tx, a, refund); err != nil {
			return nil, refundNotRecorded(a, refund, err)
		}
		if err := tx.Commit(); err != nil {
			return nil, refundNotRecorded(a, refund, err)
		}
		return refund, nil
	case refundRejected:
		if _, err := tx.ExecContext(ctx, refundMarkFailedSQL, a.RowID); err != nil {
			return nil, fmt.Errorf("mark refund %d failed: %w", a.RowID, err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("mark refund %d failed: %w", a.RowID, err)
		}
		log.Printf("[admin-refund] YooKassa rejected %s (payment %d, %s): %v", key, a.PaymentID, ykAmount(a.AmountKop), callErr)
		return nil, refundRefusal(http.StatusBadGateway, "ЮKassa отклонила возврат: %v", callErr)
	default:
		log.Printf("[admin-refund] %s unanswered, %s RUB stays reserved (payment %d): %v", key, ykAmount(a.AmountKop), a.PaymentID, callErr)
		return nil, refundRefusal(http.StatusGatewayTimeout,
			"ЮKassa не ответила (%v) — неизвестно, создан ли возврат. %s ₽ зарезервированы: повторите возврат на ту же сумму — повтор безопасен, второй возврат не создастся",
			callErr, rubles(a.AmountKop))
	}
}

func refundNotRecorded(a refundAttempt, refund *yookassa.Refund, err error) error {
	log.Printf("[admin-refund] CRITICAL refund %s for payment %d created in YooKassa but not recorded: %v", refund.ID, a.PaymentID, err)
	return refundRefusal(http.StatusInternalServerError,
		"Возврат %s создан в ЮKassa, но не записан в базу (%v). Сумма остаётся зарезервированной; повтор на ту же сумму безопасен",
		refund.ID, err)
}

// linkRefund ties the reservation to the YooKassa refund. If the refund.succeeded webhook
// recorded that refund first (possible after an unanswered attempt), its row stays and the
// reservation goes, so the refund is counted once.
func linkRefund(ctx context.Context, tx *sql.Tx, a refundAttempt, r *yookassa.Refund) error {
	var existing int64
	switch err := tx.QueryRowContext(ctx, refundByYookassaIDSQL, r.ID).Scan(&existing); {
	case err == nil && existing != a.RowID:
		if _, err := tx.ExecContext(ctx, refundDropReservationSQL, a.RowID); err != nil {
			return fmt.Errorf("drop reservation: %w", err)
		}
	case err == nil || errors.Is(err, sql.ErrNoRows):
		value := r.Amount.Value
		if value == "" {
			value = ykAmount(a.AmountKop)
		}
		if _, err := tx.ExecContext(ctx, refundLinkSQL, a.RowID, r.ID, value, r.Status); err != nil {
			return fmt.Errorf("link reservation: %w", err)
		}
	default:
		return fmt.Errorf("find refund row: %w", err)
	}
	if r.Status == yookassa.RefundStatusSucceeded {
		if _, err := tx.ExecContext(ctx, refundTotalsSQL, a.PaymentID, r.Status); err != nil {
			return fmt.Errorf("update refunded_amount: %w", err)
		}
	}
	return nil
}
