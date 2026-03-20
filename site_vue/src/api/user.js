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
  return jsonOrThrow(res, 'Failed to load connections')
}

export async function getDownloads() {
  const res = await fetch(`${API_BASE}/api/v1/user/downloads`)
  return jsonOrThrow(res, 'Failed to load downloads')
}

export async function createPayment(token, plan, returnUrl, promoCode) {
  const body = { plan, return_url: returnUrl }
  if (promoCode) body.promo_code = promoCode
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
