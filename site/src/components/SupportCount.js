import { useEffect, useState } from 'react'
import { apiFetch } from '../api/http.js'

// The cabinet menu's counters (spec 02 §30, 08 §8.6–8.7): unread support answers and news. One
// request a minute while the tab is visible, shared by every AppShell on the page.
const listeners = new Set()
let value = { support: 0, news: 0 }
let timer = null
let currentToken = ''

function set(v) {
  value = v
  listeners.forEach((fn) => fn(v))
}

async function poll() {
  if (!currentToken || document.visibilityState !== 'visible') return
  try {
    const r = await apiFetch('/api/v1/user/badges', { token: currentToken, signOutOn401: false })
    set({ support: Number(r.support) || 0, news: Number(r.news) || 0 })
  } catch { /* the badges keep their values */ }
}

export function refreshSupportUnread() { poll() }
export const refreshBadges = refreshSupportUnread

export function useBadges(token) {
  const [v, setV] = useState(value)
  useEffect(() => {
    if (!token) return undefined
    if (currentToken !== token) { set({ support: 0, news: 0 }); currentToken = token; poll() } // another account in this tab
    listeners.add(setV)
    if (!timer) {
      poll()
      timer = setInterval(poll, 60000)
    }
    return () => {
      listeners.delete(setV)
      if (!listeners.size) { clearInterval(timer); timer = null }
    }
  }, [token])
  return v
}

export function useSupportUnread(token) { return useBadges(token).support }
