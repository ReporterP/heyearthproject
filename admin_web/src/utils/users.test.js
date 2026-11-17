import { test } from 'node:test'
import assert from 'node:assert/strict'
import { hasPlan, telegramId, passwordState, resetConsequences } from './users.js'

test('users: plan from current_plan, telegram id as a number (C2)', () => {
  assert.equal(hasPlan({ current_plan: 'Всё включено' }), true)
  assert.equal(hasPlan({ current_plan: 'Нет подписки' }), false)
  assert.equal(hasPlan({ plan: 'old field' }), false)
  assert.equal(telegramId(' 123456789 '), 123456789)
  assert.equal(telegramId('0123'), null)
  assert.equal(telegramId('@nick'), null)
  assert.equal(telegramId(''), null)
  assert.equal(telegramId('99999999999999999999'), null)
})

test('users: password state and the reset dialog', () => {
  assert.deepEqual(passwordState({ has_password: false }), { label: 'не задан', variant: 'default', canReset: false })
  assert.equal(passwordState({ has_password: true }).canReset, true)
  assert.equal(passwordState({ has_password: true, password_must_change: true }).label, 'временный — ждёт смены')
  assert.equal(passwordState(null).canReset, false)
  assert.match(resetConsequences({ has_confirmed_email: true }).at(-1), /письмом на подтверждённую почту/)
  assert.match(resetConsequences({ has_confirmed_email: false }).at(-1), /письма не будет/)
  assert.ok(resetConsequences({}).some((l) => /сессии/.test(l)))
})
