import { useEffect, useState } from 'react'
import { fetchFeatures } from '../api/admin.js'

// useFeatures — the switches the menu follows (GET /features), fetched once per page load.
let cached = null
let pending = null

export function useFeatures() {
  const [features, setFeatures] = useState(cached || { b2b: false })
  useEffect(() => {
    if (cached) return undefined
    let alive = true
    pending = pending || fetchFeatures().then((f) => { cached = f || { b2b: false }; return cached }).catch(() => ({ b2b: false }))
    pending.then((f) => { if (alive) setFeatures(f) })
    return () => { alive = false }
  }, [])
  return features
}
