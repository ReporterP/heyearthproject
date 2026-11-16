import { useId } from 'react'

// Small controls of spec 02: Segmented (§7), Switch (§6), Checkbox (§5), Spinner (§3),
// Progress (§23), Select (§9). Classes 1:1 with hiearth.css.

// Segmented: 2–4 options of one level. role=radiogroup (or tablist for documents).
//   <Segmented label="Период" value={p} onChange={setP} options={[{value:'day',label:'День'},…]} />
export function Segmented({ label, value, onChange, options, className = '', size }) {
  const name = useId()
  return (
    <div className={`seg ${className}`} role="radiogroup" aria-label={label} style={size === 'lg' ? { flexWrap: 'wrap' } : undefined}>
      {options.map((o) => {
        const id = `${name}-${o.value}`
        return (
          <span key={o.value} style={{ display: 'contents' }}>
            <input type="radio" id={id} name={name} value={o.value} checked={value === o.value} onChange={() => onChange(o.value)} />
            <label htmlFor={id}>{o.label}</label>
          </span>
        )
      })}
    </div>
  )
}

// Switch: sends its request at once; the caller keeps it disabled while the request runs.
export function Switch({ checked, onChange, disabled, label, id }) {
  return (
    <input
      id={id}
      type="checkbox"
      role="switch"
      className="switch"
      checked={!!checked}
      disabled={disabled}
      aria-label={label}
      aria-checked={!!checked}
      onChange={(e) => onChange(e.target.checked)}
    />
  )
}

export function Checkbox({ checked, onChange, children, disabled }) {
  return (
    <label className="check">
      <input type="checkbox" checked={!!checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      <span>{children}</span>
    </label>
  )
}

// Spinner: only for waiting without a form; always with the text of what we wait for.
export function Spinner({ label, size }) {
  return (
    <span className="row gap-3" role="status">
      <span className="spinner" aria-hidden="true" style={size ? { width: size, height: size } : undefined} />
      {label && <span className="muted">{label}</span>}
    </span>
  )
}

// Progress: share of the subscription term left (min 2 % so it stays visible).
export function Progress({ value, warn = false, label }) {
  const pct = Math.max(2, Math.min(100, Math.round(value)))
  return (
    <div className={`progress ${warn ? 'progress--warn' : ''}`} role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} aria-label={label}>
      <span style={{ width: `${pct}%` }} />
    </div>
  )
}

export function Select({ value, onChange, options, disabled, label }) {
  return (
    <select className="sel" value={value} disabled={disabled} aria-label={label} onChange={(e) => onChange(e.target.value)}>
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  )
}
