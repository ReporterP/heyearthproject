import { useEffect, useState } from 'react'
import { apiFetch } from '../api/http.js'

// The unread support answers for the «Поддержка» menu item (spec 02 §30, 08 §8.6): one request a
// minute while the tab is visible, shared by every AppShell on the page.
const listeners = new Set()
let value = 0
let timer = null
let currentToken = ''

function set(v) {
  value = v
  listeners.forEach((fn) => fn(v))
}

async function poll() {
  if (!currentToken || document.visibilityState !== 'visible') return
  try {
    const r = await apiFetch('/api/v1/support/unread', { token: currentToken, signOutOn401: false })
    set(Number(r.unread) || 0)
  } catch { /* the badge just keeps its value */ }
}

export function refreshSupportUnread() { poll() }

export function useSupportUnread(token) {
  const [n, setN] = useState(value)
  useEffect(() => {
    if (!token) return undefined
    if (currentToken !== token) { set(0); currentToken = token; poll() } // another account in this tab
    listeners.add(setN)
    if (!timer) {
      poll()
      timer = setInterval(poll, 60000)
    }
    return () => {
      listeners.delete(setN)
      if (!listeners.size) { clearInterval(timer); timer = null }
    }
  }, [token])
  return n
}
