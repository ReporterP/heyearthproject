import { useEffect, useRef } from 'react'
import { createModalKeyHandler } from './a11y.js'

const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])'

/**
 * useFocusTrap — keeps Tab/Shift+Tab inside `ref` while `active`, focuses `initialFocus`
 * (or the first focusable element) on activation and returns focus to the previously
 * focused element afterwards. The base ui/Dialog has no trap of its own; wrap its content.
 *
 * options.initialFocus — ref of the element to focus first (read once per activation).
 * options.onEscape     — when given, the modal owns Escape: the key is caught in the capture phase on
 *                        `document` and stopped there, so window listeners of a Drawer/Dialog underneath
 *                        never see it; then onEscape() runs (latest callback, no re-subscription).
 */
export function useFocusTrap(ref, active, { initialFocus, onEscape } = {}) {
  const escRef = useRef(onEscape)
  useEffect(() => { escRef.current = onEscape })

  useEffect(() => {
    if (!active) return undefined
    const root = ref.current
    if (!root) return undefined
    const restore = document.activeElement
    const focusables = () => [...root.querySelectorAll(FOCUSABLE)].filter((el) => !el.disabled && el.getClientRects().length > 0)

    const first = initialFocus?.current || focusables()[0]
    first?.focus()

    const onKeyDown = createModalKeyHandler({ focusables, active: () => document.activeElement, onEscape: () => escRef.current })
    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown, true)
      if (restore instanceof HTMLElement && restore.isConnected) restore.focus()
    }
    // initialFocus is read once per activation on purpose (not a dependency).
  }, [active, ref])
}
