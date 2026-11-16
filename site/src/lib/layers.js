import { useEffect } from 'react'

// One scroll lock for every layer (dialog, drawers): the page stops scrolling while at least one
// layer is open, whatever order they close in.
let locks = 0
export function lockScroll() {
  if (locks++ === 0) document.body.style.overflow = 'hidden'
  return () => {
    locks = Math.max(0, locks - 1)
    if (locks === 0) document.body.style.overflow = ''
  }
}

const FOCUSABLE = 'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'
export const focusables = (root) => (root ? [...root.querySelectorAll(FOCUSABLE)].filter((x) => x.offsetParent !== null || x === document.activeElement) : [])

// useLayer(ref, open, onEscape, { capture }) — while `open`: locks the page scroll, keeps Tab inside
// `ref`, closes on Esc. A layer opened on top (dialog over a drawer) listens in the capture phase and
// marks the event handled, so the layer below does not close too (03 §2).
export function useLayer(ref, open, onEscape, { capture = false } = {}) {
  useEffect(() => {
    if (!open) return undefined
    const unlock = lockScroll()
    const onKey = (e) => {
      if (e.defaultPrevented) return
      if (e.key === 'Escape') {
        e.preventDefault()
        onEscape?.()
        return
      }
      if (e.key !== 'Tab' || !ref.current) return
      const els = focusables(ref.current)
      if (!els.length) return
      const first = els[0]
      const last = els[els.length - 1]
      const inside = ref.current.contains(document.activeElement)
      if (!inside) { e.preventDefault(); (e.shiftKey ? last : first).focus(); return }
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey, capture)
    return () => {
      document.removeEventListener('keydown', onKey, capture)
      unlock()
    }
  }, [open]) // eslint-disable-line react-hooks/exhaustive-deps
}
