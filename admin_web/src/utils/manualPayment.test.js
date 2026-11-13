import { test } from 'node:test'
import assert from 'node:assert/strict'
import { manualPaymentBody } from './manualPayment.js'

test('manual payment: rubles, a known status, the user on create only (C7)', () => {
  assert.deepEqual(manualPaymentBody({ userDbId: '42', amount: '300,5', status: 'succeeded', notes: ' наличные ' }, 'create'),
    { body: { amount: 300.5, status: 'succeeded', notes: 'наличные', user_db_id: 42 }, error: '' })
  assert.deepEqual(manualPaymentBody({ amount: '250', status: 'canceled', notes: '' }, 'edit'),
    { body: { amount: 250, status: 'canceled', notes: '' }, error: '' })
  assert.match(manualPaymentBody({ userDbId: 'abc', amount: '1', status: 'succeeded' }, 'create').error, /ID пользователя/)
  assert.match(manualPaymentBody({ amount: '0', status: 'succeeded' }, 'edit').error, /Сумма/)
  assert.match(manualPaymentBody({ amount: '10', status: 'refunded' }, 'edit').error, /статус/)
})
