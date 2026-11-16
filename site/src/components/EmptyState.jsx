import Hedgehog from './Hedgehog.jsx'

// Empty state (spec 02 §17): what will appear here and how to make it appear.
// Full version with the hedgehog (search = empty / 404, idle = a hint, worried = load error);
// `small` — inside a card, without the hedgehog.
export default function EmptyState({ mood = 'search', title, children, actions, small = false, className = '', style }) {
  return (
    <div className={`empty ${className}`} style={small ? { padding: '20px 8px', ...style } : style}>
      {!small && <Hedgehog mood={mood} size={132} />}
      {title && <p className="empty__t">{title}</p>}
      {children && <p className="empty__d">{children}</p>}
      {actions && <div className="row wrap gap-2" style={{ justifyContent: 'center', marginTop: 6 }}>{actions}</div>}
    </div>
  )
}
