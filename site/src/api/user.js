import { apiFetch } from './http.js'

export async function registerUser(platform = '', deviceId = '') {
  return apiFetch('/api/v1/auth/register', { method: 'POST', body: { platform, device_id: deviceId }, fallback: 'Не удалось создать аккаунт — попробуйте ещё раз.' })
}

export async function loginUser(idOrUsername, password = '') {
  return apiFetch('/api/v1/auth/login', { method: 'POST', body: { id_or_username: idOrUsername, password }, fallback: 'Не удалось войти — попробуйте ещё раз.', signOutOn401: false })
}

export async function getProfile(token) {
  return apiFetch('/api/v1/user/profile', { token, fallback: 'Не удалось загрузить профиль.' })
}

export async function updateProfile(token, { username, newPassword, currentPassword }) {
  return apiFetch('/api/v1/user/profile', {
    token,
    method: 'PATCH',
    body: {
      username: username || undefined,
      new_password: newPassword || undefined,
      current_password: currentPassword || undefined,
    },
    fallback: 'Не удалось сохранить изменения.',
  })
}

export async function getSubscription(token) {
  return apiFetch('/api/v1/user/subscription', { token, fallback: 'Не удалось загрузить подписку.' })
}

export async function getPayments(token) {
  return apiFetch('/api/v1/user/payments', { token, fallback: 'Не удалось загрузить платежи.' })
}

export async function getConnections(token, platform) {
  const q = platform ? `?platform=${encodeURIComponent(platform)}` : ''
  return apiFetch(`/api/v1/user/connections${q}`, { token, fallback: 'Не удалось загрузить подключение.' })
}

// AmneziaWG ("Пальма") per-user config. Heavy (keys + CPS) — fetched lazily,
// only for users whose connections.amnezia_available is true. Returns
// { available, config_text, deeplink, endpoint, ip }.
export async function getAwgConfig(token) {
  return apiFetch('/api/v1/user/awg-config', { token, fallback: 'Не удалось загрузить конфигурацию Пальмы.' })
}

export async function getDownloads() {
  return apiFetch('/api/v1/user/downloads', { fallback: 'Не удалось загрузить ссылки на приложения.' })
}

export async function createPayment(token, plan, returnUrl, planId, promocode, saveCard = false, email = '', confirmationType = 'redirect') {
  const body = { return_url: returnUrl, save_card: saveCard }
  if (planId) body.plan_id = planId
  else if (plan) body.plan = plan
  if (promocode) body.promocode = promocode
  if (email) body.email = email
  if (confirmationType && confirmationType !== 'redirect') body.confirmation_type = confirmationType
  return apiFetch('/api/v1/payment/create', { token, method: 'POST', body, fallback: 'Не удалось создать платёж — попробуйте ещё раз.' })
}

export async function applyPromocode(token, code) {
  return apiFetch('/api/v1/payment/apply-promocode', { token, method: 'POST', body: { code }, fallback: 'Не удалось применить промокод.' })
}

export async function createDonation(token, amount, returnUrl) {
  return apiFetch('/api/v1/payment/donate', { token, method: 'POST', body: { amount, return_url: returnUrl }, fallback: 'Не удалось создать платёж.' })
}

export async function getTrafficStats(token) {
  return apiFetch('/api/v1/user/traffic', { token, fallback: 'Не удалось загрузить статистику трафика.' })
}

export async function getSubscribeLink(token) {
  return apiFetch('/api/v1/user/subscribe-token', { token, fallback: 'Не удалось загрузить ссылку подписки.' })
}

// ── Cards ──
export async function getUserCards(token) {
  return apiFetch('/api/v1/user/cards', { token, fallback: 'Не удалось загрузить карты.' })
}

export async function deleteUserCard(token, cardId) {
  return apiFetch(`/api/v1/user/cards/${cardId}`, { token, method: 'DELETE', fallback: 'Не удалось удалить карту.' })
}

// ── Refund ──
export async function requestRefund(token, paymentId, reason) {
  return apiFetch('/api/v1/payment/refund', { token, method: 'POST', body: { payment_id: paymentId, reason }, fallback: 'Не удалось отправить заявку на возврат.' })
}

// ── Auto-renew ──
export async function toggleAutoRenew(token, enabled) {
  return apiFetch('/api/v1/user/auto-renew', { token, method: 'POST', body: { auto_renew: enabled }, fallback: 'Не удалось изменить автопродление.' })
}

export async function getPaymentDetail(token, paymentId) {
  return apiFetch(`/api/v1/user/payments/${paymentId}`, { token, fallback: 'Не удалось загрузить платёж.' })
}

export async function pauseSubscription(token, months) {
  return apiFetch('/api/v1/user/pause-subscription', { token, method: 'POST', body: { months }, fallback: 'Не удалось приостановить подписку.' })
}

export async function resumeSubscription(token) {
  return apiFetch('/api/v1/user/resume-subscription', { token, method: 'POST', fallback: 'Не удалось возобновить подписку.' })
}

// ── MTProto ──
export async function getMtproto(token) {
  return apiFetch('/api/v1/user/mtproto', { token, fallback: 'Не удалось загрузить прокси для Telegram.' })
}

// ── Donation settings ──
export async function getDonationSettings() {
  try {
    return await apiFetch('/api/v1/app/donation-settings')
  } catch {
    return null
  }
}
