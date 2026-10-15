// The Bamboo (Hysteria2 on the Hub) badge for GET /api/admin/hysteria2/status: deployment first,
// then the outside probe (a failed probe means users cannot connect, whatever else is true),
// then sync errors. "No probe yet" is never shown as working.
export function bambooVerdict(st) {
  if (!st) return { variant: 'default', label: 'нет данных' }
  if (!st.binary_installed || !st.config_present) return { variant: 'default', label: 'не развёрнут' }
  if (!st.active_plans) return { variant: 'default', label: 'нет тарифов с Бамбуком' }
  if (st.probe && !st.probe.ok) return { variant: 'danger', label: 'не отвечает снаружи' }
  if (st.last_error) return { variant: 'warning', label: 'ошибка синхронизации' }
  if (st.probe?.ok) return { variant: 'success', label: 'работает' }
  return { variant: 'warning', label: 'внешняя проба ещё не прошла' }
}

const HOUR = 3600000
const DAY = 24 * HOUR

// ageText: how long ago an RFC3339 moment was («3 дн 4 ч назад»). An unparseable value or one in
// the future (clock skew) is returned as is rather than turned into a made-up age.
export function ageText(iso, now = Date.now()) {
  if (!iso) return 'не было'
  const ms = now - Date.parse(iso)
  if (!(ms >= 0)) return iso
  const d = Math.floor(ms / DAY)
  const h = Math.floor((ms % DAY) / HOUR)
  if (d > 0) return `${d} дн ${h} ч назад`
  return h > 0 ? `${h} ч назад` : 'меньше часа назад'
}
