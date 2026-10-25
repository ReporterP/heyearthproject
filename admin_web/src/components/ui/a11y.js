// Pure keyboard rules of the ui primitives (WAI-ARIA APG tabs and modal dialog patterns).
// Kept free of React/DOM so `node --test` covers them.

/**
 * tabKeyTarget — index of the tab a key moves to, or -1 when the key is not a tab-navigation key.
 * ←/→ wrap around, Home/End jump to the first/last tab.
 */
export function tabKeyTarget(key, index, count) {
  if (count <= 0) return -1
  switch (key) {
    case 'ArrowRight': return (index + 1) % count
    case 'ArrowLeft': return (index - 1 + count) % count
    case 'Home': return 0
    case 'End': return count - 1
    default: return -1
  }
}

/** rovingIndex — the one tab reachable with Tab: the selected one, else the first (so the list is never skipped). */
export function rovingIndex(tabs, value) {
  if (!tabs?.length) return -1
  const i = tabs.findIndex((t) => t.key === value)
  return i >= 0 ? i : 0
}

/**
 * focusTrapTarget — where Tab/Shift+Tab must send focus inside a modal.
 *   count — focusable elements in the modal, index — the focused one (-1 = focus is outside).
 * Returns the index to focus, null to let the browser move focus normally,
 * or -1 when nothing is focusable (prevent the move, focus stays).
 */
export function focusTrapTarget(count, index, shift) {
  if (count <= 0) return -1
  if (index < 0) return shift ? count - 1 : 0
  if (shift && index === 0) return count - 1
  if (!shift && index === count - 1) return 0
  return null
}
