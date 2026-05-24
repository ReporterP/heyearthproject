import { useEffect } from 'react'

/**
 * useHotkey — bind a global keyboard shortcut.
 *
 *   useHotkey({ key: 'k', meta: true }, () => setCmdkOpen(true))
 *
 * `meta:true` matches both Cmd (Mac) and Ctrl (Win/Linux).
 */
export function useHotkey({ key, meta = false, ctrl = false, shift = false }, handler) {
  useEffect(() => {
    const onKey = (e) => {
      if (e.key.toLowerCase() !== key.toLowerCase()) return
      if (meta && !(e.metaKey || e.ctrlKey)) return
      if (ctrl && !e.ctrlKey) return
      if (shift && !e.shiftKey) return
      e.preventDefault()
      handler(e)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [key, meta, ctrl, shift, handler])
}
