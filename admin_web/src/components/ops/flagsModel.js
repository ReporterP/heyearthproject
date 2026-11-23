// «Операции → Подписка»: pure helpers of the flags, the canary and the client kinds
// (GET /api/admin/ops/flags, the «clients» runs of GET /api/admin/ops/probes).
import { wayLabel, targetLabel } from './opsModel.js'

const STATE = { off: 'выключено', test: 'тест', on: 'всем' }

export function flagStateLabel(state) {
  return STATE[state] || state
}

const PHASE = {
  test: 'Выдержка на тестовом пользователе',
  ready: 'PASS: можно «Всем»',
  guard: 'Страховочный час',
  done: 'Завершено',
  rolled_back: 'Откат',
  aborted: 'Отменено',
}

const SOAK_MIN = 30
const GUARD_MIN = 60

// canaryView: what the card shows for a change at nowMs.
export function canaryView(change, nowMs) {
  const c = change || {}
  let minutesLeft = Number.isFinite(c.minutes_left) ? c.minutes_left : 0
  if (!Number.isFinite(c.minutes_left)) {
    const from = c.phase === 'test' ? Date.parse(c.started_at) : Date.parse(c.phase_at)
    const span = c.phase === 'test' ? SOAK_MIN : c.phase === 'guard' ? GUARD_MIN : 0
    if (span && Number.isFinite(from)) minutesLeft = Math.max(0, Math.ceil((from + span * 60000 - nowMs) / 60000))
  }
  const verdict = c.verdict || c.note || ''
  let verdictTone = 'default'
  if (c.phase === 'ready') verdictTone = 'success'
  else if (c.phase === 'rolled_back' || (c.phase === 'test' && verdict.startsWith('FAIL'))) verdictTone = 'danger'
  else if ((c.blind_streak || 0) >= 3 || (c.phase === 'guard' && (c.bad_streak || 0) > 0)) verdictTone = 'warning'
  return { phaseLabel: PHASE[c.phase] || c.phase || '', minutesLeft, canPromote: c.phase === 'ready', verdictTone, verdict }
}

// checkLabel: «line/way/target» in words.
function checkLabel(key) {
  const [line, way, target] = String(key).split('/')
  return [line, way ? wayLabel(way) : '', targetLabel(target)].filter(Boolean).join(' · ')
}

// diffChecks: what broke after the change (worse) and what was broken before it (unchanged).
export function diffChecks(baseline, newFailing) {
  return { worse: (newFailing || []).map(checkLabel), unchanged: (baseline || []).map(checkLabel) }
}

export function bypassAllowed(reason) {
  return String(reason || '').trim().length >= 10
}

// clientKindsSummary: the newest «clients» run, one row per app kind, sorted by name.
export function clientKindsSummary(runs) {
  const run = (runs || []).filter(r => r.vantage === 'clients').sort((a, b) => Date.parse(b.at) - Date.parse(a.at))[0]
  if (!run) return []
  const kinds = new Map()
  for (const r of run.results || []) {
    const [name] = String(r.line).split(': ')
    const k = kinds.get(name) || { name, lines: 0, accepted: 0, bulkOK: null, errors: [], at: run.at }
    if (r.target === 'parse') {
      k.lines++
      if (r.ok) k.accepted++
      else if (r.error) k.errors.push(r.error)
    } else if (r.target === 'bulk') {
      k.bulkOK = (k.bulkOK ?? true) && r.ok
    }
    kinds.set(name, k)
  }
  return [...kinds.values()].sort((a, b) => a.name.localeCompare(b.name))
}
