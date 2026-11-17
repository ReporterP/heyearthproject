// One way to call the API from the site. Errors are decided by the HTTP status, never
// by the text of the message:
//   401 → the session is gone: ApiError kind 'auth', and the whole site signs out
//         (AuthContext listens for UNAUTHORIZED_EVENT; RequireAuth sends to /login);
//   503 → kind 'unavailable': a temporary failure, NOT "no subscription" and NOT a
//         sign-out (the API answers 503 when its database hiccups);
//   429 → kind 'rate': too many attempts;
//   network failure / non-JSON → kind 'network'.
// Messages are Russian and say what to do.

const API_BASE = ''

export const UNAUTHORIZED_EVENT = 'hiearth:unauthorized'

export class ApiError extends Error {
  constructor(message, { status = 0, kind = 'error', data = null } = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.kind = kind
    this.data = data
  }
}

// Known English texts some endpoints still return, in Russian.
const RU = {
  'user is blocked': 'Аккаунт заблокирован. Напишите в поддержку.',
  'too many requests': 'Слишком много попыток — подождите минуту и попробуйте снова.',
  'username already taken': 'Это имя уже занято — выберите другое.',
  'invalid current password': 'Неверный текущий пароль.',
  'password required': 'Введите пароль.',
  'user not found': 'Аккаунт не найден. Проверьте ID.',
  'invalid credentials': 'Неверный ID или пароль.',
  'plan not found': 'Тариф не найден — обновите страницу.',
  'downgrade_not_allowed': 'Сейчас у вас тариф дороже — сменить на более дешёвый можно после окончания текущего.',
}

export function translateError(msg) {
  if (!msg) return ''
  const key = String(msg).trim().toLowerCase()
  if (RU[key]) return RU[key]
  if (key.startsWith('username can only contain')) return 'Имя может содержать только латинские буквы, цифры, «_» и «-».'
  if (key.startsWith('password must be at least')) return 'Пароль слишком короткий — минимум 4 символа.'
  return msg
}

const MSG = {
  auth: 'Сессия истекла — войдите снова.',
  unavailable: 'Сервис временно недоступен — попробуйте через минуту.',
  rate: 'Слишком много попыток — подождите минуту и попробуйте снова.',
  network: 'Нет связи с сервером — проверьте интернет и повторите.',
  badResponse: 'Сервер ответил неожиданно — обновите страницу.',
}

/**
 * apiFetch(path, { token, method, body, fallback, signOutOn401 })
 * Returns the parsed JSON body; throws ApiError.
 */
export async function apiFetch(path, { token, method = 'GET', body, form, fallback = 'Что-то пошло не так — попробуйте ещё раз.', signOutOn401 = true, timeoutMs = 20000 } = {}) {
  const headers = {}
  if (body !== undefined && form === undefined) headers['Content-Type'] = 'application/json'
  if (token) headers.Authorization = `Bearer ${token}`

  const ctrl = typeof AbortController !== 'undefined' ? new AbortController() : null
  const timer = ctrl ? setTimeout(() => ctrl.abort(), timeoutMs) : null
  let res
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body: form !== undefined ? form : body !== undefined ? JSON.stringify(body) : undefined, // form: multipart (FormData)
      signal: ctrl ? ctrl.signal : undefined,
    })
  } catch {
    throw new ApiError(MSG.network, { kind: 'network' })
  } finally {
    if (timer) clearTimeout(timer)
  }

  let data = null
  const ct = res.headers.get('content-type') || ''
  if (ct.includes('application/json')) {
    try { data = await res.json() } catch { data = null }
  }

  if (res.status === 401) {
    if (!signOutOn401) {
      // The sign-in form itself: 401 means wrong ID or password, not a lost session.
      throw new ApiError(translateError(data && data.error) || fallback, { status: 401, kind: 'error', data })
    }
    if (token && typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    }
    throw new ApiError(MSG.auth, { status: 401, kind: 'auth', data })
  }
  if (res.status === 503 || res.status === 502 || res.status === 504) {
    throw new ApiError((data && data.error && res.status === 503) ? translateError(data.error) : MSG.unavailable, { status: res.status, kind: 'unavailable', data })
  }
  if (res.status === 429) {
    throw new ApiError(MSG.rate, { status: 429, kind: 'rate', data })
  }
  if (!res.ok) {
    throw new ApiError(translateError(data && data.error) || fallback, { status: res.status, kind: 'error', data })
  }
  if (data === null) {
    throw new ApiError(MSG.badResponse, { status: res.status, kind: 'network' })
  }
  return data
}
