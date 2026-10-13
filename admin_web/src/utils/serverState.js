// Truthful words for node state (phase 1): lifecycle from servers.lifecycle_state, paths from the
// tunnel monitor. Anything unexpected is shown as it is, never as «В работе».
export const LIFECYCLE = {
  pending: { variant: 'default', label: 'Ожидает настройки' },
  provisioning: { variant: 'info', label: 'Настраивается' },
  online: { variant: 'success', label: 'В работе' },
  failed: { variant: 'danger', label: 'Ошибка настройки' },
  deprovisioning: { variant: 'warning', label: 'Выводится' },
  offline: { variant: 'default', label: 'Выключена' },
}

export function lifecycleView(state) {
  return Object.hasOwn(LIFECYCLE, state)
    ? LIFECYCLE[state]
    : { variant: 'warning', label: state ? `Неизвестно: ${state}` : 'Нет данных' }
}

export const PATH = {
  healthy: { variant: 'success', label: 'работает' },
  unhealthy: { variant: 'danger', label: 'не работает' },
  unknown: { variant: 'warning', label: 'проверяется' },
  absent: { variant: 'default', label: 'нет' },
}

export function pathView(status) {
  return Object.hasOwn(PATH, status) ? PATH[status] : PATH.unknown
}

// inWork: a node that actually carries user traffic.
export function inWork(s) {
  return s.lifecycle_state === 'online' && s.tunnel_status === 'healthy'
}

// The list filter: the same values GET /api/admin/servers?lifecycle_state= accepts.
export const LIFECYCLE_FILTER = [
  { value: '', label: 'Все состояния' },
  ...Object.entries(LIFECYCLE).map(([value, v]) => ({ value, label: v.label })),
]
