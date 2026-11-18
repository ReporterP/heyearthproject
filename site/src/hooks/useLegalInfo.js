// The operator's details from the settings (GET /api/v1/legal-info): name, address, e-mail,
// phone, INN. Loaded once per page load; nothing personal is written in the code (cleanup plan E4).
// A failed request is not cached: the documents show an error with a retry (spec 06 §6.3).
import { useCallback, useEffect, useState } from 'react'

let pending = null
let value = null

export function loadLegalInfo() {
  if (value) return Promise.resolve(value)
  if (!pending) {
    pending = fetch('/api/v1/legal-info')
      .then((r) => { if (!r.ok) throw new Error(`legal-info ${r.status}`); return r.json() })
      .then((v) => { value = v || {}; return value })
      .finally(() => { pending = null })
  }
  return pending
}

// useLegalDoc: the details plus the load state — 'loading' | 'ready' | 'error' — and a retry.
export function useLegalDoc() {
  const [state, setState] = useState(() => ({ info: value || {}, status: value ? 'ready' : 'loading' }))
  const [attempt, setAttempt] = useState(0)
  useEffect(() => {
    if (value) { setState({ info: value, status: 'ready' }); return undefined }
    let alive = true
    setState((s) => ({ ...s, status: 'loading' }))
    loadLegalInfo()
      .then((v) => { if (alive) setState({ info: v, status: 'ready' }) })
      .catch(() => { if (alive) setState((s) => ({ ...s, status: 'error' })) })
    return () => { alive = false }
  }, [attempt])
  const retry = useCallback(() => setAttempt((n) => n + 1), [])
  return { ...state, retry }
}

// The details only (the footer): {} until they arrive or when they don't.
export default function useLegalInfo() {
  return useLegalDoc().info
}
