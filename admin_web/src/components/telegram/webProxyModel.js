// Telegram WEB proxy card as pure helpers (node --test).
// Input: GET /api/admin/telegram/webproxy (settings + status + the last run; the secret is never sent).

export const RUN_POLL_MS = 3000
export const IDLE_POLL_MS = 30000

export const isRunning = (d) => d?.run?.status === 'running'
export const pollDelay = (d) => (isRunning(d) ? RUN_POLL_MS : IDLE_POLL_MS)

// overallState: the badge next to «WEB-прокси». Working = subscribers see the link AND the relay is ready.
export function overallState(d) {
  if (d?.link_ready && d?.status?.readyz === true) return { label: 'Работает', variant: 'success' }
  if (!d?.enabled) return { label: 'Выключен', variant: 'default' }
  return { label: 'Не готов', variant: 'warning', hint: notReadyHint(d) }
}

function notReadyHint(d) {
  if (isRunning(d)) return 'Идёт установка — состояние обновится само.'
  if (d?.artifact && !d.artifact.present) return 'На сервере нет сборки tproxy-server — установка невозможна.'
  if (!d?.link_ready) return 'Ссылка подписчикам ещё не выдаётся: нужна успешная установка на текущий адрес («Установить / обновить»).'
  if (!d?.status) return 'Проверки ещё не было.'
  return d.status.readyz_error ? `Relay не готов: ${d.status.readyz_error}` : 'Relay не готов.'
}

// The form (enabled + host). An empty host field means the default address (it is the placeholder).
export const formFrom = (d) => ({ enabled: !!d?.enabled, host: d?.host || '' })
export const effectiveHost = (host, defaultHost) => String(host || '').trim().toLowerCase() || String(defaultHost || '').trim().toLowerCase()
export const payloadFrom = (form, d) => ({ enabled: !!form.enabled, host: effectiveHost(form.host, d?.default_host) })

export function isDirty(form, d) {
  if (!form || !d) return false
  return !!form.enabled !== !!d.enabled || effectiveHost(form.host, d.default_host) !== effectiveHost(d.host, d.default_host)
}

const HOST_RE = /^(?=.{4,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$/

// hostError: '' when the host is a plain DNS name (empty = the default one).
export function hostError(host) {
  const h = String(host || '').trim().toLowerCase()
  if (!h) return ''
  if (/^[a-z]+:\/\//.test(h) || h.includes('/')) return 'Только имя хоста — без https:// и пути'
  if (h.includes(':')) return 'Без порта: WEB-прокси всегда на 443'
  if (!HOST_RE.test(h)) return 'Не похоже на имя хоста (пример: tg.example.ru)'
  return ''
}

// saveCheck: may «Сохранить» be pressed, and does it need the «выключить?» confirmation.
export function saveCheck(form, d, { canEdit = false } = {}) {
  const dirty = isDirty(form, d)
  const confirmOff = dirty && !!d?.enabled && !form.enabled
  let reason = ''
  if (!canEdit) reason = 'Нужно право «settings.write»'
  else if (!dirty) reason = 'Нет изменений'
  else if (isRunning(d)) reason = 'Дождитесь окончания текущего запуска'
  else reason = hostError(form.host)
  return { dirty, canSave: dirty && !reason, reason, confirmOff }
}

// saveMessage: the toast after a successful PUT.
export function saveMessage(prev, next) {
  if (prev?.enabled && !next.enabled) return 'Выключается: ссылка скрыта, relay и MTProxy останавливаются'
  if (!prev?.enabled && next.enabled) return 'Включено — запущена установка'
  if (next.enabled) return 'Адрес сохранён — запущена установка на новом адресе'
  return 'Адрес сохранён — применится при включении'
}

// ── formatting ──

const NBSP = ' '

export function fmtInt(n) {
  if (n == null || !Number.isFinite(Number(n))) return '—'
  return String(Math.round(Number(n))).replace(/\B(?=(\d{3})+(?!\d))/g, NBSP)
}

const UNITS = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ']

export function humanBytes(n) {
  if (n == null || !Number.isFinite(Number(n))) return '—'
  let v = Math.max(0, Number(n))
  let i = 0
  while (v >= 1024 && i < UNITS.length - 1) { v /= 1024; i++ }
  const text = i === 0 || v >= 100 ? String(Math.round(v)) : v.toFixed(1).replace('.', ',')
  return `${text}${NBSP}${UNITS[i]}`
}

// plural: 1 день, 2 дня, 5 дней (Russian rules).
export function plural(n, [one, few, many]) {
  const a = Math.abs(n) % 100
  const b = a % 10
  if (a > 10 && a < 20) return many
  if (b === 1) return one
  if (b >= 2 && b <= 4) return few
  return many
}

export const daysText = (n) => `${n}${NBSP}${plural(n, ['день', 'дня', 'дней'])}`

export function fmtDateTime(iso) {
  if (!iso) return '—'
  const t = Date.parse(iso)
  return Number.isNaN(t) ? String(iso) : new Date(t).toLocaleString('ru-RU')
}

// agoText: «только что», «5 мин назад», «3 ч назад», else the local date and time.
export function agoText(iso, now = Date.now()) {
  if (!iso) return '—'
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return String(iso)
  const s = Math.floor((now - t) / 1000)
  if (s < 0) return fmtDateTime(iso)
  if (s < 60) return 'только что'
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`
  if (s < 86400) return `${Math.floor(s / 3600)} ч назад`
  return fmtDateTime(iso)
}

// ── «Состояние» rows. tone: default | success | warning | danger (a text colour in the card). ──

export const probeView = (ok) => (ok == null
  ? { text: '—', tone: 'default' }
  : ok ? { text: 'ok', tone: 'success' } : { text: 'нет', tone: 'danger' })

// The three units the install puts on the Hub, in this order; any other unit the API reports follows.
export const SERVICE_UNITS = ['tproxy-server', 'mtproxy', 'refresh-mtproxy-config.timer']

export function serviceView(state, enabled) {
  if (state === 'active') return { text: 'active', tone: 'success' }
  if (state === 'failed') return { text: 'failed', tone: 'danger' }
  if (!state) return { text: '—', tone: enabled ? 'warning' : 'default' }
  return { text: state, tone: enabled ? 'warning' : 'default' }
}

export function servicesList(services, enabled) {
  const map = services && typeof services === 'object' ? services : {}
  const units = [...SERVICE_UNITS, ...Object.keys(map).filter((u) => !SERVICE_UNITS.includes(u)).sort()]
  return units.map((unit) => ({ unit, ...serviceView(map[unit], enabled) }))
}

export function disconnectsView(n) {
  if (n == null || !Number.isFinite(Number(n))) return { text: '—', tone: 'default' }
  return { text: fmtInt(n), tone: Number(n) > 0 ? 'warning' : 'default' }
}

export function certView(cert) {
  if (!cert) return { text: '—', tone: 'default' }
  if (!cert.present) return { text: 'нет', tone: 'warning', detail: cert.error || '' }
  const days = Number(cert.days_left)
  const detail = cert.error || ''
  if (!Number.isFinite(days)) return { text: 'есть', tone: 'default', detail }
  if (days <= 0) return { text: 'истёк', tone: 'danger', detail }
  return { text: `${daysText(days)} до конца`, tone: days < 14 ? 'warning' : 'success', detail }
}

// dnsView: the A records against the expected entry addresses (the same as go.<домен>).
export function dnsView(dns) {
  if (!dns) return null
  const a = Array.isArray(dns.a) ? dns.a : []
  const expected = Array.isArray(dns.expected) ? dns.expected : []
  const aaaa = Array.isArray(dns.aaaa) ? dns.aaaa : []
  return {
    a, expected, aaaa,
    ok: !!dns.ok,
    missing: expected.filter((ip) => !a.includes(ip)),
    extra: a.filter((ip) => !expected.includes(ip)),
    problem: dns.ok ? '' : (dns.problem || 'A-записи адреса не совпадают с адресами входов'),
  }
}

// ── «Последний запуск» ──

export const RUN_KIND = { install: 'Установка', disable: 'Выключение' }

const RUN_STATUS = {
  running: { label: 'Выполняется', variant: 'info' },
  succeeded: { label: 'Готово', variant: 'success' },
  failed: { label: 'Ошибка', variant: 'danger' },
}

export function runView(run) {
  if (!run) return null
  const st = Object.hasOwn(RUN_STATUS, run.status) ? RUN_STATUS[run.status] : { label: run.status || '—', variant: 'warning' }
  return { kind: Object.hasOwn(RUN_KIND, run.kind) ? RUN_KIND[run.kind] : (run.kind || 'Запуск'), ...st }
}

const STEP_STATUS = {
  pending: { label: 'ждёт', variant: 'default' },
  running: { label: 'выполняется', variant: 'info' },
  ok: { label: 'в порядке', variant: 'success' },
  done: { label: 'сделано', variant: 'success' },
  skipped: { label: 'пропущено', variant: 'default' },
  failed: { label: 'ошибка', variant: 'danger' },
  rolled_back: { label: 'откачено', variant: 'danger' },
}

export function stepView(status) {
  return Object.hasOwn(STEP_STATUS, status) ? { ...STEP_STATUS[status] } : { label: status || '—', variant: 'warning' }
}

export const stepKey = (step, i) => step?.name || `step-${i}`

// autoOpenStep: the step expanded unless the owner chose — the first problem, else the running one.
export function autoOpenStep(run) {
  const steps = run?.steps || []
  const i = steps.findIndex((s) => s?.status === 'failed' || s?.status === 'rolled_back')
  const j = i >= 0 ? i : steps.findIndex((s) => s?.status === 'running')
  return j >= 0 ? stepKey(steps[j], j) : null
}
