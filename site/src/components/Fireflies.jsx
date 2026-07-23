import { useEffect, useRef } from 'react'

/**
 * Fireflies — ambient bioluminescent particle layer for «Лес в ночи».
 * Absolutely fills its positioned parent. DPR-aware, wraps at edges,
 * ~30% warm (sun) particles, rest emerald. Fully disabled under
 * prefers-reduced-motion. Reference: scratchpad/hiearth-preview.html #ff.
 *
 * Props:
 *   density  - divisor for particle count (smaller = denser). Default 15000.
 *   className - extra classes for the canvas.
 */
export default function Fireflies({ density = 15000, className = '' }) {
  const ref = useRef(null)

  useEffect(() => {
    const cv = ref.current
    if (!cv) return
    if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return

    const ctx = cv.getContext('2d')
    let flies = []
    let raf = 0
    let t = 0
    const dpr = Math.min(2, window.devicePixelRatio || 1)

    function resize() {
      const w = cv.clientWidth, h = cv.clientHeight
      cv.width = w * dpr
      cv.height = h * dpr
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
      seed()
    }
    function seed() {
      const w = cv.clientWidth, h = cv.clientHeight
      const n = Math.min(90, Math.max(14, Math.round((w * h) / density)))
      flies = []
      for (let i = 0; i < n; i++) {
        flies.push({
          x: Math.random() * w,
          y: Math.random() * h,
          r: 1 + Math.random() * 2.3,
          a: Math.random() * Math.PI * 2,
          sp: 0.08 + Math.random() * 0.2,
          ph: Math.random() * Math.PI * 2,
          warm: Math.random() < 0.3,
        })
      }
    }
    function tick() {
      const w = cv.clientWidth, h = cv.clientHeight
      ctx.clearRect(0, 0, w, h)
      t += 16
      for (const f of flies) {
        f.a += (Math.random() - 0.5) * 0.24
        f.x += Math.cos(f.a) * f.sp
        f.y += Math.sin(f.a) * f.sp
        if (f.x < -4) f.x = w + 4; if (f.x > w + 4) f.x = -4
        if (f.y < -4) f.y = h + 4; if (f.y > h + 4) f.y = -4
        const tw = 0.35 + 0.65 * (0.5 + 0.5 * Math.sin(t * 0.002 + f.ph))
        const col = f.warm ? '252,201,75' : '52,211,153'
        ctx.beginPath()
        ctx.arc(f.x, f.y, f.r, 0, Math.PI * 2)
        ctx.fillStyle = 'rgba(' + col + ',' + (tw * 0.95) + ')'
        ctx.shadowBlur = 11
        ctx.shadowColor = 'rgba(' + col + ',.8)'
        ctx.fill()
      }
      ctx.shadowBlur = 0
      raf = requestAnimationFrame(tick)
    }

    resize()
    tick()
    window.addEventListener('resize', resize)
    return () => {
      cancelAnimationFrame(raf)
      window.removeEventListener('resize', resize)
    }
  }, [density])

  return (
    <canvas
      ref={ref}
      aria-hidden="true"
      className={`pointer-events-none absolute inset-0 h-full w-full ${className}`}
    />
  )
}
