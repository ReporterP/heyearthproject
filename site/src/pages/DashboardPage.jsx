import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getProfile, getSubscription, getConnections, getPayments, getDownloads, applyPromocode, createDonation, getTrafficStats, getSubscribeLink } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'

function formatDate(d) {
  if (!d) return '---'
  return new Date(d).toLocaleDateString('ru-RU')
}

function formatAmount(amount) {
  if (!amount && amount !== 0) return '---'
  return Number(amount).toLocaleString('ru-RU') + ' \u20BD'
}

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i]
}

/* ─── Copy helper with fallback for HTTP ─── */
async function copyToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return true
  }
  // Fallback для HTTP
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

/* ─── Icons ─── */
const CopyIcon = () => (
  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 17.25v3.375c0 .621-.504 1.125-1.125 1.125h-9.75a1.125 1.125 0 01-1.125-1.125V7.875c0-.621.504-1.125 1.125-1.125H6.75a9.06 9.06 0 011.5.124m7.5 10.376h3.375c.621 0 1.125-.504 1.125-1.125V11.25c0-4.46-3.243-8.161-7.5-8.876a9.06 9.06 0 00-1.5-.124H9.375c-.621 0-1.125.504-1.125 1.125v3.5m7.5 10.375H9.375a1.125 1.125 0 01-1.125-1.125v-9.25m12 6.625v-1.875a3.375 3.375 0 00-3.375-3.375h-1.5a1.125 1.125 0 01-1.125-1.125v-1.5a3.375 3.375 0 00-3.375-3.375H9.75" />
  </svg>
)
const CheckIcon = () => (
  <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
  </svg>
)

export default function DashboardPage() {
  const navigate = useNavigate()
  const { token, logout } = useAuth()

  const [profile, setProfile] = useState(null)
  const [subscription, setSubscription] = useState(null)
  const [connections, setConnections] = useState(null)
  const [payments, setPayments] = useState([])
  const [downloads, setDownloads] = useState({})
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [copiedField, setCopiedField] = useState('')

  // Промокод
  const [promoCode, setPromoCode] = useState('')
  const [promoLoading, setPromoLoading] = useState(false)
  const [promoMsg, setPromoMsg] = useState('')
  const [promoError, setPromoError] = useState('')

  // Донат
  const [donationAmount, setDonationAmount] = useState(500)
  const [donationLoading, setDonationLoading] = useState(false)

  // Трафик
  const [trafficStats, setTrafficStats] = useState(null)
  // Ссылка подписки
  const [subscribeLink, setSubscribeLink] = useState(null)

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    Promise.all([
      getProfile(token),
      getSubscription(token).catch(() => null),
      getConnections(token).catch(() => null),
      getPayments(token).catch(() => ({ items: [] })),
      getDownloads().catch(() => ({ downloads: {} })),
      getTrafficStats(token).catch(() => null),
      getSubscribeLink(token).catch(() => null),
    ]).then(([profileData, subData, connData, payData, dlData, trafficData, subLinkData]) => {
      setProfile(profileData)
      // subData = { items: [...] } — берём первую активную
      if (subData?.items?.length > 0) {
        const active = subData.items.find(s => s.active) || subData.items[0]
        setSubscription(active)
      }
      setConnections(connData)
      setPayments(payData?.items || [])
      setDownloads(dlData?.downloads || {})
      setTrafficStats(trafficData)
      setSubscribeLink(subLinkData)
    }).catch(e => {
      if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
        logout()
        navigate('/login')
        return
      }
      setError(e.message || 'Ошибка загрузки данных')
    }).finally(() => setLoading(false))
  }, [])

  const userId = profile?.user_id || '...'
  const username = profile?.username || ''
  const currentPlan = profile?.current_plan || subscription?.group_name || subscription?.plan || ''
  const validUntil = profile?.valid_until || subscription?.valid_until
  const isPremium = profile?.is_premium || subscription?.plan === 'premium'
  const hasActiveSub = !!currentPlan && currentPlan !== ''
  const canConnect = connections?.can_connect

  const socks5Addr = connections?.socks5 || ''
  const httpProxy = connections?.http_proxy || ''
  const vlessReality = connections?.vless_reality || ''

  // Парсим SOCKS URI на составные части для карточек сервисов
  const socks5Parts = socks5Addr ? (() => {
    try {
      const url = new URL(socks5Addr)
      return { host: url.hostname, port: url.port || '1080', user: decodeURIComponent(url.username), pass: decodeURIComponent(url.password) }
    } catch { return null }
  })() : null
  const httpParts = httpProxy ? (() => {
    try {
      const url = new URL(httpProxy)
      return { host: url.hostname, port: url.port || '8080', user: decodeURIComponent(url.username), pass: decodeURIComponent(url.password) }
    } catch { return null }
  })() : null

  // Telegram SOCKS deeplink
  const tgSocksLink = socks5Parts
    ? `https://t.me/socks?server=${socks5Parts.host}&port=${socks5Parts.port}&user=${encodeURIComponent(socks5Parts.user)}&pass=${encodeURIComponent(socks5Parts.pass)}`
    : ''

  // QR code helper (external API, no deps needed)
  const qrUrl = (data) => `https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(data)}`

  async function copyText(text, field) {
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopiedField(field)
      setTimeout(() => setCopiedField(''), 2000)
    }
  }

  function CopyButton({ field, text }) {
    return (
      <button
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium glass hover:bg-white/10 transition-all duration-200"
        onClick={() => copyText(text, field)}
      >
        {copiedField === field ? <CheckIcon /> : <CopyIcon />}
        {copiedField === field ? 'Скопировано!' : 'Копировать'}
      </button>
    )
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
      // Перезагрузим подписку
      const subData = await getSubscription(token).catch(() => null)
      if (subData?.items?.length > 0) {
        const active = subData.items.find(s => s.active) || subData.items[0]
        setSubscription(active)
      }
      // Перезагрузим подключения
      const connData = await getConnections(token).catch(() => null)
      if (connData) setConnections(connData)
    } catch (e) {
      setPromoError(e.message || 'Ошибка применения промокода')
    } finally {
      setPromoLoading(false)
    }
  }

  async function handleDonate() {
    if (!donationAmount || donationAmount < 50) return
    setDonationLoading(true)
    try {
      const returnUrl = window.location.origin + '/dashboard'
      const data = await createDonation(token, donationAmount, returnUrl)
      if (data.confirmation_url) {
        window.location.href = data.confirmation_url
      }
    } catch (e) {
      alert(e.message || 'Ошибка создания доната')
    } finally {
      setDonationLoading(false)
    }
  }

  /* ─── Loading ─── */
  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="text-center">
        <div className="w-10 h-10 rounded-full border-2 border-brand/30 border-t-brand animate-spin mx-auto mb-4" />
        <p className="text-text-secondary text-sm">Загрузка...</p>
      </div>
    </div>
  )

  /* ─── Error ─── */
  if (error) return (
    <div className="flex items-center justify-center py-32">
      <div className="text-center glass-strong rounded-2xl p-8 max-w-sm mx-6">
        <div className="w-12 h-12 rounded-full bg-red-500/10 flex items-center justify-center mx-auto mb-4">
          <svg className="w-6 h-6 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
          </svg>
        </div>
        <p className="text-red-400 mb-4 text-sm">{error}</p>
        <button
          className="px-6 py-2.5 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-sm font-semibold hover:shadow-lg hover:shadow-brand/25 transition-all duration-300"
          onClick={() => window.location.reload()}
        >
          Повторить
        </button>
      </div>
    </div>
  )

  return (
    <section className="relative py-10 md:py-16">
      <div className="absolute top-0 left-1/4 w-96 h-96 bg-brand/5 rounded-full blur-[120px] pointer-events-none" />
      <div className="absolute bottom-0 right-1/4 w-96 h-96 bg-accent/5 rounded-full blur-[120px] pointer-events-none" />

      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">
        {/* Header */}
        <div className="flex items-center justify-between mb-8 animate-fade-in">
          <div>
            <h1 className="text-2xl md:text-3xl font-bold">Личный кабинет</h1>
            <p className="text-sm text-text-secondary mt-1">
              {username ? `@${username}` : 'Управление аккаунтом и подключениями'}
            </p>
          </div>
          <Link
            to="/settings"
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl glass hover:bg-white/10 text-sm text-text-secondary hover:text-text-primary transition-all duration-200"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" />
              <path strokeLinecap="round" strokeLinejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
            Настройки
          </Link>
        </div>

        {/* ─── Top cards: ID + Subscription ─── */}
        <div className="grid md:grid-cols-2 gap-4 md:gap-6 mb-6">
          {/* HeYearth ID */}
          <div className="glass-card rounded-2xl p-6 animate-fade-in-up delay-100">
            <div className="flex items-start justify-between mb-3">
              <div className="flex items-center gap-3">
                <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-brand/20 to-accent/10 flex items-center justify-center">
                  <svg className="w-5 h-5 text-brand-light" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M15 9h3.75M15 12h3.75M15 15h3.75M4.5 19.5h15a2.25 2.25 0 002.25-2.25V6.75A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25v10.5A2.25 2.25 0 004.5 19.5zm6-10.125a1.875 1.875 0 11-3.75 0 1.875 1.875 0 013.75 0zm1.294 6.336a6.721 6.721 0 01-3.17.789 6.721 6.721 0 01-3.168-.789 3.376 3.376 0 016.338 0z" />
                  </svg>
                </div>
                <p className="text-xs font-medium text-text-secondary uppercase tracking-wider">HeYearth ID</p>
              </div>
              <CopyButton field="id" text={userId} />
            </div>
            <p className="text-lg md:text-xl font-mono font-bold text-brand-light break-all leading-tight">{userId}</p>
            {username && (
              <p className="text-sm text-text-secondary mt-1.5">@{username}</p>
            )}
          </div>

          {/* Subscription */}
          <div className={`relative rounded-2xl p-6 overflow-hidden animate-fade-in-up delay-200 ${
            hasActiveSub
              ? 'glass-card border border-green-500/20'
              : 'glass-card border border-amber-500/20'
          }`}>
            {hasActiveSub && (
              <div className="absolute top-0 right-0 w-32 h-32 bg-green-500/5 rounded-bl-full pointer-events-none" />
            )}
            <div className="flex items-start justify-between mb-3">
              <div className="flex items-center gap-3">
                <div className={`w-10 h-10 rounded-xl flex items-center justify-center ${
                  hasActiveSub ? 'bg-green-500/15' : 'bg-amber-500/15'
                }`}>
                  <svg className={`w-5 h-5 ${hasActiveSub ? 'text-green-400' : 'text-amber-400'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12c0 1.268-.63 2.39-1.593 3.068a3.745 3.745 0 01-1.043 3.296 3.745 3.745 0 01-3.296 1.043A3.745 3.745 0 0112 21c-1.268 0-2.39-.63-3.068-1.593a3.746 3.746 0 01-3.296-1.043 3.745 3.745 0 01-1.043-3.296A3.745 3.745 0 013 12c0-1.268.63-2.39 1.593-3.068a3.745 3.745 0 011.043-3.296 3.746 3.746 0 013.296-1.043A3.746 3.746 0 0112 3c1.268 0 2.39.63 3.068 1.593a3.746 3.746 0 013.296 1.043 3.746 3.746 0 011.043 3.296A3.745 3.745 0 0121 12z" />
                  </svg>
                </div>
                <p className="text-xs font-medium text-text-secondary uppercase tracking-wider">Подписка</p>
              </div>
              {hasActiveSub && isPremium && (
                <span className="px-2.5 py-0.5 rounded-full bg-gradient-to-r from-amber-500/20 to-orange-500/20 text-amber-400 text-xs font-bold">
                  Premium
                </span>
              )}
            </div>

            {hasActiveSub ? (
              <>
                <p className="text-lg font-semibold">{currentPlan}</p>
                {validUntil && (
                  <div className="flex items-center gap-1.5 mt-1.5">
                    <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
                    <p className="text-sm text-text-secondary">Действует до {formatDate(validUntil)}</p>
                  </div>
                )}
                <Link
                  to="/payment"
                  className="inline-flex items-center gap-1.5 mt-3 text-sm text-brand-light hover:text-accent transition-colors"
                >
                  Продлить подписку
                  <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                  </svg>
                </Link>
              </>
            ) : (
              <>
                <p className="text-lg font-semibold text-amber-400">Нет активной подписки</p>
                <p className="text-sm text-text-secondary mt-1">Оформите подписку или введите промокод для доступа</p>
                <Link
                  to="/payment"
                  className="inline-flex items-center gap-2 mt-3 px-5 py-2.5 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white text-sm font-semibold hover:shadow-lg hover:shadow-brand/25 transition-all duration-300"
                >
                  Оформить подписку
                  <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                  </svg>
                </Link>
              </>
            )}
          </div>
        </div>

        {/* ─── Traffic Stats ─── */}
        {trafficStats && (trafficStats.traffic_used > 0 || trafficStats.traffic_limit) && (
          <div className="glass-card rounded-2xl p-6 mb-6 animate-fade-in-up delay-250">
            <div className="flex items-center gap-3 mb-4">
              <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-cyan-500/20 to-blue-500/10 flex items-center justify-center">
                <svg className="w-5 h-5 text-cyan-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z" />
                </svg>
              </div>
              <div>
                <h2 className="text-base font-semibold">Трафик</h2>
                <p className="text-xs text-text-secondary">Использование за текущий период</p>
              </div>
            </div>
            <div className="space-y-3">
              <div className="flex justify-between text-sm">
                <span className="text-text-secondary">Использовано</span>
                <span className="font-medium">{formatBytes(trafficStats.traffic_used)}</span>
              </div>
              {trafficStats.traffic_limit && (
                <>
                  <div className="w-full bg-white/10 rounded-full h-2.5">
                    <div
                      className="bg-gradient-to-r from-brand to-accent h-2.5 rounded-full transition-all duration-500"
                      style={{ width: `${Math.min(100, (trafficStats.traffic_used / trafficStats.traffic_limit) * 100)}%` }}
                    />
                  </div>
                  <div className="flex justify-between text-xs text-text-secondary">
                    <span>{Math.round((trafficStats.traffic_used / trafficStats.traffic_limit) * 100)}%</span>
                    <span>Лимит: {formatBytes(trafficStats.traffic_limit)}</span>
                  </div>
                </>
              )}
              {trafficStats.upload_bytes > 0 && (
                <div className="flex gap-4 pt-2 border-t border-white/5">
                  <div className="flex items-center gap-1.5 text-xs text-text-secondary">
                    <svg className="w-3.5 h-3.5 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 19.5l15-15m0 0H8.25m11.25 0v11.25" />
                    </svg>
                    {formatBytes(trafficStats.upload_bytes)}
                  </div>
                  <div className="flex items-center gap-1.5 text-xs text-text-secondary">
                    <svg className="w-3.5 h-3.5 text-blue-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M19.5 4.5l-15 15m0 0h11.25m-11.25 0V8.25" />
                    </svg>
                    {formatBytes(trafficStats.download_bytes)}
                  </div>
                </div>
              )}
            </div>
          </div>
        )}

        {/* Subscription Link removed — users use SOCKS/HTTP proxy directly */}

        {/* ─── Promo code ─── */}
        <div className="glass-card rounded-2xl p-6 mb-6 animate-fade-in-up delay-250">
          <div className="flex items-center gap-3 mb-4">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-purple-500/20 to-pink-500/10 flex items-center justify-center">
              <svg className="w-5 h-5 text-purple-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M9.568 3H5.25A2.25 2.25 0 003 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 005.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 009.568 3z" />
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 6h.008v.008H6V6z" />
              </svg>
            </div>
            <div>
              <h2 className="text-base font-semibold">Промокод</h2>
              <p className="text-xs text-text-secondary">Введите промокод для активации подписки</p>
            </div>
          </div>
          <div className="flex flex-col sm:flex-row gap-3">
            <input
              type="text"
              value={promoCode}
              onChange={e => setPromoCode(e.target.value.toUpperCase())}
              placeholder="Введите промокод"
              onKeyDown={e => e.key === 'Enter' && handleApplyPromo()}
              className="flex-1 px-4 py-3 rounded-xl bg-white/5 border border-white/10 focus:border-brand/50 focus:bg-white/[0.07] focus:outline-none focus:ring-1 focus:ring-brand/30 transition-all duration-300 text-sm placeholder:text-text-secondary/50 font-mono uppercase tracking-wider"
            />
            <button
              disabled={promoLoading || !promoCode.trim()}
              onClick={handleApplyPromo}
              className="px-6 py-3 rounded-xl bg-gradient-to-r from-purple-600 to-pink-600 text-white text-sm font-semibold hover:shadow-lg hover:shadow-purple-500/25 transition-all duration-300 disabled:opacity-50 whitespace-nowrap"
            >
              {promoLoading ? (
                <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
              ) : 'Активировать'}
            </button>
          </div>
          {promoMsg && (
            <div className="flex items-center gap-2 mt-3 animate-fade-in">
              <svg className="w-4 h-4 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
              </svg>
              <p className="text-green-400 text-sm">{promoMsg}</p>
            </div>
          )}
          {promoError && (
            <div className="flex items-center gap-2 mt-3 animate-fade-in">
              <svg className="w-4 h-4 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
              <p className="text-red-400 text-sm">{promoError}</p>
            </div>
          )}
        </div>

        {/* ─── Connections ─── */}
        <div className="glass-card rounded-2xl p-6 md:p-8 mb-6 animate-fade-in-up delay-300">
          <div className="flex items-center gap-3 mb-6">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-accent/20 to-cyan-500/10 flex items-center justify-center">
              <svg className="w-5 h-5 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold">Подключение</h2>
          </div>

          {!canConnect ? (
            <div className="flex items-center gap-3 p-4 rounded-xl bg-amber-500/10 border border-amber-500/20">
              <svg className="w-5 h-5 text-amber-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
              </svg>
              <div>
                <p className="text-amber-400 text-sm font-medium">Нет активной подписки</p>
                <p className="text-text-secondary text-xs mt-0.5">Оформите подписку или введите промокод, чтобы получить данные для подключения.</p>
              </div>
            </div>
          ) : (
            <>
              {/* ─── Quick Actions: Telegram + Happ ─── */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-5">
                {tgSocksLink && (
                  <a href={tgSocksLink} target="_blank" rel="noopener noreferrer"
                    className="flex items-center gap-4 p-4 rounded-xl bg-gradient-to-r from-blue-500/10 to-blue-600/5 border border-blue-500/20 hover:border-blue-500/40 transition-all duration-300 hover:-translate-y-0.5 group">
                    <div className="w-12 h-12 rounded-xl bg-blue-500/20 flex items-center justify-center flex-shrink-0 group-hover:scale-110 transition-transform">
                      <svg className="w-6 h-6 text-blue-400" viewBox="0 0 24 24" fill="currentColor"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm4.64 6.8c-.15 1.58-.8 5.42-1.13 7.19-.14.75-.42 1-.68 1.03-.58.05-1.02-.38-1.58-.75-.88-.58-1.38-.94-2.23-1.5-.99-.65-.35-1.01.22-1.59.15-.15 2.71-2.48 2.76-2.69a.2.2 0 00-.05-.18c-.06-.05-.14-.03-.21-.02-.09.02-1.49.95-4.22 2.79-.4.27-.76.41-1.08.4-.36-.01-1.04-.2-1.55-.37-.63-.2-1.12-.31-1.08-.66.02-.18.27-.36.74-.55 2.92-1.27 4.86-2.11 5.83-2.51 2.78-1.16 3.35-1.36 3.73-1.36.08 0 .27.02.39.12.1.08.13.19.14.27-.01.06.01.24 0 .38z"/></svg>
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-semibold text-blue-300">Подключить Telegram</p>
                      <p className="text-xs text-text-secondary">Нажмите — прокси настроится автоматически</p>
                    </div>
                    <svg className="w-5 h-5 text-blue-400/50 flex-shrink-0 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" /></svg>
                  </a>
                )}
                <a href="https://happ.su" target="_blank" rel="noopener noreferrer"
                  className="flex items-center gap-4 p-4 rounded-xl bg-gradient-to-r from-emerald-500/10 to-green-600/5 border border-emerald-500/20 hover:border-emerald-500/40 transition-all duration-300 hover:-translate-y-0.5 group">
                  <div className="w-12 h-12 rounded-xl bg-emerald-500/20 flex items-center justify-center flex-shrink-0 group-hover:scale-110 transition-transform">
                    <span className="text-2xl">🌿</span>
                  </div>
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-semibold text-emerald-300">Скачать Happ</p>
                    <p className="text-xs text-text-secondary">Приложение для iOS, Android, Windows</p>
                  </div>
                  <svg className="w-5 h-5 text-emerald-400/50 flex-shrink-0 group-hover:translate-x-1 transition-transform" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" /></svg>
                </a>
              </div>

              {/* ─── Connection cards: VLESS + SOCKS + HTTP ─── */}
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4 mb-5">
                {vlessReality && (
                  <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between mb-2">
                      <div className="flex items-center gap-2">
                        <span className="px-2 py-0.5 rounded-md bg-violet-500/15 text-violet-400 text-xs font-bold">VLESS Reality</span>
                        <span className="text-[10px] text-green-400 bg-green-500/10 px-1.5 py-0.5 rounded">anti-DPI</span>
                      </div>
                      <CopyButton field="vless" text={vlessReality} />
                    </div>
                    <code className="text-[11px] font-mono text-text-primary break-all select-all line-clamp-2">{vlessReality}</code>
                    <p className="text-xs text-text-secondary mt-1.5">Максимальная защита от блокировок. Для Happ, v2rayNG, Streisand</p>
                    <details className="mt-3">
                      <summary className="text-xs text-brand cursor-pointer hover:text-brand-light transition">QR-код</summary>
                      <img src={qrUrl(vlessReality)} alt="QR" className="mt-2 rounded-lg bg-white p-2 w-[180px] h-[180px]" loading="lazy" />
                    </details>
                  </div>
                )}
                {socks5Addr && (
                  <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between mb-2">
                      <span className="px-2 py-0.5 rounded-md bg-cyan-500/15 text-cyan-400 text-xs font-bold">SOCKS</span>
                      <CopyButton field="socks5" text={socks5Addr} />
                    </div>
                    <code className="text-[11px] font-mono text-text-primary break-all select-all line-clamp-2">{socks5Addr}</code>
                    <p className="text-xs text-text-secondary mt-1.5">Для Telegram, браузера, системных настроек</p>
                    <details className="mt-3">
                      <summary className="text-xs text-brand cursor-pointer hover:text-brand-light transition">QR-код</summary>
                      <img src={qrUrl(socks5Addr)} alt="QR" className="mt-2 rounded-lg bg-white p-2 w-[180px] h-[180px]" loading="lazy" />
                    </details>
                  </div>
                )}
                {httpProxy && (
                  <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between mb-2">
                      <span className="px-2 py-0.5 rounded-md bg-emerald-500/15 text-emerald-400 text-xs font-bold">HTTP</span>
                      <CopyButton field="http" text={httpProxy} />
                    </div>
                    <code className="text-[11px] font-mono text-text-primary break-all select-all line-clamp-2">{httpProxy}</code>
                    <p className="text-xs text-text-secondary mt-1.5">HTTP прокси для системных настроек</p>
                  </div>
                )}
              </div>

              {/* ─── Subscription URL (для V2Ray/Clash клиентов) ─── */}
              {subscribeLink?.subscribe_url && (
                <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5 mb-5">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <span className="px-2 py-0.5 rounded-md bg-amber-500/15 text-amber-400 text-xs font-bold">Подписка</span>
                      <span className="text-[10px] text-text-secondary">V2RayNG / Happ / Streisand / Clash</span>
                    </div>
                    <CopyButton field="subscribe" text={subscribeLink.subscribe_url} />
                  </div>
                  <code className="text-[11px] font-mono text-text-primary break-all select-all">{subscribeLink.subscribe_url}</code>
                  <p className="text-xs text-text-secondary mt-1.5">Вставьте эту ссылку в приложение как подписку — настройки обновятся автоматически</p>
                  <details className="mt-3">
                    <summary className="text-xs text-brand cursor-pointer hover:text-brand-light transition">QR-код подписки</summary>
                    <img src={qrUrl(subscribeLink.subscribe_url)} alt="QR" className="mt-2 rounded-lg bg-white p-2 w-[180px] h-[180px]" loading="lazy" />
                  </details>
                </div>
              )}

              {/* ─── Proxy credentials (отдельные поля) ─── */}
              {socks5Parts && (
                <div className="mt-5 p-4 rounded-xl bg-white/[0.02] border border-white/5">
                  <p className="text-xs font-medium text-text-secondary mb-3 uppercase tracking-wider">Данные для подключения</p>
                  <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
                    {[
                      { label: 'Сервер', value: socks5Parts.host, field: 'proxy-host' },
                      { label: 'Порт SOCKS', value: socks5Parts.port, field: 'proxy-port' },
                      { label: 'Логин', value: socks5Parts.user, field: 'proxy-user' },
                      { label: 'Пароль', value: socks5Parts.pass, field: 'proxy-pass' },
                    ].map(item => (
                      <div key={item.field}>
                        <p className="text-[10px] text-text-secondary mb-1">{item.label}</p>
                        <div className="flex items-center gap-1">
                          <code className="text-xs font-mono text-text-primary truncate flex-1">{item.value}</code>
                          <button onClick={() => copyText(item.value, item.field)} className="p-1 rounded hover:bg-white/10 transition-colors flex-shrink-0">
                            {copiedField === item.field ? <CheckIcon /> : <CopyIcon />}
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                  {httpParts && (
                    <p className="text-[10px] text-text-secondary mt-2">HTTP порт: <span className="text-text-primary font-mono">{httpParts.port}</span> (логин и пароль такие же)</p>
                  )}
                </div>
              )}

              {/* ─── Настройка для сервисов ─── */}
              {socks5Parts && (
                <div className="mt-5">
                  <p className="text-xs font-medium text-text-secondary mb-3 uppercase tracking-wider">Настройка прокси для сервисов</p>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {/* Telegram — simplified with deeplink */}
                    <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                      <div className="flex items-center gap-2 mb-2">
                        <span className="text-lg">&#9992;&#65039;</span>
                        <span className="text-sm font-medium">Telegram</span>
                      </div>
                      {tgSocksLink ? (
                        <a href={tgSocksLink} target="_blank" rel="noopener noreferrer"
                          className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-500/20 text-blue-300 text-sm font-medium hover:bg-blue-500/30 transition mb-2">
                          Подключить автоматически
                        </a>
                      ) : null}
                      <p className="text-xs text-text-secondary">Или вручную: Настройки → Данные → Прокси → SOCKS → сервер и порт выше</p>
                    </div>

                    {/* Browser */}
                    <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                      <div className="flex items-center gap-2 mb-2">
                        <span className="text-lg">&#127760;</span>
                        <span className="text-sm font-medium">Браузер (Chrome / Firefox)</span>
                      </div>
                      <ol className="text-xs text-text-secondary space-y-1 list-decimal list-inside">
                        <li>Установите расширение <span className="text-text-primary">Proxy SwitchyOmega</span></li>
                        <li>Создайте профиль → тип SOCKS</li>
                        <li>Сервер: <code className="text-text-primary">{socks5Parts.host}</code></li>
                        <li>Порт: <code className="text-text-primary">{socks5Parts.port}</code></li>
                        <li>Авторизация: логин и пароль выше</li>
                      </ol>
                    </div>

                    {/* Windows */}
                    {httpParts && (
                      <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                        <div className="flex items-center gap-2 mb-2">
                          <span className="text-lg">&#128187;</span>
                          <span className="text-sm font-medium">Windows (системный прокси)</span>
                        </div>
                        <ol className="text-xs text-text-secondary space-y-1 list-decimal list-inside">
                          <li>Параметры → Сеть → Прокси</li>
                          <li>Ручная настройка прокси → Вкл</li>
                          <li>Адрес: <code className="text-text-primary">{httpParts.host}</code></li>
                          <li>Порт: <code className="text-text-primary">{httpParts.port}</code></li>
                        </ol>
                      </div>
                    )}

                    {/* macOS */}
                    <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                      <div className="flex items-center gap-2 mb-2">
                        <span className="text-lg">&#63743;</span>
                        <span className="text-sm font-medium">macOS (системный прокси)</span>
                      </div>
                      <ol className="text-xs text-text-secondary space-y-1 list-decimal list-inside">
                        <li>Системные настройки → Сеть</li>
                        <li>Wi-Fi → Подробнее → Прокси</li>
                        <li>SOCKS-прокси → Вкл</li>
                        <li>Сервер: <code className="text-text-primary">{socks5Parts.host}</code></li>
                        <li>Порт: <code className="text-text-primary">{socks5Parts.port}</code></li>
                      </ol>
                    </div>
                  </div>
                </div>
              )}
            </>
          )}
        </div>

        {/* ─── Downloads ─── */}
        {Object.keys(downloads).length > 0 && (
          <div className="glass-card rounded-2xl p-6 md:p-8 mb-6 animate-fade-in-up delay-350">
            <div className="flex items-center gap-3 mb-5">
              <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500/20 to-indigo-500/10 flex items-center justify-center">
                <svg className="w-5 h-5 text-blue-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" />
                </svg>
              </div>
              <h2 className="text-base font-semibold">Скачать приложение</h2>
            </div>
            <div className="flex flex-wrap gap-3">
              {Object.entries(downloads).map(([platform, info]) => (
                <a
                  key={platform}
                  href={info.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="group inline-flex items-center gap-2.5 px-5 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white font-semibold text-sm transition-all duration-300 hover:shadow-lg hover:shadow-brand/25 hover:-translate-y-0.5"
                >
                  <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" />
                  </svg>
                  {platform.charAt(0).toUpperCase() + platform.slice(1)}
                  {info.version && <span className="opacity-60 text-xs">v{info.version}</span>}
                </a>
              ))}
            </div>
          </div>
        )}

        {/* "Для iPhone" section removed — replaced by Happ CTA in connection section above */}

        {/* ─── iOS/macOS Donation ─── */}
        <div className="glass-card rounded-2xl p-6 md:p-8 mb-6 animate-fade-in-up delay-400 border border-pink-500/10">
          <div className="flex items-center gap-3 mb-4">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-pink-500/20 to-rose-500/10 flex items-center justify-center">
              <svg className="w-5 h-5 text-pink-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M21 8.25c0-2.485-2.099-4.5-4.688-4.5-1.935 0-3.597 1.126-4.312 2.733-.715-1.607-2.377-2.733-4.313-2.733C5.1 3.75 3 5.765 3 8.25c0 7.22 9 12 9 12s9-4.78 9-12z" />
              </svg>
            </div>
            <div>
              <h2 className="text-base font-semibold">Поддержать разработчика</h2>
              <p className="text-xs text-text-secondary">Помогите нам выпустить приложение для iOS и macOS</p>
            </div>
          </div>

          <div className="p-4 rounded-xl bg-gradient-to-r from-pink-500/5 to-rose-500/5 border border-pink-500/10 mb-4">
            <p className="text-sm text-text-secondary leading-relaxed">
              Мы очень хотим выпустить нативное приложение для <strong className="text-text-primary">iOS</strong> и <strong className="text-text-primary">macOS</strong>,
              но у нашего разработчика пока нет MacBook для разработки.
              Ваш донат поможет приобрести оборудование и ускорить выпуск приложения.
              А пока вы можете использовать <strong className="text-text-primary">Happ</strong> для подключения с Apple устройств.
            </p>
          </div>

          <div className="flex flex-wrap gap-2 mb-4">
            {[100, 300, 500, 1000, 2000, 5000].map(amt => (
              <button
                key={amt}
                onClick={() => setDonationAmount(amt)}
                className={`px-4 py-2 rounded-xl text-sm font-medium transition-all duration-200 ${
                  donationAmount === amt
                    ? 'bg-gradient-to-r from-pink-600 to-rose-600 text-white shadow-lg shadow-pink-500/20'
                    : 'glass hover:bg-white/10 text-text-secondary hover:text-text-primary'
                }`}
              >
                {amt} &#8381;
              </button>
            ))}
          </div>

          <button
            disabled={donationLoading}
            onClick={handleDonate}
            className="group relative px-8 py-3 rounded-xl font-semibold text-sm text-white overflow-hidden transition-all duration-300 disabled:opacity-50 hover:shadow-lg hover:shadow-pink-500/20"
          >
            <span className="absolute inset-0 bg-gradient-to-r from-pink-600 to-rose-600" />
            <span className="absolute inset-0 bg-gradient-to-r from-pink-500 to-rose-500 opacity-0 group-hover:opacity-100 transition-opacity duration-500" />
            <span className="relative z-10 flex items-center gap-2">
              {donationLoading ? (
                <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
              ) : (
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M21 8.25c0-2.485-2.099-4.5-4.688-4.5-1.935 0-3.597 1.126-4.312 2.733-.715-1.607-2.377-2.733-4.313-2.733C5.1 3.75 3 5.765 3 8.25c0 7.22 9 12 9 12s9-4.78 9-12z" />
                </svg>
              )}
              Поддержать — {donationAmount} &#8381;
            </span>
          </button>
        </div>

        {/* ─── Payment history ─── */}
        <div className="glass-card rounded-2xl p-6 md:p-8 animate-fade-in-up delay-500">
          <div className="flex items-center gap-3 mb-6">
            <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-green-500/20 to-emerald-500/10 flex items-center justify-center">
              <svg className="w-5 h-5 text-green-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 002.25-2.25V6.75A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25v10.5A2.25 2.25 0 004.5 19.5z" />
              </svg>
            </div>
            <h2 className="text-lg font-semibold">История платежей</h2>
          </div>

          {payments.length === 0 ? (
            <div className="text-center py-8">
              <div className="w-12 h-12 rounded-full bg-white/5 flex items-center justify-center mx-auto mb-3">
                <svg className="w-6 h-6 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z" />
                </svg>
              </div>
              <p className="text-text-secondary text-sm">Платежей пока нет</p>
            </div>
          ) : (
            <div className="overflow-x-auto -mx-6 md:-mx-8 px-6 md:px-8">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs text-text-secondary uppercase tracking-wider border-b border-white/5">
                    <th className="pb-3 pr-4 font-medium">Дата</th>
                    <th className="pb-3 pr-4 font-medium">Описание</th>
                    <th className="pb-3 pr-4 font-medium">Сумма</th>
                    <th className="pb-3 font-medium">Статус</th>
                  </tr>
                </thead>
                <tbody>
                  {payments.map((pay, i) => (
                    <tr key={pay.id || i} className="border-b border-white/5 last:border-0 hover:bg-white/[0.02] transition-colors">
                      <td className="py-3.5 pr-4 text-text-secondary whitespace-nowrap">{formatDate(pay.created_at || pay.date)}</td>
                      <td className="py-3.5 pr-4 font-medium">{pay.notes || pay.description || pay.plan || '---'}</td>
                      <td className="py-3.5 pr-4 font-medium whitespace-nowrap">{formatAmount(pay.amount)}</td>
                      <td className="py-3.5">
                        <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium ${
                          pay.status === 'succeeded' || pay.status === 'paid'
                            ? 'bg-green-500/15 text-green-400'
                            : pay.status === 'pending'
                              ? 'bg-amber-500/15 text-amber-400'
                              : 'bg-gray-500/15 text-gray-400'
                        }`}>
                          <span className={`w-1.5 h-1.5 rounded-full ${
                            pay.status === 'succeeded' || pay.status === 'paid' ? 'bg-green-400' : pay.status === 'pending' ? 'bg-amber-400' : 'bg-gray-400'
                          }`} />
                          {pay.status === 'succeeded' || pay.status === 'paid' ? 'Оплачен' : pay.status === 'pending' ? 'Ожидание' : pay.status || '---'}
                        </span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </section>
  )
}
