import { apiFetch } from './http.js'

// Wallet API (kopecks in *_kop fields).

export function getWallet(token) {
  return apiFetch('/api/v1/wallet', { token, fallback: 'Не удалось загрузить кошелёк.' })
}

export function getQuote(token, planId, promocode = '') {
  const q = new URLSearchParams({ plan_id: String(planId) })
  if (promocode) q.set('promocode', promocode)
  return apiFetch(`/api/v1/wallet/quote?${q}`, { token, fallback: 'Не удалось рассчитать стоимость.' })
}

export function walletPurchase(token, planId, promocode, requestId) {
  return apiFetch('/api/v1/wallet/purchase', {
    token, method: 'POST',
    body: { plan_id: planId, promocode: promocode || '', request_id: requestId },
    fallback: 'Не удалось оплатить с кошелька.',
  })
}

export function walletTopup(token, { amountRub, purchase, email, confirmationType = 'embedded', returnUrl }) {
  const body = { amount_rub: amountRub, confirmation_type: confirmationType }
  if (purchase) body.purchase = { plan_id: purchase.planId, promocode: purchase.promocode || '' }
  if (email) body.email = email
  if (returnUrl) body.return_url = returnUrl
  return apiFetch('/api/v1/wallet/topup', { token, method: 'POST', body, fallback: 'Не удалось создать платёж.' })
}

export function getTopupStatus(token, paymentId) {
  return apiFetch(`/api/v1/wallet/topup/${paymentId}`, { token, fallback: 'Не удалось проверить платёж.' })
}

// resumeTopup: the widget's token again for a payment still waiting for the client (E7);
// 409 when it cannot go on any more.
export function resumeTopup(token, paymentId) {
  return apiFetch(`/api/v1/wallet/topup/${paymentId}/resume`, { token, method: 'POST', fallback: 'Не удалось продолжить платёж.' })
}

export function newRequestId() {
  if (typeof crypto !== 'undefined' && crypto.randomUUID) return crypto.randomUUID()
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`
}

export function rub(kop) {
  const v = (kop || 0) / 100
  return `${Number.isInteger(v) ? v : v.toFixed(2).replace('.', ',')} ₽`
}

export function formatDay(iso) {
  if (!iso) return ''
  return new Date(iso).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}
