import test from 'node:test'
import assert from 'node:assert/strict'
import { mobileCheckSummary, healthyRelays, modeBanner, ENTRY_MODES, AUTO_MODES, subscriptionDomainState, suggestedSubDomain } from './entriesModel.js'

test('mobileCheckSummary: none, pass, fail, mixed by operator', () => {
  assert.equal(mobileCheckSummary([]).state, 'none')
  assert.equal(mobileCheckSummary(null).state, 'none')
  const pass = mobileCheckSummary([{ ok: true, operator: 'МТС' }, { ok: true, operator: 'МТС' }])
  assert.equal(pass.state, 'pass')
  assert.match(pass.text, /проходит \(МТС\)/)
  const fail = mobileCheckSummary([{ ok: false, operator: 'МегаФон' }])
  assert.equal(fail.state, 'fail')
  assert.match(fail.text, /Смените IP/)
  const mixed = mobileCheckSummary([{ ok: true, operator: 'МТС' }, { ok: false, operator: 'T2' }, { ok: false }])
  assert.equal(mixed.state, 'mixed')
  assert.match(mixed.text, /у МТС, не проходит у T2, неизвестный оператор/)
})

test('healthyRelays: only relays answering from outside', () => {
  const list = healthyRelays([
    { id: 0, kind: 'hub', health: { external: 'healthy' } },
    { id: 5, kind: 'relay', health: { external: 'healthy' } },
    { id: 6, kind: 'relay', health: { external: 'unhealthy' } },
  ])
  assert.deepEqual(list.map((e) => e.id), [5])
})

test('modeBanner: pinned (alive / dead), automatic failover, auto, hub only', () => {
  const entries = [{ id: 5, name: 'Москва', address: '203.0.113.50', kind: 'relay', health: { external: 'healthy' } }]
  assert.equal(modeBanner({ active: true, pinned: true, target: 5 }, entries).tone, 'success')
  assert.equal(modeBanner({ active: true, pinned: true, target: 5 }, [{ ...entries[0], health: { external: 'unhealthy' } }]).tone, 'danger')
  assert.match(modeBanner({ active: true, target: 5 }, entries).text, /Авария/)
  assert.match(modeBanner({ enabled: true }, entries).text, /сама переключится/)
  assert.match(modeBanner({ enabled: false }, entries).text, /только к Hub/)
})

test('ENTRY_MODES: the three modes of the API, each explained', () => {
  assert.deepEqual(ENTRY_MODES.map((m) => m.key), ['relay', 'auto', 'hub'])
  for (const m of ENTRY_MODES) assert.ok(m.title && m.text.length > 20)
})

test('subscriptionDomainState: from no domain to all good', () => {
  assert.equal(subscriptionDomainState(null).tone, 'default')
  assert.equal(subscriptionDomainState({ domain: 'go.example.test', dns: [], dns_error: 'no such host' }).tone, 'danger')
  const base = { domain: 'go.example.test', dns: ['203.0.113.5'], dns_foreign: [], dns_missing: [], cert_ok: true }
  assert.equal(subscriptionDomainState({ ...base, cert_ok: false, cert_error: 'x509' }).tone, 'danger')
  assert.match(subscriptionDomainState({ ...base, dns_foreign: ['203.0.113.1'] }).text, /203\.0\.113\.1/)
  assert.equal(subscriptionDomainState({ ...base, dns_missing: ['203.0.113.6'] }).tone, 'warning')
  assert.equal(subscriptionDomainState(base).tone, 'success')
})

test('AUTO_MODES: the three switches the API accepts', () => {
  assert.deepEqual(AUTO_MODES.map((m) => m.key), ['off', 'test', 'on'])
  assert.equal(suggestedSubDomain('hiearth.ru'), 'go.hiearth.ru')
})

// ── entries management (docs/prd/entries-management.prd.md) ──
import { stabilityView, rotationView, entryActions, formatPct } from './entriesModel.js'

const w = (ok, total, checks) => ({ ok, total, checks })

test('stabilityView: the worst kind, tones by 99 / 95 %, few checks', () => {
  const v = stabilityView({ h1: { tcp443: w(796, 800, 100), tcp2443: w(776, 800, 100) }, h24: { tcp443: w(9000, 9000, 1200), tcp2443: w(9000, 9500, 1200) } })
  assert.equal(v.h1.tone, 'warning') // 97 %
  assert.equal(v.h1.text, '97,0 %')
  assert.equal(v.h24.tone, 'danger') // 94,7 %
  assert.equal(stabilityView({ h1: { tcp443: w(800, 800, 100) } }).h1.tone, 'success')
  const few = stabilityView({ h1: { tcp443: w(8, 8, 1) } }).h1
  assert.equal(few.few, true)
  assert.equal(few.tone, 'default')
  assert.equal(stabilityView(null).h1.text, '—')
  assert.deepEqual(stabilityView({ h1: { udp: w(3, 4, 1), tcp443: w(8, 8, 1) } }).byKind.map((k) => k.label), [':443', 'Бамбук'])
  assert.equal(formatPct(0.9995), '99,9 %')
})

test('rotationView: lifecycle first, then the rotation with its reason', () => {
  assert.equal(rotationView({ kind: 'hub' }).label, 'основной')
  assert.equal(rotationView({ kind: 'relay', lifecycle: 'failed', rotation: 'all' }).label, 'ошибка настройки')
  assert.equal(rotationView({ kind: 'relay', lifecycle: 'online', rotation: 'all' }).tone, 'success')
  const off = rotationView({ kind: 'relay', lifecycle: 'online', rotation: 'off', rotation_reason: 'потери у провайдера' })
  assert.equal(off.label, 'вне ротации')
  assert.equal(off.reason, 'потери у провайдера')
  assert.equal(rotationView({ kind: 'relay', lifecycle: 'online', rotation: 'test' }).label, 'только тест')
})

const now = new Date('2026-11-21T12:00:00Z')
const relay = (patch) => ({ id: 6, kind: 'relay', lifecycle: 'online', rotation: 'off', tunnel: false,
  rotation_at: '2026-11-20T10:00:00Z', checks: { path_fresh: true, tunnel_fresh: false }, ...patch })

test('entryActions: what each button needs, and why it is off', () => {
  const ctx = { testUser: 'canary', failover: {}, now }
  let a = entryActions(relay(), ctx)
  assert.equal(a.toTest.enabled, true)
  assert.equal(a.out.enabled, false)
  assert.equal(a.tunnelOn.enabled, false)
  assert.match(a.tunnelOn.reason, /Проверить туннель/)
  assert.equal(a.tunnelcheck.enabled, true)
  a = entryActions(relay({ checks: { path_fresh: false } }), ctx)
  assert.match(a.toTest.reason, /Проверить путь/)
  a = entryActions(relay(), { ...ctx, testUser: '' })
  assert.match(a.toTest.reason, /Тестовый пользователь/)
  a = entryActions(relay({ rotation: 'test', rotation_at: '2026-11-21T09:00:00Z' }), ctx)
  assert.equal(a.toAll.enabled, true)
  assert.equal(a.toAll.early, true)
  assert.equal(entryActions(relay({ rotation: 'test', rotation_at: '2026-11-19T09:00:00Z' }), ctx).toAll.early, false)
  a = entryActions(relay({ rotation: 'all' }), { ...ctx, failover: { active: true, target: 6 } })
  assert.equal(a.out.enabled, false)
  assert.match(a.out.reason, /режим/)
  a = entryActions(relay({ tunnel: true }), ctx)
  assert.equal(a.tunnelOff.enabled, true)
  assert.equal(a.tunnelcheck.enabled, false)
  a = entryActions(relay({ lifecycle: 'offline' }), ctx)
  assert.equal(a.remove.stage, 'record')
  assert.equal(a.pathcheck.enabled, false)
  assert.equal(entryActions(relay(), ctx).remove.stage, 'deprovision')
})

test('healthyRelays: a mode target must be in rotation and online', () => {
  const list = healthyRelays([
    { id: 1, kind: 'relay', rotation: 'all', lifecycle: 'online', health: { external: 'healthy' } },
    { id: 2, kind: 'relay', rotation: 'test', lifecycle: 'online', health: { external: 'healthy' } },
    { id: 3, kind: 'relay', rotation: 'off', lifecycle: 'online', health: { external: 'healthy' } },
  ])
  assert.deepEqual(list.map((e) => e.id), [1])
})
