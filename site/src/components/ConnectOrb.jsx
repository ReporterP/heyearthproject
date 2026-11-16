// The connect orb (spec 02 §34.1) — shows the subscription status; it is not a toggle (the
// connection is switched on inside Happ). Pulse ring only when the subscription is active.
//   <ConnectOrb active label="Готово" /> · <ConnectOrb active={false} label="Нет подписки" small />
export default function ConnectOrb({ active = true, label, small = false, ariaLabel, style, className = '' }) {
  return (
    <div
      className={`orb ${active ? '' : 'orb--off'} ${small ? 'orb--sm' : ''} ${className}`}
      role="img"
      aria-label={ariaLabel ?? (active ? 'Подписка активна' : 'Подписки нет')}
      style={style}
    >
      <div className="orb__core">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
          <path d="M12 3v8" />
          <path d="M6.3 6.3a8 8 0 1 0 11.4 0" />
        </svg>
        {!small && label ? <span className="orb__label" aria-hidden="true">{label}</span> : null}
      </div>
    </div>
  )
}
