package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// Refunds through YooKassa without double refunds, lost refunds or lock waits on HTTP.
//
// Every writer of payment_refunds — the admin refund, the refund webhook, the reconcile
// sweep — starts a SHORT transaction with SELECT … FROM payments … FOR UPDATE and never
// keeps a transaction open across an HTTP call. Writers of one payment therefore
// serialize on that row lock before touching payment_refunds (no unique-index vs FK-check
// deadlock), and nobody waits for YooKassa while holding a lock.
//
// Admin refund (handleAdminPaymentCancel):
//  1. reserveRefund, locked tx: the payment is paid, the amount fits into amount − pending −
//     succeeded refunds, a 'pending' row (the reservation) is inserted with its
//     Idempotence-Key refund-<payment id>-<row id>-<created_at unix>. If a reservation
//     without a YooKassa refund id is still open (in flight or unanswered), the request
//     repeats it when it asks for the same amount, otherwise it gets 409.
//  2. ONE POST /v3/refunds with that key, no transaction open, client timeout 25 s (nginx
//     gives /api/admin/ 60 s).
//  3. recordRefund (or mark 'failed' on a 4xx) in a short locked tx. No answer → 202
//     "pending": the reservation keeps holding the amount until the webhook, a repeat
//     (same key and body → YooKassa returns the same refund) or reconcileRefunds settles it.

// refundAPI is the part of the YooKassa client the refund code needs (a test seam).
type refundAPI interface {
	CreateRefundWithKey(req yookassa.CreateRefundRequest, idempotenceKey string) (*yookassa.Refund, error)
	GetRefund(refundID string) (*yookassa.Refund, error)
	ListRefunds(paymentID string, deadline time.Time) ([]yookassa.Refund, error)
}

var newRefundClient = func() (refundAPI, error) {
	c, err := yookassa.NewClientFromSettings()
	if err != nil {
		return nil, err
	}
	return c.WithTimeout(refundCallTimeout), nil
}

const (
	// refundCallTimeout bounds each YooKassa call of the refund code: one call per admin
	// request keeps the answer inside nginx's 60 s proxy_read_timeout for /api/admin/.
	refundCallTimeout = 25 * time.Second
	// refundDBTimeout bounds each short locked transaction.
	refundDBTimeout = 10 * time.Second
	// refundMaxReasonRunes is YooKassa's limit for a refund description.
	refundMaxReasonRunes = 250
	// refundKeyWindow: YooKassa keeps an Idempotence-Key for 24 h. An open reservation is
	// repeated with its key only inside this window; reconcile gives up on it after it.
	refundKeyWindow = 23 * time.Hour
	// A reservation without a refund id is looked up in GET /v3/refunds only once its call
	// is certainly over (the call times out after 25 s): by the sweep after 10 min, by the
	// admin's «Проверить статус» after 1 min.
	refundListMinAgeSweep = 10 * time.Minute
	refundListMinAgeCheck = time.Minute
	// checkStatusBudget: «Проверить статус» starts no YooKassa call (nor a ListRefunds
	// page) after this; the last one may still take refundCallTimeout.
	checkStatusBudget = 20 * time.Second
	// refundSweepBatch: payments reconciled per sweep run.
	refundSweepBatch = 50
)

// refundPendingMessage is the 202 answer when YooKassa did not answer the refund request.
const refundPendingMessage = "ЮKassa не ответила — нажмите «Проверить статус» через минуту"

// errRefundPending: the refund request got no answer; the amount stays reserved.
var errRefundPending = errors.New("no answer from YooKassa, refund reservation stays pending")

// refundRowColumns is what refundRow scans.
const refundRowColumns = `id, COALESCE(yookassa_refund_id, ''), status, ROUND(amount * 100)::bigint,
	COALESCE(description, ''), COALESCE(idempotence_key, ''), created_at`

const (
	refundLockPaymentSQL = `SELECT COALESCE(yookassa_payment_id, ''), COALESCE(status, ''), ROUND(amount * 100)::bigint
		FROM payments WHERE id = $1 FOR UPDATE`
	refundLockPaymentByYookassaSQL = `SELECT id FROM payments WHERE yookassa_payment_id = $1 FOR UPDATE`
	refundHeldSQL                  = biz.RefundHeldKopSQL
	refundOpenReservationSQL       = `SELECT ` + refundRowColumns + ` FROM payment_refunds
		WHERE payment_id = $1 AND status = 'pending' AND yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL
		  AND created_at > NOW() - INTERVAL '23 hours'
		ORDER BY id LIMIT 1`
	refundReserveSQL = `INSERT INTO payment_refunds (payment_id, amount, currency, status, description)
		VALUES ($1, $2::numeric, 'RUB', 'pending', $3) RETURNING id, EXTRACT(EPOCH FROM created_at)::bigint`
	refundSetKeySQL           = `UPDATE payment_refunds SET idempotence_key = $2 WHERE id = $1`
	refundRowByIDSQL          = `SELECT ` + refundRowColumns + ` FROM payment_refunds WHERE id = $1`
	refundRowByRefundIDSQL    = `SELECT ` + refundRowColumns + ` FROM payment_refunds WHERE yookassa_refund_id = $1`
	refundMatchReservationSQL = `SELECT ` + refundRowColumns + ` FROM payment_refunds
		WHERE payment_id = $1 AND status = 'pending' AND yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL
		  AND ROUND(amount * 100)::bigint = $2
		ORDER BY id LIMIT 1`
	refundPendingRowsSQL = `SELECT ` + refundRowColumns + ` FROM payment_refunds
		WHERE payment_id = $1 AND status = 'pending' ORDER BY id`
	refundInsertRowSQL = `INSERT INTO payment_refunds (payment_id, yookassa_refund_id, amount, currency, status, description, updated_at)
		VALUES ($1, $2, $3::numeric, $4, $5, $6, CURRENT_TIMESTAMP)`
	refundLinkSQL = `UPDATE payment_refunds SET yookassa_refund_id = $2, amount = $3::numeric, status = $4,
		updated_at = CURRENT_TIMESTAMP WHERE id = $1 AND (yookassa_refund_id IS NULL OR yookassa_refund_id = $2)`
	refundSetStatusSQL = `UPDATE payment_refunds SET status = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $1`
	refundUnlinkSQL    = `UPDATE payment_refunds SET yookassa_refund_id = NULL, status = 'pending',
		updated_at = CURRENT_TIMESTAMP WHERE id = $1`
	refundDeleteWebhookRowSQL = `DELETE FROM payment_refunds WHERE id = $1 AND idempotence_key IS NULL`
	refundMarkFailedSQL       = `UPDATE payment_refunds SET status = 'failed', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'pending' AND yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL`
	refundSweepPaymentsSQL = `SELECT DISTINCT payment_id FROM payment_refunds
		WHERE status = 'pending'
		  AND ((yookassa_refund_id IS NOT NULL AND updated_at < NOW() - INTERVAL '3 minutes')
		    OR (yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL AND created_at < NOW() - INTERVAL '10 minutes'))
		ORDER BY payment_id LIMIT $1`
)

// refundTotalsSQL recomputes payments.refunded_amount from the succeeded refunds and marks
// the payment 'refunded' once they cover its amount ($1 = payment id, $2 = status of the
// refund just recorded).
const refundTotalsSQL = `
		UPDATE payments SET
			refunded_amount = COALESCE((SELECT SUM(amount) FROM payment_refunds WHERE payment_id = $1 AND status = 'succeeded'), 0),
			status = CASE WHEN $2 = 'succeeded' AND COALESCE((SELECT SUM(amount) FROM payment_refunds WHERE payment_id = $1 AND status = 'succeeded'), 0) >= amount THEN 'refunded' ELSE status END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`

// refundClearUnappliedSQL: a payment that became fully refunded no longer waits for a
// decision — its "paid but not applied" mark (biz.UnappliedReasonKey) goes in the same
// transaction as the refund that completed it.
const refundClearUnappliedSQL = `UPDATE payments SET metadata = metadata - 'unapplied_reason', updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status = 'refunded' AND metadata ? 'unapplied_reason'`

// ─── Small pure helpers ──────────────────────────────────────────────────

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

// parseYKAmount parses a YooKassa amount ("249.50", "249.5", "249") into kopecks exactly.
func parseYKAmount(s string) (int64, error) {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if whole == "" || len(frac) > 2 {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err := strconv.ParseUint(whole, 10, 40)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	f, err := strconv.ParseUint(frac, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("bad amount %q", s)
	}
	return int64(w)*100 + int64(f), nil
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

// refundIdempotenceKey is the YooKassa Idempotence-Key of a refund attempt: stable for the
// attempt (its reservation row), different for every new attempt — also after a restore
// that reuses SERIAL ids, thanks to the row's creation time.
func refundIdempotenceKey(paymentID int, rowID, createdUnix int64) string {
	return fmt.Sprintf("refund-%d-%d-%d", paymentID, rowID, createdUnix)
}

// refundStatusRank orders refund states so a stale answer never undoes a newer one: a
// refund YooKassa reports beats 'failed', a final state beats 'pending'.
var refundStatusRank = map[string]int{"failed": 0, "pending": 1, "canceled": 2, "succeeded": 3}

func mergeRefundStatus(a, b string) string {
	if refundStatusRank[b] > refundStatusRank[a] {
		return b
	}
	return a
}

// ─── Decision ────────────────────────────────────────────────────────────

// refundRow is a payment_refunds row.
type refundRow struct {
	ID          int64
	RefundID    string // yookassa_refund_id, "" = not known yet
	Status      string
	AmountKop   int64
	Description string
	Key         string // idempotence key; "" = a row the webhook inserted for a refund made elsewhere
	CreatedAt   time.Time
}

type rowScanner interface{ Scan(dest ...any) error }

func scanRefundRow(s rowScanner) (*refundRow, error) {
	var r refundRow
	err := s.Scan(&r.ID, &r.RefundID, &r.Status, &r.AmountKop, &r.Description, &r.Key, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// refundState is what the refund decision reads under the payment row lock.
type refundState struct {
	YookassaID string // payments.yookassa_payment_id
	Status     string // payments.status
	AmountKop  int64  // payments.amount
	HeldKop    int64  // pending + succeeded refunds, open reservations included
	Open       *refundRow
}

// decideRefund returns the kopecks to reserve for a new attempt, or the open reservation to
// repeat, or the refusal. requestedKop 0 means "everything that is left".
func decideRefund(st refundState, requestedKop int64) (int64, *refundRow, *refundError) {
	if st.YookassaID == "" {
		return 0, nil, refundRefusal(http.StatusBadRequest, "У платежа нет идентификатора ЮKassa — вернуть его через ЮKassa нельзя")
	}
	if !biz.IsPaidPaymentStatus(st.Status) {
		return 0, nil, refundRefusal(http.StatusBadRequest, "Вернуть можно только оплаченный платёж (статус платежа: %s)", st.Status)
	}
	if o := st.Open; o != nil {
		if requestedKop == o.AmountKop {
			return 0, o, nil
		}
		return 0, nil, refundRefusal(http.StatusConflict, "Предыдущий возврат по этому платежу ещё обрабатывается — проверьте статус")
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

// ─── Locked transactions ─────────────────────────────────────────────────

// paymentLockTx is a short transaction holding the payment row lock (withPaymentLock,
// processRefundEvent); the refund recorder notes in it what to announce after COMMIT.
type paymentLockTx struct {
	*sql.Tx
	paymentID int
	// unappliedCleared: a refund completed the payment and cleared its "paid but not
	// applied" mark; the alert is resolved once that is committed.
	unappliedCleared bool
}

func (t *paymentLockTx) commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if t.unappliedCleared {
		resolveUnappliedAlert(t.paymentID, fmt.Sprintf("Платёж #%d полностью возвращён — решение больше не требуется.", t.paymentID))
	}
	return nil
}

// withPaymentLock runs fn in a short transaction that first locks the payment row.
func withPaymentLock(ctx context.Context, paymentID int, fn func(context.Context, *paymentLockTx) error) error {
	ctx, cancel := context.WithTimeout(ctx, refundDBTimeout)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	var ykID, status string
	var amountKop int64
	if err := tx.QueryRowContext(ctx, refundLockPaymentSQL, paymentID).Scan(&ykID, &status, &amountKop); err != nil {
		return fmt.Errorf("lock payment %d: %w", paymentID, err)
	}
	ltx := &paymentLockTx{Tx: tx, paymentID: paymentID}
	if err := fn(ctx, ltx); err != nil {
		return err
	}
	return ltx.commit()
}

// refundAttempt is one refund attempt: a reservation row and the YooKassa request it sends.
type refundAttempt struct {
	PaymentID  int
	RowID      int64
	Key        string
	YookassaID string
	AmountKop  int64
	Reason     string
	Resumed    bool // repeating an open reservation
}

func (a refundAttempt) request() yookassa.CreateRefundRequest {
	return yookassa.CreateRefundRequest{
		PaymentID:   a.YookassaID,
		Amount:      yookassa.Amount{Value: ykAmount(a.AmountKop), Currency: "RUB"},
		Description: a.Reason,
	}
}

// reserveRefund checks the payment and reserves the amount in one locked transaction, or
// picks the open reservation this request repeats.
func reserveRefund(ctx context.Context, paymentID int, requestedKop int64, reason string) (refundAttempt, error) {
	ctx, cancel := context.WithTimeout(ctx, refundDBTimeout)
	defer cancel()
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
	if st.Open, err = scanRefundRow(tx.QueryRowContext(ctx, refundOpenReservationSQL, paymentID)); err != nil {
		return refundAttempt{}, fmt.Errorf("find open reservation: %w", err)
	}

	amountKop, resume, refusal := decideRefund(st, requestedKop)
	if refusal != nil {
		return refundAttempt{}, refusal
	}
	var kind, userText string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(metadata->>'kind', ''), COALESCE(user_id_text, '') FROM payments WHERE id = $1`, paymentID).Scan(&kind, &userText); err != nil {
		return refundAttempt{}, fmt.Errorf("payment kind: %w", err)
	}
	a := refundAttempt{PaymentID: paymentID, YookassaID: st.YookassaID}
	if resume != nil {
		// Same key and same body as the first call, or YooKassa would not deduplicate it.
		a.RowID, a.Key, a.AmountKop, a.Reason, a.Resumed = resume.ID, resume.Key, resume.AmountKop, resume.Description, true
	} else {
		var createdUnix int64
		if err := tx.QueryRowContext(ctx, refundReserveSQL, paymentID, ykAmount(amountKop), reason).Scan(&a.RowID, &createdUnix); err != nil {
			return refundAttempt{}, fmt.Errorf("reserve refund: %w", err)
		}
		a.Key, a.AmountKop, a.Reason = refundIdempotenceKey(paymentID, a.RowID, createdUnix), amountKop, reason
		if _, err := tx.ExecContext(ctx, refundSetKeySQL, a.RowID, a.Key); err != nil {
			return refundAttempt{}, fmt.Errorf("store idempotence key: %w", err)
		}
		if kind == biz.PaymentKindWalletTopup {
			// The money of a top-up sits on the wallet: hold the refund amount there first.
			// No more than what is left on the wallet can go back to the card.
			if err := holdWalletRefundTx(ctx, tx, userText, paymentID, a.RowID, amountKop); err != nil {
				return refundAttempt{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return refundAttempt{}, fmt.Errorf("commit reservation: %w", err)
	}
	if kind == biz.PaymentKindWalletTopup && !a.Resumed {
		alertRefundedReferral(paymentID)
	}
	return a, nil
}

// alertRefundedReferral tells the owner when a refunded top-up paid the purchase that
// earned a «Приведи друга» reward: the bonuses stay on the wallets until the owner decides.
func alertRefundedReferral(paymentID int) {
	var rewardID int64
	var refKop, friendKop int64
	if err := db.DB.QueryRow(`
		SELECT rr.id, rr.referrer_kop, rr.friend_kop FROM referral_rewards rr
		JOIN wallet_receipts wr ON wr.wallet_tx_id = rr.trigger_wallet_tx
		WHERE wr.payment_id = $1 AND rr.status = 'paid' LIMIT 1`, paymentID).Scan(&rewardID, &refKop, &friendKop); err != nil {
		return
	}
	RaiseAlert(fmt.Sprintf("referral-refunded-%d", rewardID), AlertWarn, "Возврат платежа, за который выплачен бонус за друга",
		fmt.Sprintf("Платёж #%d возвращён, а покупка на эти деньги дала бонус «Приведи друга» #%d (%s + %s ₽). Бонусы остались на кошельках — при необходимости спишите их в карточке пользователя.",
			paymentID, rewardID, rubles(refKop), rubles(friendKop)))
}

// holdWalletRefundTx debits the top-up's refund amount from the user's wallet (key
// refund-hold:<reservation id>). releaseFailedWalletRefunds gives it back if YooKassa
// refuses or cancels the refund.
func holdWalletRefundTx(ctx context.Context, tx *sql.Tx, userText string, paymentID int, rowID int64, amountKop int64) error {
	var userDBID int
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userText).Scan(&userDBID); err != nil {
		return refundRefusal(http.StatusBadRequest, "Пользователь пополнения не найден — вернуть деньги на кошелёк нельзя")
	}
	_, err := biz.WalletApply(ctx, tx, userDBID, -amountKop, biz.WalletRefund, fmt.Sprintf("refund-hold:%d", rowID),
		biz.WalletRefs{PaymentID: paymentID, Comment: fmt.Sprintf("возврат платежа #%d", paymentID)})
	if errors.Is(err, biz.ErrInsufficientFunds) {
		bal, _ := biz.WalletBalance(ctx, userDBID)
		return refundRefusal(http.StatusBadRequest, "На кошельке клиента осталось %s ₽ — вернуть можно не больше (остальное уже потрачено на услуги)", rubles(bal))
	}
	if err != nil {
		return fmt.Errorf("hold refund on the wallet: %w", err)
	}
	return nil
}

// holdExternalWalletRefundsSQL: refunds of wallet top-ups that YooKassa reported but no
// admin reservation made (a refund from the YooKassa account): settled ones older than
// 15 min (an admin reservation still matching its webhook row is done by then) without a
// wallet hold yet.
const holdExternalWalletRefundsSQL = `
	SELECT r.id, r.payment_id, ROUND(r.amount * 100)::bigint, COALESCE(p.user_id_text, '')
	FROM payment_refunds r JOIN payments p ON p.id = r.payment_id
	WHERE p.metadata->>'kind' = 'wallet_topup'
	  AND r.idempotence_key IS NULL AND r.status = 'succeeded'
	  AND r.created_at < NOW() - INTERVAL '15 minutes'
	  AND NOT EXISTS (SELECT 1 FROM wallet_transactions x WHERE x.idempotency_key = 'refund-hold:' || r.id)
	ORDER BY r.id LIMIT 100`

// holdExternalWalletRefunds takes a top-up refunded from the YooKassa account off the
// wallet: the money went back to the card, so it must not stay spendable. What the client
// already spent cannot be taken: the hold is capped by the balance and the owner is told.
func holdExternalWalletRefunds() {
	rows, err := db.DB.Query(holdExternalWalletRefundsSQL)
	if err != nil {
		log.Printf("[wallet-refund] external select: %v", err)
		return
	}
	type ext struct {
		rowID     int64
		paymentID int
		kop       int64
		user      string
	}
	var list []ext
	for rows.Next() {
		var x ext
		if err := rows.Scan(&x.rowID, &x.paymentID, &x.kop, &x.user); err == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	for _, x := range list {
		ctx, cancel := context.WithTimeout(context.Background(), refundDBTimeout)
		err := func() error {
			tx, err := db.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback() //nolint:errcheck
			var userDBID int
			var balance int64
			if err := tx.QueryRowContext(ctx, `SELECT u.id, COALESCE(w.balance_kop, 0) FROM users u LEFT JOIN user_wallets w ON w.user_id = u.id WHERE u.user_id = $1`, x.user).
				Scan(&userDBID, &balance); err != nil {
				return fmt.Errorf("user %q: %w", x.user, err)
			}
			hold := x.kop
			if hold > balance {
				hold = balance
			}
			if hold > 0 {
				if _, err := biz.WalletApply(ctx, tx, userDBID, -hold, biz.WalletRefund, fmt.Sprintf("refund-hold:%d", x.rowID),
					biz.WalletRefs{PaymentID: x.paymentID, Comment: fmt.Sprintf("возврат платежа #%d через ЮKassa", x.paymentID)}); err != nil {
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
			alertRefundedReferral(x.paymentID)
			if hold < x.kop {
				RaiseAlert(fmt.Sprintf("wallet-external-refund-%d", x.rowID), AlertWarn, "Возврат пополнения больше остатка кошелька",
					fmt.Sprintf("Платёж #%d вернули через ЮKassa на %s ₽, а на кошельке клиента было %s ₽ — остальное уже потрачено на услуги. Списано с кошелька %s ₽.",
						x.paymentID, rubles(x.kop), rubles(balance), rubles(hold)))
			}
			return nil
		}()
		cancel()
		if err != nil {
			log.Printf("[wallet-refund] external refund %d: %v", x.rowID, err)
		}
	}
}

// releaseFailedWalletRefundsSQL: wallet holds whose refund YooKassa refused or canceled
// and that were not given back yet.
const releaseFailedWalletRefundsSQL = `
	SELECT r.id, wt.user_id, -wt.amount_kop, COALESCE(wt.payment_id, 0)
	FROM payment_refunds r
	JOIN wallet_transactions wt ON wt.idempotency_key = 'refund-hold:' || r.id
	WHERE r.status IN ('failed', 'canceled')
	  AND NOT EXISTS (SELECT 1 FROM wallet_transactions x WHERE x.idempotency_key = 'refund-release:' || r.id)
	ORDER BY r.id LIMIT 100`

// releaseFailedWalletRefunds puts the held amount of a refused or canceled top-up refund
// back on the wallet. Idempotent (key refund-release:<reservation id>); run after an
// admin refund and by the payment sync.
func releaseFailedWalletRefunds() {
	rows, err := db.DB.Query(releaseFailedWalletRefundsSQL)
	if err != nil {
		log.Printf("[wallet-refund] select: %v", err)
		return
	}
	type rel struct {
		rowID     int64
		userDBID  int
		kop       int64
		paymentID int
	}
	var list []rel
	for rows.Next() {
		var x rel
		if err := rows.Scan(&x.rowID, &x.userDBID, &x.kop, &x.paymentID); err == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	for _, x := range list {
		ctx, cancel := context.WithTimeout(context.Background(), refundDBTimeout)
		tx, err := db.DB.BeginTx(ctx, nil)
		if err == nil {
			_, err = biz.WalletApply(ctx, tx, x.userDBID, x.kop, biz.WalletRefund, fmt.Sprintf("refund-release:%d", x.rowID),
				biz.WalletRefs{PaymentID: x.paymentID, Comment: fmt.Sprintf("возврат платежа #%d не прошёл — деньги снова на кошельке", x.paymentID)})
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		cancel()
		if err != nil {
			log.Printf("[wallet-refund] release of refund %d: %v", x.rowID, err)
		} else {
			log.Printf("[wallet-refund] refund %d refused: %s ₽ back on the wallet", x.rowID, rubles(x.kop))
		}
	}
}

// ─── YooKassa call ───────────────────────────────────────────────────────

type refundCallOutcome int

const (
	refundUnknown  refundCallOutcome = iota // no answer: the refund may or may not exist
	refundCreated                           // YooKassa returned the refund
	refundRejected                          // YooKassa answered 4xx: nothing was created
)

// classifyRefundCall sorts a YooKassa answer. Only an answer that certainly created nothing
// frees the reservation: a 4xx for a new attempt (except 429), and for a repeat of an
// open reservation only invalid_request — any other 4xx there may hide the first call's
// refund, so it counts as no answer and is left to reconcile.
func classifyRefundCall(ref *yookassa.Refund, err error, repeat bool) refundCallOutcome {
	if err == nil {
		if ref != nil && ref.ID != "" {
			return refundCreated
		}
		return refundUnknown // HTTP 202: YooKassa is still processing this key
	}
	var apiErr *yookassa.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus >= 400 && apiErr.HTTPStatus < 500 {
		switch {
		case apiErr.HTTPStatus == http.StatusTooManyRequests:
			return refundUnknown
		case repeat && apiErr.Code != "invalid_request":
			return refundUnknown
		default:
			return refundRejected
		}
	}
	return refundUnknown // transport error, timeout, 5xx, unreadable answer
}

// executeRefund makes the one YooKassa call of an admin request (no transaction open) and
// records the answer in a short locked transaction.
func executeRefund(ctx context.Context, a refundAttempt, client refundAPI) (*yookassa.Refund, error) {
	ref, callErr := client.CreateRefundWithKey(a.request(), a.Key)
	switch classifyRefundCall(ref, callErr, a.Resumed) {
	case refundCreated:
		err := withPaymentLock(ctx, a.PaymentID, func(ctx context.Context, tx *paymentLockTx) error {
			return recordRefund(ctx, tx, a.PaymentID, a.RowID, ref)
		})
		if err != nil {
			log.Printf("[admin-refund] CRITICAL refund %s (%s) created in YooKassa but not recorded: %v", ref.ID, a.Key, err)
			return nil, refundRefusal(http.StatusInternalServerError,
				"Возврат %s создан в ЮKassa, но не записан в базу (%v). Сумма остаётся зарезервированной — нажмите «Проверить статус»", ref.ID, err)
		}
		return ref, nil
	case refundRejected:
		err := withPaymentLock(ctx, a.PaymentID, func(ctx context.Context, tx *paymentLockTx) error {
			_, err := tx.ExecContext(ctx, refundMarkFailedSQL, a.RowID)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("mark refund %d failed: %w", a.RowID, err)
		}
		log.Printf("[admin-refund] YooKassa rejected %s (%s RUB): %v", a.Key, ykAmount(a.AmountKop), callErr)
		releaseFailedWalletRefunds() // a held top-up refund goes back on the wallet
		return nil, refundRefusal(http.StatusBadGateway, "ЮKassa отклонила возврат: %v", callErr)
	default:
		log.Printf("[admin-refund] %s unanswered, %s RUB stays reserved: %v", a.Key, ykAmount(a.AmountKop), callErr)
		return nil, errRefundPending
	}
}

// ─── Recording a YooKassa refund ─────────────────────────────────────────

// recordRefund records YooKassa refund r of the payment; tx must hold the payment row lock.
//
// reservationID is the reservation whose key created r (admin answer, reconcile match). 0
// means the caller does not know (webhook): r then goes to its own row if one exists, else
// to the oldest pending reservation of the same amount without a refund id, else to a new
// row. The webhook may match by amount wrongly, so with a known reservationID:
//   - r already sits in a row the webhook inserted -> that row is merged into the reservation;
//   - r sits in ANOTHER reservation -> that one goes back to pending (its own call is open);
//   - the reservation holds another refund D -> D moves to a row of its own.
func recordRefund(ctx context.Context, tx *paymentLockTx, paymentID int, reservationID int64, r *yookassa.Refund) error {
	amountKop, err := parseYKAmount(r.Amount.Value)
	if err != nil {
		return fmt.Errorf("refund %s: %w", r.ID, err)
	}
	currency := r.Amount.Currency
	if currency == "" {
		currency = "RUB"
	}
	owner, err := scanRefundRow(tx.QueryRowContext(ctx, refundRowByRefundIDSQL, r.ID))
	if err != nil {
		return fmt.Errorf("find refund %s: %w", r.ID, err)
	}
	status := r.Status

	var target *refundRow
	if reservationID == 0 {
		if owner != nil {
			if _, err := tx.ExecContext(ctx, refundSetStatusSQL, owner.ID, mergeRefundStatus(owner.Status, status)); err != nil {
				return fmt.Errorf("update refund %s: %w", r.ID, err)
			}
			return updateRefundTotals(ctx, tx, paymentID, mergeRefundStatus(owner.Status, status))
		}
		if target, err = scanRefundRow(tx.QueryRowContext(ctx, refundMatchReservationSQL, paymentID, amountKop)); err != nil {
			return fmt.Errorf("match reservation for %s: %w", r.ID, err)
		}
		if target == nil {
			if _, err := tx.ExecContext(ctx, refundInsertRowSQL, paymentID, r.ID, r.Amount.Value, currency, status, r.Description); err != nil {
				return fmt.Errorf("insert refund %s: %w", r.ID, err)
			}
			return updateRefundTotals(ctx, tx, paymentID, status)
		}
	} else {
		if target, err = scanRefundRow(tx.QueryRowContext(ctx, refundRowByIDSQL, reservationID)); err != nil {
			return fmt.Errorf("read reservation %d: %w", reservationID, err)
		}
		if target == nil {
			return fmt.Errorf("reservation %d for refund %s is gone", reservationID, r.ID)
		}
	}

	if owner != nil {
		status = mergeRefundStatus(status, owner.Status)
		if owner.ID != target.ID {
			if owner.Key != "" {
				if _, err := tx.ExecContext(ctx, refundUnlinkSQL, owner.ID); err != nil {
					return fmt.Errorf("give reservation %d back its pending state: %w", owner.ID, err)
				}
			} else if _, err := tx.ExecContext(ctx, refundDeleteWebhookRowSQL, owner.ID); err != nil {
				return fmt.Errorf("merge webhook row %d: %w", owner.ID, err)
			}
		}
	}
	if target.RefundID != "" && target.RefundID != r.ID {
		other := target.RefundID
		if _, err := tx.ExecContext(ctx, refundUnlinkSQL, target.ID); err != nil {
			return fmt.Errorf("unlink reservation %d: %w", target.ID, err)
		}
		if _, err := tx.ExecContext(ctx, refundInsertRowSQL, paymentID, other, ykAmount(target.AmountKop), "RUB", target.Status, ""); err != nil {
			return fmt.Errorf("move refund %s to its own row: %w", other, err)
		}
	} else if target.RefundID == r.ID {
		status = mergeRefundStatus(status, target.Status)
	}
	res, err := tx.ExecContext(ctx, refundLinkSQL, target.ID, r.ID, r.Amount.Value, status)
	if err != nil {
		return fmt.Errorf("link reservation %d to %s: %w", target.ID, r.ID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("link reservation %d to %s: %d rows (%v)", target.ID, r.ID, n, err)
	}
	return updateRefundTotals(ctx, tx, paymentID, status)
}

func updateRefundTotals(ctx context.Context, tx *paymentLockTx, paymentID int, status string) error {
	if _, err := tx.ExecContext(ctx, refundTotalsSQL, paymentID, status); err != nil {
		return fmt.Errorf("update refunded_amount: %w", err)
	}
	res, err := tx.ExecContext(ctx, refundClearUnappliedSQL, paymentID)
	if err != nil {
		return fmt.Errorf("clear unapplied mark: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		tx.unappliedCleared = true
	}
	return nil
}

// processRefundEvent records a verified refund from the webhook under the payment lock. An
// error means it is NOT recorded: the webhook must answer non-200 so YooKassa repeats it.
func processRefundEvent(r *yookassa.Refund) error {
	ctx, cancel := context.WithTimeout(context.Background(), refundDBTimeout)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	var paymentDBID int
	switch err := tx.QueryRowContext(ctx, refundLockPaymentByYookassaSQL, r.PaymentID).Scan(&paymentDBID); {
	case errors.Is(err, sql.ErrNoRows):
		log.Printf("[yookassa-webhook] refund %s: payment %s is not ours, ignored", r.ID, r.PaymentID)
		return nil
	case err != nil:
		return fmt.Errorf("lock payment for refund: %w", err)
	}
	ltx := &paymentLockTx{Tx: tx, paymentID: paymentDBID}
	if err := recordRefund(ctx, ltx, paymentDBID, 0, r); err != nil {
		return err
	}
	if err := ltx.commit(); err != nil {
		return fmt.Errorf("commit refund %s: %w", r.ID, err)
	}
	log.Printf("[yookassa-webhook] ✓ refund %s for payment %s: %s₽ (%s)", r.ID, r.PaymentID, r.Amount.Value, r.Status)
	return nil
}

// refundNotificationStatus verifies and records a refund notification and returns the HTTP
// status for YooKassa: anything but 200 makes it repeat the notification (for 24 h).
func refundNotificationStatus(refundID string, verify func(string) (*yookassa.Refund, error)) int {
	if refundID == "" {
		return http.StatusBadRequest
	}
	refund, err := verify(refundID)
	if err != nil {
		log.Printf("[yookassa-webhook] verify refund %s failed: %v", refundID, err)
		return http.StatusBadGateway
	}
	if err := processRefundEvent(refund); err != nil {
		log.Printf("[yookassa-webhook] refund %s NOT recorded, YooKassa will retry: %v", refundID, err)
		return http.StatusInternalServerError
	}
	return http.StatusOK
}

// ─── Reconcile ───────────────────────────────────────────────────────────

// reconcileRefunds settles the pending payment_refunds rows of one payment with YooKassa:
// rows with a refund id get its current status; reservations without one, older than
// listMinAge, are matched to a refund of the payment that is not recorded yet, has the same
// amount and was created within the key window after the reservation, or marked failed
// once older than 23 h with no such refund. The HTTP calls run before the short locked
// transaction that applies their results; none starts after callBudget (zero = no limit).
func reconcileRefunds(ctx context.Context, client refundAPI, paymentID int, listMinAge time.Duration, callBudget time.Time) error {
	callAllowed := func() bool { return callBudget.IsZero() || time.Now().Before(callBudget) }
	var ykPayment string
	if err := db.DB.QueryRowContext(ctx, `SELECT COALESCE(yookassa_payment_id, '') FROM payments WHERE id = $1`, paymentID).Scan(&ykPayment); err != nil {
		return fmt.Errorf("read payment %d: %w", paymentID, err)
	}
	rows, err := db.DB.QueryContext(ctx, refundPendingRowsSQL, paymentID)
	if err != nil {
		return fmt.Errorf("pending refunds of %d: %w", paymentID, err)
	}
	var pending []refundRow
	for rows.Next() {
		r, err := scanRefundRow(rows)
		if err != nil {
			rows.Close()
			return fmt.Errorf("scan pending refund: %w", err)
		}
		pending = append(pending, *r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("pending refunds of %d: %w", paymentID, err)
	}

	var (
		fetched  []*yookassa.Refund
		unlinked []refundRow
		listed   []yookassa.Refund
		listOK   bool
		callErr  error
	)
	for _, row := range pending {
		switch {
		case row.RefundID != "":
			if !callAllowed() {
				continue
			}
			ref, err := client.GetRefund(row.RefundID)
			if err != nil {
				callErr = fmt.Errorf("get refund %s: %w", row.RefundID, err)
				continue
			}
			fetched = append(fetched, ref)
		case row.Key != "" && time.Since(row.CreatedAt) >= listMinAge:
			unlinked = append(unlinked, row)
		}
	}
	if len(unlinked) > 0 && ykPayment != "" && callAllowed() {
		if listed, err = client.ListRefunds(ykPayment, callBudget); err != nil { // budget checked per page
			callErr = fmt.Errorf("list refunds of %s: %w", ykPayment, err)
		} else {
			listOK = true
		}
	}
	if len(fetched) == 0 && !listOK {
		return callErr
	}

	err = withPaymentLock(ctx, paymentID, func(ctx context.Context, tx *paymentLockTx) error {
		for _, ref := range fetched {
			if err := recordRefund(ctx, tx, paymentID, 0, ref); err != nil {
				return err
			}
		}
		if !listOK {
			return nil
		}
		used := map[string]bool{}
		for _, u := range unlinked {
			cur, err := scanRefundRow(tx.QueryRowContext(ctx, refundRowByIDSQL, u.ID))
			if err != nil {
				return fmt.Errorf("re-read reservation %d: %w", u.ID, err)
			}
			if cur == nil || cur.Status != "pending" || cur.RefundID != "" {
				continue // settled meanwhile (webhook, admin repeat)
			}
			var match *yookassa.Refund
			for i := range listed {
				ref := &listed[i]
				if used[ref.ID] || !refundMatchesReservation(ref, cur) {
					continue
				}
				recorded, err := scanRefundRow(tx.QueryRowContext(ctx, refundRowByRefundIDSQL, ref.ID))
				if err != nil {
					return fmt.Errorf("find refund %s: %w", ref.ID, err)
				}
				if recorded == nil {
					match = ref
					break
				}
			}
			switch {
			case match != nil:
				used[match.ID] = true
				if err := recordRefund(ctx, tx, paymentID, cur.ID, match); err != nil {
					return err
				}
				log.Printf("[refund-reconcile] reservation %d (%s) is refund %s (%s)", cur.ID, cur.Key, match.ID, match.Status)
			case time.Since(cur.CreatedAt) > refundKeyWindow:
				if _, err := tx.ExecContext(ctx, refundMarkFailedSQL, cur.ID); err != nil {
					return fmt.Errorf("mark reservation %d failed: %w", cur.ID, err)
				}
				log.Printf("[refund-reconcile] reservation %d (%s, %s RUB): no refund in YooKassa after 23 h, marked failed",
					cur.ID, cur.Key, ykAmount(cur.AmountKop))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return callErr
}

// refundMatchesReservation: same amount, created from a minute before the reservation
// (clock skew) to the end of its key's 24 h window.
func refundMatchesReservation(ref *yookassa.Refund, row *refundRow) bool {
	kop, err := parseYKAmount(ref.Amount.Value)
	if err != nil || kop != row.AmountKop {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, ref.CreatedAt)
	if err != nil {
		return false
	}
	return !created.Before(row.CreatedAt.Add(-time.Minute)) && created.Before(row.CreatedAt.Add(24*time.Hour))
}

// reconcilePendingRefunds is the periodic sweep (payment sync cron, every 5 min): payments
// with a refund YooKassa reported as pending 3+ min ago, or with a reservation that got no
// refund id within 10 min.
func reconcilePendingRefunds() {
	client, err := newRefundClient()
	if err != nil {
		return // YooKassa not configured
	}
	ctx := context.Background()
	rows, err := db.DB.QueryContext(ctx, refundSweepPaymentsSQL, refundSweepBatch)
	if err != nil {
		log.Printf("[refund-reconcile] select: %v", err)
		return
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := reconcileRefunds(ctx, client, id, refundListMinAgeSweep, time.Time{}); err != nil {
			log.Printf("[refund-reconcile] payment %d: %v", id, err)
		}
	}
}
