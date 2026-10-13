// Refund form rules, shared by RefundDialog and its node tests (no React here).
// The backend (POST /payments/{id}/cancel) takes the amount in rubles and sends it to
// YooKassa with two decimals, so the form accepts at most two decimals too.
export const REFUND_CONFIRM_WORD = 'ВОЗВРАТ'

const round2 = (n) => Math.round(n * 100) / 100

export function formatRub(n) {
  return `${Number(n).toLocaleString('ru-RU', { minimumFractionDigits: 2, maximumFractionDigits: 2 })} ₽`
}

// refundableAmount is what is left to refund of a payment.
export function refundableAmount(p) {
  return Math.max(0, round2((parseFloat(p.amount) || 0) - (parseFloat(p.refunded_amount) || 0)))
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
