import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import NarrowHeader from '../components/NarrowHeader.jsx'
import MiniPage from '../components/MiniPage.jsx'
import Alert from '../components/Alert.jsx'
import AppDownloads from '../components/AppDownloads.jsx'
import Button from '../components/Button.jsx'
import ConnectOrb from '../components/ConnectOrb.jsx'
import CopyField from '../components/CopyField.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import QRDialog from '../components/QRCode.jsx'
import usePageMeta from '../hooks/usePageMeta.js'
import { HAPP, detectDevice } from '../lib/apps.js'

// Открытие Happ /connect?sub=<token> (spec 08 §8.3, artboard «Подключение»). Works without signing in:
// the link may be opened on another device, the token is the key. preparing → opening → (2.5 s and
// the tab is still visible) not-installed; the manual way is always below. No primary button on
// purpose — the main action already happened by itself.
const TIMEOUT_MS = 2500

// Android: an intent URI falls back to Google Play by itself when Happ is not installed
function androidIntent(happLink) {
  const tail = happLink.replace(/^happ:\/\//, '')
  return `intent://${tail}#Intent;scheme=happ;package=com.happproxy;S.browser_fallback_url=${encodeURIComponent(HAPP.android.url)};end`
}

export default function ConnectPage() {
  const [params] = useSearchParams()
  const token = (params.get('sub') || '').trim()
  const { isLoggedIn } = useAuth()
  usePageMeta('Подключение — HiEarth', { noindex: true })
  const device = detectDevice()
  const [phase, setPhase] = useState(token ? 'preparing' : 'bad') // preparing | opening | not-installed | bad
  const [happ, setHapp] = useState('')
  const fallbackUrl = token ? `${window.location.origin}/api/v1/user/subscribe/${token}` : ''
  const [subUrl, setSubUrl] = useState(fallbackUrl)
  const [qr, setQr] = useState(false)
  const timer = useRef(null)

  useEffect(() => {
    if (!token) return undefined
    let alive = true
    fetch(`/api/v1/user/happ-link/${encodeURIComponent(token)}`)
      .then(async (r) => {
        if (!alive) return
        if (r.status === 400 || r.status === 404) { setPhase('bad'); return }
        const d = r.ok ? await r.json().catch(() => null) : null
        if (d?.subscribe_url) setSubUrl(d.subscribe_url)
        // no answer: Happ also takes the plain subscription address
        setHapp(d?.happ_link || `happ://add/${fallbackUrl}`)
        setPhase('opening')
      })
      .catch(() => { if (alive) { setHapp(`happ://add/${fallbackUrl}`); setPhase('opening') } })
    return () => { alive = false }
  }, [token]) // eslint-disable-line react-hooks/exhaustive-deps

  function open() {
    if (!happ) return
    clearTimeout(timer.current)
    if (device === 'android') { window.location.href = androidIntent(happ); return }
    window.location.href = happ
    timer.current = setTimeout(() => { if (!document.hidden) setPhase('not-installed') }, TIMEOUT_MS)
  }
  useEffect(() => {
    if (phase === 'opening' && happ) open()
    return () => clearTimeout(timer.current)
  }, [phase === 'opening' && happ]) // eslint-disable-line react-hooks/exhaustive-deps

  if (phase === 'bad') {
    return (
      <MiniPage label="Ссылка некорректна">
        <Hedgehog mood="worried" size={120} />
        <h1 className="t-h3">Ссылка некорректна</h1>
        <p className="muted t-sm">В ссылке нет ключа подписки. Откройте «Подключение» в кабинете и нажмите «Добавить в Happ».</p>
        <Button variant="secondary" to="/connect">К подключению</Button>
      </MiniPage>
    )
  }

  return (
    <>
      <NarrowHeader back={isLoggedIn ? { to: '/dashboard', label: 'Назад в кабинет' } : undefined} />
      <main id="main" className="container" style={{ maxWidth: 760, paddingTop: 48, paddingBottom: 80 }}>
        <div className="stack gap-3" style={{ alignItems: 'center', textAlign: 'center' }}>
          {phase === 'preparing'
            ? <span className="spinner" aria-hidden="true" style={{ margin: '40px 0 12px' }} />
            : <ConnectOrb active small ariaLabel="Подписка готова к добавлению" />}
          <h1 className="t-h1" style={{ marginTop: 10 }} aria-live="polite">{phase === 'preparing' ? 'Готовим ссылку…' : 'Открываем Happ…'}</h1>
          {phase !== 'preparing' && (
            <p className="muted" style={{ maxWidth: 520 }}>Если приложение не открылось само, выберите устройство ниже. После установки вернитесь и нажмите кнопку ещё раз.</p>
          )}
        </div>

        {phase === 'not-installed' && (
          <div className="swap stack gap-3" style={{ marginTop: 32 }}>
            <Alert tone="warn" title="Похоже, приложение не установлено.">Скачайте его — или добавьте подписку вручную ниже.</Alert>
            <AppDownloads />
            <div><Button variant="secondary" icon="refresh" onClick={() => setPhase('opening')}>Попробовать снова</Button></div>
          </div>
        )}
        {phase === 'opening' && (
          <div style={{ marginTop: 32 }}><AppDownloads /></div>
        )}

        {phase !== 'preparing' && (
          <section className="card" style={{ marginTop: 28 }}>
            <h2 className="t-h3">Вручную</h2>
            <p className="muted t-sm" style={{ marginTop: 6 }}>Скопируйте ссылку подписки и вставьте в приложение: Happ — «+» → «Вставить из буфера».</p>
            <CopyField value={subUrl} label="Ссылка подписки" secret style={{ marginTop: 16 }} />
            <Button variant="ghost" size="sm" icon="qr" onClick={() => setQr(true)} style={{ marginTop: 12 }}>Показать QR-код</Button>
            <QRDialog open={qr} onClose={() => setQr(false)} data={subUrl} />
          </section>
        )}
      </main>
    </>
  )
}
