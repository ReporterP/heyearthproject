package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// Paid payments. Whatever reports a YooKassa payment — the webhook, «Проверить статус»,
// the payment sync cron — calls processPaymentEvent,
// and a paid payment is applied exactly once, in ONE transaction:
//
//	BEGIN
//	UPDATE payments SET status = 'succeeded' … WHERE id = $1
//	       AND status NOT IN ('succeeded', 'completed', 'refunded') RETURNING …
//	  no row -> nothing to do (applied before, or refunded)
//	  a row  -> activate the subscription / credit the tenant wallet in this transaction
//	COMMIT
//	after COMMIT, best effort and idempotent: Xray sync, emails, receipts, invoice mark
//
// If the activation fails, everything rolls back: the payment stays unpaid and the next
// report retries. payments.activation_failures counts the failures; the sync cron stops
// retrying after paymentActivationMaxFailures and an alert asks for a manual check.

const (
	paidTxTimeout                = 15 * time.Second
	paymentActivationMaxFailures = 5
)

// errOrphanNotOurs: a YooKassa payment that is not in the database and carries no user
// to recover it for — not ours to apply; the webhook acknowledges it.
var errOrphanNotOurs = errors.New("payment is not in the database and has no user_id to recover it")

const paymentRowColumns = `p.id, COALESCE(p.user_id_text, ''), COALESCE(p.metadata::text, '{}'), COALESCE(p.yookassa_payment_id, '')`

const (
	paymentByIDSQL           = `SELECT ` + paymentRowColumns + ` FROM payments p WHERE p.id = $1`
	paymentByYookassaIDSQL   = `SELECT ` + paymentRowColumns + ` FROM payments p WHERE p.yookassa_payment_id = $1`
	paymentLinkYookassaIDSQL = `UPDATE payments SET yookassa_payment_id = $2, external_id = COALESCE(NULLIF(external_id, ''), $2),
		updated_at = CURRENT_TIMESTAMP WHERE id = $1 AND COALESCE(yookassa_payment_id, '') = ''`
	// paymentDetailsSQL refreshes what YooKassa reports about a payment without touching
	// status or paid_at.
	paymentDetailsSQL = `
		UPDATE payments SET
			yookassa_status = $1::text,
			payment_method_id = COALESCE(NULLIF($2::text, ''), payment_method_id),
			payment_method_type = $3::text,
			payment_method_details = $4::jsonb,
			receipt_registered = $5,
			cancellation_reason = $6::text,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $7`
	// paymentClaimPaidSQL moves a payment to 'succeeded' unless it is already paid or
	// refunded. Run inside the activation transaction: a returned row means THIS
	// transaction made the move and applies the payment; a concurrent duplicate waits for
	// the row lock, re-checks the WHERE on the committed row and gets nothing.
	paymentClaimPaidSQL = `
		UPDATE payments SET
			status = 'succeeded',
			paid_at = COALESCE(paid_at, NOW()),
			confirmed_at = COALESCE(confirmed_at, NOW()),
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status NOT IN (` + biz.PaidPaymentStatusesSQL + `, 'refunded')
		RETURNING activation_failures`
	// paymentSetUnpaidStatusSQL records pending/waiting_for_capture/canceled, never over a
	// paid or refunded payment.
	paymentSetUnpaidStatusSQL = `
		UPDATE payments SET status = $2::text, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND status NOT IN (` + biz.PaidPaymentStatusesSQL + `, 'refunded')`
	paymentActivationFailureSQL = `UPDATE payments SET activation_failures = activation_failures + 1,
		updated_at = CURRENT_TIMESTAMP WHERE id = $1 RETURNING activation_failures`
	// paymentMarkUnappliedSQL stores, in the claim transaction, why a paid payment changed
	// nothing: metadata.unapplied_reason (biz.UnappliedReasonKey).
	paymentMarkUnappliedSQL = `UPDATE payments SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('unapplied_reason', $2::text),
		updated_at = CURRENT_TIMESTAMP WHERE id = $1`
)

// paymentRow is the local payments row an event applies to.
type paymentRow struct {
	ID     int
	UserID string
	// Meta is the YooKassa metadata (what we sent when creating the payment) overlaid
	// with the row's metadata: the webhook can find a row by payment_db_id before the
	// code that created it has written its metadata.
	Meta map[string]any
}

func (r paymentRow) kind() string { s, _ := r.Meta["kind"].(string); return s }

func (r paymentRow) metaInt(key string) int {
	switch v := r.Meta[key].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func scanPaymentRow(s rowScanner, p *yookassa.Payment) (paymentRow, string, error) {
	var row paymentRow
	var meta, ykID string
	if err := s.Scan(&row.ID, &row.UserID, &meta, &ykID); err != nil {
		return paymentRow{}, "", err
	}
	row.Meta = map[string]any{}
	for k, v := range p.Metadata {
		row.Meta[k] = v
	}
	var dbMeta map[string]any
	_ = json.Unmarshal([]byte(meta), &dbMeta)
	for k, v := range dbMeta {
		row.Meta[k] = v
	}
	if row.UserID == "" {
		row.UserID = p.Metadata["user_id"]
	}
	return row, ykID, nil
}

// findPaymentRow finds the row a YooKassa payment belongs to: metadata payment_db_id
// first (every payment we create carries it; it also covers the moment before a cron
// wrote yookassa_payment_id), then yookassa_payment_id, then orphan recovery.
func findPaymentRow(p *yookassa.Payment) (paymentRow, error) {
	if id, err := strconv.Atoi(p.Metadata["payment_db_id"]); err == nil && id > 0 {
		row, ykID, err := scanPaymentRow(db.DB.QueryRow(paymentByIDSQL, id), p)
		switch {
		case err == nil && ykID == p.ID:
			return row, nil
		case err == nil && ykID == "":
			res, lerr := db.DB.Exec(paymentLinkYookassaIDSQL, id, p.ID)
			if lerr == nil {
				if n, _ := res.RowsAffected(); n == 1 {
					return row, nil
				}
			}
			log.Printf("[yookassa] payment %s: could not link row %d (%v), looking it up by YooKassa id", p.ID, id, lerr)
		case err == nil:
			log.Printf("[yookassa] payment %s: metadata payment_db_id=%d belongs to YooKassa payment %s — ignored", p.ID, id, ykID)
		case !errors.Is(err, sql.ErrNoRows):
			return paymentRow{}, fmt.Errorf("find payment %d: %w", id, err)
		}
	}
	row, _, err := scanPaymentRow(db.DB.QueryRow(paymentByYookassaIDSQL, p.ID), p)
	if err == nil {
		return row, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return paymentRow{}, fmt.Errorf("find payment: %w", err)
	}
	log.Printf("[yookassa] ORPHAN payment %s not in DB — attempting recovery from metadata", p.ID)
	newID, err := recoverOrphanPayment(p)
	if err != nil {
		if errors.Is(err, errOrphanNotOurs) {
			return paymentRow{}, err
		}
		return paymentRow{}, fmt.Errorf("orphan recovery failed for %s: %w", p.ID, err)
	}
	row, _, err = scanPaymentRow(db.DB.QueryRow(paymentByIDSQL, newID), p)
	if err != nil {
		return paymentRow{}, fmt.Errorf("read recovered payment %d: %w", newID, err)
	}
	return row, nil
}

// processPaymentEvent records what YooKassa reports about a payment and, if it is paid,
// applies it once (see the top of this file).
func processPaymentEvent(p *yookassa.Payment) error {
	row, err := findPaymentRow(p)
	if err != nil {
		return err
	}

	methodType, methodDetails, paymentMethodID := "", "{}", ""
	if p.PaymentMethod != nil {
		methodType = p.PaymentMethod.Type
		paymentMethodID = p.PaymentMethod.ID
		if p.PaymentMethod.Card != nil {
			methodDetails = mustJSON(p.PaymentMethod.Card)
		} else {
			methodDetails = mustJSON(map[string]string{"type": p.PaymentMethod.Type, "title": p.PaymentMethod.Title})
		}
	}
	if _, err := db.DB.Exec(paymentDetailsSQL, p.Status, paymentMethodID, methodType, methodDetails,
		p.ReceiptRegistration == "succeeded", cancellationReason(p), row.ID); err != nil {
		return fmt.Errorf("update payment: %w", err)
	}

	if p.Status != yookassa.PaymentStatusSucceeded {
		if _, err := db.DB.Exec(paymentSetUnpaidStatusSQL, row.ID, mapYookassaStatus(p.Status)); err != nil {
			return fmt.Errorf("update payment status: %w", err)
		}
		return nil
	}
	return applyPaidPayment(p, row)
}

// applyPaidPayment is the claim + activation transaction.
func applyPaidPayment(p *yookassa.Payment, row paymentRow) error {
	ctx, cancel := context.WithTimeout(context.Background(), paidTxTimeout)
	defer cancel()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	var failures int
	switch err := tx.QueryRowContext(ctx, paymentClaimPaidSQL, row.ID).Scan(&failures); {
	case errors.Is(err, sql.ErrNoRows):
		log.Printf("[yookassa] payment %s (db %d) is already paid or refunded — nothing applied again", p.ID, row.ID)
		return nil
	case err != nil:
		return fmt.Errorf("mark payment %d paid: %w", row.ID, err)
	}
	after, err := paidPaymentEffects(ctx, tx, p, row)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		notePaymentActivationFailure(row, err)
		return fmt.Errorf("apply paid payment %d: %w", row.ID, err)
	}
	if failures >= paymentActivationMaxFailures {
		resolvePaymentAlert(paymentAlertKey(row.ID), fmt.Sprintf("Платёж #%d активирован", row.ID))
	}
	after()
	return nil
}

// Test seams.
var (
	paidPaymentEffects  = applyPaidPaymentEffects
	raisePaymentAlert   = RaiseAlert
	resolvePaymentAlert = ResolveAlert
	// raiseUnappliedAlert: the payment sync cron re-raises payment-unapplied-<id> every run;
	// a delivered alert is not repeated, an undelivered one is retried.
	raiseUnappliedAlert = RaiseAlertNoRepeat
	openUnappliedAlerts = func() []string { return alerts.activeKeys(unappliedAlertPrefix) }
	getPlanByID         = biz.GetPlanByID
	consumePaidPromoTx  = func(ctx context.Context, tx *sql.Tx, userID, code string, paymentID int) error {
		var userDBID int
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
			return err
		}
		return biz.ConsumePaidPromocodeTx(ctx, tx, code, userDBID, biz.RedeemSourcePayment, paymentID)
	}
	activatePlanTx = biz.ActivateSubscriptionWithPlanTx
	creditWalletTx = biz.CreditTenantWalletTx
	// subscriptionActivated runs after COMMIT of an activation: Xray sync, email.
	subscriptionActivated = func(row paymentRow, plan *biz.Plan, pricePaid int, autoRenew bool, amount string) {
		log.Printf("[yookassa] ✓ activated plan '%s' for user %s (%s ₽, payment %d)", plan.Name, row.UserID, amount, row.ID)
		biz.NotifySubscriptionChange()
		RequestXraySync()
		go sendActivationEmail(row.UserID, plan, pricePaid, autoRenew, row.ID)
	}
)

func paymentAlertKey(id int) string { return fmt.Sprintf("payment-activation-failed-%d", id) }

// notePaymentActivationFailure counts a failed activation; from the 5th on the sync cron
// no longer retries the payment and the owner gets an alert.
func notePaymentActivationFailure(row paymentRow, cause error) {
	var n int
	if err := db.DB.QueryRow(paymentActivationFailureSQL, row.ID).Scan(&n); err != nil {
		log.Printf("[yookassa] payment %d: activation failed (%v); counting it failed too: %v", row.ID, cause, err)
		return
	}
	log.Printf("[yookassa] payment %d: activation failed (%d/%d): %v", row.ID, n, paymentActivationMaxFailures, cause)
	if n < paymentActivationMaxFailures {
		return
	}
	user := row.UserID
	if user == "" {
		user = "—"
	}
	raisePaymentAlert(paymentAlertKey(row.ID), AlertCritical, "Оплата не активирована",
		fmt.Sprintf("Платёж #%d (пользователь %s) оплачен в ЮKassa, но активировать его не удалось %d раз подряд: %v. "+
			"Синхронизация больше не повторяет его — проверьте платёж вручную.", row.ID, user, n, cause))
}

// applyPaidPaymentEffects applies a payment that just became paid inside the claim
// transaction and returns what runs after COMMIT.
func applyPaidPaymentEffects(ctx context.Context, tx *sql.Tx, p *yookassa.Payment, row paymentRow) (func(), error) {
	var after []func()
	switch row.kind() {
	case "subscription", "":
		f, err := activatePaidSubscriptionTx(ctx, tx, p, row)
		if err != nil {
			return nil, err
		}
		after = append(after, f)
	case biz.PaymentKindWalletTopup:
		f, err := creditWalletFromPaymentTx(ctx, tx, p, row)
		if err != nil {
			return nil, err
		}
		after = append(after, f)
	case biz.PaymentKindTenantTopup, biz.PaymentKindB2BInvoice:
		f, err := creditTenantFromPaymentTx(ctx, tx, p, row)
		if err != nil {
			return nil, err
		}
		after = append(after, f)
	}
	return func() {
		for _, f := range after {
			f()
		}
	}, nil
}

// Paid but not applied. A paid payment that changes nothing keeps the reason in its
// metadata (biz.UnappliedReasonKey), written in the claim transaction: it commits together
// with the move to 'succeeded' or not at all, so no crash or Telegram outage between COMMIT
// and the alert can lose it. The mark drives payment-unapplied-<id>:
//   - raised after COMMIT, then re-raised by the payment sync cron while the mark is there
//     (an undelivered alert is retried, a delivered one is not repeated);
//   - cleared with the change that settles the payment — the admin attach-tenant credit, a
//     refund that completes the payment — and the alert resolved after that COMMIT; the
//     cron resolves an open alert whose payment lost its mark any other way (admin edit).
const (
	unappliedAlertPrefix = "payment-unapplied-"
	unappliedAlertTitle  = "Оплата получена, но не применена"
)

func unappliedAlertKey(paymentID int) string { return unappliedAlertPrefix + strconv.Itoa(paymentID) }

// unappliedAlertDetails is the alert text, built only from what the payment row keeps, so
// the cron re-raises exactly what the claim raised.
func unappliedAlertDetails(paymentID int, kind, userID string, tenantID int, amount, reason string) string {
	who, unchanged := userLabel(userID), "подписка не изменена"
	if biz.IsTenantPaymentKind(kind) {
		who, unchanged = "компания не указана", "баланс компании не изменён"
		if tenantID != 0 {
			who = fmt.Sprintf("компания #%d", tenantID)
		}
	}
	return fmt.Sprintf("Платёж #%d (%s, %s ₽): %s. Деньги получены, %s — решите вручную: вернуть или применить.",
		paymentID, who, amount, reason, unchanged)
}

// resolveUnappliedAlert closes payment-unapplied-<id> (a no-op when it is not open). Call it
// after the COMMIT that cleared the mark.
func resolveUnappliedAlert(paymentID int, message string) {
	resolvePaymentAlert(unappliedAlertKey(paymentID), message)
}

// paymentUnapplied marks, inside the claim transaction, a paid payment that changed nothing
// and returns what runs after COMMIT: the alert. The money is in; the owner decides by hand
// whether to refund or apply it. A failed mark fails the claim: everything rolls back and
// the next report retries.
func paymentUnapplied(ctx context.Context, tx *sql.Tx, row paymentRow, p *yookassa.Payment, reason string) (func(), error) {
	if _, err := tx.ExecContext(ctx, paymentMarkUnappliedSQL, row.ID, reason); err != nil {
		return nil, fmt.Errorf("mark payment %d unapplied: %w", row.ID, err)
	}
	details := unappliedAlertDetails(row.ID, row.kind(), row.UserID, row.metaInt("tenant_id"), p.Amount.Value, reason)
	return func() {
		log.Printf("[yookassa] payment %d paid but NOT applied: %s", row.ID, details)
		raiseUnappliedAlert(unappliedAlertKey(row.ID), AlertCritical, unappliedAlertTitle, details)
	}, nil
}

func userLabel(userID string) string {
	if userID == "" {
		return "пользователь не указан"
	}
	return "пользователь " + userID
}

// activatePaidSubscriptionTx extends/activates the plan the payment was for.
func activatePaidSubscriptionTx(ctx context.Context, tx *sql.Tx, p *yookassa.Payment, row paymentRow) (func(), error) {
	planID := row.metaInt("plan_id")
	if planID <= 0 || row.UserID == "" {
		return paymentUnapplied(ctx, tx, row, p, "в платеже нет тарифа или пользователя")
	}
	plan, err := getPlanByID(planID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && plan == nil) {
		return paymentUnapplied(ctx, tx, row, p, fmt.Sprintf("тариф #%d больше не существует", planID))
	}
	if err != nil {
		return nil, fmt.Errorf("load plan %d: %w", planID, err)
	}
	// What the subscription row records as the source: the promocode, or the cron.
	label, _ := row.Meta["promocode"].(string)
	autoRenew := row.Meta["auto_renew"] == true || row.Meta["auto_renew"] == "true"
	if label == "" && autoRenew {
		label = "auto-renew"
		if _, retry := row.Meta["retry_id"]; retry {
			label = "auto-renew-retry"
		}
	}
	var pricePaid int
	fmt.Sscanf(p.Amount.Value, "%d", &pricePaid)

	switch err := activatePlanTx(ctx, tx, row.UserID, plan.ID, plan.DurationDays, pricePaid, label); {
	case errors.Is(err, biz.ErrDowngradeNotAllowed):
		return paymentUnapplied(ctx, tx, row, p, fmt.Sprintf("тариф «%s» дешевле текущего — понижение тарифа не применяется", plan.Name))
	case err != nil:
		return nil, fmt.Errorf("activate plan %d: %w", planID, err)
	}
	// The promocode the payment was made with is used now, with the purchase.
	if promo, _ := row.Meta["promocode"].(string); promo != "" {
		if err := consumePaidPromoTx(ctx, tx, row.UserID, promo, row.ID); err != nil {
			return nil, fmt.Errorf("promocode %s: %w", promo, err)
		}
	}
	return func() { subscriptionActivated(row, plan, pricePaid, autoRenew, p.Amount.Value) }, nil
}

// creditTenantFromPaymentTx credits the tenant's wallet for a B2B payment (tenant_topup,
// b2b_invoice). Without tenant_id or with a bad amount the payment stays paid and an
// alert asks for manual reconciliation.
func creditTenantFromPaymentTx(ctx context.Context, tx *sql.Tx, p *yookassa.Payment, row paymentRow) (func(), error) {
	kind := row.kind()
	tenantID := row.metaInt("tenant_id")
	if tenantID == 0 {
		return paymentUnapplied(ctx, tx, row, p, fmt.Sprintf("в платеже %s нет tenant_id (привяжите его к компании)", kind))
	}
	amount, perr := strconv.ParseFloat(p.Amount.Value, 64)
	if perr != nil || amount <= 0 {
		return paymentUnapplied(ctx, tx, row, p, fmt.Sprintf("некорректная сумма %q", p.Amount.Value))
	}
	paymentDBID := row.ID
	res, err := creditWalletTx(ctx, tx, tenantID, amount, fmt.Sprintf("YooKassa %s #%s", kind, p.ID), &paymentDBID)
	if err != nil {
		return nil, fmt.Errorf("credit tenant %d for %s payment %s: %w", tenantID, kind, p.ID, err)
	}
	return func() {
		log.Printf("[yookassa-webhook] ✓ %s tenant_id=%d credited %.2f ₽ (balance=%.2f, activated=%v, resumed=%v, month=%.2f)",
			kind, tenantID, amount, res.NewBalance, res.Activated, res.Resumed, res.Charged)
		if res.AccessChanged {
			biz.NotifySubscriptionChange() // members got the company-paid access (jzd5.23)
		}
		if kind == biz.PaymentKindB2BInvoice {
			// Best effort, idempotent: the invoice row may not exist.
			_, _ = db.DB.Exec(`
				UPDATE invoice_records
				SET status = 'paid', paid_at = NOW(), updated_at = NOW()
				WHERE payment_id = $1 AND status != 'paid'
			`, paymentDBID)
		}
		go sendTenantCreditEmail(tenantID, amount, res, kind, paymentDBID)
	}, nil
}

// paymentNotificationStatus verifies and applies a payment notification and returns the
// HTTP status for YooKassa: 500 when it could not be recorded or applied (YooKassa
// repeats the notification for 24 h), 200 when done or when the payment is not ours.
func paymentNotificationStatus(paymentID string, verify func(string) (*yookassa.Payment, error)) int {
	if paymentID == "" {
		return http.StatusBadRequest
	}
	payment, err := verify(paymentID)
	if err != nil {
		log.Printf("[yookassa-webhook] verify failed for %s: %v", paymentID, err)
		return http.StatusBadGateway
	}
	switch err := processPaymentEvent(payment); {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, errOrphanNotOurs):
		log.Printf("[yookassa-webhook] payment %s ignored: %v", paymentID, err)
		return http.StatusOK
	default:
		log.Printf("[yookassa-webhook] payment %s NOT applied, YooKassa will retry: %v", paymentID, err)
		return http.StatusInternalServerError
	}
}
