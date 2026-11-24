import { test } from 'node:test'
import assert from 'node:assert/strict'
import { actionKind, actionLink, confirmView, canRunNow, canSchedule, targetFromKey } from './actionsModel.js'

const registry = [
  { id: 'hub.restart-xray', impact: 'K3', allowed: true },
  { id: 'probes.run', impact: 'K0', allowed: true },
]

test('actionKind: a registered action is a button, another session\'s a link, the rest text', () => {
  assert.equal(actionKind('hub.restart-xray', registry), 'button')
  assert.equal(actionKind('entry.pathcheck', registry), 'link')
  assert.equal(actionKind('exit.reprovision', registry), 'link')
  assert.equal(actionKind('payments.open', registry), 'link')
  assert.equal(actionKind('hub.address', registry), 'link')
  assert.equal(actionKind('something.else', registry), 'text')
  assert.equal(actionLink('entry.pathcheck'), '/entries')
  assert.equal(actionLink('subscription.rollback'), '/operations?tab=subscription')
})

test('confirmView: the class, who is affected, before/after, the window', () => {
  const v = confirmView({ id: 'hub.restart-bamboo', title: 'Перезапустить сервер Бамбука', impact: 'K2', impact_note: 'оборвутся соединения части пользователей',
    affected_now: 7, affected_note: 'в сети на Бамбуке', pre: [{ name: 'x', ok: true }], post: ['прогон синтетики'], if_fails: 'смотрите журнал',
    best_window: '04:00–05:30', confirm_word: '', allowed: true, mode: 'on' })
  assert.equal(v.impactLabel, 'К2')
  assert.equal(v.affected, 'Заденет сейчас: 7 (в сети на Бамбуке)')
  assert.equal(v.window, 'Лучшее окно: 04:00–05:30 МСК')
  assert.equal(v.defaultWhen, 'window')
  assert.equal(v.dryRun, false)
  assert.deepEqual(v.post, ['прогон синтетики'])
})

test('confirmView: an unknown number means everyone; dry-run is said', () => {
  const v = confirmView({ impact: 'K3', affected_now: null, affected_note: 'число неизвестно — считать, что заденет всех', mode: 'dry-run', allowed: true })
  assert.match(v.affected, /считать, что заденет всех/)
  assert.equal(v.dryRun, true)
  const k0 = confirmView({ impact: 'K0', affected_now: 0, affected_note: 'никого', allowed: true })
  assert.equal(k0.defaultWhen, 'now')
})

test('canRunNow: K3 only with the exact word; a failed check before or the rate limit stops it', () => {
  const p = { impact: 'K3', confirm_word: 'XRAY', allowed: true, pre: [], retry_after_sec: 0 }
  assert.equal(canRunNow(p, ''), false)
  assert.equal(canRunNow(p, 'xray'), false)
  assert.equal(canRunNow(p, 'XRAY'), true)
  assert.equal(canRunNow({ ...p, allowed: false }, 'XRAY'), false)
  assert.equal(canRunNow({ ...p, pre: [{ name: 'nginx -t', ok: false }] }, 'XRAY'), false)
  assert.equal(canRunNow({ ...p, retry_after_sec: 120 }, 'XRAY'), false)
  assert.equal(canRunNow({ impact: 'K0', confirm_word: '', allowed: true, pre: [{ ok: true }], retry_after_sec: 0 }, ''), true)
})

test('targetFromKey: the server id of an alert key', () => {
  assert.equal(targetFromKey('exit-down-3'), '3')
  assert.equal(targetFromKey('exit-path-down-12-hy'), '12')
  assert.equal(targetFromKey('relay-5-unstable'), '5')
  assert.equal(targetFromKey('node-reboot-pending:4'), '4')
  assert.equal(targetFromKey('db-unreachable'), '')
})

test('canSchedule: planning a K3 needs the word too (the server asks for it); a failed check before does not stop planning', () => {
  const p = { impact: 'K3', confirm_word: 'XRAY', allowed: true, pre: [{ name: 'x', ok: false }], retry_after_sec: 600 }
  assert.equal(canSchedule(p, ''), false)
  assert.equal(canSchedule(p, 'XRAY'), true)
  assert.equal(canSchedule({ ...p, allowed: false }, 'XRAY'), false)
  assert.equal(canSchedule({ impact: 'K2', confirm_word: '', allowed: true }, ''), true)
})
