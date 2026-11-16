// The connect orb — the product symbol. It shows status; it is not a real toggle
// (the connection is switched on inside Happ). Pulse ring only when the subscription is active.
// Usage: <ConnectOrb active label="Подключено" /> · <ConnectOrb active={false} label="Нет подписки" small />
export default function ConnectOrb({ active = true, label, small = false }) {
  return (
    <div className={`orb ${active ? '' : 'orb--off'} ${small ? 'orb--sm' : ''}`} role="img" aria-label={label ?? (active ? 'Подписка активна' : 'Нет подписки')}>
      <div className="orb__core">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
          <path d="M12 3v8" />
          <path d="M6.3 6.3a8 8 0 1 0 11.4 0" />
        </svg>
        {!small && label ? <span className="orb__label">{label}</span> : null}
      </div>
    </div>
  )
}
