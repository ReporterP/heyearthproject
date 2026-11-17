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
  constructor(message, status, options) {
    super(message, options)
    this.name = 'ApiError'
    this.status = status
    this.data = options?.data ?? null // the error body (e.g. totp_required on the admin login)
  }
}

export const FORBIDDEN_MESSAGE = 'Нет прав на это действие'

// The text RequirePermission (internal/admin/auth.go) sends for a missing permission; other
// 403s (path outside the allowed dirs, tenant out of scope) explain themselves and are kept.
const GENERIC_FORBIDDEN = 'Forbidden'

async function readJSON(res) {
  const ct = res.headers.get('content-type') || ''
  if (!ct.includes('application/json')) return null
  try {
    return await res.json()
  } catch {
    return null
  }
}

function serverMessage(data) {
  return (data && (data.error || data.message)) || ''
}

// sessionErrors: false — 401/403 are ordinary errors with the server's text and never log out
// (the login request: a wrong password is not an expired session).
export async function apiFetch(path, { method = 'GET', body, headers = {}, raw = false, sessionErrors = true } = {}) {
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

  let res
  try {
    res = await fetch(`${API_BASE}${path}`, opts)
  } catch (err) {
    throw new ApiError('Нет связи с сервером', 0, { cause: err })
  }

  if (sessionErrors) {
    // 401: the session is gone (expired or revoked) — log out. 403: the session is fine, this
    // admin just lacks the permission — say so and stay logged in.
    if (res.status === 401) {
      if (logoutCallback) logoutCallback()
      throw new ApiError('Сессия истекла — войдите снова', 401)
    }
    if (res.status === 403) {
      const msg = serverMessage(await readJSON(res))
      throw new ApiError(msg && msg !== GENERIC_FORBIDDEN ? msg : FORBIDDEN_MESSAGE, 403)
    }
  }

  if (raw) return res

  const data = await readJSON(res)
  if (!res.ok) {
    throw new ApiError(serverMessage(data) || `Ошибка ${res.status}`, res.status, { data })
  }
  if (data === null) {
    throw new ApiError('Сервер вернул не JSON', res.status)
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
