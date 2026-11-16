import Logo from './Logo.jsx'

// A single centred card under the logo (spec 02 §33 `mini` / `res`): pages from letters, the company
// invitation, the payment result, a bad connection link. The card enters with `pop` (04 §3).
//   <MiniPage kind="mini|res" label="Подтверждение почты">…</MiniPage>
export default function MiniPage({ kind = 'mini', children, label, maxWidth = 440, wide = false }) {
  return (
    <main id="main" style={{ flex: 1, minHeight: '100vh', display: 'grid', gridTemplateColumns: 'minmax(0, 1fr)', placeItems: 'center', padding: '40px 16px 64px' }}>
      <div className="stack" style={{ alignItems: 'center', gap: 28, width: '100%', maxWidth: wide ? 640 : maxWidth }}>
        <Logo />
        <section className={`${kind} pop`} style={{ width: '100%' }} aria-label={label}>
          {children}
        </section>
      </div>
    </main>
  )
}
