// The friend's code from a /r/<code> link is kept in the browser for 30 days and sent with
// the registration (cleanup plan R2).
const KEY = 'hiearth_ref'
const TTL_MS = 30 * 24 * 60 * 60 * 1000

export function rememberReferral(code) {
  const c = String(code || '').trim().toUpperCase()
  if (!/^[A-Z0-9]{4,16}$/.test(c)) return
  try { localStorage.setItem(KEY, JSON.stringify({ code: c, at: Date.now() })) } catch { /* storage off */ }
}

export function pendingReferral() {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || 'null')
    if (!v || !v.code || Date.now() - v.at > TTL_MS) return ''
    return v.code
  } catch {
    return ''
  }
}

export function forgetReferral() {
  try { localStorage.removeItem(KEY) } catch { /* storage off */ }
}
