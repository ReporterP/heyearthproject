import { useEffect } from 'react'

/**
 * useReveal — scroll-triggered reveal for elements marked `.reveal`.
 * Adds `.in` (CSS transitions opacity+translateY → visible) as elements enter
 * view. Uses getBoundingClientRect on scroll/resize (works without
 * IntersectionObserver/compositing). A safety timer reveals everything after
 * 1.5s so content is NEVER left invisible, in any environment. Stagger via
 * inline transitionDelay. Respects prefers-reduced-motion via CSS.
 * MASTER.md §5B.
 *
 * Usage: call `useReveal([asyncDeps])` once per page; add `className="reveal"`.
 */
export default function useReveal(deps = []) {
  useEffect(() => {
    const els = Array.from(document.querySelectorAll('.reveal:not(.in)'))
    if (els.length === 0) return

    let done = false
    let safety = 0

    function cleanup() {
      if (done) return
      done = true
      clearTimeout(safety)
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
    }
    const revealAll = () => { els.forEach((el) => el.classList.add('in')); cleanup() }
    const check = () => {
      const vh = window.innerHeight || document.documentElement.clientHeight
      let remaining = false
      for (const el of els) {
        if (el.classList.contains('in')) continue
        const r = el.getBoundingClientRect()
        if (r.top < vh * 0.94 && r.bottom > -40) el.classList.add('in')
        else remaining = true
      }
      if (!remaining) cleanup()
    }
    const onScroll = () => window.requestAnimationFrame(check)

    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll)
    window.requestAnimationFrame(check)
    const t1 = setTimeout(check, 300)
    // safety: guarantee visibility even if scroll never fires (headless, embeds)
    safety = setTimeout(revealAll, 1500)

    return () => { cleanup(); clearTimeout(t1) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
}
