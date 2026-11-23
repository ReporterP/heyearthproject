import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  pathTone, ageLabel, sortAlerts, impactLabel, groupProbes, feedIcon, actionLabel, levelLabel, buildLine, probeSummary,
} from './opsModel.js'

test('ageLabel: seconds, minutes, hours, days', () => {
  assert.equal(ageLabel(42), '42 с')
  assert.equal(ageLabel(200), '3 м')
  assert.equal(ageLabel(7300), '2 ч')
  assert.equal(ageLabel(450000), '5 д')
  assert.equal(ageLabel(0), '0 с')
  assert.equal(ageLabel(-5), '0 с', 'a clock a little ahead is not a negative age')
  assert.equal(ageLabel(undefined), '—')
})

test('pathTone: the state of a path as a colour role', () => {
  assert.equal(pathTone('ok'), 'ok')
  assert.equal(pathTone('warn'), 'warn')
  assert.equal(pathTone('down'), 'danger')
  assert.equal(pathTone('off'), 'muted')
  assert.equal(pathTone('unknown'), 'muted')
  assert.equal(pathTone('something-new'), 'muted', 'an unknown state never looks healthy')
})

test('sortAlerts: critical first, the ones explained by their cause last, then the newest', () => {
  const alerts = [
    { key: 'warn-old', level: 'warn', since: '2026-11-23T08:00:00Z', inhibited_by: '' },
    { key: 'muted-critical', level: 'critical', since: '2026-11-23T11:00:00Z', inhibited_by: 'all-exits-down' },
    { key: 'critical', level: 'critical', since: '2026-11-23T09:00:00Z', inhibited_by: '' },
    { key: 'warn-new', level: 'warn', since: '2026-11-23T10:00:00Z', inhibited_by: '' },
  ]
  const before = JSON.stringify(alerts)
  assert.deepEqual(sortAlerts(alerts).map(a => a.key), ['critical', 'warn-new', 'warn-old', 'muted-critical'])
  assert.equal(JSON.stringify(alerts), before, 'the input is not reordered in place')
  assert.deepEqual(sortAlerts(undefined), [])
})

test('impactLabel: what an action or an event does to users', () => {
  assert.equal(impactLabel('K0'), 'К0 · никого не заденет')
  assert.equal(impactLabel('K1'), 'К1 · секунды: сайт и новые входы')
  assert.equal(impactLabel('K2'), 'К2 · рвёт соединения части пользователей')
  assert.equal(impactLabel('K3'), 'К3 · рвёт всех')
  assert.equal(impactLabel(''), '')
  assert.equal(impactLabel('K9'), 'K9')
})

test('groupProbes: one row per check, its history newest first', () => {
  const runs = [
    { id: 2, at: '2026-11-23T12:10:00Z', state: 'fail', vantage: 'outside', results: [
      { line: '⚡ Авто', way: 'p-hub-tcp', target: 'bulk', ok: true, ms: 600, bytes: 87533, error: '' },
      { line: '⚡ Авто', way: 'p-r5-tcp', target: 'bulk', ok: false, ms: 20000, bytes: 20000, error: 'stalled after 20000 bytes: timeout' },
      { line: '⚡ Авто', way: '', target: 'blocked', ok: true, ms: 1700, bytes: 4096, error: '' },
    ] },
    { id: 1, at: '2026-11-23T12:00:00Z', state: 'pass', vantage: 'outside', results: [
      { line: '⚡ Авто', way: 'p-hub-tcp', target: 'bulk', ok: true, ms: 580, bytes: 87533, error: '' },
      { line: '⚡ Авто', way: 'p-r5-tcp', target: 'bulk', ok: true, ms: 1000, bytes: 87533, error: '' },
    ] },
    { id: 3, at: '2026-11-23T12:05:00Z', state: 'blind', vantage: 'outside', results: [] },
    { id: 4, at: '2026-11-23T12:07:00Z', state: 'pass', vantage: 'ru-agent:test', results: [
      { line: '⚡ Авто', way: 'p-hub-tcp', target: 'bulk', ok: true, ms: 900, bytes: 87533, error: '' },
    ] },
  ]
  const rows = groupProbes(runs, 'outside')
  assert.deepEqual(rows.map(r => r.key), ['⚡ Авто/p-hub-tcp/bulk', '⚡ Авто/p-r5-tcp/bulk', '⚡ Авто//blocked'])
  const r5 = rows[1]
  assert.equal(r5.line, '⚡ Авто')
  assert.equal(r5.label, 'вход 5 · Берёза')
  assert.equal(r5.history.length, 2, 'two runs of one check are one row with two points')
  assert.deepEqual(r5.history.map(h => h.ok), [false, true], 'newest first')
  assert.equal(r5.last.error, 'stalled after 20000 bytes: timeout')
  assert.equal(r5.fails, 1)
  // a check a run did not make has no point for that run — not a failure
  assert.equal(rows[2].history.length, 1)
  assert.equal(rows[2].label, 'заблокированный сайт')
  assert.deepEqual(groupProbes(undefined, 'outside'), [])
  assert.deepEqual(groupProbes(runs, 'ru-agent:test').map(r => r.key), ['⚡ Авто/p-hub-tcp/bulk'])
})

test('probeSummary: how the last runs went', () => {
  const runs = [{ state: 'pass', vantage: 'outside' }, { state: 'fail', vantage: 'outside' }, { state: 'blind', vantage: 'outside' }, { state: 'pass', vantage: 'ru-agent:x' }]
  assert.deepEqual(probeSummary(runs, 'outside'), { total: 3, pass: 1, fail: 1, blind: 1 })
  assert.deepEqual(probeSummary([], 'outside'), { total: 0, pass: 0, fail: 0, blind: 0 })
})

test('feedIcon: one icon per kind of record', () => {
  assert.equal(feedIcon({ source: 'ops', kind: 'restart' }), 'restart')
  assert.equal(feedIcon({ source: 'ops', kind: 'start' }), 'deploy')
  assert.equal(feedIcon({ source: 'ops', kind: 'boot' }), 'power')
  assert.equal(feedIcon({ source: 'ops', kind: 'auto' }), 'auto')
  assert.equal(feedIcon({ source: 'alert', kind: 'raise' }), 'alert')
  assert.equal(feedIcon({ source: 'alert', kind: 'resolve' }), 'resolved')
  assert.equal(feedIcon({ source: 'alert', kind: 'cleared' }), 'resolved')
  assert.equal(feedIcon({ source: 'audit', kind: 'update_setting' }), 'admin')
  assert.equal(feedIcon({ source: 'new', kind: 'x' }), 'note')
})

test('actionLabel: an action id in words; an unknown one stays as it is', () => {
  assert.equal(actionLabel('entry.pathcheck'), 'Проверить путь входа')
  assert.equal(actionLabel('hub.restart-xray'), 'Перезапустить xray (К3)')
  assert.equal(actionLabel('made.up'), 'made.up')
})

test('levelLabel and buildLine', () => {
  assert.equal(levelLabel('critical'), 'критично')
  assert.equal(levelLabel('warn'), 'внимание')
  assert.equal(buildLine({ sha: 'abc123def456', started_at: '2026-11-20T17:41:00Z' }, ts => `[${ts}]`), 'сборка abc123def456 · запущена [2026-11-20T17:41:00Z]')
  assert.equal(buildLine({ sha: 'dev', started_at: '' }, ts => ts), 'сборка dev')
  assert.equal(buildLine(undefined, ts => ts), '')
})
