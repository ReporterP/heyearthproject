import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getProfile, getSubscription, applyPromocode, createDonation, getTrafficStats, getDonationSettings, getSubscribeLink, getMtproto } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import TrafficCard from '../components/TrafficCard.jsx'
import { CabinetSkeleton } from '../components/Skeleton.jsx'
import useReveal from '../hooks/useReveal.js'

// Extracts the trailing token segment from a subscribe URL like
// https://hiearth.ru/api/v1/user/subscribe/<token>
function extractTokenFromUrl(url) {
  if (!url) return ''
  const parts = url.split('/')
  return parts[parts.length - 1] || ''
}

function ConnectionActions({ token, subscribeUrl }) {
  const [mtproto, setMtproto] = useState(null)

  useEffect(() => {
    if (!token) return
    let cancelled = false
    getMtproto(token).then((data) => {
      if (!cancelled) setMtproto(data)
    }).catch(() => {/* hide MTProto buttons on error */})
    return () => { cancelled = true }
  }, [token])

  const happHref = subscribeUrl
    ? `/connect?sub=${encodeURIComponent(extractTokenFromUrl(subscribeUrl))}`
    : '#'

  const mtprotoEnabled = mtproto && mtproto.enabled
  const cols = mtprotoEnabled ? 'sm:grid-cols-3' : 'sm:grid-cols-1'

  return (
    <div className={`grid grid-cols-1 ${cols} gap-3`}>
      <a href={happHref} className="btn-primary !py-4 !rounded-2xl min-h-[60px] text-base">
        <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
        </svg>
        <span>Добавить в приложение</span>
      </a>

      {mtprotoEnabled && (
        <a
          href={mtproto.tg_deeplink}
          className="group flex items-center justify-center gap-3 px-5 py-4 rounded-2xl bg-[#229ED9] hover:bg-[#38b0e8] text-white font-semibold transition-all duration-200 min-h-[60px]"
        >
          <svg className="w-6 h-6" viewBox="0 0 24 24" fill="currentColor">
            <path d="M21.05 3.05 2.95 9.13c-.93.32-.92 1.6 0 1.92l4.62 1.55 1.55 4.62c.32.93 1.6.92 1.92 0l1.05-3.13 3.65 2.74c.7.52 1.7.13 1.86-.72L21.6 4.07c.17-.84-.6-1.55-1.55-1.02Z" />
          </svg>
          <span>Открыть в Telegram</span>
        </a>
      )}

      {mtprotoEnabled && (
        <a
          href={mtproto.t_me_link}
          target="_blank"
          rel="noopener noreferrer"
          className="btn-ghost !py-4 !rounded-2xl min-h-[60px] text-base"
        >
          <svg className="w-6 h-6 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" />
          </svg>
          <span>Открыть через t.me</span>
        </a>
      )}
    </div>
  )
}

function formatDate(d) {
  if (!d) return '—'
  return new Date(d).toLocaleDateString('ru-RU')
}

/* ─── Icons ─── */
const CopyIcon = () => (
  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25" />
  </svg>
)
const CheckIcon = () => (
  <svg className="w-4 h-4 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
  </svg>
)
const CloseIcon = () => (
  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
  </svg>
)
const ChevronIcon = () => (
  <svg className="w-5 h-5 text-faint flex-shrink-0 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
  </svg>
)

/* ─── Client Apps Data ─── */
const CLIENT_APPS = [
  { name: 'Happ', platforms: 'iOS, Android, Windows', url: 'https://www.happ.su/main/ru' },
  { name: 'V2RayTun', platforms: 'iOS, Android', url: 'https://v2raytun.com' },
  { name: 'v2rayNG', platforms: 'Android', url: 'https://play.google.com/store/apps/details?id=com.v2ray.ang' },
  { name: 'Streisand', platforms: 'iOS, macOS', url: 'https://apps.apple.com/app/streisand/id6450534064' },
]

/* ─── Modal Overlay ─── */
function ModalOverlay({ open, onClose, children }) {
  useEffect(() => {
    if (open) document.body.style.overflow = 'hidden'
    else document.body.style.overflow = ''
    return () => { document.body.style.overflow = '' }
  }, [open])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center p-4" onClick={onClose}>
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
      <div
        className="relative w-full max-w-md max-h-[90vh] overflow-y-auto rounded-[22px] glass-strong p-6 animate-fade-in-scale"
        style={{ WebkitOverflowScrolling: 'touch' }}
        onClick={e => e.stopPropagation()}
      >
        <button
          onClick={onClose}
          aria-label="Закрыть"
          className="absolute top-4 right-4 p-1.5 rounded-lg hover:bg-white/10 text-text-secondary hover:text-text-primary transition min-h-[44px] min-w-[44px] flex items-center justify-center"
        >
          <CloseIcon />
        </button>
        {children}
      </div>
    </div>
  )
}

/* ─── Copy helper with fallback for HTTP ─── */
async function copyToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '-9999px'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()
  try {
    document.execCommand('copy')
    return true
  } catch {
    return false
  } finally {
    document.body.removeChild(textarea)
  }
}

/* Reusable secondary-nav tile */
function NavTile({ to, onClick, disabled, icon, title, subtitle, delay }) {
  const cls = `group card card-hover flex items-center gap-4 p-5 rounded-[22px] min-h-[44px] reveal ${disabled ? 'opacity-40 pointer-events-none' : ''}`
  const inner = (
    <>
      <div className="w-11 h-11 rounded-xl bg-brand/12 border border-brand/20 text-brand flex items-center justify-center flex-shrink-0 group-hover:scale-110 transition-transform">{icon}</div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold">{title}</p>
        <p className="text-xs text-text-secondary mt-0.5">{subtitle}</p>
      </div>
      <ChevronIcon />
    </>
  )
  const style = { transitionDelay: `${delay}ms` }
  return to
    ? <Link to={to} onClick={onClick} className={cls} style={style}>{inner}</Link>
    : <button onClick={onClick} className={`${cls} text-left w-full`} style={style}>{inner}</button>
}

export default function DashboardPage() {
  const navigate = useNavigate()
  const { token, logout } = useAuth()

  const [profile, setProfile] = useState(null)
  const [subscription, setSubscription] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [copiedField, setCopiedField] = useState('')

  // Promo code
  const [promoCode, setPromoCode] = useState('')
  const [promoLoading, setPromoLoading] = useState(false)
  const [promoMsg, setPromoMsg] = useState('')
  const [promoError, setPromoError] = useState('')

  // Donation
  const [donationAmount, setDonationAmount] = useState(500)
  const [customDonationAmount, setCustomDonationAmount] = useState('')
  const [donationLoading, setDonationLoading] = useState(false)
  const [donationError, setDonationError] = useState('')
  const [donationSettings, setDonationSettings] = useState(null)
  const [donationSettingsLoaded, setDonationSettingsLoaded] = useState(false)

  // Traffic + subscribe link
  const [trafficStats, setTrafficStats] = useState(null)
  const [subscribeLink, setSubscribeLink] = useState(null)

  // Modals
  const [showAppsModal, setShowAppsModal] = useState(false)
  const [showHelpModal, setShowHelpModal] = useState(false)

  const [paymentPopup, setPaymentPopup] = useState(null) // { name, price, activated }

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    Promise.all([
      getProfile(token),
      getSubscription(token).catch(() => null),
      getTrafficStats(token).catch(() => null),
      getDonationSettings().catch(() => null),
      getSubscribeLink(token).catch(() => null),
    ]).then(([profileData, subData, trafficData, donationData, subLinkData]) => {
      setProfile(profileData)
      if (subData?.items?.length > 0) {
        const active = subData.items.find(s => s.active) || subData.items[0]
        setSubscription(active)
      }
      setTrafficStats(trafficData)
      setDonationSettings(donationData)
      setDonationSettingsLoaded(true)
      setSubscribeLink(subLinkData)

      // Check for pending payment from PaymentPage redirect
      try {
        const pendingRaw = sessionStorage.getItem('payment_pending')
        if (pendingRaw) {
          sessionStorage.removeItem('payment_pending')
          const pending = JSON.parse(pendingRaw)
          const activePlan = profileData?.current_plan || ''
          const activated = activePlan && pending.name && activePlan.toLowerCase().includes(pending.name.toLowerCase().split(' ')[0])
          setPaymentPopup({ name: pending.name, price: pending.price, activated: !!activated })
        }
      } catch {}
    }).catch(e => {
      if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
        logout()
        navigate('/login')
        return
      }
      setError(e.message || 'Не удалось загрузить данные. Обновите страницу.')
    }).finally(() => setLoading(false))
  }, [])

  useReveal([loading, trafficStats])

  const userId = profile?.user_id || '…'
  const username = profile?.username || ''
  const currentPlan = profile?.current_plan || subscription?.plan_name || subscription?.group_name || ''
  const validUntil = profile?.valid_until || subscription?.valid_until
  const pausedUntil = profile?.paused_until
  const isPaused = pausedUntil ? new Date(pausedUntil) > new Date() : false
  const isExpired = validUntil ? new Date(validUntil) < new Date() : false
  const hasActiveSub = !!currentPlan && currentPlan !== '' && !isExpired && !isPaused

  const donationEnabled = donationSettingsLoaded
    ? (donationSettings?.donation_enabled !== 'false' && donationSettings?.donation_enabled !== false)
    : true
  const donationTitle = donationSettings?.donation_title || 'Поддержать разработчика'
  const donationText = donationSettings?.donation_text || null

  async function copyText(text, field) {
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopiedField(field)
      setTimeout(() => setCopiedField(''), 2000)
    }
  }

  async function handleApplyPromo() {
    if (!promoCode.trim()) return
    setPromoLoading(true)
    setPromoMsg('')
    setPromoError('')
    try {
      const data = await applyPromocode(token, promoCode.trim())
      setPromoMsg(data.message || 'Промокод активирован!')
      setPromoCode('')
      if (data.profile) setProfile(data.profile)
      const subData = await getSubscription(token).catch(() => null)
      if (subData?.items?.length > 0) {
        const active = subData.items.find(s => s.active) || subData.items[0]
        setSubscription(active)
      }
    } catch (e) {
      setPromoError(e.message || 'Не удалось применить промокод')
    } finally {
      setPromoLoading(false)
    }
  }

  const effectiveDonationAmount = customDonationAmount ? Number(customDonationAmount) : donationAmount

  async function handleDonate() {
    if (!effectiveDonationAmount || effectiveDonationAmount < 50) return
    setDonationLoading(true)
    setDonationError('')
    try {
      const returnUrl = window.location.origin + '/dashboard'
      const data = await createDonation(token, effectiveDonationAmount, returnUrl)
      if (data.confirmation_url) window.location.href = data.confirmation_url
      else setDonationError('Не удалось создать платёж. Попробуйте позже.')
    } catch (e) {
      setDonationError(e.message || 'Не удалось создать платёж. Попробуйте позже.')
    } finally {
      setDonationLoading(false)
    }
  }

  /* ─── Loading (skeleton, not a bare spinner) ─── */
  if (loading) return <CabinetSkeleton />

  /* ─── Error ─── */
  if (error) return (
    <div className="flex items-center justify-center py-32">
      <div className="text-center glass-strong rounded-[22px] p-8 max-w-sm mx-6">
        <div className="w-12 h-12 rounded-full bg-danger/10 flex items-center justify-center mx-auto mb-4">
          <svg className="w-6 h-6 text-danger" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
          </svg>
        </div>
        <p className="text-danger mb-4 text-sm">{error}</p>
        <button className="btn-primary !py-2.5 !text-sm" onClick={() => window.location.reload()}>Повторить</button>
      </div>
    </div>
  )

  // status pill for the connect hero
  const statusPill = hasActiveSub
    ? { dot: 'bg-brand', label: 'Подписка активна', cls: 'text-brand' }
    : isPaused && currentPlan ? { dot: 'bg-accent animate-pulse', label: 'На паузе', cls: 'text-accent' }
    : isExpired && currentPlan ? { dot: 'bg-danger', label: 'Подписка истекла', cls: 'text-danger' }
    : { dot: 'bg-accent', label: 'Нет активной подписки', cls: 'text-accent' }

  return (
    <section className="relative py-10 md:py-16 overflow-x-hidden" style={{ paddingBottom: 'max(2.5rem, env(safe-area-inset-bottom))' }}>
      <div className="pointer-events-none absolute top-0 left-1/4 w-96 h-96 bg-brand/6 rounded-full blur-[130px]" />

      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">
        {/* ─── Header: greeting + ID chip + settings ─── */}
        <div className="flex items-start justify-between gap-3 mb-8 flex-wrap animate-fade-in">
          <div className="min-w-0">
            <h1 className="serif text-2xl md:text-3xl font-semibold">Личный кабинет</h1>
            <div className="flex items-center gap-2 mt-1.5">
              <span className="text-sm text-text-secondary">{username ? `@${username}` : 'Ваш аккаунт'}</span>
              <button
                onClick={() => copyText(userId, 'id-header')}
                className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md bg-white/[0.03] border border-brand/10 text-[11px] font-mono text-faint hover:text-text-secondary hover:border-brand/25 transition"
                title="Скопировать HiEarth ID"
              >
                ID {copiedField === 'id-header' ? <CheckIcon /> : <span className="max-w-[120px] truncate">{userId}</span>}
              </button>
            </div>
          </div>
          <Link to="/settings" className="btn-ghost !py-2 !px-4 !text-sm">
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" />
              <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
            Настройки
          </Link>
        </div>

        {/* ═══ CONNECT HERO — the one job of this page, grouped ═══ */}
        <div className="card-feat rounded-[22px] p-6 md:p-7 mb-6 reveal">
          {/* status strip */}
          <div className="flex items-start justify-between gap-4 flex-wrap mb-1">
            <div>
              <span className={`inline-flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wider ${statusPill.cls}`}>
                <span className={`w-1.5 h-1.5 rounded-full ${statusPill.dot}`} />{statusPill.label}
              </span>
              <p className="serif text-xl md:text-2xl font-semibold mt-1">{currentPlan || 'Подключение'}</p>
              {hasActiveSub && validUntil && <p className="text-sm text-text-secondary mt-0.5">Действует до {formatDate(validUntil)}</p>}
              {isPaused && currentPlan && <p className="text-sm text-text-secondary mt-0.5">На паузе до {formatDate(pausedUntil)}</p>}
              {isExpired && currentPlan && <p className="text-sm text-text-secondary mt-0.5">Закончилась {formatDate(validUntil)}</p>}
            </div>
            {!hasActiveSub && (
              <Link to={isPaused ? '/payments' : '/payment'} className="btn-primary !py-2.5 !text-sm">
                {isPaused ? 'Возобновить' : isExpired ? 'Продлить' : 'Оформить подписку'}
                <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" /></svg>
              </Link>
            )}
          </div>

          {hasActiveSub ? (
            <div className="mt-5 space-y-4">
              {/* primary connect action */}
              <ConnectionActions token={token} subscribeUrl={subscribeLink?.subscribe_url} />

              {/* subscribe link — one place, with copy */}
              {subscribeLink?.subscribe_url && (
                <div>
                  <div className="flex items-center justify-between gap-2 mb-1.5">
                    <p className="text-xs text-faint">Ссылка для ручного добавления в приложение</p>
                    <button onClick={() => setShowAppsModal(true)} className="text-xs text-brand hover:text-brand-light">Какое приложение?</button>
                  </div>
                  <div className="flex items-stretch gap-2">
                    <code className="flex-1 px-3 py-2 rounded-lg bg-bg-app border border-brand/10 text-xs font-mono break-all overflow-hidden text-ellipsis whitespace-nowrap">{subscribeLink.subscribe_url}</code>
                    <button onClick={() => copyText(subscribeLink.subscribe_url, 'sub-url')} className="px-3 py-2 rounded-lg bg-brand/15 text-brand hover:bg-brand/25 text-sm transition shrink-0 min-w-[104px]">
                      {copiedField === 'sub-url' ? '✓ Скопировано' : 'Копировать'}
                    </button>
                  </div>
                </div>
              )}
            </div>
          ) : (
            <p className="text-sm text-text-secondary mt-3">
              {isPaused ? 'Доступ закрыт на время паузы. Возобновите, чтобы продолжить.' : 'Оформите подписку или введите промокод ниже, чтобы получить доступ.'}
            </p>
          )}
        </div>

        {/* ─── Secondary actions grid (one consistent grid, no scattered buttons) ─── */}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-6">
          <NavTile
            to="/payments" delay={0}
            title="Оплата и подписка" subtitle="История, карты, автопродление, пауза"
            icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 002.25-2.25V6.75A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25v10.5A2.25 2.25 0 004.5 19.5z" /></svg>}
          />
          <NavTile
            to={hasActiveSub ? '/connections' : undefined} onClick={hasActiveSub ? undefined : (e) => e.preventDefault()} disabled={!hasActiveSub} delay={60}
            title="Подключение и настройка" subtitle="Ссылки, QR-коды, усиленный канал"
            icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" /></svg>}
          />
          <NavTile
            onClick={() => setShowAppsModal(true)} delay={120}
            title="Приложения-клиенты" subtitle="Happ, v2rayNG, Streisand и другие"
            icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" /></svg>}
          />
          <NavTile
            to="/settings" delay={180}
            title={profile?.username || profile?.has_password ? 'Логин и пароль' : 'Создать логин и пароль'}
            subtitle={profile?.username ? `@${profile.username} — изменить` : 'Входить по короткому имени вместо ID'}
            icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" /></svg>}
          />
        </div>

        {/* Help nudge — one link, not a redundant button */}
        <button onClick={() => setShowHelpModal(true)} className="w-full mb-6 flex items-center justify-center gap-2 px-4 py-3 rounded-xl border border-dashed border-brand/20 hover:border-brand/40 hover:bg-brand/5 text-sm text-text-secondary hover:text-text-primary transition reveal">
          <svg className="w-4 h-4 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}><path strokeLinecap="round" strokeLinejoin="round" d="M9.879 7.519c1.171-1.025 3.071-1.025 4.242 0 1.172 1.025 1.172 2.687 0 3.712-.203.179-.43.326-.67.442-.745.361-1.45.999-1.45 1.827v.75M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9 5.25h.008v.008H12v-.008z" /></svg>
          Не знаете, как подключиться? Покажем за 4 шага
        </button>

        {/* ─── Traffic ─── */}
        {trafficStats && (
          <div className="mb-6 reveal">
            <TrafficCard data={trafficStats} />
          </div>
        )}

        {/* ─── Quiet row: promo + donation, side by side on desktop ─── */}
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {/* Promo code */}
          <div className="card rounded-[22px] p-6 reveal">
            <h2 className="serif text-base font-semibold mb-1">Промокод</h2>
            <p className="text-xs text-text-secondary mb-4">Есть код? Активируйте подписку или бонус</p>
            <div className="flex flex-col sm:flex-row gap-3">
              <input
                type="text"
                value={promoCode}
                onChange={e => setPromoCode(e.target.value.toUpperCase())}
                placeholder="Введите промокод"
                onKeyDown={e => e.key === 'Enter' && handleApplyPromo()}
                className="flex-1 px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:bg-white/[0.06] focus:outline-none focus:ring-2 focus:ring-brand/25 transition-all duration-300 text-sm placeholder:text-faint font-mono uppercase tracking-wider"
              />
              <button disabled={promoLoading || !promoCode.trim()} onClick={handleApplyPromo} className="btn-primary !py-3 whitespace-nowrap disabled:opacity-50">
                {promoLoading ? (
                  <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                    <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                    <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                  </svg>
                ) : 'Активировать'}
              </button>
            </div>
            {promoMsg && <p className="flex items-center gap-2 mt-3 text-brand text-sm animate-fade-in"><CheckIcon />{promoMsg}</p>}
            {promoError && <p className="text-danger text-sm mt-3 animate-fade-in">{promoError}</p>}
          </div>

          {/* Donation */}
          {donationEnabled && (
            <div className="card rounded-[22px] p-6 reveal !border-accent/15">
              <h2 className="serif text-base font-semibold mb-1">{donationTitle}</h2>
              <p className="text-xs text-text-secondary mb-4">{donationText ? '' : 'Поможете выпустить нативное приложение для iOS/macOS'}{donationText}</p>
              <div className="flex flex-wrap gap-2 mb-3">
                {[100, 300, 500, 1000].map(amt => (
                  <button
                    key={amt}
                    onClick={() => { setDonationAmount(amt); setCustomDonationAmount('') }}
                    className={`px-3.5 py-2 rounded-xl text-sm font-medium transition-all duration-200 min-h-[40px] ${!customDonationAmount && donationAmount === amt ? 'bg-accent text-[#2a1e02] font-semibold' : 'glass hover:bg-white/10 text-text-secondary hover:text-text-primary'}`}
                  >{amt} ₽</button>
                ))}
                <input
                  type="number" min="50" step="50" value={customDonationAmount}
                  onChange={e => setCustomDonationAmount(e.target.value)}
                  placeholder="Своя"
                  className="w-24 px-3 py-2 rounded-xl bg-white/5 border border-accent/15 focus:border-accent/50 focus:outline-none focus:ring-2 focus:ring-accent/25 text-sm placeholder:text-faint"
                />
              </div>
              <button
                disabled={donationLoading || effectiveDonationAmount < 50}
                onClick={handleDonate}
                className="inline-flex items-center justify-center gap-2 px-6 py-2.5 rounded-full font-semibold text-sm bg-accent text-[#2a1e02] transition-all duration-200 hover:brightness-105 active:scale-[0.97] disabled:opacity-50 min-h-[44px]"
              >
                {donationLoading ? (
                  <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" /><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" /></svg>
                ) : (
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M21 8.25c0-2.485-2.099-4.5-4.688-4.5-1.935 0-3.597 1.126-4.312 2.733-.715-1.607-2.377-2.733-4.313-2.733C5.1 3.75 3 5.765 3 8.25c0 7.22 9 12 9 12s9-4.78 9-12z" /></svg>
                )}
                Поддержать — {effectiveDonationAmount} ₽
              </button>
              {donationError && <p className="text-danger text-sm mt-3">{donationError}</p>}
            </div>
          )}
        </div>
      </div>

      {/* ─── Apps Modal ─── */}
      <ModalOverlay open={showAppsModal} onClose={() => setShowAppsModal(false)}>
        <div className="pr-8">
          <h2 className="serif text-lg font-semibold mb-1">Приложения-клиенты</h2>
          <p className="text-sm text-text-secondary mb-5">Выберите приложение для вашего устройства</p>
        </div>
        <div className="space-y-3">
          {CLIENT_APPS.map(app => (
            <div key={app.name} className="flex items-center justify-between p-4 rounded-xl bg-white/[0.04] border border-brand/10 hover:border-brand/20 transition">
              <div className="min-w-0">
                <p className="font-semibold text-sm">{app.name}</p>
                <p className="text-xs text-text-secondary mt-0.5">{app.platforms}</p>
              </div>
              <a href={app.url} target="_blank" rel="noopener noreferrer" className="flex-shrink-0 ml-3 px-4 py-2 rounded-xl bg-brand/15 text-brand text-xs font-medium hover:bg-brand/25 transition min-h-[44px] flex items-center">Открыть</a>
            </div>
          ))}
        </div>
      </ModalOverlay>

      {/* ─── Help Modal ─── */}
      <ModalOverlay open={showHelpModal} onClose={() => setShowHelpModal(false)}>
        <div className="pr-8">
          <h2 className="serif text-lg font-semibold mb-4">Как начать пользоваться HiEarth?</h2>
        </div>
        <div className="space-y-5">
          <div className="flex gap-3">
            <div className="flex-shrink-0 w-7 h-7 rounded-full bg-brand/20 text-brand flex items-center justify-center text-sm font-bold">1</div>
            <div>
              <p className="text-sm font-medium">Установите одно из приложений-клиентов</p>
              <button onClick={() => { setShowHelpModal(false); setShowAppsModal(true) }} className="text-xs text-brand hover:text-brand-light transition mt-1 min-h-[44px] flex items-center">Открыть список приложений →</button>
            </div>
          </div>
          <div className="flex gap-3">
            <div className="flex-shrink-0 w-7 h-7 rounded-full bg-brand/20 text-brand flex items-center justify-center text-sm font-bold">2</div>
            <div>
              <p className="text-sm font-medium">Нажмите «Добавить в приложение»</p>
              <p className="text-xs text-text-secondary mt-1">Кнопка в блоке подключения выше — откроет приложение автоматически</p>
            </div>
          </div>
          <div className="flex gap-3">
            <div className="flex-shrink-0 w-7 h-7 rounded-full bg-brand/20 text-brand flex items-center justify-center text-sm font-bold">3</div>
            <div>
              <p className="text-sm font-medium">Или вставьте ссылку подписки вручную</p>
              <div className="mt-1.5 space-y-1.5 text-xs text-text-secondary">
                <p><span className="text-text-primary font-medium">Happ:</span> Подписки → + → Вставить из буфера</p>
                <p><span className="text-text-primary font-medium">v2rayNG:</span> + → Импорт из буфера обмена</p>
              </div>
            </div>
          </div>
          <div className="flex gap-3">
            <div className="flex-shrink-0 w-7 h-7 rounded-full bg-brand/20 text-brand flex items-center justify-center text-sm font-bold">4</div>
            <div><p className="text-sm font-medium">Готово! Включите подключение в приложении</p></div>
          </div>
        </div>
        <div className="mt-5 p-3 rounded-xl bg-white/[0.04] border border-brand/10">
          <p className="text-xs text-text-secondary leading-relaxed">Если что-то не работает, попробуйте другое приложение или напишите нам: <a href="mailto:alex_step04@mail.ru" className="text-brand hover:text-brand-light transition">alex_step04@mail.ru</a></p>
        </div>
      </ModalOverlay>

      {/* ─── Payment Status Popup ─── */}
      <ModalOverlay open={!!paymentPopup} onClose={() => setPaymentPopup(null)}>
        {paymentPopup && (
          <div className="text-center py-2">
            {paymentPopup.activated ? (
              <>
                <div className="w-16 h-16 rounded-full bg-brand/15 border border-brand/25 flex items-center justify-center mx-auto mb-4">
                  <svg className="w-8 h-8 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
                </div>
                <h2 className="serif text-lg font-semibold mb-2">Подписка активирована!</h2>
                <p className="text-sm text-text-secondary mb-1">Тариф «{paymentPopup.name}»</p>
                {paymentPopup.price > 0 && <p className="serif text-2xl font-semibold text-brand mb-4 tabular-nums">{paymentPopup.price} ₽</p>}
                <p className="text-xs text-text-secondary mb-6">Все настройки подключения доступны в блоке подключения выше</p>
              </>
            ) : (
              <>
                <div className="w-16 h-16 rounded-full bg-accent/15 border border-accent/25 flex items-center justify-center mx-auto mb-4">
                  <svg className="w-8 h-8 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}><path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                </div>
                <h2 className="serif text-lg font-semibold mb-2">Платёж обрабатывается</h2>
                <p className="text-sm text-text-secondary mb-1">Тариф «{paymentPopup.name}»</p>
                {paymentPopup.price > 0 && <p className="serif text-2xl font-semibold text-accent mb-4 tabular-nums">{paymentPopup.price} ₽</p>}
                <p className="text-xs text-text-secondary mb-6">Подписка будет активирована в течение нескольких минут. Обновите страницу чуть позже.</p>
              </>
            )}
            <button onClick={() => setPaymentPopup(null)} className="btn-primary w-full !py-3">Закрыть</button>
          </div>
        )}
      </ModalOverlay>
    </section>
  )
}
