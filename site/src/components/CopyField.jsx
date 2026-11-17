import { useEffect, useRef, useState } from 'react'
import { copyText, selectContents, isPhone } from '../lib/clipboard.js'

// CopyField (spec 02 §18): a value in mono + «Копировать». A click copies; for 2 s the field turns
// `is-copied` and the button says «✓ Скопировано». If the clipboard refuses, the value is selected
// and the button says «Выделено — нажмите Ctrl+C» for 3 s. Long values (subscription link) can be
// expanded with «Показать ссылку целиком» / «Скрыть».
//   <CopyField value={link} display={shown} label="Ссылка подписки" expandable secret />
export default function CopyField({ value, display, label, expandable = false, secret = false, onCopied, className = '', style, buttonLabel = 'Копировать' }) {
  const [state, setState] = useState('idle') // idle | copied | selected
  const [open, setOpen] = useState(false)
  const v = useRef(null)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])

  async function copy() {
    const ok = await copyText(value)
    clearTimeout(timer.current)
    if (ok) {
      setState('copied')
      onCopied?.()
      timer.current = setTimeout(() => setState('idle'), 2000)
    } else {
      selectContents(v.current)
      setState('selected')
      timer.current = setTimeout(() => setState('idle'), 3000)
    }
  }

  const btn = state === 'copied' ? '✓ Скопировано' : state === 'selected' ? (isPhone() ? 'Выделено — скопируйте' : 'Выделено — нажмите Ctrl+C') : buttonLabel

  return (
    <div className={`stack gap-2 ${className}`} style={style}>
      <div className={`copy ${state === 'copied' ? 'is-copied' : ''}`}>
        {label && <span className="sr">{label}</span>}
        <span
          ref={v}
          className="copy__v"
          onClick={() => selectContents(v.current)}
          style={open ? { whiteSpace: 'normal', wordBreak: 'break-all', lineHeight: 1.5 } : undefined}
        >
          {display ?? value}
        </span>
        <button type="button" className="btn btn--secondary btn--sm" onClick={copy} aria-live="polite">{btn}</button>
      </div>
      {(expandable || secret) && (
        <div className="row wrap gap-2 between">
          {secret ? <span className="t-xs subtle">Не показывайте посторонним — это доступ к вашей подписке.</span> : <span />}
          {expandable && (
            <button type="button" className="btn btn--ghost btn--sm" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
              {open ? 'Скрыть' : 'Показать ссылку целиком'}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
