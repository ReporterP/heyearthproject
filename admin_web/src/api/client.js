const API_BASE = '/api/admin'

let logoutCallback = null

export function setLogoutCallback(fn) {
  logoutCallback = fn
}

function getToken() {
  return localStorage.getItem('admin_token') || ''
}

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

  if (res.status === 401 || res.status === 403) {
    if (logoutCallback) logoutCallback()
    throw new Error('Session expired')
  }

  if (raw) return res

  const ct = res.headers.get('content-type') || ''
  if (!ct.includes('application/json')) {
    throw new Error('Server returned non-JSON response')
  }

  const data = await res.json()
  if (!res.ok) {
    throw new Error(data.error || data.message || `Error ${res.status}`)
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
