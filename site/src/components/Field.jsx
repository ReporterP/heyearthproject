import { Children, cloneElement, isValidElement, useId, useState } from 'react'
import Icon from './Icon.jsx'

// Field (spec 02 §4): visible label → control → hint or error. The error replaces the hint, marks the
// control `is-invalid` + aria-invalid and is linked by aria-describedby.
//   <Field label="Пароль" hint="минимум 4 символа" error={err} optional aside="если задавали">
//     <Input type="password" … />
//   </Field>
export default function Field({ label, hint, hintTone, error, optional, aside, id: idProp, srLabel = false, className = '', children }) {
  const auto = useId()
  const id = idProp || `f${auto.replace(/:/g, '')}`
  const msgId = `${id}-msg`
  const control = Children.only(children)
  // a wrapper (e.g. div.input-group with the input inside) is left as is: pass `id` to Field and
  // give the same id to the inner Input
  const wrapper = isValidElement(control) && typeof control.type === 'string' && !['input', 'textarea', 'select'].includes(control.type)
  const enhanced = isValidElement(control) && !wrapper
    ? cloneElement(control, {
        id: control.props.id || id,
        invalid: error ? true : control.props.invalid,
        'aria-describedby': error || hint ? msgId : undefined,
      })
    : control
  return (
    <div className={`field ${className}`}>
      {label && (
        <div className="row between gap-2">
          <label className={srLabel ? 'sr' : 'field__label'} htmlFor={wrapper ? id : (control?.props?.id || id)}>
            {label}{optional && <span className="subtle"> (необязательно)</span>}
          </label>
          {aside && <span className="t-xs subtle">{aside}</span>}
        </div>
      )}
      {enhanced}
      {error
        ? <span id={msgId} className="field__error">{error}</span>
        : hint
          ? <span id={msgId} className={`field__hint ${hintTone || ''}`}>{hint}</span>
          : null}
    </div>
  )
}

// Input: `.input` with the variants of 02 §4. `invalid` comes from Field.
// type="password" draws `input-pw`: the field + the «show password» button (02 §4).
export function Input({ mono, size, invalid, className = '', as = 'input', type, id: idProp, ...rest }) {
  const auto = useId()
  const [shown, setShown] = useState(false)
  const Tag = as
  const cls = ['input', mono ? 'input--mono' : '', size === 'lg' ? 'input--lg' : '', invalid ? 'is-invalid' : '', className].filter(Boolean).join(' ')
  if (Tag !== 'input' || type !== 'password') {
    return <Tag className={cls} type={type} id={idProp} aria-invalid={invalid || undefined} {...rest} />
  }
  const id = idProp || `pw${auto.replace(/:/g, '')}`
  return (
    <div className={`input-pw ${size === 'lg' ? 'input-pw--lg' : ''}`}>
      <input
        className={cls}
        id={id}
        type={shown ? 'text' : 'password'}
        aria-invalid={invalid || undefined}
        autoCapitalize="none"
        autoCorrect="off"
        spellCheck={false}
        {...rest}
      />
      <button
        type="button"
        className="input-pw__toggle"
        aria-label={shown ? 'Скрыть пароль' : 'Показать пароль'}
        aria-controls={id}
        onClick={() => setShown((s) => !s)}
      >
        <Icon name={shown ? 'eyeOff' : 'eye'} size={18} />
      </button>
    </div>
  )
}
