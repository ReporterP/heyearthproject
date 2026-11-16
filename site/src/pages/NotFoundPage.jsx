import { useAuth } from '../auth/AuthContext.jsx'
import SiteHeader from '../components/SiteHeader.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Flap from '../components/Flap.jsx'
import Button from '../components/Button.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// 404 (spec 06 §6.4): also the answer of the closed B2B routes.
export default function NotFoundPage() {
  const { isLoggedIn } = useAuth()
  usePageMeta('404 — HiEarth', { noindex: true })
  return (
    <>
      <SiteHeader />
      <main id="main" className="container" style={{ display: 'grid', placeItems: 'center', padding: '72px 0 120px' }}>
        <div className="pop stack" style={{ alignItems: 'center', textAlign: 'center', gap: 16, maxWidth: 520 }}>
          <Flap value="404" size={64} delayMs={200} label="Ошибка 404" />
          <Hedgehog mood="search" size={140} />
          <h1 className="t-h1">Такой страницы нет</h1>
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
