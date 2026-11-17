import { Link } from 'react-router-dom'

// The connect orb (spec 02 §34.1) — shows the subscription status and is the big button: wherever it
// stands it leads to the next step (`to` → router link, `href` → plain link, `onClick` → button).
// Without an action it is an indicator (role="img"). Pulse ring only when the subscription is active.
//   <ConnectOrb active label="Готово" to="/dashboard" ariaLabel="Подписка активна — открыть кабинет" />
//   <ConnectOrb active={false} label="Подключиться" to="/register" small />
export default function ConnectOrb({ active = true, label, small = false, ariaLabel, style, className = '', to, href, onClick }) {
  const cls = `orb ${active ? '' : 'orb--off'} ${small ? 'orb--sm' : ''} ${className}`
  const name = ariaLabel ?? (active ? 'Подписка активна' : 'Подписки нет')
  const core = (
    <span className="orb__core">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" aria-hidden="true">
        <path d="M12 3v8" />
        <path d="M6.3 6.3a8 8 0 1 0 11.4 0" />
      </svg>
      {!small && label ? <span className="orb__label" aria-hidden="true">{label}</span> : null}
    </span>
  )
  if (to) return <Link to={to} className={cls} style={style} aria-label={name} onClick={onClick}>{core}</Link>
  if (href) return <a href={href} className={cls} style={style} aria-label={name} onClick={onClick}>{core}</a>
  if (onClick) return <button type="button" className={cls} style={style} aria-label={name} onClick={onClick}>{core}</button>
  return <div className={cls} role="img" aria-label={name || undefined} aria-hidden={name === '' ? true : undefined} style={style}>{core}</div>
}
