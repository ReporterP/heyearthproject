import { useEffect, useState } from 'react'

// useMedia('(max-width: 640px)') — true while the query matches. Phone ≤640, desktop ≥1025 (01 §3).
export default function useMedia(query) {
  const get = () => typeof window !== 'undefined' && window.matchMedia(query).matches
  const [on, setOn] = useState(get)
  useEffect(() => {
    const mq = window.matchMedia(query)
    const f = () => setOn(mq.matches)
    f()
    mq.addEventListener('change', f)
    return () => mq.removeEventListener('change', f)
  }, [query])
  return on
}

export const PHONE = '(max-width: 640px)'
// the cabinet's content beside the 248 px side menu: tablet layout up to 1365 (spec 01 §3)
export const CABINET_NARROW = '(max-width: 1365px)'
