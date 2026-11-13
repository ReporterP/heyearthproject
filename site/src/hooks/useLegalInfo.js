// The operator's details from the settings (GET /api/v1/legal-info): name, address, e-mail,
// phone, INN. Loaded once per page load; nothing personal is written in the code (cleanup plan E4).
import { useEffect, useState } from 'react'

let pending = null
let value = null

export function loadLegalInfo() {
  if (value) return Promise.resolve(value)
  if (!pending) {
    pending = fetch('/api/v1/legal-info')
      .then((r) => (r.ok ? r.json() : {}))
      .then((v) => { value = v || {}; return value })
      .catch(() => { pending = null; return {} })
  }
  return pending
}

export default function useLegalInfo() {
  const [info, setInfo] = useState(value || {})
  useEffect(() => {
    if (value) return
    let alive = true
    loadLegalInfo().then((v) => { if (alive) setInfo(v) })
    return () => { alive = false }
  }, [])
  return info
}
