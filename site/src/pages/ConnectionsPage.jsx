import { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import { copyToClipboard } from '../components/CopyButton.jsx'
import TelegramProxyButton from '../components/TelegramProxyButton.jsx'
import QRCode from '../components/QRCode.jsx'
import { CabinetSkeleton } from '../components/Skeleton.jsx'
import useReveal from '../hooks/useReveal.js'

/* ── Icons ── */
const IconLink = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round"><path d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" /></svg>)
const IconApple = (p) => (<svg {...p} viewBox="0 0 24 24" fill="currentColor"><path d="M16.3 1.5c.1 1-.3 2-.9 2.7-.7.8-1.7 1.4-2.7 1.3-.1-1 .4-2 1-2.7.6-.7 1.7-1.2 2.6-1.3zM19 17.3c-.5 1.1-.7 1.6-1.4 2.6-.9 1.4-2.2 3.1-3.8 3.1-1.4 0-1.8-.9-3.7-.9s-2.4.9-3.7.9c-1.6 0-2.8-1.5-3.7-2.9C.7 16.4.4 12 2 9.3c1-1.6 2.6-2.6 4.1-2.6 1.5 0 2.5 1 3.7 1 1.2 0 1.9-1 3.7-1 1.3 0 2.7.7 3.7 2-3.2 1.8-2.7 6.4.8 8.6z" /></svg>)
const IconAndroid = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round"><rect x="6" y="8" width="12" height="11" rx="2" /><path d="M9 8V6a3 3 0 016 0v2M8 5L6.5 3M16 5l1.5-2M9.5 12h.01M14.5 12h.01" /></svg>)
const IconDesktop = (p) => (<svg {...p} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6} strokeLinecap="round" strokeLinejoin="round"><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" /></svg>)

/* Styled disclosure (replaces dated native <details>) */
function Reveal({ label, children }) {
  const [open, setOpen] = useState(false)
  return (
    <div>
      <button onClick={() => setOpen(o => !o)} aria-expanded={open} className="inline-flex items-center gap-1.5 text-xs text-brand hover:text-brand-light transition">
        <svg className={`w-3.5 h-3.5 transition-transform duration-300 ${open ? 'rotate-90' : ''}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.2}><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" /></svg>
        {label}
      </button>
      <div className="grid transition-all duration-300 ease-out" style={{ gridTemplateRows: open ? '1fr' : '0fr' }}>
        <div className="overflow-hidden"><div className="pt-3">{children}</div></div>
      </div>
    </div>
  )
}

/* Device-specific setup steps */
const DEVICE_STEPS = {
  ios: [
    'Установите приложение Happ из App Store',
    'Нажмите «Подключить автоматически» выше — приложение откроется само',
    'Если не открылось: скопируйте ссылку и в Happ нажмите Подписки → + → Вставить из буфера',
    'Включите подключение переключателем в приложении',
  ],
  android: [
    'Установите Happ из Google Play',
    'Нажмите «Подключить автоматически» выше',
    'Если не открылось: скопируйте ссылку, в приложении → + → Импорт из буфера обмена',
    'Включите подключение переключателем',
  ],
  desktop: [
    'Скачайте Happ для Windows / macOS / Linux',
    'Скопируйте ссылку подписки кнопкой «Копировать ссылку» выше',
    'В приложении: Подписки → Добавить → вставьте ссылку',
    'Включите подключение',
  ],
}
const DEVICES = [
  { id: 'ios', label: 'iPhone', Icon: IconApple },
  { id: 'android', label: 'Android', Icon: IconAndroid },
  { id: 'desktop', label: 'ПК', Icon: IconDesktop },
]

function detectDevice() {
  if (typeof navigator === 'undefined') return 'desktop'
  const ua = navigator.userAgent || ''
  if (/iPad|iPhone|iPod/.test(ua)) return 'ios'
  if (/Android/.test(ua)) return 'android'
  return 'desktop'
}

function extractTokenFromUrl(url) {
  if (!url) return ''
  const parts = url.split('/')
  return parts[parts.length - 1] || ''
}

export default function ConnectionsPage() {
  const { token } = useAuth()
  // One account load (E6): the connection state is required — a failure (503) must show
  // «временно недоступно», never «нет подписки»; the link is optional.
  const { data, loading, error: loadError } = useAccount(['connections', 'subLink'], { optional: ['subLink'] })
  const connections = data.connections
  const subscribeLink = data.subLink
  const error = loadError && loadError.kind !== 'auth' ? (loadError.message || 'Не удалось загрузить данные. Обновите страницу.') : ''
  const [configUpdated, setConfigUpdated] = useState(false)
  const [copiedField, setCopiedField] = useState('')
  const [device, setDevice] = useState(detectDevice())

  useEffect(() => {
    const v = connections?.config_version
    if (!v) return
    const savedVersion = localStorage.getItem('heyearth_config_version')
    if (savedVersion && savedVersion !== String(v)) setConfigUpdated(true)
    localStorage.setItem('heyearth_config_version', String(v))
  }, [connections?.config_version])

  const canConnect = connections?.can_connect
  const subUrl = subscribeLink?.subscribe_url || ''

  useReveal([loading, canConnect])

  async function copyText(text, field) {
    const ok = await copyToClipboard(text)
    if (ok) { setCopiedField(field); setTimeout(() => setCopiedField(''), 2000) }
  }

  if (loading) return <CabinetSkeleton />

  if (error) return (
    <div className="flex items-center justify-center py-32">
      <div className="text-center glass-strong rounded-[22px] p-8 max-w-sm mx-6">
        <div className="w-12 h-12 rounded-full bg-danger/10 flex items-center justify-center mx-auto mb-4">
          <svg className="w-6 h-6 text-danger" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" /></svg>
        </div>
        <p className="text-danger mb-4 text-sm">{error}</p>
        <button className="btn-primary !py-2.5 !text-sm" onClick={() => window.location.reload()}>Повторить</button>
      </div>
    </div>
  )

  /* ── Empty state: no active subscription ── */
  if (!canConnect) return (
    <section className="relative py-16 md:py-24">
      <div className="max-w-md mx-auto px-4 text-center">
        <div className="w-14 h-14 mx-auto rounded-full bg-accent/12 border border-accent/25 text-accent flex items-center justify-center mb-5">
          <svg className="w-7 h-7" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" /></svg>
        </div>
        <h1 className="serif text-2xl font-semibold mb-2">Нет активной подписки</h1>
        <p className="text-sm text-text-secondary mb-7">Оформите подписку или активируйте промокод — и данные для подключения появятся здесь.</p>
        <div className="flex flex-col sm:flex-row gap-3 justify-center">
          <Link to="/payment" className="btn-primary !py-2.5 !text-sm">Выбрать тариф</Link>
          <Link to="/dashboard" className="btn-ghost !py-2.5 !text-sm">В кабинет</Link>
        </div>
      </div>
    </section>
  )

  const happHref = subUrl ? `/connect?sub=${encodeURIComponent(extractTokenFromUrl(subUrl))}` : '#'

  return (
    <section className="relative py-10 md:py-16 overflow-hidden">
      <div className="pointer-events-none absolute top-0 left-1/4 w-96 h-96 bg-brand/6 rounded-full blur-[130px]" />

      <div className="relative max-w-3xl mx-auto px-4 sm:px-6">
        {/* Header */}
        <div className="flex items-center justify-between mb-8 gap-3 flex-wrap animate-fade-in">
          <div>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Подключение</h1>
            <p className="text-sm text-text-secondary mt-1">Настройте устройство за пару минут</p>
          </div>
          <Link to="/dashboard" className="btn-ghost !py-2 !px-4 !text-sm">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" /></svg>
            Кабинет
          </Link>
        </div>

        {/* Config updated notice */}
        {configUpdated && subUrl && (
          <div className="p-4 rounded-xl bg-accent/10 border border-accent/20 mb-5 animate-fade-in">
            <div className="flex items-start gap-3">
              <svg className="w-5 h-5 text-accent flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" /></svg>
              <div className="flex-1">
                <p className="text-accent text-sm font-medium">Настройки обновились</p>
                <p className="text-xs text-text-secondary mt-1">Параметры подключения оптимизированы. Обновите подписку в приложении, чтобы применить их.</p>
                <div className="flex items-center gap-3 mt-3">
                  <button onClick={() => { copyText(subUrl, 'sub-update'); setConfigUpdated(false) }} className="px-4 py-2 rounded-lg bg-accent/15 text-accent text-sm font-medium hover:bg-accent/25 transition">
                    {copiedField === 'sub-update' ? '✓ Скопировано' : 'Скопировать ссылку'}
                  </button>
                  <button onClick={() => setConfigUpdated(false)} className="text-xs text-text-secondary hover:text-text-primary transition">Скрыть</button>
                </div>
              </div>
            </div>
          </div>
        )}

        {/* ═══ STEP 1 — the recommended path, front and centre ═══ */}
        <div className="card-feat rounded-[22px] p-6 md:p-7 mb-5 reveal">
          <div className="flex items-center gap-2 mb-1">
            <span className="w-6 h-6 rounded-full bg-brand text-[#04231a] text-xs font-bold grid place-items-center">1</span>
            <h2 className="serif text-lg font-semibold">Подключить автоматически</h2>
          </div>
          <p className="text-sm text-text-secondary mb-5">Откроет приложение и добавит подписку — ничего копировать не нужно.</p>
          <a href={happHref} className="btn-primary w-full !py-4 !rounded-2xl text-base">
            <IconLink className="w-5 h-5" />
            Подключить в приложении
          </a>
          <p className="text-xs text-faint mt-3 text-center">
            Нет приложения? <a href="https://www.happ.su/main/ru" target="_blank" rel="noopener noreferrer" className="text-brand hover:text-brand-light">Скачайте Happ</a> — займёт минуту
          </p>
        </div>

        <TelegramProxyButton token={token} />

        {/* ═══ STEP 2 — manual methods, one at a time (progressive disclosure) ═══ */}
        <div className="card rounded-[22px] p-6 md:p-7 mb-5 reveal">
          <div className="flex items-center gap-2 mb-1">
            <span className="w-6 h-6 rounded-full bg-brand/15 border border-brand/25 text-brand text-xs font-bold grid place-items-center">2</span>
            <h2 className="serif text-lg font-semibold">Или добавить вручную</h2>
          </div>
          <p className="text-sm text-text-secondary mb-5">Отсканируйте QR-код в приложении или скопируйте ссылку подписки.</p>


          {/* ── Подписка ── */}
          {subUrl ? (
            <div className="flex flex-col sm:flex-row gap-5 items-start animate-fade-in">
              <QRCode data={subUrl} size={168} className="flex-shrink-0 mx-auto sm:mx-0" />
              <div className="flex-1 min-w-0 w-full">
                <p className="text-sm font-medium mb-1">Ссылка подписки</p>
                <p className="text-xs text-text-secondary mb-3">Работает в Happ и других приложениях, которые принимают ссылку подписки. Настройки обновляются сами.</p>
                <div className="flex gap-2 mb-3">
                  <button onClick={() => copyText(subUrl, 'sub')} className="btn-primary !py-2.5 !text-sm flex-1 sm:flex-none">
                    {copiedField === 'sub' ? '✓ Скопировано' : 'Копировать ссылку'}
                  </button>
                </div>
                <Reveal label="Показать ссылку целиком">
                  <code className="block text-[11px] font-mono text-text-primary break-all select-all bg-bg-app border border-brand/10 rounded-lg p-2.5">{subUrl}</code>
                </Reveal>
              </div>
            </div>
          ) : (
            <p className="text-sm text-text-secondary">Ссылку подписки сейчас не удалось получить — обновите страницу через минуту.</p>
          )}

        </div>

        {/* ═══ STEP 3 — device-specific instructions ═══ */}
        <div className="card rounded-[22px] p-6 md:p-7 mb-5 reveal">
          <div className="flex items-center gap-2 mb-4">
            <span className="w-6 h-6 rounded-full bg-brand/15 border border-brand/25 text-brand text-xs font-bold grid place-items-center">3</span>
            <h2 className="serif text-lg font-semibold">Пошаговая инструкция</h2>
          </div>

          <div className="inline-flex p-1 rounded-xl bg-white/[0.04] border border-brand/10 mb-5 gap-1" role="tablist">
            {DEVICES.map(d => (
              <button
                key={d.id} role="tab" aria-selected={device === d.id}
                onClick={() => setDevice(d.id)}
                className={`px-3.5 py-2 rounded-lg text-sm font-medium transition-all duration-200 inline-flex items-center gap-2 ${
                  device === d.id ? 'bg-brand text-[#04231a]' : 'text-text-secondary hover:text-text-primary'
                }`}
              >
                <d.Icon className="w-4 h-4" />{d.label}
              </button>
            ))}
          </div>

          <ol className="space-y-3.5">
            {DEVICE_STEPS[device].map((step, i) => (
              <li key={i} className="flex gap-3 animate-fade-in" style={{ animationDelay: `${i * 45}ms` }}>
                <span className="flex-shrink-0 w-6 h-6 rounded-full bg-white/[0.05] border border-brand/12 text-faint text-xs font-semibold grid place-items-center">{i + 1}</span>
                <p className="text-sm text-text-secondary pt-0.5">{step}</p>
              </li>
            ))}
          </ol>
        </div>

      </div>
    </section>
  )
}
