const API_BASE = ''

async function jsonOrThrow(res, fallbackMsg) {
  const ct = res.headers.get('content-type') || ''
  if (!ct.includes('application/json')) {
    throw new Error('Сервер вернул некорректный ответ. Попробуйте обновить страницу.')
  }
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || fallbackMsg)
  return data
}

export async function registerUser(platform = '', deviceId = '') {
  const res = await fetch(`${API_BASE}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ platform, device_id: deviceId }),
  })
  return jsonOrThrow(res, 'Registration failed')
}

export async function loginUser(idOrUsername, password = '') {
  const res = await fetch(`${API_BASE}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id_or_username: idOrUsername, password }),
  })
  return jsonOrThrow(res, 'Login failed')
}

export async function getProfile(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/profile`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load profile')
}

export async function updateProfile(token, { username, newPassword, currentPassword }) {
  const res = await fetch(`${API_BASE}/api/v1/user/profile`, {
    method: 'PATCH',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({
      username: username || undefined,
      new_password: newPassword || undefined,
      current_password: currentPassword || undefined,
    }),
  })
  return jsonOrThrow(res, 'Failed to update profile')
}

export async function getSubscription(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/subscription`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load subscription')
}

export async function getPayments(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/payments`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load payments')
}

export async function getConnections(token, platform) {
  const url = new URL(`${API_BASE}/api/v1/user/connections`, window.location.origin)
  if (platform) url.searchParams.set('platform', platform)
  const res = await fetch(url.toString(), {
    headers: { Authorization: `Bearer ${token}` },
  })
  const data = await jsonOrThrow(res, 'Failed to load connections')
  // X-Subscription-Version header is a cheap ETag for "did anything server-side change?".
  // Body also has subscription_version field — they match. Header just saves parsing JSON if you only care about the counter.
  data.__subscriptionVersionHeader = parseInt(res.headers.get('X-Subscription-Version') || '0', 10)
  return data
}

export async function getDownloads() {
  const res = await fetch(`${API_BASE}/api/v1/user/downloads`)
  return jsonOrThrow(res, 'Failed to load downloads')
}

export async function createPayment(token, plan, returnUrl, planId, promocode, saveCard = false, email = '', confirmationType = 'redirect') {
  const body = { return_url: returnUrl, save_card: saveCard }
  if (planId) body.plan_id = planId
  else if (plan) body.plan = plan
  if (promocode) body.promocode = promocode
  if (email) body.email = email
  if (confirmationType && confirmationType !== 'redirect') body.confirmation_type = confirmationType
  const res = await fetch(`${API_BASE}/api/v1/payment/create`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify(body),
  })
  return jsonOrThrow(res, 'Failed to create payment')
}

export async function applyPromocode(token, code) {
  const res = await fetch(`${API_BASE}/api/v1/payment/apply-promocode`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ code }),
  })
  return jsonOrThrow(res, 'Failed to apply promocode')
}

export async function createDonation(token, amount, returnUrl) {
  const res = await fetch(`${API_BASE}/api/v1/payment/donate`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ amount, return_url: returnUrl }),
  })
  return jsonOrThrow(res, 'Failed to create donation')
}

export async function getTrafficStats(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/traffic`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load traffic stats')
}

export async function getSubscribeLink(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/subscribe-token`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load subscribe link')
}

// ── Cards ──
export async function getUserCards(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/cards`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load cards')
}

export async function deleteUserCard(token, cardId) {
  const res = await fetch(`${API_BASE}/api/v1/user/cards/${cardId}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to delete card')
}

// ── Refund ──
export async function requestRefund(token, paymentId, reason) {
  const res = await fetch(`${API_BASE}/api/v1/payment/refund`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ payment_id: paymentId, reason }),
  })
  return jsonOrThrow(res, 'Failed to request refund')
}

// ── Auto-renew ──
export async function toggleAutoRenew(token, enabled) {
  const res = await fetch(`${API_BASE}/api/v1/user/auto-renew`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ auto_renew: enabled }),
  })
  return jsonOrThrow(res, 'Failed to toggle auto-renew')
}

export async function getPaymentDetail(token, paymentId) {
  const res = await fetch(`${API_BASE}/api/v1/user/payments/${paymentId}`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to fetch payment detail')
}

export async function pauseSubscription(token, months) {
  const res = await fetch(`${API_BASE}/api/v1/user/pause-subscription`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ months }),
  })
  return jsonOrThrow(res, 'Failed to pause subscription')
}

export async function resumeSubscription(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/resume-subscription`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to resume subscription')
}

// ── Donation settings ──
export async function getDonationSettings() {
  try {
    const res = await fetch(`${API_BASE}/api/v1/app/donation-settings`)
    if (!res.ok) return null
    const ct = res.headers.get('content-type') || ''
    if (!ct.includes('application/json')) return null
    return await res.json()
  } catch {
    return null
  }
}
