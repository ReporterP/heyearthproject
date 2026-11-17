import { test } from 'node:test'
import assert from 'node:assert/strict'
import { hasPlan, telegramId } from './users.js'

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
