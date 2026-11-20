import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  WIZARD_STEPS, INSTALL_STEP, initialWizard, wizardReducer, canGo, parsePort, countryValid,
  matchPasted, pasteVerdict, canConfirmKey, canCreate, canStart, passwordFlags, probeRequest, createRequest, provisionRequest, wizardSteps,
  routingRequest, closeNeedsConfirm, normalizePlans,
} from './wizardModel.js'

const FP_A = 'SHA256:' + 'A'.repeat(43)
const FP_B = 'SHA256:' + 'B'.repeat(43)
const FP_C = 'SHA256:' + 'c1+/'.repeat(10) + 'xyz'
const PROBE = {
  fingerprint: FP_A, key_type: 'ssh-ed25519',
  keys: [
    { type: 'ssh-ed25519', fingerprint: FP_A, key: 'AAAAC3NzaC1lZDI1NTE5' },
    { type: 'ecdsa-sha2-nistp256', fingerprint: FP_B, key: 'AAAAE2VjZHNh' },
    { type: 'ssh-rsa', fingerprint: FP_C, key: 'AAAAB3NzaC1yc2E' },
  ],
}

const run = (state, ...actions) => actions.reduce(wizardReducer, state)
const pw = (text) => ({ type: 'passwordFlags', ...passwordFlags(text) })
// A wizard filled up to a probed address with a typed password.
const probed = () => run(initialWizard(),
  { type: 'set', patch: { country: 'de', ip: ' 203.0.113.7 ', sshPort: '2222' } }, pw('hunter2'),
  { type: 'probed', ip: '203.0.113.7', port: 2222, result: PROBE })
const created = () => run(probed(), { type: 'confirmKey', value: true }, { type: 'created', id: 9 })

test('five steps; the install step is the last', () => {
  assert.equal(WIZARD_STEPS.length, 5)
  assert.equal(INSTALL_STEP, 4)
  assert.equal(WIZARD_STEPS[INSTALL_STEP], 'Установка')
})

test('initial state: exit unless the tab asked for a relay; nothing secret, nothing chosen', () => {
  const s = initialWizard()
  assert.equal(s.step, 0)
  assert.equal(s.role, 'exit')
  assert.equal('password' in s, false, 'the password never enters the state')
  assert.equal(s.passwordEntered, false)
  assert.equal(s.fingerprintConfirmed, false)
  assert.equal(s.mtproxy, false, 'MTProxy is off by default (B2)')
  assert.equal(s.fallback, true)
  assert.equal(s.loadWeight, 1)
  assert.equal(initialWizard('relay').role, 'relay')
  assert.equal(initialWizard('weird').role, 'exit')
})

test('ports and countries', () => {
  assert.equal(parsePort(''), 22)
  assert.equal(parsePort(' 2222 '), 2222)
  assert.equal(parsePort('0'), null)
  assert.equal(parsePort('65536'), null)
  assert.equal(parsePort('22a'), null)
  assert.equal(parsePort('1.5'), null)
  assert.equal(countryValid('DE'), true)
  assert.equal(countryValid('de'), true)
  assert.equal(countryValid('D'), false)
  assert.equal(countryValid('ДЕ'), false)
})

test('the country is stored upper-case', () => {
  assert.equal(run(initialWizard(), { type: 'set', patch: { country: 'nl' } }).country, 'NL')
})

test('a probe shows every key and preselects the preferred one, unconfirmed', () => {
  const s = probed()
  assert.deepEqual(s.probe.keys.map((k) => k.fingerprint), [FP_A, FP_B, FP_C])
  assert.equal(s.fingerprint, FP_A)
  assert.equal(s.fingerprintConfirmed, false)
  assert.equal(s.probe.keys.some((k) => 'key' in k), false, 'key blobs are not kept in the UI state')
})

test('a probe answer for an address the owner has since changed is dropped', () => {
  const s = run(initialWizard(), { type: 'set', patch: { ip: '203.0.113.8' } },
    { type: 'probed', ip: '203.0.113.7', port: 22, result: PROBE })
  assert.equal(s.probe, null)
  assert.equal(s.fingerprint, '')
})

test('changing the address forgets the probe, the chosen key and its confirmation', () => {
  const s = run(probed(), { type: 'confirmKey', value: true }, { type: 'set', patch: { ip: '203.0.113.9' } })
  assert.equal(s.probe, null)
  assert.equal(s.fingerprint, '')
  assert.equal(s.fingerprintConfirmed, false)
  const p = run(probed(), { type: 'confirmKey', value: true }, { type: 'set', patch: { sshPort: '22' } })
  assert.equal(p.fingerprintConfirmed, false)
  // the same value again changes nothing
  const same = run(probed(), { type: 'confirmKey', value: true }, { type: 'set', patch: { ip: ' 203.0.113.7 ' } })
  assert.equal(same.fingerprintConfirmed, true)
})

test('choosing another key drops the confirmation; unknown fingerprints are refused', () => {
  const s = run(probed(), { type: 'confirmKey', value: true }, { type: 'selectKey', fingerprint: FP_B })
  assert.equal(s.fingerprint, FP_B)
  assert.equal(s.fingerprintConfirmed, false)
  const bad = run(probed(), { type: 'selectKey', fingerprint: 'SHA256:' + 'Z'.repeat(43) })
  assert.equal(bad.fingerprint, FP_A)
})

test('pasted ssh-keygen output selects the matching key; confirming stays the owner’s explicit act', () => {
  const line = `256 ${FP_B} root@vps (ECDSA)`
  assert.equal(matchPasted(line, PROBE.keys)?.fingerprint, FP_B)
  assert.equal(matchPasted(FP_C.slice(7), PROBE.keys)?.fingerprint, FP_C, 'without the SHA256: prefix')
  assert.equal(matchPasted('', PROBE.keys), null)
  const s = run(probed(), { type: 'paste', text: line })
  assert.equal(s.fingerprint, FP_B)
  assert.equal(pasteVerdict(s), 'match')
  assert.equal(s.fingerprintConfirmed, false, 'a paste never confirms')
  assert.equal(run(s, { type: 'confirmKey', value: true }).fingerprintConfirmed, true)
})

test('copying a fingerprint from the wizard and pasting it back does not self-confirm', () => {
  const s = run(probed(), { type: 'paste', text: FP_A })
  assert.equal(pasteVerdict(s), 'match')
  assert.equal(s.fingerprintConfirmed, false)
  assert.equal(canCreate(s), false)
  // an already confirmed key stays confirmed when the paste only re-selects it
  const confirmed = run(probed(), { type: 'confirmKey', value: true }, { type: 'paste', text: FP_A })
  assert.equal(confirmed.fingerprintConfirmed, true)
  // a paste that moves the choice to another key drops the confirmation
  assert.equal(run(probed(), { type: 'confirmKey', value: true }, { type: 'paste', text: FP_B }).fingerprintConfirmed, false)
})

test('a pasted fingerprint must be a whole token, not a slice of a longer base64 string', () => {
  assert.equal(matchPasted('X' + 'A'.repeat(43), PROBE.keys), null)
  assert.equal(matchPasted('A'.repeat(44), PROBE.keys), null)
})

test('a pasted fingerprint that matches no key blocks the confirmation', () => {
  const s = run(probed(), { type: 'confirmKey', value: true }, { type: 'paste', text: 'SHA256:' + 'Q'.repeat(43) })
  assert.equal(pasteVerdict(s), 'mismatch')
  assert.equal(s.fingerprintConfirmed, false)
  assert.equal(canConfirmKey(s), false)
  assert.equal(run(s, { type: 'confirmKey', value: true }).fingerprintConfirmed, false)
  // clearing the paste lets the owner confirm by hand again
  const cleared = run(s, { type: 'paste', text: '' }, { type: 'confirmKey', value: true })
  assert.equal(cleared.fingerprintConfirmed, true)
})

test('choosing another key than the pasted one is a mismatch; choosing it back needs a new confirmation', () => {
  const s = run(probed(), { type: 'paste', text: FP_B }, { type: 'confirmKey', value: true }, { type: 'selectKey', fingerprint: FP_A })
  assert.equal(s.fingerprintConfirmed, false)
  assert.equal(pasteVerdict(s), 'mismatch', 'the pasted fingerprint is not the chosen key')
  const back = run(s, { type: 'selectKey', fingerprint: FP_B })
  assert.equal(back.fingerprintConfirmed, false)
  assert.equal(pasteVerdict(back), 'match')
  // re-selecting the key already chosen changes nothing
  const same = run(probed(), { type: 'confirmKey', value: true }, { type: 'selectKey', fingerprint: FP_A })
  assert.equal(same.fingerprintConfirmed, true)
})

test('no confirmation without a probe', () => {
  const s = run(initialWizard(), { type: 'confirmKey', value: true })
  assert.equal(s.fingerprintConfirmed, false)
})

test('the typed password is never kept in the state nor dispatched — only whether one was entered', () => {
  assert.deepEqual(passwordFlags('hunter2'), { entered: true, valid: true })
  assert.deepEqual(passwordFlags('   '), { entered: true, valid: false })
  assert.deepEqual(passwordFlags(''), { entered: false, valid: false })
  assert.deepEqual(passwordFlags(undefined), { entered: false, valid: false })
  const s = probed()
  assert.equal(JSON.stringify(s).includes('hunter2'), false)
  assert.equal(s.passwordEntered, true)
  assert.equal(s.passwordValid, true)
  const blank = run(s, pw('   '))
  assert.equal(blank.passwordEntered, true)
  assert.equal(blank.passwordValid, false)
  assert.equal(run(s, pw('')).passwordEntered, false)
  // the text is not an editable field any more
  const viaSet = run(initialWizard(), { type: 'set', patch: { password: 'hunter2' } })
  assert.equal(JSON.stringify(viaSet).includes('hunter2'), false)
  assert.equal(viaSet.passwordEntered, false)
})

test('leaving the access step before saving forgets the password (its field is empty again)', () => {
  const onAccess = run(initialWizard(), { type: 'set', patch: { country: 'DE' } }, { type: 'go', step: 1 },
    pw('hunter2'))
  assert.equal(onAccess.passwordValid, true)
  const back = run(onAccess, { type: 'go', step: 0 })
  assert.equal(back.passwordEntered, false)
  assert.equal(back.passwordValid, false)
})

test('the node is saved only with a confirmed key of the current probe; the password is optional', () => {
  assert.equal(canCreate(probed()), false, 'not confirmed')
  const ok = run(probed(), { type: 'confirmKey', value: true })
  assert.equal(canCreate(ok), true)
  assert.equal(canCreate(run(ok, pw('   '))), false, 'blank password')
  assert.equal(canCreate(run(ok, pw(''))), true, 'no password: the Hub key is checked by the server')
  assert.equal(canCreate(run(ok, { type: 'set', patch: { country: 'D' } })), false, 'no country')
  assert.equal(canCreate({ ...ok, role: 'relay', country: 'RU' }), true, 'a reserve entry in Russia')
  assert.equal(canCreate({ ...ok, role: 'relay', country: 'DE' }), false, 'a reserve entry abroad')
  assert.equal(canCreate({ ...ok, probe: { ...ok.probe, ip: '203.0.113.99' } }), false, 'probe of another address')
})

test('requests: the probe never carries the password; create carries the chosen key', () => {
  const s = run(probed(), { type: 'selectKey', fingerprint: FP_B }, { type: 'confirmKey', value: true },
    { type: 'set', patch: { name: '  Франкфурт ' } })
  assert.deepEqual(probeRequest(s), { ip_address: '203.0.113.7', ssh_port: 2222 })
  assert.deepEqual(createRequest(s, 'hunter2'), {
    name: 'Франкфурт', role: 'exit', country_code: 'DE', ip_address: '203.0.113.7', ssh_port: 2222,
    ssh_user: 'root', ssh_password: 'hunter2', ssh_host_key_fp: FP_B,
  })
  assert.equal(probeRequest(run(initialWizard(), { type: 'set', patch: { ip: '1.2.3.4', sshPort: 'x' } })), null)
  assert.equal(probeRequest(initialWizard()), null)
})

test('after saving: the password flags are cleared and the access fields are locked', () => {
  const s = created()
  assert.equal(s.serverId, 9)
  assert.equal(s.passwordEntered, false)
  assert.equal(s.passwordValid, false)
  assert.equal(s.step, 2, 'goes straight to the preflight')
  const locked = run(s, { type: 'set', patch: { ip: '198.51.100.1', country: 'FR', role: 'relay', name: 'n' } }, pw('x'))
  assert.equal(locked.ip, s.ip)
  assert.equal(locked.passwordEntered, false)
  assert.equal(locked.country, 'DE')
  assert.equal(locked.role, 'exit')
  assert.equal(locked.probe, s.probe)
  assert.equal(run(s, { type: 'created', id: 10 }).serverId, 9, 'saved once')
})

test('the state machine never skips an unfinished step', () => {
  const s0 = run(initialWizard(), { type: 'set', patch: { country: 'DE' } })
  assert.equal(canGo(s0, 1), true)
  assert.equal(canGo(s0, 2), false, 'access not done')
  assert.equal(canGo(initialWizard(), 1), false, 'no country')
  assert.equal(canGo(initialWizard('relay'), 1), true, 'a relay is in RU from the start')
  assert.equal(run(s0, { type: 'go', step: 3 }).step, 0)
  const s2 = created()
  assert.equal(canGo(s2, 3), false, 'preflight not passed')
  const failed = run(s2, { type: 'preflight', result: { ok: false, checks: [{ name: 'Диск', ok: false }] } })
  assert.equal(canGo(failed, 3), false)
  const passed = run(s2, { type: 'preflight', result: { ok: true, checks: [] } })
  assert.equal(canGo(passed, 3), true)
  assert.equal(canGo(passed, INSTALL_STEP), false, 'the install step is entered only by starting the job')
  assert.equal(canGo(passed, 1), true, 'back is allowed')
  assert.equal(canGo(passed, -1), false)
  const opts = run(passed, { type: 'go', step: 3 }, { type: 'set', patch: { loadWeight: 11 } })
  assert.equal(opts.step, 3)
  assert.equal(canGo({ ...opts, step: 2 }, 3), true, 'priority is checked at the options step itself')
})

test('a preflight result that is not explicitly ok does not pass', () => {
  const s = run(created(), { type: 'preflight', result: { ok: 'yes', checks: null } })
  assert.equal(s.preflight.ok, false)
  assert.deepEqual(s.preflight.checks, [])
  assert.equal(run(s, { type: 'preflightStarted' }).preflight, null)
})

test('starting the job enters the install step for good', () => {
  const s = run(created(), { type: 'preflight', result: { ok: true, checks: [] } }, { type: 'go', step: 3 },
    { type: 'started', jobId: 41 })
  assert.equal(s.step, INSTALL_STEP)
  assert.equal(s.jobId, 41)
  assert.equal(canGo(s, 3), false, 'no way back from a running install')
  assert.equal(run(s, { type: 'go', step: 0 }).step, INSTALL_STEP)
  assert.equal(run(initialWizard(), { type: 'started', jobId: 1 }).step, 0, 'no job without a saved node')
})

test('plans: all active paid by default, the owner’s choice is kept on reload', () => {
  const plans = [
    { id: 1, name: 'Пробный', is_active: true, price_rub: 0 },
    { id: 2, name: 'Базовый', is_active: true, price_rub: 250 },
    { id: 3, name: 'Архив', is_active: false, price_rub: 300 },
  ]
  const s = run(initialWizard(), { type: 'plans', plans })
  assert.deepEqual(s.planIds, [2])
  assert.equal(s.plansState, 'ready')
  const toggled = run(s, { type: 'togglePlan', id: 1, on: true }, { type: 'togglePlan', id: 2, on: false })
  assert.deepEqual(toggled.planIds, [1])
  assert.deepEqual(run(toggled, { type: 'togglePlan', id: 1, on: true }).planIds, [1], 'no duplicates')
  assert.deepEqual(run(toggled, { type: 'plans', plans }).planIds, [1])
  const failed = run(initialWizard(), { type: 'plansFailed', error: 'Нет прав на это действие' })
  assert.equal(failed.plansState, 'failed')
  assert.equal(failed.planIds, null)
})

test('normalizePlans accepts the array and the wrapped form', () => {
  assert.equal(normalizePlans([{ id: 1 }]).length, 1)
  assert.equal(normalizePlans({ plans: [{ id: 1 }, { id: 2 }] }).length, 2)
  assert.deepEqual(normalizePlans(null), [])
})

test('provision options: plans are sent only when they were loaded', () => {
  const base = run(created(), { type: 'set', patch: { mtproxy: false, loadWeight: 4 } })
  assert.deepEqual(provisionRequest(run(base, { type: 'plans', plans: [{ id: 2, is_active: true, price_rub: 250 }] })),
    { mtproxy: false, load_weight: 4, plan_ids: [2] })
  // plans unreadable (no settings.read): omit plan_ids rather than unbinding with []
  assert.deepEqual(provisionRequest(run(base, { type: 'plansFailed', error: 'x' })), { mtproxy: false, load_weight: 4 })
})

test('the TCP fallback switch is saved through routing only when it differs from what is stored', () => {
  const s = run(created(), { type: 'plans', plans: [{ id: 2, is_active: true, price_rub: 250 }] })
  assert.equal(routingRequest(s), null, 'default on: nothing to save')
  const off = run(s, { type: 'set', patch: { fallback: false, loadWeight: 3 } })
  assert.deepEqual(routingRequest(off), { tunnel_fallback_enabled: false }, 'the switch only: plans and priority go with provision')
  const saved = run(off, { type: 'fallbackStored', value: false })
  assert.equal(routingRequest(saved), null)
  const backOn = run(saved, { type: 'set', patch: { fallback: true } })
  assert.equal(routingRequest(backOn).tunnel_fallback_enabled, true)
  const noPlans = run(created(), { type: 'plansFailed', error: 'x' }, { type: 'set', patch: { fallback: false } })
  assert.deepEqual(routingRequest(noPlans), { tunnel_fallback_enabled: false }, 'no plan list: nothing about plans is sent')
})

test('closing asks first once something would be lost; never during the install', () => {
  assert.equal(closeNeedsConfirm(initialWizard()), false)
  assert.equal(closeNeedsConfirm(run(initialWizard(), { type: 'set', patch: { country: 'DE' } })), false)
  assert.equal(closeNeedsConfirm(run(initialWizard(), pw('x'))), true)
  assert.equal(closeNeedsConfirm(probed()), true)
  assert.equal(closeNeedsConfirm(created()), true)
  const installing = run(created(), { type: 'preflight', result: { ok: true, checks: [] } }, { type: 'started', jobId: 5 })
  assert.equal(closeNeedsConfirm(installing), false)
})

test('the reducer ignores unknown actions and fields', () => {
  const s = initialWizard()
  assert.equal(wizardReducer(s, { type: 'nope' }), s)
  assert.equal(run(s, { type: 'set', patch: { serverId: 5, step: 4, jobId: 1 } }).serverId, null)
})

test('«Установить» waits for the plans: never a silent common-pool node while they load', () => {
  const ready = run(created(), { type: 'preflight', result: { ok: true, checks: [] } }, { type: 'go', step: 3 })
  assert.equal(ready.plansState, 'loading')
  assert.equal(canStart(ready), false)
  assert.equal(canStart(run(ready, { type: 'plans', plans: [] })), true)
  assert.equal(canStart(run(ready, { type: 'plansFailed', error: 'Нет прав на это действие' })), true, 'a failure is shown and explained')
  assert.equal(canStart(run(ready, { type: 'plans', plans: [] }, { type: 'set', patch: { loadWeight: 0 } })), false)
  assert.equal(canStart({ ...run(ready, { type: 'plans', plans: [] }), serverId: null }), false)
})

test('relay: RU fixed, steps of its own, no exit options in the requests', () => {
  const r = initialWizard('relay')
  assert.equal(r.country, 'RU')
  assert.equal(run(r, { type: 'set', patch: { country: 'DE' } }).country, 'RU', 'country locked to RU')
  const back = run(r, { type: 'set', patch: { role: 'exit' } })
  assert.equal(back.role, 'exit')
  assert.equal(back.country, '', 'leaving relay clears RU')
  assert.equal(run(initialWizard(), { type: 'set', patch: { role: 'relay' } }).country, 'RU')
  assert.deepEqual(provisionRequest({ ...r, plansState: 'ready', planIds: [1] }), { relay_tunnel: false }, 'an entry sends only its tunnel choice, off by default')
  assert.deepEqual(provisionRequest({ ...r, tunnel: true }), { relay_tunnel: true })
  assert.equal('relay_tunnel' in provisionRequest(initialWizard()), false, 'an exit has no tunnel option')
  assert.equal(routingRequest({ ...r, fallback: false, fallbackStored: true }), null)
  assert.equal(wizardSteps('relay')[3], 'Проверка с телефона')
  assert.equal(wizardSteps('exit')[3], 'Настройки')
})
