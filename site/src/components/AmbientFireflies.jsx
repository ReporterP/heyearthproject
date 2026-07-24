import Fireflies from './Fireflies.jsx'

/**
 * AmbientFireflies — one persistent, viewport-fixed firefly layer rendered
 * behind ALL routes (mounted once in the app shell). Sparse + low-opacity so
 * it reads as living atmosphere everywhere without competing with text or
 * forms. Denser hero/CTA <Fireflies> instances layer on top on marketing
 * pages. Fully disabled under prefers-reduced-motion (handled inside Fireflies).
 */
export default function AmbientFireflies() {
  return (
    <div className="fixed inset-0 z-0 pointer-events-none opacity-70" aria-hidden="true">
      <Fireflies density={30000} />
    </div>
  )
}
