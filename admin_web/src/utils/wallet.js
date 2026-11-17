// Kopecks as «1 234,50 ₽».
export function formatKop(kop) {
  const v = (Number(kop) || 0) / 100
  return v.toLocaleString('ru-RU', { minimumFractionDigits: Number.isInteger(v) ? 0 : 2, maximumFractionDigits: 2 }) + ' ₽'
}

export function newRequestId() {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

// The type of a payment row (payments.metadata.kind) as the payments table shows it.
export function paymentKindLabel(kind) {
  switch (kind) {
    case 'wallet_topup': return 'Пополнение'
    case 'subscription':
    case '':
    case undefined:
    case null: return 'Подписка'
    case 'tenant_topup':
    case 'b2b_invoice': return 'B2B'
    case 'donation': return 'Донат'
    default: return 'Прочее'
  }
}
