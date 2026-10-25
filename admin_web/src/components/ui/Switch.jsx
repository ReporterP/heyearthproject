import { useId } from 'react'

/**
 * Switch — on/off control (role="switch", aria-checked). The single switch of the admin (no ad-hoc toggles).
 * Props: checked, onChange(bool), disabled, label, description, id, className (wrapper);
 *        other props (e.g. aria-label when there is no visible label) go to the switch button.
 * Keyboard: it is a native button — Space and Enter toggle; clicking the label toggles too.
 */
export function Switch({ checked, onChange, disabled = false, label, description, id, className = '', ...rest }) {
  const autoId = useId()
  const switchId = id || `switch-${autoId}`
  const labelId = `${switchId}-label`
  const descId = `${switchId}-desc`
  return (
    <div className={`flex items-start gap-3 ${disabled ? 'opacity-40' : ''} ${className}`}>
      <button id={switchId} type="button" role="switch" aria-checked={!!checked} disabled={disabled}
        aria-labelledby={label ? labelId : undefined} aria-describedby={description ? descId : undefined}
        onClick={() => onChange?.(!checked)} {...rest}
        className={`relative flex-shrink-0 mt-0.5 w-8 h-[18px] rounded border transition-colors
          focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent focus-visible:ring-offset-1 focus-visible:ring-offset-canvas
          ${checked ? 'bg-accent border-accent' : 'bg-hover border-strong'} ${disabled ? 'cursor-not-allowed' : 'cursor-pointer'}`}>
        <span aria-hidden="true"
          className={`absolute left-0 top-[2px] w-3 h-3 rounded-sm transition-transform
            ${checked ? 'translate-x-[16px] bg-canvas' : 'translate-x-[2px] bg-current text-tertiary'}`} />
      </button>
      {(label || description) && (
        <div className="min-w-0">
          {label && (
            <label id={labelId} htmlFor={switchId}
              className={`block text-sm text-primary select-none ${disabled ? 'cursor-not-allowed' : 'cursor-pointer'}`}>
              {label}
            </label>
          )}
          {description && <p id={descId} className="text-xs text-tertiary mt-0.5">{description}</p>}
        </div>
      )}
    </div>
  )
}
