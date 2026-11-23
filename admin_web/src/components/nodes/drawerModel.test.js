import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  jobActive, drawerTabs, initialTab, initialJobId, nodeActions, parsePriority, clampPriority, routingBody, fallbackBody,
  routingDirty, fallbackOffRisky, pathRow, mtprotoView, jobListStatus, probeKeyList, keyRows, initialKeyCheck, keyCheckReducer,
  canConfirmProbeKey, keyPasteVerdict, storedKeyNotice, keyReplaceWarning, reprovisionConsequences, deprovisionConsequences, isLastExit, LAST_EXIT_PHRASE, logServices,
  deleteConsequences, fallbackOffConsequences, plansNote, plansFailure,
} from './drawerModel.js'

const FP_ED = 'SHA256:' + 'E'.repeat(43)
const FP_EC = 'SHA256:' + 'C'.repeat(43)
const FP_RSA = 'SHA256:' + 'r1+/'.repeat(10) + 'xyz'

const ALL = { manage: true, logs: true }
const exit = {
  id: 2, name: 'Амстердам', role: 'exit', lifecycle_state: 'online', tunnel_status: 'healthy', tunnel_fallback_enabled: true,
  mtproto_status: 'healthy', load_weight: 3, plan_ids: [4, 2], host_key: { fingerprint: FP_ED, key_type: 'ssh-ed25519', confirmed: true },
  paths: { hy: { status: 'healthy', source: 'monitor' }, tcp: { status: 'healthy', source: 'monitor' } }, last_job: null,
}
const node = (patch = {}) => ({ ...exit, ...patch })
const running = { last_job: { id: 7, kind: 'provision', status: 'running' } }
const unconfirmed = { host_key: { fingerprint: FP_EC, key_type: 'ecdsa-sha2-nistp256', confirmed: false } }

test('a job is active while running or queued only', () => {
  assert.equal(jobActive(node(running)), true)
  assert.equal(jobActive(node({ last_job: { id: 1, status: 'queued' } })), true)
  for (const status of ['succeeded', 'failed', 'interrupted']) assert.equal(jobActive(node({ last_job: { id: 1, status } })), false)
  assert.equal(jobActive(node()), false)
})

test('tabs: plans and priority only for an exit, logs with servers.logs, the danger zone with nodes.provision', () => {
  const keys = (n, p) => drawerTabs(n, p).map((t) => t.key)
  assert.deepEqual(keys(node(), ALL), ['overview', 'load', 'routing', 'settings', 'jobs', 'logs', 'danger'])
  assert.deepEqual(keys(node(), {}), ['overview', 'load', 'routing', 'settings', 'jobs'])
  assert.deepEqual(keys(node({ role: 'relay' }), ALL), ['overview', 'load', 'settings', 'jobs', 'logs', 'danger'])
  assert.equal(drawerTabs(node(), ALL).find((t) => t.key === 'routing').label, 'Тарифы и приоритет')
})

test('the card opens on the key while it is unconfirmed, on the job while one runs or was interrupted', () => {
  assert.equal(initialTab(node()), 'overview')
  assert.equal(initialTab(node(running)), 'jobs')
  assert.equal(initialTab(node({ last_job: { id: 3, status: 'interrupted' } })), 'jobs')
  assert.equal(initialTab(node({ ...running, ...unconfirmed })), 'overview')
  assert.equal(initialJobId(node(running)), 7)
  assert.equal(initialJobId(node({ last_job: { id: 5, status: 'failed' } })), 5)
  assert.equal(initialJobId(node({ last_job: { id: 5, status: 'succeeded' } })), null)
})

test('actions need nodes.provision; a running job and an unconfirmed key block the SSH actions with a reason', () => {
  const ro = nodeActions(node(), {})
  for (const k of ['verify', 'setup', 'mtproxy', 'deprovision', 'remove', 'fallback', 'routing']) assert.equal(ro[k].show, false, k)
  assert.equal(ro.tunnelCheck.show, true) // read-only check: servers.read

  const busy = nodeActions(node(running), ALL)
  for (const k of ['verify', 'setup', 'mtproxy', 'deprovision', 'remove']) assert.match(busy[k].reason, /задача/i, k)

  const key = nodeActions(node(unconfirmed), ALL)
  for (const k of ['verify', 'setup', 'mtproxy', 'logs']) assert.match(key[k].reason, /подтвердите отпечаток/i, k)
  assert.equal(key.deprovision.reason, '') // deprovision does not need the key (the node leaves routing first)
})

test('setup: a live exit is re-provisioned behind ConfirmDanger, a node out of service is set up directly', () => {
  const live = nodeActions(node(), ALL).setup
  assert.deepEqual([live.kind, live.label, live.confirm, live.reason], ['reprovision', 'Переустановить', true, ''])
  const pending = nodeActions(node({ lifecycle_state: 'pending' }), ALL).setup
  assert.deepEqual([pending.kind, pending.label, pending.confirm], ['provision', 'Настроить', false])
  for (const s of ['failed', 'offline']) {
    const a = nodeActions(node({ lifecycle_state: s }), ALL).setup
    assert.deepEqual([a.kind, a.label, a.confirm, a.reason], ['provision', 'Настроить заново', false, ''], s)
  }
  assert.match(nodeActions(node({ lifecycle_state: 'deprovisioning' }), ALL).setup.reason, /отключени/)
  assert.equal(nodeActions(node({ role: 'relay' }), ALL).setup.show, false) // relay setup comes with phase 3
})

test('verify only where there is something to compare', () => {
  assert.equal(nodeActions(node(), ALL).verify.reason, '')
  assert.equal(nodeActions(node({ lifecycle_state: 'failed' }), ALL).verify.reason, '')
  assert.notEqual(nodeActions(node({ lifecycle_state: 'pending' }), ALL).verify.reason, '')
  assert.notEqual(nodeActions(node({ lifecycle_state: 'offline' }), ALL).verify.reason, '')
})

test('Telegram proxy reinstall: online exit, nodes.provision only (POST /servers/{id}/mtproxy), not switched off', () => {
  assert.equal(nodeActions(node(), { manage: true }).mtproxy.reason, '')
  assert.equal(nodeActions(node(), { logs: true }).mtproxy.show, false)
  assert.equal(nodeActions(node({ lifecycle_state: 'pending' }), ALL).mtproxy.show, false)
  assert.match(nodeActions(node({ mtproto_status: 'disabled' }), ALL).mtproxy.reason, /выключен/)
})

test('delete only a pending or offline node; a failed one is switched off first', () => {
  assert.equal(nodeActions(node({ lifecycle_state: 'pending' }), ALL).remove.reason, '')
  assert.equal(nodeActions(node({ lifecycle_state: 'offline' }), ALL).remove.reason, '')
  const failed = nodeActions(node({ lifecycle_state: 'failed' }), ALL).remove.reason
  assert.match(failed, /Отключить/)
  assert.match(failed, /клиент туннеля/) // why: the backend refuses a failed node with the same explanation
  for (const s of ['online', 'provisioning', 'deprovisioning']) assert.match(nodeActions(node({ lifecycle_state: s }), ALL).remove.reason, /Отключить/, s)
  assert.notEqual(nodeActions(node({ lifecycle_state: 'pending' }), ALL).deprovision.reason, '') // nothing to switch off
  assert.equal(nodeActions(node({ lifecycle_state: 'offline' }), ALL).deprovision.reason, '') // again: a skipped node side
})

test('actions confirmed by typing the name are blocked for a node without a name', () => {
  const a = nodeActions(node({ name: '  ' }), ALL)
  for (const k of ['deprovision', 'setup']) assert.match(a[k].reason, /названи/, k)
  assert.match(nodeActions(node({ name: '', lifecycle_state: 'offline' }), ALL).remove.reason, /названи/)
})

test('logs: servers.logs and a confirmed key; tunnel check only for an online exit', () => {
  assert.equal(nodeActions(node(), { logs: true }).logs.reason, '')
  assert.equal(nodeActions(node(), {}).logs.show, false)
  assert.equal(nodeActions(node({ lifecycle_state: 'offline' }), ALL).tunnelCheck.show, false)
  assert.equal(nodeActions(node({ role: 'relay' }), ALL).tunnelCheck.show, false)
})

test('priority is an integer 1..10; routing sends only what changed', () => {
  assert.equal(parsePriority('7'), 7)
  for (const bad of ['0', '11', '2.5', '', 'x', ' ']) assert.equal(parsePriority(bad), null, bad)
  assert.equal(clampPriority(0), 1)
  assert.equal(clampPriority(42), 10)
  assert.equal(clampPriority(undefined), 1)

  assert.deepEqual(routingBody(node(), { planIds: [5, 1, 5], weight: '4', plansLoaded: true }), { body: { plan_ids: [1, 5], load_weight: 4 } })
  // plans not loaded (no settings.read, a failed request): only the priority, the binding is kept by the backend
  assert.deepEqual(routingBody(node(), { planIds: [9], weight: '4', plansLoaded: false }), { body: { load_weight: 4 } })
  assert.deepEqual(routingBody(node(), { planIds: [2, 4, 5], weight: '3', plansLoaded: true }), { body: { plan_ids: [2, 4, 5] } })
  assert.deepEqual(routingBody(node(), { planIds: [], weight: '3', plansLoaded: true }), { body: { plan_ids: [] } }) // [] = common pool only
  assert.match(routingBody(node(), { planIds: [4, 2], weight: '3', plansLoaded: true }).error, /изменений нет/)
  assert.match(routingBody(node(), { planIds: [], weight: '0', plansLoaded: true }).error, /от 1 до 10/)
  // the routing save never touches the TCP switch (the backend keeps it when the field is absent)
  assert.equal('tunnel_fallback_enabled' in routingBody(node(), { planIds: [], weight: '1', plansLoaded: true }).body, false)
})

test('without the plan list the owner is told why and what is kept', () => {
  assert.equal(plansNote({ state: 'ready' }, node()), '')
  assert.equal(plansNote({ state: 'loading' }, node()), '')
  const forbidden = plansNote({ state: 'forbidden' }, node())
  assert.match(forbidden, /settings\.read/)
  assert.match(forbidden, /2/) // the stored binding count
  assert.match(plansNote({ state: 'failed', error: 'Нет связи с сервером' }, node()), /Нет связи с сервером/)
  assert.equal(plansFailure({ status: 403, message: 'Нет прав' }).state, 'forbidden')
  assert.deepEqual(plansFailure({ status: 0, message: 'Нет связи' }), { state: 'failed', items: [], error: 'Нет связи' })
})

test('the TCP switch sends only itself — never the plans or the priority, saved or not', () => {
  assert.deepEqual(fallbackBody(node(), false), { tunnel_fallback_enabled: false })
  assert.deepEqual(fallbackBody(node({ plan_ids: null, load_weight: 0 }), true), { tunnel_fallback_enabled: true })
})

test('unsaved routing edits are noticed', () => {
  assert.equal(routingDirty(node(), { planIds: [4, 2], weight: '3', plansLoaded: true }), false)
  assert.equal(routingDirty(node(), { planIds: [2], weight: '3', plansLoaded: true }), true)
  assert.equal(routingDirty(node(), { planIds: [2], weight: '3', plansLoaded: false }), false)
  assert.equal(routingDirty(node(), { planIds: [4, 2], weight: '5', plansLoaded: true }), true)
})

test('switching the TCP path off is risky while the main path is not confirmed working', () => {
  assert.equal(fallbackOffRisky(node()), false)
  assert.equal(fallbackOffRisky(node({ paths: { hy: { status: 'unhealthy', source: 'monitor' }, tcp: { status: 'healthy' } } })), true)
  assert.equal(fallbackOffRisky(node({ paths: { hy: { status: 'skipped', source: 'job' } } })), true)
  assert.equal(fallbackOffRisky(node({ paths: {} })), true) // never checked
  // a legacy vless exit: TCP is its only path, the switch does not act on it
  assert.equal(fallbackOffRisky(node({ paths: { hy: { status: 'absent', source: 'monitor' } } })), false)
  assert.equal(fallbackOffRisky(node({ lifecycle_state: 'pending', paths: {} })), false)
  assert.equal(fallbackOffRisky(node({ tunnel_fallback_enabled: false, paths: {} })), false)
})

test('path rows: «skipped» main path is a warning «основной путь не отвечает»; an offline node shows no stale paths', () => {
  const hy = pathRow(node({ paths: { hy: { status: 'skipped', source: 'job', checked_at: '2026-10-20T10:00:00Z' } } }), 'hy')
  assert.equal(hy.view.variant, 'warning')
  assert.match(hy.view.hint, /основной путь не отвечает/i)
  assert.equal(hy.checkedAt, '2026-10-20T10:00:00Z')
  const tcpOff = pathRow(node({ tunnel_fallback_enabled: false, paths: { tcp: { status: 'ok', rtt_ms: 40, checked_at: 'x' } } }), 'tcp')
  assert.equal(tcpOff.view.label, 'выключен')
  assert.deepEqual([tcpOff.rttMs, tcpOff.checkedAt], [0, null]) // a switched-off path has no RTT or time
  assert.equal(pathRow(node({ paths: { hy: { status: 'healthy', rtt_ms: 12 } } }), 'hy').rttMs, 12)
  assert.equal(pathRow(node({ lifecycle_state: 'offline' }), 'hy').stale, true)
})

test('Telegram proxy and job statuses read as words; unknown ones are shown as they are', () => {
  assert.deepEqual(mtprotoView('healthy'), { label: 'работает', variant: 'success' })
  assert.equal(mtprotoView('unhealthy').variant, 'danger')
  assert.equal(mtprotoView('disabled').label, 'выключен')
  assert.equal(mtprotoView('weird').label, 'Неизвестно: weird')
  assert.deepEqual(jobListStatus({ status: 'succeeded' }), { label: 'готово', variant: 'success' })
  assert.equal(jobListStatus({ status: 'interrupted' }).variant, 'warning')
  assert.equal(jobListStatus({ status: 'zzz' }).label, 'zzz')
})

const PROBE = {
  fingerprint: FP_ED, key_type: 'ssh-ed25519',
  keys: [
    { type: 'ssh-ed25519', fingerprint: FP_ED, key: 'AAAA' },
    { type: 'ecdsa-sha2-nistp256', fingerprint: FP_EC, key: 'BBBB' },
    { type: 'ssh-rsa', fingerprint: FP_RSA, key: 'CCCC' },
    { type: 'ssh-dss', fingerprint: 'MD5:aa:bb', key: 'DDDD' }, // not a SHA256 fingerprint: never offered
  ],
}
const EXIT2 = { ...PROBE, stored: { fingerprint: FP_EC, key_type: 'ecdsa-sha2-nistp256', confirmed: false, presented: true } }
const run = (...actions) => actions.reduce(keyCheckReducer, initialKeyCheck)

test('every presented key is offered with its own ssh-keygen file (exit 2: ECDSA, not ed25519)', () => {
  const keys = probeKeyList(PROBE)
  assert.deepEqual(keys.map((k) => k.fingerprint), [FP_ED, FP_EC, FP_RSA])
  const rows = keyRows(keys, FP_EC)
  assert.equal(rows[1].cmd, 'ssh-keygen -lf /etc/ssh/ssh_host_ecdsa_key.pub')
  assert.equal(rows[1].label, 'ECDSA')
  assert.equal(rows[1].stored, true)
  assert.equal(rows[0].cmd, 'ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub')
  assert.equal(rows[2].cmd, 'ssh-keygen -lf /etc/ssh/ssh_host_rsa_key.pub')
  assert.equal(rows[0].stored, false)
  // an old probe answer without keys[]: its preferred key
  assert.deepEqual(probeKeyList({ fingerprint: FP_EC, key_type: 'ecdsa-sha2-nistp256' }), [{ type: 'ecdsa-sha2-nistp256', fingerprint: FP_EC }])
})

test('the probe preselects the stored key when the server still presents it; confirming is always the owner\'s switch', () => {
  const s = run({ type: 'probed', result: EXIT2 })
  assert.equal(s.selected, FP_EC)
  assert.equal(s.ownerConfirmed, false)
  assert.equal(canConfirmProbeKey(s), false)
  const ok = keyCheckReducer(s, { type: 'confirm', value: true })
  assert.equal(canConfirmProbeKey(ok), true)
  // another key: the confirmation belonged to the previous one
  const other = keyCheckReducer(ok, { type: 'select', fingerprint: FP_ED })
  assert.deepEqual([other.selected, other.ownerConfirmed], [FP_ED, false])
  // a fingerprint the server did not present can never be selected
  assert.equal(keyCheckReducer(other, { type: 'select', fingerprint: 'SHA256:' + 'Z'.repeat(43) }).selected, FP_ED)
  // without a stored key: the probe's preferred one
  assert.equal(run({ type: 'probed', result: PROBE }).selected, FP_ED)
})

test('a pasted ssh-keygen line only selects; a mismatch can never be confirmed', () => {
  const s = run({ type: 'probed', result: PROBE }, { type: 'confirm', value: true })
  const pasted = keyCheckReducer(s, { type: 'paste', text: `256 ${FP_EC} root@nl (ECDSA)` })
  assert.deepEqual([pasted.selected, pasted.ownerConfirmed, keyPasteVerdict(pasted)], [FP_EC, false, 'match'])
  const same = keyCheckReducer(s, { type: 'paste', text: FP_ED })
  assert.equal(same.ownerConfirmed, true) // the same key: the owner's confirmation stays
  const bad = keyCheckReducer(s, { type: 'paste', text: 'SHA256:' + 'Q'.repeat(43) })
  assert.equal(keyPasteVerdict(bad), 'mismatch')
  assert.equal(bad.ownerConfirmed, false)
  assert.equal(keyCheckReducer(bad, { type: 'confirm', value: true }).ownerConfirmed, false)
  assert.equal(keyPasteVerdict(keyCheckReducer(bad, { type: 'paste', text: '  ' })), 'none')
})

test('a stored key the server no longer presents is called out; replacing a confirmed key is said aloud', () => {
  assert.equal(storedKeyNotice(EXIT2), '')
  assert.match(storedKeyNotice({ ...PROBE, stored: { fingerprint: FP_EC, confirmed: true, presented: false } }), /переустановлен/)
  assert.notEqual(storedKeyNotice({ ...PROBE, stored: { fingerprint: FP_EC, confirmed: false, presented: false } }), '')
  assert.equal(storedKeyNotice(PROBE), '')
  const confirmed = { fingerprint: FP_EC, key_type: 'ecdsa-sha2-nistp256', confirmed: true }
  assert.match(keyReplaceWarning(confirmed, FP_ED), /заменён/)
  assert.equal(keyReplaceWarning(confirmed, FP_EC), '')
  assert.equal(keyReplaceWarning({ ...confirmed, confirmed: false }, FP_ED), '')
})

test('consequences explain what happens, in Russian', () => {
  assert.ok(reprovisionConsequences(node()).some((c) => /перезапуст/.test(c)))
  const dep = deprovisionConsequences(node())
  assert.ok(dep.some((c) => /маршрутизац/.test(c)))
  assert.ok(!dep.some((c) => /Продолжить/.test(c)))
  assert.ok(deprovisionConsequences(node(unconfirmed)).some((c) => /Продолжить/.test(c)))
  assert.ok(deprovisionConsequences(node({ lifecycle_state: 'offline' })).some((c) => /уже отключена/.test(c)))
  assert.ok(deleteConsequences(node()).some((c) => /хостер/.test(c)))
  assert.ok(fallbackOffConsequences(node({ paths: { hy: { status: 'unhealthy', source: 'monitor' } } })).some((c) => /трафик/.test(c)))
})

test('the last working exit is switched off only with the phrase (B4)', () => {
  const a = { id: 1, role: 'exit', lifecycle_state: 'online', status: 'online', tunnel_status: 'healthy' }
  const b = { id: 2, role: 'exit', lifecycle_state: 'online', status: 'online', tunnel_status: 'healthy' }
  const dead = { id: 5, role: 'exit', lifecycle_state: 'online', status: 'offline', tunnel_status: 'healthy' }
  const sick = { id: 6, role: 'exit', lifecycle_state: 'online', status: 'online', tunnel_status: 'unhealthy' }
  const leaving = { ...b, id: 8, last_job: { kind: 'deprovision', status: 'running' } }
  const r = { id: 3, role: 'relay', lifecycle_state: 'online' }
  const off = { id: 4, role: 'exit', lifecycle_state: 'offline' }
  assert.equal(isLastExit(a, [a, r, off]), true, 'a relay or a switched-off exit is no reserve')
  assert.equal(isLastExit(a, [a, dead, sick, leaving]), true, 'a dead, unhealthy or leaving exit is no reserve')
  assert.equal(isLastExit(a, [a, b]), false, 'another working exit')
  assert.equal(isLastExit(r, [r]), false, 'a relay is never «the last exit»')
  assert.equal(isLastExit(off, [off]), false)
  assert.equal(LAST_EXIT_PHRASE, 'ОТКЛЮЧИТЬ ВСЕХ')
  assert.match(deprovisionConsequences(a, { lastExit: true })[0], /у всех пользователей пропадёт интернет/)
  assert.ok(!deprovisionConsequences(a).some((c) => /пропадёт интернет/.test(c)))
})

test('journals: only the units the server runs (C5)', () => {
  const keys = (n) => logServices(n).map((s) => s.key)
  assert.deepEqual(keys({ role: 'exit', mtproto_status: 'disabled' }), ['node-agent', 'xray', 'hysteria2'])
  assert.deepEqual(keys({ role: 'exit', mtproto_status: 'healthy' }), ['node-agent', 'xray', 'hysteria2', 'mtproxy'])
  assert.deepEqual(keys({ role: 'relay' }), ['nginx', 'heyearth-relay'])
})

test('«Поставить сторожа»: a working exit only, behind the same gates as the other jobs', () => {
  assert.deepEqual(nodeActions(node(), { manage: true }).watchdog, { show: true, reason: '' })
  assert.equal(nodeActions(node(), { manage: false }).watchdog.show, false, 'needs nodes.provision')
  assert.equal(nodeActions(node({ role: 'relay' }), { manage: true }).watchdog.show, false, 'an entry sits in RU: it sees what the Hub sees')
  assert.equal(nodeActions(node({ lifecycle_state: 'failed' }), { manage: true }).watchdog.show, false)
  assert.match(nodeActions(node({ host_key: { confirmed: false } }), { manage: true }).watchdog.reason, /подтвердите отпечаток/i)
})
