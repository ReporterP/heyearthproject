// API client for B2B self-service endpoints (Phase P1).
// All functions require a logged-in user JWT (passed in Authorization header).

const API_BASE = ''

async function jsonOrThrow(res, fallback) {
  const ct = res.headers.get('content-type') || ''
  if (!ct.includes('application/json')) {
    throw new Error('Сервер вернул некорректный ответ. Попробуйте обновить страницу.')
  }
  const data = await res.json()
  if (!res.ok) throw new Error(data.error || fallback)
  return data
}

// GET /api/v1/business/me — current user's tenant info + members + transactions.
// Throws 404 if user has no membership (not a B2B account).
// Optional opts.tenantId picks a specific tenant when user belongs to multiple.
export async function getBusinessMe(token, opts = {}) {
  const url = opts.tenantId ? `${API_BASE}/api/v1/business/me?tenant_id=${opts.tenantId}` : `${API_BASE}/api/v1/business/me`
  const res = await fetch(url, { headers: { Authorization: `Bearer ${token}` } })
  return jsonOrThrow(res, 'Failed to load tenant')
}

// POST /api/v1/business/topup-init — opens a YooKassa payment for wallet topup.
// Returns { confirmation_url, payment_id, amount, tenant_id } — caller redirects
// the user to confirmation_url.
export async function initBusinessTopup(token, { amount, returnUrl, email }) {
  const res = await fetch(`${API_BASE}/api/v1/business/topup-init`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ amount, return_url: returnUrl, email }),
  })
  return jsonOrThrow(res, 'Failed to init topup')
}

// POST /api/v1/business/invite — creates a single-use claim code.
// Returns { code, claim_url, role, expires_at }.
export async function createBusinessInvite(token, { role = 'user' } = {}) {
  const res = await fetch(`${API_BASE}/api/v1/business/invite`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ role }),
  })
  return jsonOrThrow(res, 'Failed to create invite')
}

// DELETE /api/v1/business/members/{user_db_id} — removes a member (admin only).
// Server refuses to remove the last admin (returns 409).
export async function removeBusinessMember(token, userDbId) {
  const res = await fetch(`${API_BASE}/api/v1/business/members/${userDbId}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to remove member')
}
