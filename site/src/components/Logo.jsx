import { Link } from 'react-router-dom'

// The HiEarth mark: the green shield with a check, as drawn in every «Канон» artboard (26 px in the
// header, spec 01 §8). Never recoloured or stretched.
export function LogoMark({ check = true }) {
  return (
    <svg viewBox="0 0 30 30" aria-hidden="true" focusable="false">
      <path d="M15 2 L26 6 V14 C26 21 21 26 15 28 C9 26 4 21 4 14 V6 Z" style={{ fill: 'var(--accent)' }} />
      {check && <path d="M10 15 L14 19 L21 11" fill="none" style={{ stroke: 'var(--accent-ink)', strokeWidth: 2.8, strokeLinecap: 'round', strokeLinejoin: 'round' }} />}
    </svg>
  )
}

export default function Logo({ to = '/', text = 'HiEarth', className = '', style, onClick }) {
  return (
    <Link to={to} className={`logo ${className}`} style={style} onClick={onClick} aria-label={`${text} — на главную`}>
      <LogoMark />
      <span aria-hidden="true">{text}</span>
    </Link>
  )
}
