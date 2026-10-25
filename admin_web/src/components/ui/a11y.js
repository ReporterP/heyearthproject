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

/**
 * escapeAction — what Escape does in a modal that owns it: 'close', or 'ignore' while its action runs.
 * Either way the key is swallowed, so a Drawer/Dialog underneath (window listeners) does not close too.
 */
export function escapeAction(busy) {
  return busy ? 'ignore' : 'close'
}

/**
 * createModalKeyHandler — the keydown handler of a modal (useFocusTrap), DOM-free so it can be tested.
 *   focusables() — focusable elements inside the modal, active() — the focused element,
 *   onEscape()   — returns the current Escape callback or undefined (then Escape is left alone and
 *                  the base Dialog's own window listener handles it).
 * Must be registered in the capture phase on `document`: stopPropagation() there keeps Escape away from
 * window listeners of a Drawer/Dialog underneath.
 */
export function createModalKeyHandler({ focusables, active, onEscape }) {
  return function onKeyDown(e) {
    if (e.key === 'Escape') {
      const fn = onEscape()
      if (!fn) return
      e.stopPropagation()
      e.preventDefault()
      fn()
      return
    }
    if (e.key !== 'Tab') return
    const items = focusables()
    const target = focusTrapTarget(items.length, items.indexOf(active()), e.shiftKey)
    if (target === null) return
    e.preventDefault()
    if (target >= 0) items[target].focus()
  }
}

/**
 * copyAnnouncement — live-region text of CopyButton. `n` counts clicks: odd and even clicks differ by a
 * trailing no-break space, so screen readers announce a repeated «Скопировано» again.
 */
export function copyAnnouncement(status, n) {
  const text = status === 'done' ? 'Скопировано' : status === 'failed' ? 'Не удалось скопировать — выделите текст вручную' : ''
  return text && n % 2 === 0 ? `${text}\u00A0` : text
}
