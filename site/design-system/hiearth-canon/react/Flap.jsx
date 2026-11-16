// Split-flap digits («Вылет»). Each digit flips in on mount and again whenever it changes:
// the key includes the character, so React remounts that <b> and the CSS animation replays.
// Usage: <Flap value={430} suffix="₽" size={44} /> · <Flap value="00:42:17" size={17} /> · tone="accent" | "warn"
export default function Flap({ value, suffix, tone, size = 32, delayMs = 0, label, className = '' }) {
  const text = typeof value === 'number' ? value.toLocaleString('ru-RU') : String(value)
  let digitIndex = 0
  return (
    <span
      className={`flap ${tone ? `flap--${tone}` : ''} ${className}`}
      style={{ fontSize: size, '--d': `${delayMs}ms` }}
      aria-label={label ?? `${text}${suffix ? ' ' + suffix : ''}`}
      role="img"
    >
      {[...text].map((ch, i) => {
        if (/\d/.test(ch)) {
          const n = digitIndex++
          return <b key={`${i}-${ch}`} style={{ '--i': n }} aria-hidden="true">{ch}</b>
        }
        // spaces (incl. ru-RU narrow no-break space), colons, commas, minus
        return <i key={`${i}-s`} aria-hidden="true">{ch === ' ' || ch === ' ' ? ' ' : ch}</i>
      })}
      {suffix ? <i aria-hidden="true">{suffix}</i> : null}
    </span>
  )
}
