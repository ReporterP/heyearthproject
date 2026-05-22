import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

/* ── Platform constants ── */
const APP_STORE_URL = 'https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108'
const PLAY_STORE_URL = 'https://play.google.com/store/apps/details?id=com.happproxy'
const DESKTOP_HAPP_URL = 'https://www.happ.su/main/ru'
const DEEPLINK_TIMEOUT_MS = 1500

/* ── Helpers ── */
function detectPlatform() {
  if (typeof navigator === 'undefined') return 'desktop'
  const ua = navigator.userAgent || ''
  if (/iPad|iPhone|iPod/.test(ua) && !window.MSStream) return 'ios'
  if (/Android/.test(ua)) return 'android'
  return 'desktop'
}

function buildHappDeeplink(subscribeUrl) {
  // base64-encode the URL, strip trailing '=' padding (Happ accepts either)
  const b64 = btoa(subscribeUrl).replace(/=+$/, '')
  return `happ://add/${b64}`
}

function buildAndroidIntent(subscribeUrl) {
  const b64 = btoa(subscribeUrl).replace(/=+$/, '')
  const fallback = encodeURIComponent(PLAY_STORE_URL)
  return `intent://add/${b64}#Intent;scheme=happ;package=com.happproxy;S.browser_fallback_url=${fallback};end`
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
  const platform = detectPlatform()
  const [copied, setCopied] = useState(false)
  const [triggered, setTriggered] = useState(false)

  if (!token) {
    return (
      <section className="min-h-[60vh] flex items-center justify-center px-4">
        <div className="glass-card rounded-2xl p-8 max-w-md text-center">
          <h1 className="text-2xl font-bold mb-2">Ссылка некорректна</h1>
          <p className="text-sm text-text-secondary mb-6">
            Параметр <code>sub</code> отсутствует. Откройте кнопку «Добавить в приложение» из личного кабинета.
          </p>
          <Link to="/dashboard" className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-brand/20 text-brand hover:bg-brand/30 transition">
            ← В личный кабинет
          </Link>
        </div>
      </section>
    )
  }

  const subscribeUrl = `${window.location.origin}/api/v1/user/subscribe/${token}`
  const happDeeplink = buildHappDeeplink(subscribeUrl)

  useEffect(() => {
    if (typeof window === 'undefined') return

    // Android: Intent URI handles fallback natively, no JS timeout needed.
    if (platform === 'android') {
      const androidIntent = buildAndroidIntent(subscribeUrl)
      window.location.href = androidIntent
      setTriggered(true)
      return
    }

    // iOS / Desktop: try deeplink, fall back to download URL after timeout.
    const fallback = platform === 'ios' ? APP_STORE_URL : DESKTOP_HAPP_URL
    window.location.href = happDeeplink
    setTriggered(true)

    const handle = setTimeout(() => {
      // If the app opened, the tab is hidden (iOS) or unloaded. If still visible,
      // redirect to the store/site for install.
      if (!document.hidden) {
        window.location.href = fallback
      }
    }, DEEPLINK_TIMEOUT_MS)

    return () => clearTimeout(handle)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function doCopy() {
    const ok = await copyToClipboard(subscribeUrl)
    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    }
  }

  return (
    <section className="min-h-[80vh] flex items-center justify-center px-4 py-10">
      <div className="glass-card rounded-2xl p-6 md:p-8 max-w-lg w-full">
        <div className="flex items-center gap-3 mb-3">
          <div className="w-10 h-10 rounded-xl bg-brand/20 flex items-center justify-center">
            <svg className="w-6 h-6 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" />
            </svg>
          </div>
          <h1 className="text-xl md:text-2xl font-bold">Открываем Happ…</h1>
        </div>
        {triggered && (
          <p className="text-sm text-text-secondary mb-5">
            Если Happ не открылся автоматически, выберите ваше устройство ниже.
          </p>
        )}

        <div className="space-y-2 mb-6">
          <a href={APP_STORE_URL} target="_blank" rel="noopener noreferrer"
             className="block px-4 py-3 rounded-xl bg-bg-surface/50 hover:bg-bg-surface/70 border border-border text-sm text-text-primary transition">
            📱 Скачать Happ для iPhone (App Store)
          </a>
          <a href={PLAY_STORE_URL} target="_blank" rel="noopener noreferrer"
             className="block px-4 py-3 rounded-xl bg-bg-surface/50 hover:bg-bg-surface/70 border border-border text-sm text-text-primary transition">
            🤖 Скачать Happ для Android (Google Play)
          </a>
          <a href={DESKTOP_HAPP_URL} target="_blank" rel="noopener noreferrer"
             className="block px-4 py-3 rounded-xl bg-bg-surface/50 hover:bg-bg-surface/70 border border-border text-sm text-text-primary transition">
            💻 Скачать Happ для ПК (Windows / macOS / Linux)
          </a>
        </div>

        <div className="border-t border-border pt-5">
          <p className="text-xs text-text-secondary mb-2">
            Или используйте другой клиент (v2rayNG, Streisand, Hiddify):
          </p>
          <div className="flex items-stretch gap-2">
            <code className="flex-1 px-3 py-2 rounded-lg bg-bg-app text-xs font-mono break-all overflow-hidden text-ellipsis whitespace-nowrap">
              {subscribeUrl}
            </code>
            <button onClick={doCopy}
                    className="px-3 py-2 rounded-lg bg-brand/20 text-brand hover:bg-brand/30 text-sm transition shrink-0">
              {copied ? '✓ OK' : 'Копировать'}
            </button>
          </div>
        </div>

        <div className="mt-6 text-center">
          <Link to="/dashboard" className="text-xs text-text-secondary hover:text-text-primary transition">
            ← Назад в личный кабинет
          </Link>
        </div>
      </div>
    </section>
  )
}
