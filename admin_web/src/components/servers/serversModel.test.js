import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  serverTab, serversHref, loadView, flag, countryLabel, exitCountries, geoHint, PREP_PORTS, ownerForm, ownerBody,
  niceMax, chartGeometry, capacityNodeRow, capacitySummary, launchView,
} from './serversModel.js'

test('serverTab: ?tab wins, the old ?role still lands', () => {
  assert.equal(serverTab('hub'), 'hub')
  assert.equal(serverTab('', 'relay'), 'relay')
  assert.equal(serverTab('nope', 'exit'), 'exit')
  assert.equal(serverTab(undefined, undefined), 'exit')
  assert.equal(serversHref({ role: 'relay', node: 7 }), '/servers?tab=relay&node=7')
  assert.equal(serversHref({ tab: 'exit', add: true }), '/servers?tab=exit&add=1')
})

test('loadView: a fresh sample, thresholds, a stale one, none', () => {
  const now = Date.parse('2026-11-13T12:00:00Z')
  const fresh = { cpu_pct: 72.4, mem_pct: 40, net_rx_mbps: 600, net_tx_mbps: 20.25, connections: 1234, conntrack_pct: 80, cores: 2, at: '2026-11-13T11:59:00Z', stale: false }
  const v = loadView(fresh, { portMbps: 1000, now })
  assert.equal(v.cpu.text, '72 %')
  assert.equal(v.cpu.warn, true, 'exit CPU over 60 %')
  assert.equal(v.net.text, '600 ↓ · 20.3 ↑')
  assert.equal(v.net.warn, true, 'over half of a 1 Gbit/s port')
  assert.equal(v.conns.text, new Intl.NumberFormat('ru-RU').format(1234))
  assert.equal(v.conntrack.warn, true)
  assert.equal(v.why, '')
  // A relay is not judged by CPU; no port entered — the channel is not judged.
  const r = loadView(fresh, { role: 'relay', now })
  assert.equal(r.cpu.warn, false)
  assert.equal(r.net.warn, false)
  // Stale: the numbers stay, the reason is said.
  const s = loadView({ ...fresh, stale: true, at: '2026-11-13T11:40:00Z' }, { now })
  assert.equal(s.stale, true)
  assert.match(s.why, /20 мин назад/)
  // No sample yet, or only the collector's error.
  assert.equal(loadView(null).empty, true)
  assert.match(loadView({ stale: true, error: 'ключ не подтверждён' }).why, /ключ не подтверждён/)
})

test('countries: flags, names, the exit list, the GeoIP hint', () => {
  assert.equal(flag('de'), '🇩🇪')
  assert.equal(flag('D'), '')
  assert.equal(countryLabel('NL'), '🇳🇱 Нидерланды')
  assert.equal(countryLabel(''), '—')
  const list = exitCountries('')
  assert.ok(!list.some((c) => c.code === 'RU'), 'no Russia for an exit')
  assert.ok(list.some((c) => c.code === 'DE'))
  assert.ok(exitCountries('XK').some((c) => c.code === 'XK'), 'a stored code stays selectable')
  assert.deepEqual(geoHint('DE', 'nl'), { code: 'NL', matches: false, text: 'По реестру адресов этот IP — 🇳🇱 Нидерланды' })
  assert.equal(geoHint('NL', 'NL').matches, true)
  assert.equal(geoHint('DE', ''), null)
})

test('PREP_PORTS: what the plan says to open at the hoster', () => {
  const ports = (role) => PREP_PORTS[role].map((p) => `${p.proto} ${p.port}`)
  for (const p of ['TCP 50052', 'UDP 50443', 'TCP 443']) assert.ok(ports('exit').includes(p), p)
  for (const p of ['TCP 443', 'TCP 80', 'TCP 2443', 'UDP 30000–65000']) assert.ok(ports('relay').includes(p), p)
  assert.match(PREP_PORTS.exit.find((p) => p.port === '50052').note, /только с адреса Hub/)
})

test('ownerBody: empty clears, numbers are checked', () => {
  assert.deepEqual(ownerForm({ traffic_quota_tb: 2.5, monthly_price_rub: 700, port_mbps: null }),
    { traffic_quota_tb: '2.5', monthly_price_rub: '700', port_mbps: '', cpu_cores: '' })
  assert.deepEqual(ownerBody({ traffic_quota_tb: '2,5', monthly_price_rub: '700', port_mbps: '', cpu_cores: ' 2 ' }),
    { body: { traffic_quota_tb: 2.5, monthly_price_rub: 700, port_mbps: null, cpu_cores: 2 }, error: '' })
  assert.match(ownerBody({ cpu_cores: '1.5' }).error, /ядра/)
  assert.match(ownerBody({ port_mbps: '0' }).error, /порт/)
  assert.match(ownerBody({ monthly_price_rub: 'много' }).error, /цена/)
  assert.equal(ownerBody({ traffic_quota_tb: '-1' }).body, null)
})

test('chartGeometry: scale, gaps, percent top', () => {
  assert.equal(niceMax(0), 1)
  assert.equal(niceMax(37), 50)
  assert.equal(niceMax(120), 200)
  const pts = [
    { at: '2026-11-13T10:00:00Z', cpu: 50, rx: 10 },
    { at: '2026-11-13T10:10:00Z', cpu: 100, rx: null },
    { at: '2026-11-13T10:20:00Z', cpu: 0, rx: 30 },
  ]
  const g = chartGeometry(pts, (p) => p.cpu, { width: 200, height: 100, max: 100 })
  assert.equal(g.line, 'M0.0,50.0L100.0,0.0L200.0,100.0')
  assert.equal(g.top, 100)
  assert.equal(g.last, 0)
  const n = chartGeometry(pts, (p) => p.rx, { width: 200, height: 100 })
  assert.equal(n.top, 50)
  assert.equal(n.line, 'M0.0,80.0M200.0,40.0', 'a missing value breaks the line')
  assert.equal(chartGeometry([], (p) => p.cpu).empty, true)
})

test('capacity rows and summary', () => {
  const row = capacityNodeRow({ id: 3, name: 'Вход', role: 'relay', cpu_peak_pct: 90, net_peak_mbps: 20, net_pct: 20, conntrack_pct: 80,
    month_tb: 0.8, quota_tb: 1, quota_pct: 80, price_rub: 300, samples: 10, flags: ['conntrack', 'quota'] })
  assert.equal(row.cpu.warn, false)
  assert.equal(row.conntrack.warn, true)
  assert.equal(row.month.text, '0.80 из 1 ТБ (80 %)')
  assert.deepEqual(row.flags, ['conntrack', 'трафик'])
  const ex = capacityNodeRow({ id: 1, name: 'A', role: 'exit', month_tb: 0.1, quota_tb: 0, samples: 0, flags: [] })
  assert.equal(ex.month.text, '0.10 ТБ · безлимит')
  assert.equal(ex.conntrack.text, '—')
  assert.equal(ex.noData, true)
  const s = capacitySummary({ monthly_cost_rub: 3800, cost_per_user_rub: 9.5, active_users_day: 50, capacity_users_day: 60 })
  assert.match(s.cost, /₽ в месяц/)
  assert.equal(s.perUser, '9,5 ₽ на активного пользователя')
  assert.equal(s.usersWarn, true)
  assert.match(capacitySummary({ monthly_cost_rub: 0, active_users_day: 3 }).cost, /Цены серверов не указаны/)
})

test('launchView: next step, progress, folds when complete', () => {
  const data = { items: [{ key: 'hub', done: true }, { key: 'exits', done: false }, { key: 'entry', done: false }], done: 1, total: 3, complete: false }
  const v = launchView(data)
  assert.equal(v.next.key, 'exits')
  assert.equal(v.progress, '1 из 3')
  assert.equal(v.collapsed, false)
  assert.equal(launchView({ items: [{ key: 'hub', done: true }], done: 1, total: 1, complete: true }).collapsed, true)
  assert.equal(launchView(null).next, null)
})
