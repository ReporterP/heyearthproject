import { test } from 'node:test'
import assert from 'node:assert/strict'
import { promoState } from './promos.js'

const now = new Date('2026-11-18T12:00:00Z')

test('an expired code is not shown as active', () => {
  assert.equal(promoState({ is_active: true, expires_at: '2026-01-01T00:00:00Z' }, now), 'expired')
  assert.equal(promoState({ is_active: true, expires_at: '2026-12-31T23:59:59Z' }, now), 'active')
  assert.equal(promoState({ is_active: true, expires_at: '' }, now), 'active')
  assert.equal(promoState({ is_active: false, expires_at: '2026-01-01T00:00:00Z' }, now), 'off')
  assert.equal(promoState({ is_active: true, max_uses: 5, current_uses: 5 }, now), 'used_up')
})
