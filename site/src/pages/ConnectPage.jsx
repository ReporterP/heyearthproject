import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import QRCode from '../components/QRCode.jsx'
import { useNoIndex } from '../components/RequireAuth.jsx'

/* ── Platform constants ── */
const APP_STORE_URL = 'https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108'
const PLAY_STORE_URL = 'https://play.google.com/store/apps/details?id=com.happproxy'
const DESKTOP_HAPP_URL = 'https://www.happ.su/main/ru'
const DEEPLINK_TIMEOUT_MS = 1500

/* ── Icons ── */
const IconApple = (p) => (<svg {...p} viewBox="0 0 24 24" fill="currentColor"><path d="M16.3 1.5c.1 1-.3 2-.9 2.7-.7.8-1.7 1.4-2.7 1.3-.1-1 .4-2 1-2.7.6-.7 1.7-1.2 2.6-1.3zM19 17.3c-.5 1.1-.7 1.6-1.4 2.6-.9 1.4-2.2 3.1-3.8 3.1-1.4 0-1.8-.9-3.7-.9s-2.4.9-3.7.9c-1.6 0-2.8-1.5-3.7-2.9C.7 16.4.4 12 2 9.3c1-1.6 2.6-2.6 4.1-2.6 1.5 0 2.5 1 3.7 1 1.2 0 1.9-1 3.7-1 1.3 0 2.7.7 3.7 2-3.2 1.8-2.7 6.4.8 8.6z" /></svg>)
const IconAndroid = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round"><rect x="6" y="8" width="12" height="11" rx="2" /><path d="M9 8V6a3 3 0 016 0v2M8 5L6.5 3M16 5l1.5-2M9.5 12h.01M14.5 12h.01" /></svg>)
const IconDesktop = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></svg>)

/* ── Helpers ── */
function detectPlatform() {
  if (typeof navigator === 'undefined') return 'desktop'
  const ua = navigator.userAgent || ''
  if (/iPad|iPhone|iPod/.test(ua) && !window.MSStream) return 'ios'
  if (/Android/.test(ua)) return 'android'
  return 'desktop'
}

function buildAndroidIntent(happLink) {
  const tail = happLink.replace(/^happ:\/\//, '')
  const fallback = encodeURIComponent(PLAY_STORE_URL)
  return `intent://${tail}#Intent;scheme=happ;package=com.happproxy;S.browser_fallback_url=${fallback};end`
}

async function fetchHappLink(token) {
  try {
    const res = await fetch(`/api/v1/user/happ-link/${encodeURIComponent(token)}`)
    if (!res.ok) return null
    const data = await res.json()
    return data && data.happ_link ? { happLink: data.happ_link, subscribeUrl: data.subscribe_url || '' } : null
  } catch {
    return null
  }
}

async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/* ── Component ── */
export default function ConnectPage() {
  const [searchParams] = useSearchParams()
  const token = (searchParams.get('sub') || '').trim()
  useNoIndex() // the link carries the subscription key
  const platform = detectPlatform()
  const [copied, setCopied] = useState(false)
  const [triggered, setTriggered] = useState(false)
  const [notOpened, setNotOpened] = useState(false)
  const [happLink, setHappLink] = useState(null)
  const [loading, setLoading] = useState(true)

  // The server's address of the subscription (it may live on the subscription domain that
  // answers on mobile networks); this page's own origin until the server answers.
  const fallbackUrl = token ? `${window.location.origin}/api/v1/user/subscribe/${token}` : ''
  const [serverUrl, setServerUrl] = useState('')
  const subscribeUrl = serverUrl || fallbackUrl

  useEffect(() => {
    if (!token) { setLoading(false); return }
    let cancelled = false
    fetchHappLink(token).then((res) => {
      if (cancelled) return
      if (res?.subscribeUrl) setServerUrl(res.subscribeUrl)
      // No answer: Happ also takes the plain subscription address.
      setHappLink(res?.happLink || `happ://add/${fallbackUrl}`)
      setLoading(false)
    })
    return () => { cancelled = true }
  }, [token, fallbackUrl])

  useEffect(() => {
    if (loading || !happLink || typeof window === 'undefined') return
    if (platform === 'android') {
      // Android Intent URI handles the store fallback natively.
      window.location.href = buildAndroidIntent(happLink)
      setTriggered(true)
      return
    }
    window.location.href = happLink
    setTriggered(true)
    // If the app didn't take over, OFFER the store — never yank the user there.
    const handle = setTimeout(() => {
      if (!document.hidden) setNotOpened(true)
    }, DEEPLINK_TIMEOUT_MS)
    return () => clearTimeout(handle)
  }, [loading, happLink, platform])

  if (!token) {
    return (
      <section className="min-h-[60vh] flex items-center justify-center px-4">
        <div className="card rounded-[22px] p-8 max-w-md text-center">
          <h1 className="serif text-2xl font-semibold mb-2">Ссылка некорректна</h1>
          <p className="text-sm text-text-secondary mb-6">
            В ссылке нет ключа подписки. Откройте «Подключение» в личном кабинете и нажмите «Подключить в приложении».
          </p>
          <Link to="/connect" className="btn-primary !py-2.5 !text-sm">← К подключению</Link>
        </div>
      </section>
    )
  }

  async function doCopy() {
    const ok = await copyToClipboard(subscribeUrl)
    if (ok) { setCopied(true); setTimeout(() => setCopied(false), 1800) }
  }

  const heading = loading ? 'Готовим ссылку…' : happLink ? 'Открываем приложение…' : 'Готово к ручной настройке'

  const dlButtons = [
    { url: APP_STORE_URL, Icon: IconApple, label: 'Скачать для iPhone (App Store)' },
    { url: PLAY_STORE_URL, Icon: IconAndroid, label: 'Скачать для Android (Google Play)' },
    { url: DESKTOP_HAPP_URL, Icon: IconDesktop, label: 'Скачать для ПК (Windows / macOS / Linux)' },
  ]

  return (
    <section className="relative min-h-[80vh] flex items-center justify-center px-4 py-10 overflow-hidden">
      <div className="pointer-events-none absolute top-10 right-10 w-72 h-72 rounded-full bg-brand/8 blur-[110px]" />
      <div className="card rounded-[22px] p-6 md:p-8 max-w-lg w-full relative">
        <div className="flex items-center gap-3 mb-3">
          <div className="w-10 h-10 rounded-xl bg-brand/15 border border-brand/25 text-brand flex items-center justify-center">
            <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" />
            </svg>
          </div>
          <h1 className="serif text-xl md:text-2xl font-semibold">{heading}</h1>
        </div>
        {triggered && !notOpened && (
          <p className="text-sm text-text-secondary mb-5">Если приложение не открылось автоматически, выберите ваше устройство ниже.</p>
        )}

        {/* App didn't take over — offer the next step instead of hijacking */}
        {notOpened && (
          <div className="mb-5 p-4 rounded-xl bg-accent/10 border border-accent/25 animate-fade-in">
            <p className="text-sm font-medium text-accent mb-1">Похоже, приложение не установлено</p>
            <p className="text-xs text-text-secondary mb-3">Скачайте его — после установки вернитесь и нажмите ссылку ещё раз. Или добавьте подписку вручную ниже.</p>
            <div className="flex flex-wrap gap-2">
              <a href={platform === 'ios' ? APP_STORE_URL : DESKTOP_HAPP_URL} target="_blank" rel="noopener noreferrer" className="btn-primary !py-2.5 !text-sm">
                Скачать приложение
              </a>
              <button onClick={() => { setNotOpened(false); if (happLink) window.location.href = happLink }} className="btn-ghost !py-2.5 !text-sm">
                Попробовать снова
              </button>
            </div>
          </div>
        )}

        <div className="space-y-2 mb-6">
          {dlButtons.map(({ url, Icon, label }) => (
            <a key={label} href={url} target="_blank" rel="noopener noreferrer"
               className="flex items-center gap-3 px-4 py-3 rounded-xl bg-white/[0.03] hover:bg-white/[0.06] border border-brand/12 hover:border-brand/25 text-sm text-text-primary transition">
              <span className="text-brand"><Icon className="w-5 h-5" /></span>
              {label}
            </a>
          ))}
        </div>

        <div className="border-t border-brand/10 pt-5">
          <p className="text-xs text-text-secondary mb-3">Или вставьте ссылку подписки в приложение вручную:</p>
          <div className="flex items-stretch gap-2 mb-4">
            <code className="flex-1 px-3 py-2 rounded-lg bg-bg-app border border-brand/10 text-xs font-mono break-all overflow-hidden text-ellipsis whitespace-nowrap">
              {subscribeUrl}
            </code>
            <button onClick={doCopy} className="px-3 py-2 rounded-lg bg-brand/15 text-brand hover:bg-brand/25 text-sm transition shrink-0">
              {copied ? '✓ OK' : 'Копировать'}
            </button>
          </div>
          <details>
            <summary className="text-xs text-brand cursor-pointer hover:text-brand-light transition">Показать QR-код</summary>
            <div className="mt-3 flex justify-center"><QRCode data={subscribeUrl} size={190} /></div>
          </details>
        </div>

        <div className="mt-6 text-center">
          <Link to="/dashboard" className="text-xs text-text-secondary hover:text-text-primary transition">← Назад в личный кабинет</Link>
        </div>
      </div>
    </section>
  )
}
