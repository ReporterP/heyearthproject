import { useState } from 'react'

const KEY = 'hiearth-admin-recents'
const MAX = 20

export function useRecents() {
  const [recents, setRecents] = useState(() => {
    try { return JSON.parse(localStorage.getItem(KEY) || '[]') } catch { return [] }
  })
  const add = (entry) => {
    const next = [{ ...entry, ts: Date.now() }, ...recents.filter(r => r.id !== entry.id)].slice(0, MAX)
    setRecents(next)
    localStorage.setItem(KEY, JSON.stringify(next))
  }
  const clear = () => {
    setRecents([])
    localStorage.removeItem(KEY)
  }
  return { recents, add, clear }
}
