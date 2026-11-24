import { test } from 'node:test'
import assert from 'node:assert/strict'
import { factsAge, nodeIssues, sortNodes, roleLabel } from './worksModel.js'

const now = Date.parse('2026-11-24T12:00:00Z')
const iso = (ms) => new Date(ms).toISOString()
const min = 60000
const day = 24 * 60 * min

test('factsAge: minutes, hours, days, none; stale past 26 hours', () => {
  assert.deepEqual(factsAge(iso(now - 5 * min), now), { label: '5 мин назад', stale: false })
  assert.deepEqual(factsAge(iso(now - 3 * 60 * min), now), { label: '3 ч назад', stale: false })
  assert.deepEqual(factsAge(iso(now - 2 * day), now), { label: '2 дн назад', stale: true })
  assert.deepEqual(factsAge(null, now), { label: 'нет данных', stale: true })
})

test('nodeIssues: reboot past 14 days, security updates, password login, full disk, stale facts', () => {
  const ok = { collected_at: iso(now - 10 * min), reboot_required: false, security_updates: 0, ssh_password: false, disk_pct: 40 }
  assert.deepEqual(nodeIssues({ role: 'exit', facts: ok }, now), [])
  const bad = { ...ok, reboot_required: true, reboot_required_since: iso(now - 15 * day), security_updates: 3, ssh_password: true, disk_pct: 91 }
  assert.deepEqual(nodeIssues({ role: 'exit', facts: bad }, now),
    ['ждёт перезагрузки 15 дн.', 'обновлений безопасности: 3', 'вход по паролю включён', 'диск 91 %'])
  const fresh = { ...ok, reboot_required: true, reboot_required_since: iso(now - 3 * day) }
  assert.deepEqual(nodeIssues({ role: 'relay', facts: fresh }, now), [], 'a reboot pending 3 days is not yet a problem')
  assert.deepEqual(nodeIssues({ role: 'relay', facts: null }, now), ['сведений нет'])
  assert.deepEqual(nodeIssues({ role: 'exit', facts: { ...ok, collected_at: iso(now - 2 * day) } }, now), ['сведения устарели'])
})

test('sortNodes: the ones needing attention first, then the Hub, exits, entries by id', () => {
  const ok = { collected_at: iso(now - 10 * min), security_updates: 0, disk_pct: 10 }
  const nodes = [
    { id: 6, role: 'relay', facts: ok },
    { id: 3, role: 'exit', facts: { ...ok, security_updates: 2 } },
    { id: 0, role: 'hub', facts: ok },
    { id: 2, role: 'exit', facts: ok },
    { id: 5, role: 'relay', facts: null },
  ]
  assert.deepEqual(sortNodes(nodes, now).map(n => n.id), [3, 5, 0, 2, 6])
})

test('roleLabel', () => {
  assert.equal(roleLabel('hub'), 'Хаб')
  assert.equal(roleLabel('exit'), 'Выход')
  assert.equal(roleLabel('relay'), 'Вход')
})
