// «Работы» (GET /api/admin/ops/works): what needs attention on each node, the age of its facts.

const MIN = 60000
const HOUR = 60 * MIN
const DAY = 24 * HOUR
const STALE_AFTER = 26 * HOUR // the daily collection plus a margin
const REBOOT_DAYS = 14
const DISK_WARN = 85

export function factsAge(collectedAt, now = Date.now()) {
  if (!collectedAt) return { label: 'нет данных', stale: true }
  const age = Math.max(0, now - Date.parse(collectedAt))
  let label
  if (age < HOUR) label = `${Math.max(1, Math.round(age / MIN))} мин назад`
  else if (age < DAY) label = `${Math.round(age / HOUR)} ч назад`
  else label = `${Math.floor(age / DAY)} дн назад`
  return { label, stale: age > STALE_AFTER }
}

export function nodeIssues(node, now = Date.now()) {
  const f = node.facts
  if (!f) return ['сведений нет']
  const out = []
  if (f.reboot_required && f.reboot_required_since) {
    const days = Math.floor((now - Date.parse(f.reboot_required_since)) / DAY)
    if (days > REBOOT_DAYS) out.push(`ждёт перезагрузки ${days} дн.`)
  }
  if (f.security_updates > 0) out.push(`обновлений безопасности: ${f.security_updates}`)
  if (f.ssh_password) out.push('вход по паролю включён')
  if (f.disk_pct >= DISK_WARN) out.push(`диск ${f.disk_pct} %`)
  if (factsAge(f.collected_at, now).stale) out.push('сведения устарели')
  return out
}

const ROLE_ORDER = { hub: 0, exit: 1, relay: 2 }

export function sortNodes(nodes, now = Date.now()) {
  return [...nodes].sort((a, b) => {
    const ia = nodeIssues(a, now).length
    const ib = nodeIssues(b, now).length
    if ((ia > 0) !== (ib > 0)) return ia > 0 ? -1 : 1
    const ra = ROLE_ORDER[a.role] ?? 9
    const rb = ROLE_ORDER[b.role] ?? 9
    if (ra !== rb) return ra - rb
    return a.id - b.id
  })
}

export function roleLabel(role) {
  return { hub: 'Хаб', exit: 'Выход', relay: 'Вход' }[role] || role
}
