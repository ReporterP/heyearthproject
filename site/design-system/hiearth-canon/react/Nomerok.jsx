// HiEarth ID shown as a cloakroom tag («Номерок»).
// variant: 'swing' — drops onto the hook and swings (ONLY when the ID is issued on registration),
//          'idle'  — gentle sway (landing, login, settings),
//          'chip'  — compact badge for the cabinet header/side menu (shows first 4 … last 4).
// Copying is a separate button next to it, never a click on the tag.
const group = (id) => String(id).replace(/\s+/g, '').replace(/(.{4})(?=.)/g, '$1 ')

export default function Nomerok({ id, variant = 'idle', label = 'ваш номерок', withRail = true }) {
  const raw = String(id).replace(/\s+/g, '')
  if (variant === 'chip') {
    return <span className="tag tag--chip" title={group(raw)}>{`${raw.slice(0, 4)}…${raw.slice(-4)}`}</span>
  }
  const g = group(raw).split(' ')
  const tag = (
    <div className={`tag tag--${variant}`} style={withRail ? { marginTop: 22 } : undefined}>
      <span className="tag__hook" aria-hidden="true" />
      <span className="tag__k">{label}</span>
      <span className="tag__id">
        {g.slice(0, 2).join(' ')}
        <br />
        {g.slice(2).join(' ')}
      </span>
    </div>
  )
  if (!withRail) return tag
  return (
    <div style={{ width: 300 }}>
      <div className="rail" aria-hidden="true" />
      <div style={{ display: 'flex', justifyContent: 'center' }}>{tag}</div>
    </div>
  )
}
