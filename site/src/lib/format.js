// Formats of spec 05 §4: money with a thin-space-free «10 000 ₽», dates «14 февраля» (year only when
// it is not the current one), «14 ноября, 21:47», traffic «38,4 ГБ», plural forms.
const NBSP = ' '

export function plural(n, forms) {
  const a = Math.abs(n) % 100
  const b = a % 10
  if (a > 10 && a < 20) return forms[2]
  if (b > 1 && b < 5) return forms[1]
  if (b === 1) return forms[0]
  return forms[2]
}

// group: 1000 → «1 000» (non-breaking space), for every amount (05 §4 shows «1 000 ₽»).
export function group(n) {
  const neg = n < 0
  const [i, f] = Math.abs(n).toFixed(2).split('.')
  const int = i.replace(/\B(?=(\d{3})+(?!\d))/g, NBSP)
  const frac = f === '00' ? '' : `,${f}`
  return `${neg ? '−' : ''}${int}${frac}`
}

// money(rubles) → «720 ₽»; kop(kopecks) → «720 ₽»
export const money = (rub) => `${group(Number(rub) || 0)}${NBSP}₽`
export const kop = (k) => money((Number(k) || 0) / 100)
export const kopNum = (k) => Math.round((Number(k) || 0) / 100)

// signed amount for history rows: «+1 000 ₽» / «−720 ₽» (U+2212)
export const signed = (rub) => `${rub >= 0 ? '+' : '−'}${group(Math.abs(rub))}${NBSP}₽`

const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря']
const MONTHS_NOM = ['Январь', 'Февраль', 'Март', 'Апрель', 'Май', 'Июнь', 'Июль', 'Август', 'Сентябрь', 'Октябрь', 'Ноябрь', 'Декабрь']

function toDate(v) {
  if (!v) return null
  const d = v instanceof Date ? v : new Date(v)
  return Number.isNaN(d.getTime()) || d.getFullYear() < 1971 ? null : d
}

export function date(v, { year } = {}) {
  const d = toDate(v)
  if (!d) return ''
  const withYear = year ?? d.getFullYear() !== new Date().getFullYear()
  return `${d.getDate()}${NBSP}${MONTHS[d.getMonth()]}${withYear ? ` ${d.getFullYear()}` : ''}`
}

export function dateTime(v) {
  const d = toDate(v)
  if (!d) return ''
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${date(d)}, ${hh}:${mm}`
}

export function time(v) {
  const d = toDate(v)
  if (!d) return ''
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

export const monthName = (v) => { const d = toDate(v); return d ? MONTHS_NOM[d.getMonth()] : '' }

// days left until `until` (rounded up, never negative)
export function daysLeft(until) {
  const d = toDate(until)
  if (!d) return 0
  return Math.max(0, Math.ceil((d.getTime() - Date.now()) / 86400000))
}

// «38,4 ГБ» — one decimal with a comma; below 1 GB in MB
export function traffic(bytes) {
  const b = Number(bytes) || 0
  const gb = b / 1024 ** 3
  if (gb >= 1) return `${gb.toFixed(1).replace('.', ',')}${NBSP}ГБ`
  const mb = b / 1024 ** 2
  return `${mb >= 10 ? Math.round(mb) : mb.toFixed(1).replace('.', ',')}${NBSP}МБ`
}

export function trafficParts(bytes) {
  const s = traffic(bytes)
  const i = s.lastIndexOf(NBSP)
  return [s.slice(0, i), s.slice(i + 1)]
}

// «7 дней», «1 месяц», «3 месяца», «12 месяцев», «1 год»
export function term(days) {
  const d = Number(days) || 0
  if (d >= 360 && d % 365 < 10) { const y = Math.round(d / 365); return `${y}${NBSP}${plural(y, ['год', 'года', 'лет'])}` }
  if (d >= 28) { const m = Math.round(d / 30); return `${m}${NBSP}${plural(m, ['месяц', 'месяца', 'месяцев'])}` }
  return `${d}${NBSP}${plural(d, ['день', 'дня', 'дней'])}`
}

export const days = (n) => `${n}${NBSP}${plural(n, ['день', 'дня', 'дней'])}`
