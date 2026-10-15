import { test } from 'node:test'
import assert from 'node:assert/strict'
import { bambooVerdict, ageText } from './bamboo.js'

const deployed = { binary_installed: true, config_present: true, active_plans: 2, last_error: '' }

test('the badge tells the truth about Bamboo', () => {
  assert.equal(bambooVerdict(null).label, 'нет данных')
  assert.equal(bambooVerdict({ ...deployed, config_present: false }).label, 'не развёрнут')
  assert.equal(bambooVerdict({ ...deployed, binary_installed: false }).label, 'не развёрнут')
  assert.equal(bambooVerdict({ ...deployed, active_plans: 0 }).label, 'нет тарифов с Бамбуком')
  assert.equal(bambooVerdict({ ...deployed, probe: { ok: false } }).variant, 'danger')
  assert.equal(bambooVerdict({ ...deployed, probe: { ok: true } }).label, 'работает')
  assert.equal(bambooVerdict({ ...deployed, probe: { ok: true } }).variant, 'success')
  assert.equal(bambooVerdict({ ...deployed, probe: null }).label, 'внешняя проба ещё не прошла')
  assert.equal(bambooVerdict({ ...deployed, probe: { ok: true }, last_error: 'validation failed' }).variant, 'warning')
  // A failed outside probe outranks a sync error: users cannot connect, whatever the cause.
  assert.equal(bambooVerdict({ ...deployed, probe: { ok: false }, last_error: 'validation failed' }).variant, 'danger')
})

test('obfs rotation age', () => {
  const now = Date.parse('2026-10-13T12:00:00Z')
  assert.equal(ageText('', now), 'не было')
  assert.equal(ageText(undefined, now), 'не было')
  assert.equal(ageText('2026-10-10T08:00:00Z', now), '3 дн 4 ч назад')
  assert.equal(ageText('2026-10-13T09:30:00Z', now), '2 ч назад')
  // hysteria2_obfs_rotated_at is written in the Hub's local time with an offset.
  assert.equal(ageText('2026-10-13T13:30:00+03:00', now), '1 ч назад')
  assert.equal(ageText('2026-10-13T11:40:00Z', now), 'меньше часа назад')
  // Unparseable or in the future (clock skew): show the raw value rather than a made-up age.
  assert.equal(ageText('garbage', now), 'garbage')
  assert.equal(ageText('2026-10-14T00:00:00Z', now), '2026-10-14T00:00:00Z')
})
