import { test } from 'node:test'
import assert from 'node:assert/strict'

globalThis.localStorage = { getItem: () => 'test-token', setItem() {}, removeItem() {} }
const { apiFetch, setLogoutCallback, FORBIDDEN_MESSAGE } = await import('./client.js')

function respond(status, body) {
  globalThis.fetch = async (url, opts) => {
    respond.last = { url, opts }
    return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })
  }
}

test('Review Focus 5 → 403 says «Нет прав» and keeps the session', async () => {
  let logouts = 0
  setLogoutCallback(() => { logouts++ })
  respond(403, { error: 'Forbidden' })
  await assert.rejects(apiFetch('/payments/12/cancel', { method: 'POST', body: { amount: 10 } }),
    (e) => e.status === 403 && e.message === FORBIDDEN_MESSAGE)
  assert.equal(logouts, 0)
})

test('401 logs out', async () => {
  let logouts = 0
  setLogoutCallback(() => { logouts++ })
  respond(401, { error: 'Invalid token' })
  await assert.rejects(apiFetch('/overview'), (e) => e.status === 401)
  assert.equal(logouts, 1)
})

test('other errors keep the server message and status; the token is sent', async () => {
  respond(410, { error: 'Действие удалено' })
  await assert.rejects(apiFetch('/servers/2/actions', { method: 'POST', body: { action: 'sync_tunnel' } }),
    (e) => e.status === 410 && e.message === 'Действие удалено')
  assert.equal(respond.last.url, '/api/admin/servers/2/actions')
  assert.equal(respond.last.opts.headers.Authorization, 'Bearer test-token')
})
