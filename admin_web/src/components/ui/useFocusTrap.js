import { useEffect } from 'react'
import { focusTrapTarget } from './a11y.js'

const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])'

/**
 * useFocusTrap — keeps Tab/Shift+Tab inside `ref` while `active`, focuses `initialFocus`
 * (or the first focusable element) on activation and returns focus to the previously
 * focused element afterwards. The base ui/Dialog has no trap of its own; wrap its content.
 */
export function useFocusTrap(ref, active, initialFocus) {
  useEffect(() => {
    if (!active) return undefined
    const root = ref.current
    if (!root) return undefined
    const restore = document.activeElement
    const focusables = () => [...root.querySelectorAll(FOCUSABLE)].filter((el) => !el.disabled && el.getClientRects().length > 0)

    const first = initialFocus?.current || focusables()[0]
    first?.focus()

    function onKeyDown(e) {
      if (e.key !== 'Tab') return
      const items = focusables()
      const target = focusTrapTarget(items.length, items.indexOf(document.activeElement), e.shiftKey)
      if (target === null) return
      e.preventDefault()
      if (target >= 0) items[target].focus()
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('keydown', onKeyDown, true)
      if (restore instanceof HTMLElement && restore.isConnected) restore.focus()
    }
    // initialFocus is read once per activation on purpose (not a dependency).
  }, [active, ref])
}
