import { useEffect } from 'react'

// isTyping: the key goes to a field (input, textarea, select, contenteditable) — a plain-letter
// shortcut must not steal it (C3: «j»/«k»/Enter in the users search box).
export function isTyping(target) {
  if (!target) return false
  const tag = String(target.tagName || '').toUpperCase()
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  return !!target.isContentEditable
}

// hotkeyMatches: the key and exactly the asked modifiers; a shortcut without Cmd/Ctrl never fires
// while typing in a field (one with Cmd/Ctrl does: ⌘K works from the search box).
export function hotkeyMatches(e, { key, meta = false, ctrl = false, shift = false }) {
  if (String(e.key || '').toLowerCase() !== key.toLowerCase()) return false
  const cmd = e.metaKey || e.ctrlKey
  if (meta && !cmd) return false
  if (ctrl && !e.ctrlKey) return false
  if (!meta && !ctrl && (cmd || e.altKey)) return false
  if (shift !== !!e.shiftKey && key.length === 1 && /[a-z]/i.test(key)) return false
  if (!meta && !ctrl && isTyping(e.target)) return false
  if (e.isComposing) return false
  return true
}

/**
 * useHotkey — bind a global keyboard shortcut.
 *
 *   useHotkey({ key: 'k', meta: true }, () => setCmdkOpen(true))
 *
 * `meta:true` matches both Cmd (Mac) and Ctrl (Win/Linux). Plain keys are ignored while the
 * focus is in a field.
 */
export function useHotkey(spec, handler) {
  const { key, meta = false, ctrl = false, shift = false } = spec
  useEffect(() => {
    const onKey = (e) => {
      if (!hotkeyMatches(e, { key, meta, ctrl, shift })) return
      e.preventDefault()
      handler(e)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [key, meta, ctrl, shift, handler])
}
