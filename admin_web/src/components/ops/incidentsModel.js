// «Журнал» (GET /api/admin/ops/incidents): how an incident reads on the console.

const MIN = 60000
const MSK = 3 * 60 * MIN // Moscow is UTC+3 all year

function msk(ms) {
  const d = new Date(ms + MSK)
  const p = (n) => String(n).padStart(2, '0')
  return { date: `${p(d.getUTCDate())}.${p(d.getUTCMonth() + 1)}`, time: `${p(d.getUTCHours())}:${p(d.getUTCMinutes())}`,
    file: `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())}-${p(d.getUTCHours())}${p(d.getUTCMinutes())}` }
}

function span(ms) {
  const m = Math.max(0, Math.round(ms / MIN))
  if (m < 60) return `${m} мин`
  return `${Math.floor(m / 60)} ч${m % 60 ? ` ${m % 60} мин` : ''}`
}

// An open incident's `to` is Go's zero time (year 1).
const isZero = (iso) => !iso || iso.startsWith('0001-')

export function incidentView(inc, now = Date.now()) {
  const from = Date.parse(inc.from)
  const open = isZero(inc.to)
  const to = open ? now : Date.parse(inc.to)
  const a = msk(from)
  const when = open ? `${a.date} ${a.time} МСК — сейчас`
    : msk(to).date === a.date ? `${a.date} ${a.time}–${msk(to).time} МСК` : `${a.date} ${a.time} — ${msk(to).date} ${msk(to).time} МСК`
  const critical = inc.level === 'critical'
  return {
    open,
    when,
    duration: open ? `идёт ${span(to - from)}` : span(to - from),
    levelLabel: critical ? 'критичная' : 'внимание',
    tone: critical ? 'danger' : 'warning',
    filename: `incident-${a.file}.md`,
  }
}
