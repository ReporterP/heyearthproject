// Split-flap digits (spec 02 §34.2). Each digit flips in on mount and again whenever it changes:
// the key includes the character, so React remounts that <b> and the CSS animation replays.
// Money always groups thousands with a space (05 §4), so numbers are formatted here, not by the caller.
//   <Flap value={430} suffix="₽" size={44} tone="accent|warn" delayMs={200} />
function group(n) {
  const s = String(Math.trunc(Math.abs(n)))
  return (n < 0 ? '−' : '') + s.replace(/\B(?=(\d{3})+(?!\d))/g, ' ')
}

export default function Flap({ value, suffix, tone, size = 32, delayMs = 0, label, className = '', style }) {
  const text = typeof value === 'number' ? group(value) : String(value ?? '')
  let digitIndex = 0
  return (
    <span
      className={`flap ${tone ? `flap--${tone}` : ''} ${className}`}
      style={{ fontSize: size, '--d': `${delayMs}ms`, ...style }}
      aria-label={label ?? `${text.replace(/\s/g, '')}${suffix ? ' ' + suffix : ''}`}
      role="img"
    >
      {[...text].map((ch, i) => {
        if (/\d/.test(ch)) {
          const n = digitIndex++
          return <b key={`${i}-${ch}`} style={{ '--i': n }} aria-hidden="true">{ch}</b>
        }
        return <i key={`${i}-s`} aria-hidden="true">{/\s/.test(ch) ? ' ' : ch}</i>
      })}
      {suffix ? <i aria-hidden="true">{suffix}</i> : null}
    </span>
  )
}
