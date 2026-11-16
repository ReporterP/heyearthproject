import { useAuth } from '../auth/AuthContext.jsx'
import SiteHeader from '../components/SiteHeader.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Flap from '../components/Flap.jsx'
import Button from '../components/Button.jsx'
import usePageMeta from '../hooks/usePageMeta.js'
import useMedia, { PHONE } from '../hooks/useMedia.js'

// 404 (spec 06 §6.4, artboard «404»): also the answer of the closed B2B routes.
export default function NotFoundPage() {
  const { isLoggedIn } = useAuth()
  const phone = useMedia(PHONE)
  usePageMeta('404 — HiEarth', { noindex: true })
  const size = phone ? 72 : 96
  return (
    <>
      <SiteHeader variant="compact" />
      <main id="main" className="container" style={{ flex: 1, display: 'grid', placeItems: 'center', padding: phone ? '48px 16px' : '64px 40px' }}>
        <div className="stack gap-4 pop" style={{ alignItems: 'center', textAlign: 'center', maxWidth: 520 }}>
          <div style={{ position: 'relative', height: phone ? 120 : 150, width: phone ? 260 : 340 }}>
            <Flap value="404" size={size} delayMs={200} label="Ошибка 404" style={{ position: 'absolute', left: 0, top: 0 }} />
            <Hedgehog mood="search" size={phone ? 116 : 150} className="pop" style={{ '--d': '500ms', position: 'absolute', right: phone ? -16 : -40, bottom: -24 }} />
          </div>
          <h1 className="t-h1" style={{ marginTop: 24 }}>Такой страницы нет</h1>
          <p className="muted">Ёжик поискал везде — пусто. Возможно, ссылка устарела или в адресе опечатка.</p>
          <div className="row wrap gap-2" style={{ justifyContent: 'center', marginTop: 8 }}>
            <Button variant="primary" to="/">На главную</Button>
            {isLoggedIn && <Button variant="secondary" to="/dashboard">В кабинет</Button>}
          </div>
        </div>
      </main>
    </>
  )
}
