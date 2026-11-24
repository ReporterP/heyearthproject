// Console actions (GET /api/admin/ops/actions, POST …/{id}/preview): what kind of control an
// action id of the alert catalogue becomes, and the lines of the one confirmation dialog.

// Pages of other sessions and other parts of the console: a link, not a button.
const LINKS = [
  [/^entry\./, '/entries'],
  [/^exit\.(reprovision|verify)$/, '/servers'],
  [/^hub\.address$/, '/servers'],
  [/^payments\./, '/payments'],
  [/^servers\.capacity$/, '/servers'],
  [/^subscription\./, '/operations?tab=subscription'],
  [/^settings\./, '/operations?tab=settings'],
  [/^node\.facts$/, '/operations?tab=works'],
]

export function actionLink(id) {
  for (const [re, path] of LINKS) if (re.test(id)) return path
  return ''
}

export function actionKind(id, registry = []) {
  if (registry.some(a => a.id === id)) return 'button'
  if (actionLink(id)) return 'link'
  return 'text'
}

const IMPACT = { K0: 'К0', K1: 'К1', K2: 'К2', K3: 'К3' }

export function confirmView(p) {
  const known = typeof p.affected_now === 'number'
  const affected = known
    ? `Заденет сейчас: ${p.affected_now}${p.affected_note ? ` (${p.affected_note})` : ''}`
    : (p.affected_note || 'Число неизвестно — считать, что заденет всех')
  const disruptive = p.impact === 'K2' || p.impact === 'K3'
  return {
    title: p.title,
    impactLabel: IMPACT[p.impact] || p.impact,
    impactNote: p.impact_note || '',
    affected,
    pre: p.pre || [],
    post: p.post || [],
    ifFails: p.if_fails || '',
    window: `Лучшее окно: ${p.best_window || '04:00–05:30'} МСК`,
    word: p.confirm_word || '',
    defaultWhen: disruptive ? 'window' : 'now',
    dryRun: p.mode === 'dry-run',
    missing: p.allowed ? '' : `Не хватает права ${p.missing_perm || ''}`.trim(),
    retryAfter: p.retry_after_sec || 0,
  }
}

export function canRunNow(p, typed) {
  if (!p.allowed) return false
  if ((p.retry_after_sec || 0) > 0) return false
  if ((p.pre || []).some(c => !c.ok)) return false
  if (p.confirm_word && typed !== p.confirm_word) return false
  return true
}

// targetFromKey: the server id inside an alert key (exit-down-3, relay-5-unstable, …:4).
export function targetFromKey(key = '') {
  const m = String(key).match(/(?:^|[-:])(\d+)(?:$|[-:.])/)
  return m ? m[1] : ''
}

// canSchedule: planning for the window needs the permission and, for K3, the word (the server
// checks both); the checks before and the gap are taken again at the window.
export function canSchedule(p, typed) {
  if (!p.allowed) return false
  if (p.confirm_word && typed !== p.confirm_word) return false
  return true
}
