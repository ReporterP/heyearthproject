// Small pure helpers over the cabinet data (useAccount parts).
import { daysLeft } from './format.js'

// GET /api/v1/user/subscription → { items } (null when empty): the active one, else the newest.
export function currentSub(subscription) {
  const items = subscription?.items || []
  return items.find((s) => s.active) || items[0] || null
}

// active | expired | none
export function subState(sub) {
  if (!sub) return 'none'
  return sub.active ? 'active' : 'expired'
}

// /connect?sub=<token>: the token is the last segment of the subscription URL
export function subToken(url) {
  if (!url) return ''
  const parts = String(url).split('/')
  return parts[parts.length - 1] || ''
}
export const happHref = (url) => (url ? `/connect?sub=${encodeURIComponent(subToken(url))}` : '')

// share of the term left (02 §23): days left / plan days, at least 2 %
// the term is valid_until − started_at when the API gives the start (traffic.subscription), else the
// plan's duration; unknown → no bar
export function termShare(sub, plan, started) {
  const left = daysLeft(sub?.valid_until)
  const s = started ? new Date(started).getTime() : NaN
  const e = sub?.valid_until ? new Date(sub.valid_until).getTime() : NaN
  const total = Number.isFinite(s) && Number.isFinite(e) && e > s ? (e - s) / 86400000 : Number(plan?.duration_days) || 0
  return { left, pct: total ? Math.max(2, Math.min(100, (left / total) * 100)) : null }
}

// «Как начать» (02 §33 chk): ticks kept in the browser per account
const KEY = (id) => `he_checklist_${id || 'anon'}`
export function readChecklist(id) {
  try { return JSON.parse(localStorage.getItem(KEY(id)) || '{}') || {} } catch { return {} }
}
export function writeChecklist(id, v) {
  try { localStorage.setItem(KEY(id), JSON.stringify(v)) } catch { /* storage off */ }
}
