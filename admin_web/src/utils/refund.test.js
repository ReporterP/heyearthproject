import { test } from 'node:test'
import assert from 'node:assert/strict'
import { validateRefund, refundableAmount, refundOutcome, closeUnlessSaving, REFUND_CONFIRM_WORD } from './refund.js'

const ok = (amountText, maxAmount = 5000) => validateRefund({ amountText, maxAmount, confirmText: REFUND_CONFIRM_WORD })
const apiError = (message, status) => Object.assign(new Error(message), { status })

test('what is left to refund comes from the server, never from amount − refunded_amount', () => {
  assert.equal(refundableAmount({ amount: 300, refunded_amount: 0, refundable_amount: 150.5 }), 150.5)
  // A pending refund holds money that refunded_amount does not show yet.
  assert.equal(refundableAmount({ amount: 300, refunded_amount: 0, refundable_amount: 0 }), 0)
  // An answer without the field (old API) offers nothing to refund.
  assert.equal(refundableAmount({ amount: 300, refunded_amount: 0 }), 0)
  assert.equal(refundableAmount({ refundable_amount: '249.499' }), 249.5)
  assert.equal(refundableAmount({ refundable_amount: -1 }), 0)
})

test('amounts in rubles', () => {
  for (const [text, want] of [['249', 249], ['249,50', 249.5], ['1 249.5', 1249.5]]) {
    assert.deepEqual(ok(text), { ok: true, amount: want }, text)
  }
  for (const bad of ['', 'abc', '-5', '0', '1,234,5', '10.123']) {
    assert.equal(ok(bad).ok, false, bad)
  }
})

test('never more than is left', () => {
  const r = ok('250', 249.5)
  assert.equal(r.ok, false)
  assert.match(r.error, /не больше/)
})

test('the typed confirmation word is required (any case, trimmed)', () => {
  assert.equal(validateRefund({ amountText: '10', maxAmount: 100, confirmText: 'да' }).ok, false)
  assert.equal(validateRefund({ amountText: '10', maxAmount: 100, confirmText: ' возврат ' }).ok, true)
})

test('200 with refund_id: success, close, reload', () => {
  const o = refundOutcome({ data: { refund_id: 'rf-1', status: 'succeeded', message: 'Возврат создан' } }, 249.5)
  assert.equal(o.close, true)
  assert.equal(o.reload, true)
  assert.equal(o.level, 'success')
  assert.match(o.message, /249,50/)
  assert.match(o.message, /succeeded/)
})

test('202 pending (no refund_id): the server message as a warning, close, reload', () => {
  const msg = 'ЮKassa не ответила — нажмите «Проверить статус» через минуту'
  const o = refundOutcome({ data: { status: 'pending', message: msg } }, 10)
  assert.deepEqual(o, { close: true, reload: true, level: 'warn', message: msg })
  // Never a success toast without a refund id, even if the message is missing.
  const bare = refundOutcome({ data: {} }, 10)
  assert.equal(bare.level, 'warn')
  assert.ok(bare.message)
})

test('400/403/409: the server text stays in the open dialog, no reload', () => {
  for (const status of [400, 403, 404, 409]) {
    const o = refundOutcome({ error: apiError('Предыдущий возврат по этому платежу ещё обрабатывается', status) }, 10)
    assert.deepEqual(o, { close: false, reload: false, error: 'Предыдущий возврат по этому платежу ещё обрабатывается' }, String(status))
  }
})

test('5xx or no connection: the list is reloaded (a reservation may exist) and the error shown', () => {
  for (const status of [500, 502, 503, 504, 0, undefined]) {
    const o = refundOutcome({ error: apiError('Нет связи с сервером', status) }, 10)
    assert.equal(o.close, false, String(status))
    assert.equal(o.reload, true, String(status))
    assert.match(o.error, /Нет связи с сервером/, String(status))
  }
})

test('the dialog cannot be closed while the refund request is in flight', () => {
  let closed = 0
  const onClose = () => { closed++ }
  closeUnlessSaving(true, onClose)()
  assert.equal(closed, 0)
  closeUnlessSaving(false, onClose)()
  assert.equal(closed, 1)
})
