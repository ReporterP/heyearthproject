import { test } from 'node:test'
import assert from 'node:assert/strict'
import { validateRefund, refundableAmount, REFUND_CONFIRM_WORD } from './refund.js'

const ok = (amountText, maxAmount = 5000) => validateRefund({ amountText, maxAmount, confirmText: REFUND_CONFIRM_WORD })

test('what is left to refund', () => {
  assert.equal(refundableAmount({ amount: '300', refunded_amount: '50.5' }), 249.5)
  assert.equal(refundableAmount({ amount: 100, refunded_amount: 150 }), 0)
  assert.equal(refundableAmount({ amount: '249' }), 249)
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
