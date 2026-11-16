// «Серверы» as pure rules (node --test): the tabs, a load sample in words, the wizard's countries
// and what to open at the hoster, the owner's numbers, the chart geometry, «Мощность» and «Запуск».
// Inputs: GET /api/admin/nodes items (load, owner), /hub, /servers/{id}/load, /capacity,
// /launch-checklist (internal/admin/servers_ui.go, capacity_model.go, cmd/api/launch_checklist.go).

export const SERVER_TABS = [
  { key: 'exit', label: 'Выходы' },
  { key: 'relay', label: 'Входы в РФ' },
  { key: 'hub', label: 'Hub' },
  { key: 'capacity', label: 'Мощность' },
]
const TAB_KEYS = SERVER_TABS.map((t) => t.key)

// serverTab: ?tab= wins; the old ?role= of «Ноды» (and /entries → relay) still lands right.
export function serverTab(tab, role) {
  if (TAB_KEYS.includes(tab)) return tab
  return role === 'relay' ? 'relay' : 'exit'
}

// serversHref: a link into the page; tab follows the node's role when a node is named.
export function serversHref({ tab, node, role, add, phone } = {}) {
  const q = new URLSearchParams()
  q.set('tab', serverTab(tab, role))
  if (node != null && node !== '') q.set('node', String(node))
  if (add) q.set('add', '1')
  if (phone != null && phone !== '') q.set('phone', String(phone))
  return `/servers?${q.toString()}`
}

// The thresholds of «Мощность» (internal/admin/capacity_model.go): a cell turns amber over them.
export const CPU_WARN = 60
export const NET_WARN = 50 // % of the port
export const CT_WARN = 70

const num = (v) => typeof v === 'number' && Number.isFinite(v)
export const fmtPct = (v) => (num(v) ? `${Math.round(v)} %` : '—')
export function fmtMbps(v) {
  if (!num(v)) return '—'
  if (v >= 100) return String(Math.round(v))
  return String(Math.round(v * 10) / 10)
}
export const fmtInt = (v) => (num(v) ? new Intl.NumberFormat('ru-RU').format(Math.round(v)) : '—')

function minutesAgo(at, now) {
  const t = Date.parse(at || '')
  return Number.isFinite(t) ? Math.max(0, Math.round((now - t) / 60000)) : null
}

// loadView: one server's latest sample as the row and the card show it. portMbps: the owner's
// port speed (the channel cell turns amber over half of it).
export function loadView(load, { portMbps, role, now = Date.now() } = {}) {
  if (!load || !num(load.cpu_pct)) {
    return { empty: true, why: load?.error ? `Нет данных: ${load.error}` : 'Нагрузка ещё не собрана — сборщик опрашивает сервер раз в минуту' }
  }
  const rx = load.net_rx_mbps
  const tx = load.net_tx_mbps
  const peak = Math.max(num(rx) ? rx : 0, num(tx) ? tx : 0)
  const netWarn = num(portMbps) && portMbps > 0 && peak > (portMbps * NET_WARN) / 100
  const ago = minutesAgo(load.at, now)
  const v = {
    empty: false,
    stale: !!load.stale,
    cpu: { text: fmtPct(load.cpu_pct), warn: role !== 'relay' && load.cpu_pct > CPU_WARN },
    ram: { text: fmtPct(load.mem_pct), warn: false },
    net: { text: num(rx) || num(tx) ? `${fmtMbps(rx)} ↓ · ${fmtMbps(tx)} ↑` : '—', warn: netWarn },
    conns: { text: fmtInt(load.connections), warn: false },
    conntrack: { text: fmtPct(load.conntrack_pct), warn: num(load.conntrack_pct) && load.conntrack_pct > CT_WARN },
    cores: load.cores || null,
    why: '',
  }
  if (load.error) v.why = `Сборщик не получил данные: ${load.error}`
  else if (v.stale) v.why = ago != null ? `Последние данные ${ago} мин назад — сервер не отвечает сборщику` : 'Данные устарели'
  return v
}

// ── the wizard: countries and what to open at the hoster ──

export const COUNTRIES = [
  ['DE', 'Германия'], ['NL', 'Нидерланды'], ['FI', 'Финляндия'], ['SE', 'Швеция'], ['NO', 'Норвегия'], ['DK', 'Дания'],
  ['PL', 'Польша'], ['LV', 'Латвия'], ['LT', 'Литва'], ['EE', 'Эстония'], ['CZ', 'Чехия'], ['AT', 'Австрия'],
  ['CH', 'Швейцария'], ['FR', 'Франция'], ['GB', 'Великобритания'], ['IE', 'Ирландия'], ['ES', 'Испания'],
  ['PT', 'Португалия'], ['IT', 'Италия'], ['BG', 'Болгария'], ['RO', 'Румыния'], ['RS', 'Сербия'], ['MD', 'Молдова'],
  ['TR', 'Турция'], ['GE', 'Грузия'], ['AM', 'Армения'], ['KZ', 'Казахстан'], ['AE', 'ОАЭ'], ['IL', 'Израиль'],
  ['US', 'США'], ['CA', 'Канада'], ['HK', 'Гонконг'], ['SG', 'Сингапур'], ['JP', 'Япония'], ['RU', 'Россия'],
]

const CC_RE = /^[A-Za-z]{2}$/
export function flag(cc) {
  if (!CC_RE.test(cc || '')) return ''
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65))
}
export const countryName = (cc) => COUNTRIES.find(([c]) => c === String(cc || '').toUpperCase())?.[1] || String(cc || '').toUpperCase()
export const countryLabel = (cc) => (CC_RE.test(cc || '') ? `${flag(cc)} ${countryName(cc)}` : '—')

// exitCountries: the dropdown of an exit (no Russia); a stored code not in the list stays selectable.
export function exitCountries(current) {
  const list = COUNTRIES.filter(([c]) => c !== 'RU')
  const cur = String(current || '').toUpperCase()
  if (CC_RE.test(cur) && cur !== 'RU' && !list.some(([c]) => c === cur)) list.push([cur, cur])
  return list.map(([code, name]) => ({ code, label: `${flag(code)} ${name}` }))
}

// geoHint: the probe's registry country next to the chosen one (the owner decides).
export function geoHint(selected, geo) {
  const g = String(geo || '').toUpperCase()
  if (!CC_RE.test(g)) return null
  const sel = String(selected || '').toUpperCase()
  return { code: g, matches: g === sel, text: g === sel ? `Совпадает с реестром адресов: ${countryLabel(g)}` : `По реестру адресов этот IP — ${countryLabel(g)}` }
}

// PREP_PORTS: what to let in at the hoster's firewall (the Hub checks the TCP ones before the
// install: internal/admin/reach.go hubReachPorts).
export const PREP_PORTS = {
  exit: [
    { proto: 'TCP', port: '22', note: 'SSH (или ваш порт) — Hub настраивает сервер' },
    { proto: 'TCP', port: '50052', note: 'только с адреса Hub — управление нодой' },
    { proto: 'UDP', port: '50443', note: 'основной туннель от Hub' },
    { proto: 'TCP', port: '443', note: 'запасной туннель от Hub' },
  ],
  relay: [
    { proto: 'TCP', port: '22', note: 'SSH (или ваш порт) — Hub настраивает сервер' },
    { proto: 'TCP', port: '443', note: 'подключения пользователей и сайт' },
    { proto: 'TCP', port: '80', note: 'сайт (переадресация на HTTPS)' },
    { proto: 'TCP', port: '2443', note: 'второй способ подключения' },
    { proto: 'UDP', port: '30000–65000', note: 'быстрый способ подключения (UDP)' },
  ],
}
export const NO_PASSWORD_NOTE = 'Пароль root не нужен, если ключ Hub вставлен при создании сервера.'

// ── the owner's numbers (card «Настройки», the Hub tab) ──

export const OWNER_FIELDS = [
  { key: 'traffic_quota_tb', label: 'Лимит трафика у хостера, ТБ в месяц', hint: '0 или пусто — безлимит', decimal: true },
  { key: 'monthly_price_rub', label: 'Цена, ₽ в месяц', hint: 'Для «Мощности»: стоимость на активного пользователя' },
  { key: 'port_mbps', label: 'Скорость порта, Мбит/с', hint: 'Пусто — 1000. Канал выше половины порта — пора докупать' },
  { key: 'cpu_cores', label: 'Ядра', hint: 'Пусто — сколько видит сервер (nproc)' },
]

export function ownerForm(owner) {
  const f = {}
  for (const { key } of OWNER_FIELDS) f[key] = owner?.[key] == null ? '' : String(owner[key])
  return f
}

const RANGES = {
  traffic_quota_tb: [0, 10000, 'лимит трафика: от 0 до 10 000 ТБ'],
  monthly_price_rub: [0, 1000000, 'цена: от 0 до 1 000 000 ₽'],
  port_mbps: [1, 100000, 'порт: от 1 до 100 000 Мбит/с'],
  cpu_cores: [1, 256, 'ядра: от 1 до 256'],
}

// ownerBody: the PUT body (empty = null) or the first error in words.
export function ownerBody(form) {
  const body = {}
  for (const { key, decimal } of OWNER_FIELDS) {
    const raw = String(form?.[key] ?? '').trim().replace(',', '.')
    if (raw === '') { body[key] = null; continue }
    const n = Number(raw)
    const [lo, hi, msg] = RANGES[key]
    if (!Number.isFinite(n) || (!decimal && !Number.isInteger(n)) || n < lo || n > hi) return { body: null, error: `Проверьте ${msg}` }
    body[key] = n
  }
  return { body, error: '' }
}

// ── charts ──

export const LOAD_RANGES = [
  { key: '24h', label: '24 часа' },
  { key: '7d', label: '7 дней' },
]

// niceMax: a round top for a chart of non-percent values.
export function niceMax(v) {
  if (!num(v) || v <= 0) return 1
  const p = 10 ** Math.floor(Math.log10(v))
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * p) return m * p
  return 10 * p
}

// chartGeometry: the SVG paths of a series over the points (x by time, y by value; a missing
// value breaks the line). max = 100 for percentages.
export function chartGeometry(points, pick, { width = 300, height = 80, max } = {}) {
  const vals = (points || []).map((p) => { const v = pick(p); return num(v) ? v : null })
  const peak = Math.max(0, ...vals.filter((v) => v != null))
  const top = num(max) ? max : niceMax(peak)
  const times = (points || []).map((p) => Date.parse(p.at))
  const t0 = Math.min(...times)
  const t1 = Math.max(...times)
  const span = t1 > t0 ? t1 - t0 : 1
  const x = (i) => (times.length > 1 ? ((times[i] - t0) / span) * width : width / 2)
  const y = (v) => height - (Math.min(v, top) / top) * height
  let line = ''
  let area = ''
  let run = []
  const flush = () => {
    if (run.length === 0) return
    line += run.map(([px, py], j) => `${j ? 'L' : 'M'}${px.toFixed(1)},${py.toFixed(1)}`).join('')
    area += `M${run[0][0].toFixed(1)},${height}` + run.map(([px, py]) => `L${px.toFixed(1)},${py.toFixed(1)}`).join('') + `L${run[run.length - 1][0].toFixed(1)},${height}Z`
    run = []
  }
  vals.forEach((v, i) => { if (v == null) flush(); else run.push([x(i), y(v)]) })
  flush()
  const last = [...vals].reverse().find((v) => v != null)
  return { line, area, top, peak, last: last ?? null, empty: vals.every((v) => v == null) }
}

// ── «Мощность» ──

export const VERDICT_TONE = { ok: 'success', warn: 'warning', nodata: 'default' }
export const ROLE_TITLE = { hub: 'Hub', exit: 'Выходы за рубежом', relay: 'Входы в РФ' }

const FLAG_WORDS = { cpu: 'CPU', net: 'канал', conntrack: 'conntrack', quota: 'трафик' }

// capacityNodeRow: one server of a group in the «Мощность» table.
export function capacityNodeRow(n) {
  const quota = num(n.quota_tb) && n.quota_tb > 0
  return {
    id: n.id,
    name: n.name,
    noData: !n.samples,
    cpu: { text: fmtPct(n.cpu_peak_pct), warn: (n.flags || []).includes('cpu') },
    net: { text: `${fmtMbps(n.net_peak_mbps)} Мбит/с · ${fmtPct(n.net_pct)} порта`, warn: (n.flags || []).includes('net') },
    conntrack: { text: n.role === 'relay' ? fmtPct(n.conntrack_pct) : '—', warn: (n.flags || []).includes('conntrack') },
    month: {
      text: quota ? `${(n.month_tb || 0).toFixed(2)} из ${n.quota_tb} ТБ (${fmtPct(n.quota_pct)})` : `${(n.month_tb || 0).toFixed(2)} ТБ · безлимит`,
      warn: (n.flags || []).includes('quota'),
    },
    price: num(n.price_rub) && n.price_rub > 0 ? `${fmtInt(n.price_rub)} ₽` : '—',
    flags: (n.flags || []).map((f) => FLAG_WORDS[f] || f),
  }
}

// capacitySummary: the money and the users of the report in one line each.
export function capacitySummary(rep) {
  if (!rep) return null
  return {
    cost: rep.monthly_cost_rub > 0 ? `${fmtInt(rep.monthly_cost_rub)} ₽ в месяц за серверы` : 'Цены серверов не указаны — впишите их в «Настройках» серверов',
    perUser: rep.cost_per_user_rub > 0 ? `${String(rep.cost_per_user_rub).replace('.', ',')} ₽ на активного пользователя` : '',
    users: rep.capacity_users_day > 0
      ? `Активных за сутки ${fmtInt(rep.active_users_day)} из расчётных ~${fmtInt(rep.capacity_users_day)}`
      : `Активных за сутки ${fmtInt(rep.active_users_day)}`,
    usersWarn: rep.capacity_users_day > 0 && rep.active_users_day > rep.capacity_users_day * 0.7,
  }
}

// ── «Запуск» ──

// launchView: the checklist folds away once complete; next is the first step not done.
export function launchView(data) {
  const items = data?.items || []
  const next = items.find((it) => !it.done) || null
  return {
    items,
    next,
    progress: `${data?.done ?? items.filter((it) => it.done).length} из ${data?.total ?? items.length}`,
    collapsed: !!data?.complete,
  }
}
