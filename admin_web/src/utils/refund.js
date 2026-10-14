// Refund form rules, shared by RefundDialog and its node tests (no React here).
// The backend (POST /payments/{id}/cancel) takes the amount in rubles and sends it to
// YooKassa with two decimals, so the form accepts at most two decimals too.
export const REFUND_CONFIRM_WORD = 'ВОЗВРАТ'

const round2 = (n) => Math.round(n * 100) / 100

export function formatRub(n) {
  return `${Number(n).toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} ₽`
}

// refundableAmount is what is left to refund of a payment, as the server counts it
// (refundable_amount of GET /payments: 0 unless paid through YooKassa, pending refunds held).
// It is never recomputed from amount and refunded_amount here: those miss pending refunds.
export function refundableAmount(p) {
  const n = Number(p?.refundable_amount)
  return Number.isFinite(n) ? Math.max(0, round2(n)) : 0
}

// validateRefund checks the typed amount (rubles: "249", "249,50", "1 249.5") against what is
// refundable, and the typed confirmation word.
export function validateRefund({ amountText, maxAmount, confirmText }) {
  const s = String(amountText ?? '').replace(/\s/g, '').replace(',', '.')
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return { ok: false, error: 'Введите сумму в рублях, например 249 или 249,50' }
  const amount = round2(Number(s))
  if (amount <= 0) return { ok: false, error: 'Сумма должна быть больше нуля' }
  if (amount > round2(maxAmount)) return { ok: false, error: `Можно вернуть не больше ${formatRub(maxAmount)}` }
  if (String(confirmText ?? '').trim().toUpperCase() !== REFUND_CONFIRM_WORD) {
    return { ok: false, error: `Для подтверждения введите слово ${REFUND_CONFIRM_WORD}` }
  }
  return { ok: true, amount }
}

const PENDING_FALLBACK = 'ЮKassa пока не подтвердила возврат — нажмите «Статус» через минуту'

// refundOutcome turns the answer of POST /payments/{id}/cancel into what the dialog does.
// answer is { data } for a 2xx, { error } (ApiError: status, 0 = no connection) otherwise.
//   200 with refund_id    → success toast, close, reload.
//   2xx without refund_id → the 202 "pending" answer: its message as a warning, close, reload.
//   5xx / no connection   → the error stays in the dialog and the list is reloaded: the
//                           amount may be reserved already, so what is refundable changes.
//   other 4xx (400, 409…) → the server text stays in the dialog, nothing to reload.
export function refundOutcome({ data, error }, amount) {
  if (!error) {
    if (data?.refund_id) {
      return { close: true, reload: true, level: 'success', message: `Возврат ${formatRub(amount)} создан, статус ЮKassa: ${data.status || 'pending'}` }
    }
    return { close: true, reload: true, level: 'warn', message: data?.message || PENDING_FALLBACK }
  }
  const status = error.status ?? 0
  if (status === 0 || status >= 500) return { close: false, reload: true, error: error.message }
  return { close: false, reload: false, error: error.message }
}

// closeUnlessSaving is the dialog's onClose: Esc, the backdrop and «Отмена» do nothing while
// the refund request is in flight.
export function closeUnlessSaving(saving, onClose) {
  return saving ? () => {} : onClose
}
