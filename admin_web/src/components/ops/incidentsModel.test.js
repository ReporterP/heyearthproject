import { test } from 'node:test'
import assert from 'node:assert/strict'
import { incidentView } from './incidentsModel.js'

const t0 = Date.parse('2026-11-20T07:00:00Z') // 10:00 MSK
const min = 60000

test('incidentView: a closed incident shows its span and duration in Moscow time', () => {
  const v = incidentView({ id: t0 / 1000, from: new Date(t0).toISOString(), to: new Date(t0 + 50 * min).toISOString(), level: 'critical', keys: ['a', 'b'] }, t0 + 5 * 60 * min)
  assert.equal(v.when, '20.11 10:00–10:50 МСК')
  assert.equal(v.duration, '50 мин')
  assert.equal(v.levelLabel, 'критичная')
  assert.equal(v.tone, 'danger')
  assert.equal(v.open, false)
  assert.equal(v.filename, 'incident-2026-11-20-1000.md')
})

test('incidentView: an open one lasts until now; a warning has its tone', () => {
  const v = incidentView({ id: 1, from: new Date(t0).toISOString(), to: '0001-01-01T00:00:00Z', level: 'warn' }, t0 + 80 * min)
  assert.equal(v.open, true)
  assert.equal(v.duration, 'идёт 1 ч 20 мин')
  assert.equal(v.when, '20.11 10:00 МСК — сейчас')
  assert.equal(v.tone, 'warning')
  assert.equal(v.levelLabel, 'внимание')
})
