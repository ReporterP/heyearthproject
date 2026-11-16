import { useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useLayer, focusables } from '../lib/layers.js'

// Dialog (spec 02 §15): confirmations of irreversible actions, a short form in place, one large
// object (QR). Replaces window.confirm/alert/prompt everywhere.
//   <Dialog open={open} onClose={close} title="Точно выйти?" busy={busy}>…</Dialog>
// - Esc, overlay click and the cancel button close it; not while `busy`, and the overlay does not
//   close a dialog with a filled form (`closeOnOverlay={false}`).
// - Focus moves inside (the element marked data-autofocus, else the first field, else the first
//   control), is trapped, and goes back to the opener on close or unmount. The page does not scroll.
// - Leaving plays `is-leaving` for 160 ms before unmounting.
export default function Dialog({ open, onClose, title, children, busy = false, closeOnOverlay = true, className = '', style }) {
  const [mounted, setMounted] = useState(open)
  const [leaving, setLeaving] = useState(false)
  const box = useRef(null)
  const opener = useRef(null)
  const titleId = useId()

  useEffect(() => {
    if (open) {
      if (!box.current || !box.current.contains(document.activeElement)) opener.current = document.activeElement
      setMounted(true)
      setLeaving(false)
      return undefined
    }
    if (!mounted) return undefined
    setLeaving(true)
    const t = setTimeout(() => { setMounted(false); setLeaving(false) }, 160)
    return () => clearTimeout(t)
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  // focus back to the opener when the dialog goes away (closed, or the parent unmounts it)
  useEffect(() => {
    if (!mounted) return undefined
    return () => {
      const el = opener.current
      if (el && el.isConnected && el.focus) el.focus()
    }
  }, [mounted])

  // first focus
  useEffect(() => {
    if (!open || !mounted || !box.current) return
    const el = box.current.querySelector('[data-autofocus]') || box.current.querySelector('input,textarea,select') || focusables(box.current)[0] || box.current
    el.focus()
  }, [open, mounted])

  useLayer(box, open && mounted, () => { if (!busy) onClose?.() }, { capture: true })

  if (!mounted) return null
  const host = document.getElementById('he-layer') || document.body

  return createPortal(
    <div
      className={`overlay ${leaving ? 'is-leaving' : ''}`}
      onMouseDown={(e) => { if (e.target === e.currentTarget && closeOnOverlay && !busy) onClose?.() }}
    >
      <div
        ref={box}
        tabIndex={-1}
        className={`dialog ${className}`}
        style={{ outline: 'none', ...style }}
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
