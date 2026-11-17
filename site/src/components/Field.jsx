import { Children, cloneElement, isValidElement, useId } from 'react'

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
export function Input({ mono, size, invalid, className = '', as = 'input', ...rest }) {
  const Tag = as
  const cls = ['input', mono ? 'input--mono' : '', size === 'lg' ? 'input--lg' : '', invalid ? 'is-invalid' : '', className].filter(Boolean).join(' ')
  return <Tag className={cls} aria-invalid={invalid || undefined} {...rest} />
}
