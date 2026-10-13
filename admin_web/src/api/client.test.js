import { test } from 'node:test'
import assert from 'node:assert/strict'

globalThis.localStorage = { getItem: () => 'test-token', setItem() {}, removeItem() {} }
const { apiFetch, setLogoutCallback, FORBIDDEN_MESSAGE } = await import('./client.js')
const { login } = await import('./admin.js')

function respond(status, body, contentType = 'application/json') {
  globalThis.fetch = async (url, opts) => {
    respond.last = { url, opts }
    const text = typeof body === 'string' ? body : JSON.stringify(body)
    return new Response(text, { status, headers: { 'content-type': contentType } })
  }
}

function countLogouts() {
  const counter = { n: 0 }
  setLogoutCallback(() => { counter.n++ })
  return counter
}

test('Review Focus 5 → 403 says «Нет прав» and keeps the session', async () => {
  let logouts = 0
  setLogoutCallback(() => { logouts++ })
  respond(403, { error: 'Forbidden' })
  await assert.rejects(apiFetch('/payments/12/cancel', { method: 'POST', body: { amount: 10 } }),
    (e) => e.status === 403 && e.message === FORBIDDEN_MESSAGE)
  assert.equal(logouts, 0)
})

test('a 403 with its own reason keeps the server text (and the session)', async () => {
  const logouts = countLogouts()
  respond(403, { error: 'Можно читать только журналы в /var/log' })
  await assert.rejects(apiFetch('/servers/2/logs?path=/etc/passwd'),
    (e) => e.status === 403 && e.message === 'Можно читать только журналы в /var/log')
  respond(403, { error: 'tenant not in your scope' })
  await assert.rejects(apiFetch('/tenants/7'), (e) => e.status === 403 && e.message === 'tenant not in your scope')
  assert.equal(logouts.n, 0)
})

test('a 403 without a reason (no body, empty error, not JSON) says «Нет прав»', async () => {
  const logouts = countLogouts()
  for (const [body, ct] of [[{}, 'application/json'], [{ error: '' }, 'application/json'], ['Forbidden', 'text/plain']]) {
    respond(403, body, ct)
    await assert.rejects(apiFetch('/payments/12/refund', { method: 'POST' }),
      (e) => e.status === 403 && e.message === FORBIDDEN_MESSAGE, JSON.stringify(body))
  }
  assert.equal(logouts.n, 0)
})

test('401 logs out (raw requests too)', async () => {
  const logouts = countLogouts()
  respond(401, { error: 'Invalid token' })
  await assert.rejects(apiFetch('/overview'), (e) => e.status === 401)
  await assert.rejects(apiFetch('/backup', { raw: true }), (e) => e.status === 401)
  assert.equal(logouts.n, 2)
})

test('a rejected login shows the server reason and never logs out', async () => {
  const logouts = countLogouts()
  respond(401, { error: 'Invalid credentials' })
  await assert.rejects(login('admin', 'wrong'), (e) => e.status === 401 && e.message === 'Invalid credentials')
  assert.equal(respond.last.url, '/api/admin/login')
  respond(403, { error: 'Admin account is inactive' })
  await assert.rejects(login('old', 'pw'), (e) => e.status === 403 && e.message === 'Admin account is inactive')
  assert.equal(logouts.n, 0)
})

test('non-JSON responses: an error page keeps its status, a 200 is rejected', async () => {
  const logouts = countLogouts()
  respond(502, '<html>Bad Gateway</html>', 'text/html')
  await assert.rejects(apiFetch('/overview'), (e) => e.status === 502 && e.message === 'Ошибка 502')
  respond(200, '<html>index</html>', 'text/html')
  await assert.rejects(apiFetch('/overview'), (e) => e.status === 200 && e.message === 'Сервер вернул не JSON')
  respond(200, '{broken', 'application/json')
  await assert.rejects(apiFetch('/overview'), (e) => e.status === 200 && e.message === 'Сервер вернул не JSON')
  assert.equal(logouts.n, 0)
})

test('a network error is an ApiError with status 0 and keeps the session', async () => {
  const logouts = countLogouts()
  const cause = new TypeError('Failed to fetch')
  globalThis.fetch = async () => { throw cause }
  await assert.rejects(apiFetch('/overview'),
    (e) => e.name === 'ApiError' && e.status === 0 && e.message === 'Нет связи с сервером' && e.cause === cause)
  assert.equal(logouts.n, 0)
})

test('other errors keep the server message and status; the token is sent', async () => {
  respond(410, { error: 'Действие удалено' })
  await assert.rejects(apiFetch('/servers/2/actions', { method: 'POST', body: { action: 'sync_tunnel' } }),
    (e) => e.status === 410 && e.message === 'Действие удалено')
  assert.equal(respond.last.url, '/api/admin/servers/2/actions')
  assert.equal(respond.last.opts.headers.Authorization, 'Bearer test-token')
})
