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
