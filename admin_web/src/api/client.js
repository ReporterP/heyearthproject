const API_BASE = '/api/admin'

let logoutCallback = null

export function setLogoutCallback(fn) {
  logoutCallback = fn
}

function getToken() {
  try {
    return localStorage.getItem('admin_token') || ''
  } catch {
    return ''
  }
}

// ApiError carries the HTTP status, so a page can tell «нет прав» from other failures.
export class ApiError extends Error {
  constructor(message, status) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

export const FORBIDDEN_MESSAGE = 'Нет прав на это действие'

export async function apiFetch(path, { method = 'GET', body, headers = {}, raw = false } = {}) {
  const token = getToken()
  const opts = {
    method,
    headers: {
      ...headers,
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  }
  if (body && !(body instanceof FormData)) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  } else if (body) {
    opts.body = body
  }

  const res = await fetch(`${API_BASE}${path}`, opts)

  // 401: the session is gone (expired or revoked) — log out. 403: the session is fine, this
  // admin just lacks the permission — say so and stay logged in.
  if (res.status === 401) {
    if (logoutCallback) logoutCallback()
    throw new ApiError('Сессия истекла — войдите снова', 401)
  }
  if (res.status === 403) {
    throw new ApiError(FORBIDDEN_MESSAGE, 403)
  }

  if (raw) return res

  const ct = res.headers.get('content-type') || ''
  if (!ct.includes('application/json')) {
    throw new ApiError('Сервер вернул не JSON', res.status)
  }

  const data = await res.json()
  if (!res.ok) {
    throw new ApiError(data.error || data.message || `Ошибка ${res.status}`, res.status)
  }
  return data
}

export async function pagedFetch(path, { page = 1, pageSize = 20, search = '', status = '', ...extra } = {}) {
  const params = new URLSearchParams()
  params.set('page', page)
  params.set('page_size', pageSize)
  if (search) params.set('search', search)
  if (status) params.set('status', status)
  // Дополнительные параметры (period и т.д.)
  for (const [k, v] of Object.entries(extra)) {
    if (v !== undefined && v !== null && v !== '') params.set(k, v)
  }
  return apiFetch(`${path}?${params}`)
}
