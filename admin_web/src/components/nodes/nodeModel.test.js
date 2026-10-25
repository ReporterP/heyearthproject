import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  nodeState, pathView, wizardCanAdvance, defaultPlanIds, canResume, jobFinished,
  pathsStale, keyTypeLabel, hostKeyFile, unconfirmedKeys, nodeHref,
} from './nodeModel.js'

const base = { lifecycle_state: 'online', tunnel_status: 'healthy', tunnel_fallback_enabled: true,
  host_key: { confirmed: true }, paths: { hy: { status: 'ok' }, tcp: { status: 'ok' } }, last_job: null }

test('online with both paths is «Работает»', () => {
  assert.deepEqual(nodeState(base), { label: 'Работает', variant: 'success' })
})
test('UDP path down, TCP alive: working via the fallback, not «Работает»', () => {
  const s = nodeState({ ...base, paths: { hy: { status: 'failed' }, tcp: { status: 'ok' } } })
  assert.equal(s.label, 'Работает по запасному TCP')
  assert.equal(s.variant, 'warning')
})
test('exit unhealthy for the monitor is «Выход недоступен»', () => {
  assert.equal(nodeState({ ...base, tunnel_status: 'unhealthy' }).variant, 'danger')
})
test('jobs and keys win over the stored lifecycle', () => {
  assert.equal(nodeState({ ...base, last_job: { status: 'running', kind: 'provision' } }).label, 'Настраивается…')
  assert.equal(nodeState({ ...base, last_job: { status: 'interrupted', kind: 'provision' } }).variant, 'warning')
  assert.equal(nodeState({ ...base, host_key: { confirmed: false } }).label, 'Подтвердите ключ')
  assert.equal(nodeState({ ...base, lifecycle_state: 'pending' }).label, 'Ждёт настройки')
})
test('paths', () => {
  assert.equal(pathView({ status: 'done' }).label, 'работает')
  assert.equal(pathView({ status: 'drift' }).variant, 'danger')
  assert.equal(pathView(undefined).label, 'не проверялся')
  assert.equal(pathView({ status: 'ok' }, { enabled: false }).label, 'выключен')
  // the live tunnel monitor of phase 1 (GET /nodes overlays biz.GetExitPaths): wording of utils/serverState.js
  assert.equal(pathView({ status: 'healthy' }).variant, 'success')
  assert.equal(pathView({ status: 'unhealthy' }).label, 'не работает')
  assert.equal(pathView({ status: 'absent' }).label, 'нет')
})
test('the wizard never skips an unfinished step', () => {
  assert.equal(wizardCanAdvance(0, { role: 'exit', country: 'DE' }), true)
  assert.equal(wizardCanAdvance(0, { role: 'relay', country: 'RU' }), false)
  const fp = 'SHA256:' + 'A'.repeat(43)
  assert.equal(wizardCanAdvance(1, { serverId: 3, fingerprint: fp, fingerprintConfirmed: false }), false)
  assert.equal(wizardCanAdvance(1, { serverId: 3, fingerprint: fp, fingerprintConfirmed: true }), true)
  assert.equal(wizardCanAdvance(2, { preflight: { ok: false } }), false)
  assert.equal(wizardCanAdvance(3, { loadWeight: 11 }), false)
})
test('defaults and resume', () => {
  assert.deepEqual(defaultPlanIds([{ id: 1, is_active: true, price_rub: 0 }, { id: 2, is_active: true, price_rub: 250 }, { id: 3, is_active: false, price_rub: 300 }]), [2])
  assert.equal(canResume({ status: 'interrupted' }), true)
  assert.equal(canResume({ status: 'succeeded' }), false)
})

// ── Beyond the plan's cases: the same rule «never «Работает» when it does not» ──

test('a job path check that skipped the main tunnel is a warning, and the exit works over TCP', () => {
  const skipped = { status: 'skipped', source: 'job' }
  assert.equal(pathView(skipped).variant, 'warning')
  assert.equal(pathView(skipped).label, 'не отвечает')
  assert.match(pathView(skipped).hint, /основной путь не отвечает/i)
  assert.equal(nodeState({ ...base, paths: { hy: skipped, tcp: { status: 'ok', source: 'job' } } }).label, 'Работает по запасному TCP')
})
test('the live monitor: UDP dead, TCP alive is the fallback; a disabled TCP path is absent, not dead', () => {
  const s = nodeState({ ...base, paths: { hy: { status: 'unhealthy' }, tcp: { status: 'healthy' } } })
  assert.equal(s.label, 'Работает по запасному TCP')
  const off = pathView({ status: 'absent' }, { enabled: false })
  assert.equal(off.variant, 'default')
  assert.match(off.hint, /запасной TCP-путь отключён/i)
})
test('a dead exit is never hidden behind the key reminder', () => {
  assert.equal(nodeState({ ...base, tunnel_status: 'unhealthy', host_key: { confirmed: false } }).label, 'Выход недоступен')
})
test('the key reminder says what it blocks and that traffic is not stopped', () => {
  const s = nodeState({ ...base, host_key: { confirmed: false } })
  assert.equal(s.variant, 'warning')
  assert.match(s.hint, /SSH/)
})
test('an online exit the monitor has not judged yet carries no traffic: not «Работает»', () => {
  const s = nodeState({ ...base, tunnel_status: 'unknown' })
  assert.notEqual(s.label, 'Работает')
  assert.notEqual(s.variant, 'success')
})
test('relays have no exit tunnel: their tunnel_status is not read', () => {
  assert.equal(nodeState({ ...base, role: 'relay', tunnel_status: 'unknown', paths: { hy: {}, tcp: {} } }).label, 'Работает')
})
test('an unknown or missing lifecycle state is shown as it is, never as «Работает»', () => {
  assert.match(nodeState({ ...base, lifecycle_state: 'weird' }).label, /weird/)
  assert.notEqual(nodeState({ ...base, lifecycle_state: undefined }).label, 'Работает')
  assert.equal(nodeState({ ...base, lifecycle_state: 'constructor' }).variant, 'warning')
})
test('an interrupted job is named by its kind', () => {
  assert.equal(nodeState({ ...base, last_job: { status: 'interrupted', kind: 'verify' } }).label, 'Проверка прервана')
  assert.equal(nodeState({ ...base, last_job: { status: 'interrupted', kind: 'deprovision' } }).label, 'Отключение прервано')
  assert.equal(nodeState({ ...base, last_job: { status: 'running', kind: 'deprovision' } }).label, 'Отключается…')
})
test('object built-ins are not path statuses; shared views are not handed out', () => {
  assert.equal(pathView({ status: 'constructor' }).label, 'не проверялся')
  pathView({ status: 'healthy' }).label = 'broken'
  assert.equal(pathView({ status: 'healthy' }).label, 'работает')
})
test('jobFinished', () => {
  assert.equal(jobFinished({ status: 'running' }), false)
  assert.equal(jobFinished({ status: 'failed' }), true)
  assert.equal(jobFinished(null), false)
})
test('paths of a switched-off node are stale', () => {
  assert.equal(pathsStale({ lifecycle_state: 'offline' }), true)
  assert.equal(pathsStale({ lifecycle_state: 'online' }), false)
  assert.equal(pathsStale({ lifecycle_state: 'failed' }), false)
})
test('host key types read as owners see them in the provider console', () => {
  assert.equal(keyTypeLabel('ecdsa-sha2-nistp256'), 'ECDSA')
  assert.equal(keyTypeLabel('ssh-ed25519'), 'Ed25519')
  assert.equal(keyTypeLabel('ssh-rsa'), 'RSA')
  assert.equal(keyTypeLabel(''), '')
  assert.equal(hostKeyFile('ecdsa-sha2-nistp256'), '/etc/ssh/ssh_host_ecdsa_key.pub')
  assert.equal(hostKeyFile('ssh-ed25519'), '/etc/ssh/ssh_host_ed25519_key.pub')
  assert.equal(hostKeyFile(''), '')
})
test('unconfirmedKeys lists every node whose host key the owner has not confirmed', () => {
  const items = [{ id: 1, host_key: { confirmed: true } }, { id: 2, host_key: { confirmed: false } }, { id: 3 }]
  assert.deepEqual(unconfirmedKeys(items).map((n) => n.id), [2, 3])
  assert.deepEqual(unconfirmedKeys(null), [])
})

// ── Review of 3b80d88 ──

test('main path down and TCP not confirmed: never a green «Работает»', () => {
  const s = nodeState({ ...base, paths: { hy: { status: 'unhealthy', source: 'monitor' }, tcp: { status: 'unknown', source: 'monitor' } } })
  assert.equal(s.label, 'Основной туннель не работает')
  assert.equal(s.variant, 'warning')
  const jobFailed = nodeState({ ...base, paths: { hy: { status: 'failed', source: 'job' }, tcp: {} } })
  assert.equal(jobFailed.label, 'Основной туннель не работает')
  const tcpOff = nodeState({ ...base, tunnel_fallback_enabled: false, paths: { hy: { status: 'unhealthy' }, tcp: { status: 'absent', source: 'monitor' } } })
  assert.equal(tcpOff.label, 'Основной туннель не работает')
  assert.match(tcpOff.hint, /отключён/)
})
test('legacy vless exit: the TCP switch does not hide the monitor’s verdict on its only path', () => {
  assert.equal(pathView({ status: 'healthy', source: 'monitor' }, { enabled: false }).label, 'работает')
  assert.equal(pathView({ status: 'unhealthy', source: 'monitor' }, { enabled: false }).variant, 'danger')
  assert.equal(pathView({ status: 'absent', source: 'monitor' }, { enabled: false }).label, 'выключен')
  assert.equal(pathView({ status: 'ok', source: 'job' }, { enabled: false }).label, 'выключен')
  // a vless exit whose Hysteria path is absent: TCP alive is plain «Работает»
  assert.deepEqual(nodeState({ ...base, tunnel_fallback_enabled: false,
    paths: { hy: { status: 'absent', source: 'monitor' }, tcp: { status: 'healthy', source: 'monitor' } } }), { label: 'Работает', variant: 'success' })
})
test('a verify job is worded apart from the monitor’s «Проверяется»', () => {
  const job = nodeState({ ...base, last_job: { status: 'running', kind: 'verify' } }).label
  const monitor = nodeState({ ...base, tunnel_status: 'unknown' }).label
  assert.equal(job, 'Идёт проверка (задача)')
  assert.notEqual(job.replace(/…$/, ''), monitor)
})
test('nodeHref opens the card on the tab of the node’s role', () => {
  assert.equal(nodeHref(2), '/nodes?role=exit&node=2')
  assert.equal(nodeHref(7, 'relay'), '/nodes?role=relay&node=7')
  assert.equal(nodeHref(3, 'weird'), '/nodes?role=exit&node=3')
})
