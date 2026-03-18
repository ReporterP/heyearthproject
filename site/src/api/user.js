const API_BASE = import.meta.env.VITE_API_BASE || ''

export async function registerUser(platform = '', deviceId = '') {
  const res = await fetch(`${API_BASE}/api/v1/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ platform, device_id: deviceId }),
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Registration failed')
  return res.json()
}

export async function loginUser(idOrUsername, password = '') {
  const res = await fetch(`${API_BASE}/api/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id_or_username: idOrUsername, password }),
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Login failed')
  return res.json()
}

export async function getProfile(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/profile`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to load profile')
  return res.json()
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
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to update profile')
  return res.json()
}

export async function getSubscription(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/subscription`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to load subscription')
  return res.json()
}

export async function getPayments(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/payments`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to load payments')
  return res.json()
}

export async function getConnections(token, platform) {
  const url = new URL(`${API_BASE}/api/v1/user/connections`, window.location.origin)
  if (platform) url.searchParams.set('platform', platform)
  const res = await fetch(url.toString(), {
    headers: { Authorization: `Bearer ${token}` },
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to load connections')
  return res.json()
}

export async function getDownloads() {
  const res = await fetch(`${API_BASE}/api/v1/user/downloads`)
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to load downloads')
  return res.json()
}

export async function createPayment(token, plan, returnUrl) {
  const res = await fetch(`${API_BASE}/api/v1/payment/create`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    },
    body: JSON.stringify({ plan, return_url: returnUrl }),
  })
  if (!res.ok) throw new Error((await res.json()).error || 'Failed to create payment')
  return res.json()
}
