import { useEffect, useRef, useState } from 'react'
import { Check, Copy, X } from 'lucide-react'
import { copyAnnouncement } from './a11y.js'

const FEEDBACK_MS = 1500

async function writeClipboard(text) {
  if (navigator.clipboard?.writeText) return navigator.clipboard.writeText(text)
  // No async clipboard (plain http, some embedded browsers): the legacy copy command.
  const prev = document.activeElement
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  ta.setSelectionRange(0, ta.value.length) // iOS ignores select() on a readonly field
  try {
    if (!document.execCommand('copy')) throw new Error('copy command refused')
  } finally {
    ta.remove()
    if (prev instanceof HTMLElement && prev.isConnected) prev.focus() // select() moved focus away
  }
}

/**
 * CopyButton — icon-only button that copies `text`; a check (or a cross on failure) for 1.5 s,
 * announced through a polite live region (again on every click). Props: text, label (aria-label/title,
 * default «Скопировать»), className. The click does not bubble, so the button is safe inside clickable rows.
 */
export function CopyButton({ text, label = 'Скопировать', className = '' }) {
  const [feedback, setFeedback] = useState({ status: 'idle', n: 0 }) // status: idle | done | failed
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])

  async function copy(e) {
    e.stopPropagation()
    let status = 'done'
    try {
      await writeClipboard(String(text ?? ''))
    } catch {
      status = 'failed' // clipboard blocked: the value stays selectable on the page
    }
    setFeedback((f) => ({ status, n: f.n + 1 }))
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setFeedback((f) => ({ ...f, status: 'idle' })), FEEDBACK_MS)
  }

  const { status, n } = feedback
  return (
    <>
      <button type="button" onClick={copy} aria-label={label} title={label}
        className={`inline-flex items-center justify-center flex-shrink-0 w-7 h-7 rounded text-tertiary transition-colors
          hover:text-primary hover:bg-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent ${className}`}>
        {status === 'done' ? <Check className="w-3.5 h-3.5 text-success" aria-hidden="true" />
          : status === 'failed' ? <X className="w-3.5 h-3.5 text-danger" aria-hidden="true" />
            : <Copy className="w-3.5 h-3.5" aria-hidden="true" />}
      </button>
      <span className="sr-only" role="status" aria-live="polite">{copyAnnouncement(status, n)}</span>
    </>
  )
}
