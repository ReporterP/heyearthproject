import { useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

// Dialog (spec 02 §15): confirmations of irreversible actions, a short form in place, one large
// object (QR). Replaces window.confirm/alert/prompt everywhere.
//   <Dialog open={open} onClose={close} title="Точно выйти?" busy={busy} initialFocus="cancel">…</Dialog>
// - Esc, overlay click and the cancel button close it; not while `busy`, and the overlay does not
//   close a dialog with a filled form (`closeOnOverlay={false}`).
// - Focus moves inside (first field, or the element marked data-autofocus), is trapped, and goes back
//   to the opener on close. The page under it does not scroll.
// - Leaving plays `is-leaving` for 160 ms before unmounting.
const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'

export default function Dialog({ open, onClose, title, children, busy = false, closeOnOverlay = true, className = '', style }) {
  const [mounted, setMounted] = useState(open)
  const [leaving, setLeaving] = useState(false)
  const box = useRef(null)
  const opener = useRef(null)
  const titleId = useId()

  useEffect(() => {
    if (open) {
      opener.current = document.activeElement
      setMounted(true)
      setLeaving(false)
      return undefined
    }
    if (!mounted) return undefined
    setLeaving(true)
    const t = setTimeout(() => {
      setMounted(false)
      setLeaving(false)
      if (opener.current && opener.current.focus) opener.current.focus()
    }, 160)
    return () => clearTimeout(t)
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  // first focus
  useEffect(() => {
    if (!open || !mounted || !box.current) return
    const el = box.current.querySelector('[data-autofocus]') || box.current.querySelector('input,textarea,select') || box.current.querySelector(FOCUSABLE)
    if (el) el.focus()
  }, [open, mounted])

  // scroll lock
  useEffect(() => {
    if (!mounted) return undefined
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [mounted])

  if (!mounted) return null
  const host = document.getElementById('he-layer') || document.body

  function onKeyDown(e) {
    if (e.key === 'Escape') {
      e.stopPropagation()
      if (!busy) onClose?.()
      return
    }
    if (e.key !== 'Tab' || !box.current) return
    const els = [...box.current.querySelectorAll(FOCUSABLE)].filter((x) => x.offsetParent !== null)
    if (!els.length) return
    const first = els[0]
    const last = els[els.length - 1]
    if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
  }

  return createPortal(
    <div
      className={`overlay ${leaving ? 'is-leaving' : ''}`}
      onMouseDown={(e) => { if (e.target === e.currentTarget && closeOnOverlay && !busy) onClose?.() }}
      onKeyDown={onKeyDown}
    >
      <div
        ref={box}
        className={`dialog ${className}`}
        style={style}
        role="dialog"
        aria-modal="true"
        aria-labelledby={title ? titleId : undefined}
      >
        {title && <h2 id={titleId} className="t-h3">{title}</h2>}
        {children}
      </div>
    </div>,
    host,
  )
}

// DialogActions: cancel on the left, the action on the right; stacked full-width on a phone with
// the main one at the bottom (02 §15).
export function DialogActions({ children }) {
  return <div className="row gap-2 justify-end mt-5 max-sm:flex-col max-sm:items-stretch">{children}</div>
}
