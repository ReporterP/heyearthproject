import { useEffect, useRef, useState } from 'react'
import { Check, Copy, X } from 'lucide-react'

const FEEDBACK_MS = 1500

async function writeClipboard(text) {
  if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text)
  // No async clipboard (plain http, some embedded browsers): the legacy copy command.
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  try {
    if (!document.execCommand('copy')) throw new Error('copy command refused')
  } finally {
    ta.remove()
  }
}

/**
 * CopyButton — icon-only button that copies `text`; a check (or a cross on failure) for 1.5 s,
 * announced through a polite live region. Props: text, label (aria-label/title, default «Скопировать»), className.
 * The click does not bubble, so the button is safe inside clickable table rows.
 */
export function CopyButton({ text, label = 'Скопировать', className = '' }) {
  const [state, setState] = useState('idle') // idle | done | failed
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])

  async function copy(e) {
    e.stopPropagation()
    let next = 'done'
    try {
      await writeClipboard(String(text ?? ''))
    } catch {
      next = 'failed' // clipboard blocked: the value stays selectable on the page
    }
    setState(next)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setState('idle'), FEEDBACK_MS)
  }

  return (
    <>
      <button type="button" onClick={copy} aria-label={label} title={label}
        className={`inline-flex items-center justify-center flex-shrink-0 w-7 h-7 rounded text-tertiary transition-colors
          hover:text-primary hover:bg-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent ${className}`}>
        {state === 'done' ? <Check className="w-3.5 h-3.5 text-success" aria-hidden="true" />
          : state === 'failed' ? <X className="w-3.5 h-3.5 text-danger" aria-hidden="true" />
            : <Copy className="w-3.5 h-3.5" aria-hidden="true" />}
      </button>
      <span className="sr-only" role="status" aria-live="polite">
        {state === 'done' ? 'Скопировано' : state === 'failed' ? 'Не удалось скопировать — выделите текст вручную' : ''}
      </span>
    </>
  )
}
