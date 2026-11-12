import test from 'node:test'
import assert from 'node:assert/strict'
import { mobileCheckSummary, healthyRelays, modeBanner, ENTRY_MODES } from './entriesModel.js'

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
