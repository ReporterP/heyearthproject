// Badge (spec 02 §11): status capsule with a dot. Not clickable.
//   <Badge tone="ok|warn|bad|info|plain">Активна</Badge> — no tone = neutral.
export default function Badge({ tone, children, className = '', style }) {
  return <span className={`badge ${tone ? `badge--${tone}` : ''} ${className}`} style={style}>{children}</span>
}

// Status → badge for payments (05 §6).
export function paymentBadge(status) {
  switch (status) {
    case 'succeeded': return { tone: 'ok', text: 'Оплачено' }
    case 'pending':
    case 'waiting_for_capture': return { tone: 'warn', text: 'Ожидание' }
    case 'canceled': return { tone: undefined, text: 'Отменён' }
    case 'rejected':
    case 'failed': return { tone: 'bad', text: 'Отклонён' }
    case 'refunded': return { tone: 'info', text: 'Возврат' }
    default: return { tone: undefined, text: status || '—' }
  }
}
