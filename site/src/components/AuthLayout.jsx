import Logo from './Logo.jsx'

// The auth template (spec 02 §33 `auth`, 07): the form on the left (logo on top, the form centred),
// the signature element on the right on --bg-raised with the green halo. ≤1024 px the right half is
// hidden, except `reveal` (the «ID выдан» screen), where it moves above the form.
export default function AuthLayout({ children, side, reveal = false, sideLabel, width = 420 }) {
  return (
    <div className={`auth ${reveal ? 'auth--reveal' : ''}`}>
      <div className="stack max-sm:!px-4 max-lg:!px-6" style={{ padding: '32px 56px' }}>
        <Logo style={{ alignSelf: 'flex-start' }} />
        <main id="main" style={{ flex: 1, display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', placeItems: 'center', padding: '32px 0' }}>
          <div className="stack gap-6" style={{ width: '100%', maxWidth: width }}>{children}</div>
        </main>
      </div>
      <aside className="auth__side" aria-label={sideLabel} aria-hidden={sideLabel ? undefined : 'true'}>
        {side}
      </aside>
    </div>
  )
}
