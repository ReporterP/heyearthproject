import { test } from 'node:test'
import assert from 'node:assert/strict'
import { flagStateLabel, canaryView, diffChecks, bypassAllowed, clientKindsSummary } from './flagsModel.js'

const t0 = Date.parse('2026-11-24T12:00:00Z')
const min = 60000

test('flagStateLabel: the three states and an unknown one', () => {
  assert.equal(flagStateLabel('off'), 'выключено')
  assert.equal(flagStateLabel('test'), 'тест')
  assert.equal(flagStateLabel('on'), 'всем')
  assert.equal(flagStateLabel('6'), '6')
})

test('canaryView: the soak counts down; «Всем» only in ready', () => {
  const soak = canaryView({ phase: 'test', started_at: new Date(t0).toISOString(), phase_at: new Date(t0).toISOString(), verdict: 'идёт выдержка, осталось 20 мин' }, t0 + 10 * min)
  assert.equal(soak.minutesLeft, 20)
  assert.equal(soak.canPromote, false)
  assert.match(soak.phaseLabel, /Выдержка/)
  const ready = canaryView({ phase: 'ready', verdict: 'PASS' }, t0)
  assert.equal(ready.canPromote, true)
  assert.equal(ready.verdictTone, 'success')
})

test('canaryView: a failing soak, a blind soak and a rollback have their tones', () => {
  assert.equal(canaryView({ phase: 'test', verdict: 'FAIL: стало хуже — x', bad_streak: 2 }, t0).verdictTone, 'danger')
  const blind = canaryView({ phase: 'test', verdict: 'проверка слепа: выдержка не засчитывается', blind_streak: 3 }, t0)
  assert.equal(blind.verdictTone, 'warning')
  assert.equal(blind.canPromote, false)
  const rb = canaryView({ phase: 'rolled_back', note: 'откат: стало хуже — x' }, t0)
  assert.equal(rb.verdictTone, 'danger')
  assert.match(rb.phaseLabel, /Откат/)
  const guard = canaryView({ phase: 'guard', phase_at: new Date(t0).toISOString(), minutes_left: 45 }, t0 + 5 * min)
  assert.equal(guard.minutesLeft, 45)
})

test('diffChecks: what broke after the change, and what was broken before', () => {
  const d = diffChecks(['🎋 Бамбук/b-r5/bulk'], ['⚡ Авто/p-r5-tcp/bulk', '⚡ Авто//blocked'])
  assert.deepEqual(d.worse, ['⚡ Авто · вход 5 · Берёза · объект 64 КБ', '⚡ Авто · заблокированный сайт'])
  assert.deepEqual(d.unchanged, ['🎋 Бамбук · вход 5 · Бамбук · объект 64 КБ'])
  assert.deepEqual(diffChecks(null, undefined), { worse: [], unchanged: [] })
})

test('bypassAllowed: a reason of at least 10 characters', () => {
  assert.equal(bypassAllowed('   коротко  '), false)
  assert.equal(bypassAllowed('Instagram не открывается'), true)
  assert.equal(bypassAllowed(undefined), false)
})

test('clientKindsSummary: the last «clients» run per kind', () => {
  const runs = [
    { vantage: 'outside', at: '2026-11-24T12:10:00Z', results: [{ line: '⚡ Авто', target: 'bulk', ok: true }] },
    { vantage: 'clients', at: '2026-11-24T12:00:00Z', results: [
      { line: 'v2rayTun: Берёза', target: 'parse', ok: true }, { line: 'v2rayTun: Дуб', target: 'parse', ok: false, error: 'ядро не принимает' },
      { line: 'v2rayTun: Берёза', target: 'bulk', ok: true }, { line: 'Clash: профиль', target: 'parse', ok: true },
    ] },
    { vantage: 'clients', at: '2026-11-24T11:00:00Z', results: [{ line: 'INCY: Берёза', target: 'parse', ok: true }] },
  ]
  const s = clientKindsSummary(runs)
  assert.deepEqual(s.map(k => [k.name, k.lines, k.accepted, k.bulkOK]), [['Clash', 1, 1, null], ['v2rayTun', 2, 1, true]])
  assert.equal(s[1].at, '2026-11-24T12:00:00Z')
  assert.deepEqual(clientKindsSummary([]), [])
})
