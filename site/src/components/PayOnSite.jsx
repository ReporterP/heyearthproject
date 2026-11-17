import { useCallback, useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { apiFetch } from '../api/http.js'
import { isMiniApp, openExternal } from '../lib/telegram.js'
import Alert from './Alert.jsx'
import Button from './Button.jsx'
import Hedgehog from './Hedgehog.jsx'
import MiniPage from './MiniPage.jsx'
import usePageMeta from '../hooks/usePageMeta.js'

// «Оплата — на сайте» (spec 12 §12.3): inside the Telegram Mini App nothing is paid — buttons open
// the site in the external browser, already signed in, with a one-time link. The link is prepared
// when the screen shows (and every 4 minutes): Telegram opens a browser only right in a tap.
export function useMagicLink(next, enabled = true) {
  const { token } = useAuth()
  const [url, setUrl] = useState('')
  const [error, setError] = useState(null)
  const load = useCallback(() => {
    setError(null)
    apiFetch('/api/v1/auth/magic', { token, method: 'POST', body: { next } })
      .then((r) => setUrl(r.url || ''))
      .catch((e) => { setUrl(''); setError(e) })
  }, [token, next])
  useEffect(() => {
    if (!enabled || !token) return undefined
    load()
    const t = setInterval(load, 4 * 60 * 1000)
    return () => clearInterval(t)
  }, [enabled, token, load])
  return { url, error, retry: load }
}

// PayOnSiteButton opens next on the site (Mini App); outside Telegram it is a plain link.
export function PayOnSiteButton({ next, children, variant = 'primary', size = 'lg', block, onOpened }) {
  const mini = isMiniApp()
  const link = useMagicLink(next, mini)
  if (!mini) return <Button variant={variant} size={size} block={block} to={next}>{children}</Button>
  return (
    <div className="stack gap-2" style={block ? { width: '100%' } : undefined}>
      {link.error && (
        <Alert tone="bad" title="Не удалось подготовить ссылку.">
          Попробуйте через минуту.{' '}
          <button type="button" className="link" onClick={link.retry} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Повторить</button>
        </Alert>
      )}
      <Button variant={variant} size={size} block={block} loading={!link.url && !link.error} disabled={!link.url}
        onClick={() => { if (link.url) { openExternal(link.url); onOpened?.() } }}>{children}</Button>
    </div>
  )
}

// PayOnSitePage replaces /payment and /checkout/:id in the Mini App.
export function PayOnSitePage() {
  usePageMeta('Оплата — HiEarth', { noindex: true })
  const { pathname, search } = useLocation()
  const next = pathname.startsWith('/checkout') ? '/wallet' : pathname + search
  return (
    <MiniPage label="Оплата — на сайте">
      <Hedgehog mood="idle" size={120} />
      <h1 className="t-h3">Оплата — на сайте</h1>
      <p className="muted t-sm">Telegram не даёт оплачивать подписку внутри приложения, поэтому оплата проходит на hiearth.ru. Кнопка откроет сайт в браузере уже с входом — ничего вводить не нужно.</p>
      <PayOnSiteButton next={next} block>Открыть оплату в браузере</PayOnSiteButton>
      <p className="t-xs subtle">Ссылка действует 5 минут.</p>
      <Button variant="ghost" to="/dashboard">В кабинет</Button>
    </MiniPage>
  )
}

// MiniAppPay renders the page on the site and «Оплата — на сайте» in the Mini App.
export default function MiniAppPay({ children }) {
  return isMiniApp() ? <PayOnSitePage /> : children
}
