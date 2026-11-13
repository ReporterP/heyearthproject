import { Badge } from './ui/index.js'

// StatusBadge — a status word in a ui Badge (C11): the variant by meaning, the label in Russian.
const VARIANT = {
  active: 'success', healthy: 'success', online: 'success', paid: 'success', succeeded: 'success',
  inactive: 'default', offline: 'default', canceled: 'default', unknown: 'default',
  blocked: 'danger', unhealthy: 'danger', failed: 'danger',
  pending: 'warning', configuring: 'warning', provisioning: 'warning', maintenance: 'warning', waiting_for_capture: 'warning',
  refunded: 'info',
}
const LABEL = {
  active: 'активна', healthy: 'работает', online: 'онлайн', paid: 'оплачен', succeeded: 'оплачен',
  inactive: 'неактивна', offline: 'офлайн', canceled: 'отменён', unknown: 'нет данных',
  blocked: 'заблокирован', unhealthy: 'не работает', failed: 'ошибка',
  pending: 'ожидает', configuring: 'настраивается', provisioning: 'настраивается', maintenance: 'обслуживание',
  waiting_for_capture: 'ждёт подтверждения', refunded: 'возвращён',
}

export default function StatusBadge({ status }) {
  const key = String(status || '').toLowerCase().replace(/\s+/g, '_')
  return <Badge variant={VARIANT[key] || 'default'}>{LABEL[key] || status || '—'}</Badge>
}
