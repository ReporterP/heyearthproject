package biz

// Refund accounting rules shared by the refund handler (cmd/api) and the admin payments
// list, so «сколько ещё можно вернуть» is computed the same way everywhere.

// PaidPaymentStatusesSQL lists the payments.status values that mean the money was received:
// YooKassa payments are 'succeeded' (processPaymentEvent); 'completed' is the legacy paid
// status. A fully refunded payment becomes 'refunded' and is not refundable.
const PaidPaymentStatusesSQL = `'succeeded', 'completed'`

// RefundHoldStatusesSQL lists the payment_refunds.status values that hold part of the
// payment: refunds already done ('succeeded') and refunds that may still happen ('pending',
// including a reservation whose YooKassa call has not been answered yet). 'canceled'
// (YooKassa canceled the refund) and 'failed' (YooKassa rejected the request) hold nothing.
const RefundHoldStatusesSQL = `'pending', 'succeeded'`

// RefundHeldKopSQL sums, in kopecks, what refunds took or may still take from payment $1
// (RefundHoldStatusesSQL). Run it under the payment row lock: every refund writer takes that
// lock before touching payment_refunds.
const RefundHeldKopSQL = `SELECT COALESCE(SUM(ROUND(amount * 100)), 0)::bigint FROM payment_refunds
		WHERE payment_id = $1 AND status IN (` + RefundHoldStatusesSQL + `)`

// RefundableAmountSQL is the SQL expression (payments aliased as p) for what can still be
// refunded through YooKassa, in rubles with 2 decimals: 0 unless the payment is paid and has
// a YooKassa id, otherwise amount minus every pending or succeeded refund, never below 0.
const RefundableAmountSQL = `CASE WHEN p.status IN (` + PaidPaymentStatusesSQL + `)
		AND COALESCE(p.yookassa_payment_id, '') <> ''
	THEN ROUND(GREATEST(p.amount - COALESCE((
		SELECT SUM(r.amount) FROM payment_refunds r
		WHERE r.payment_id = p.id AND r.status IN (` + RefundHoldStatusesSQL + `)), 0), 0), 2)
	ELSE 0 END`

// IsPaidPaymentStatus reports whether a payments.status value is one of
// PaidPaymentStatusesSQL — the only statuses a refund may be created for.
func IsPaidPaymentStatus(status string) bool {
	return status == "succeeded" || status == "completed"
}
