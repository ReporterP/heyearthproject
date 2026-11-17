import { useEffect, useState } from 'react'

// Skeleton (spec 02 §16): a placeholder in the shape of the coming content. Shown only when the
// first load takes longer than 300 ms (useDelayed) — before that the space stays empty.
export default function Skeleton({ w = '100%', h = 14, r, className = '', style }) {
  return <span aria-hidden="true" className={`skel ${className}`} style={{ display: 'block', width: w, height: h, borderRadius: r, ...style }} />
}

// A card-shaped skeleton: same frame and padding as a card, a few bars inside.
export function SkeletonCard({ lines = 3, className = '', style, big = false }) {
  return (
    <div className={`card ${className}`} style={style} aria-hidden="true">
      <Skeleton w="40%" h={12} />
      {big && <Skeleton w={120} h={32} style={{ marginTop: 16 }} />}
      {Array.from({ length: lines }).map((_, i) => (
        <Skeleton key={i} w={`${80 - i * 15}%`} h={14} style={{ marginTop: i === 0 ? 18 : 10 }} />
      ))}
    </div>
  )
}

// useDelayed(flag, ms): true only after `flag` has stayed true for `ms` (03 §5: no flash under 300 ms).
export function useDelayed(flag, ms = 300) {
  const [on, setOn] = useState(false)
  useEffect(() => {
    if (!flag) { setOn(false); return undefined }
    const t = setTimeout(() => setOn(true), ms)
    return () => clearTimeout(t)
  }, [flag, ms])
  return flag && on
}
