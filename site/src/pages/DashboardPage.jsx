import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getProfile, getSubscription, getConnections, getPayments, getDownloads } from '../api/user.js'

function formatDate(d) {
  if (!d) return '---'
  return new Date(d).toLocaleDateString('ru-RU')
}

function formatAmount(amount) {
  if (!amount && amount !== 0) return '---'
  return Number(amount).toLocaleString('ru-RU') + ' \u20BD'
}

/* ─── Reusable icon components ─── */
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
  const token = localStorage.getItem('token')

  const [profile, setProfile] = useState(null)
  const [subscription, setSubscription] = useState(null)
  const [connections, setConnections] = useState(null)
  const [payments, setPayments] = useState([])
  const [downloads, setDownloads] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [copiedField, setCopiedField] = useState('')
  const [activeTab, setActiveTab] = useState('apple')

  const tabs = [
    { id: 'apple', label: 'iOS / macOS', icon: (
      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 1.5H8.25A2.25 2.25 0 006 3.75v16.5a2.25 2.25 0 002.25 2.25h7.5A2.25 2.25 0 0018 20.25V3.75a2.25 2.25 0 00-2.25-2.25H13.5m-3 0V3h3V1.5m-3 0h3" />
      </svg>
    )},
    { id: 'other', label: 'Windows / Android / Linux', icon: (
      <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M9 17.25v1.007a3 3 0 01-.879 2.122L7.5 21h9l-.621-.621A3 3 0 0115 18.257V17.25m6-12V15a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 15V5.25m18 0A2.25 2.25 0 0018.75 3H5.25A2.25 2.25 0 003 5.25m18 0V12a2.25 2.25 0 01-2.25 2.25H5.25A2.25 2.25 0 013 12V5.25" />
      </svg>
    )},
  ]

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    Promise.all([
      getProfile(token),
      getSubscription(token).catch(() => null),
      getConnections(token).catch(() => null),
      getPayments(token).catch(() => []),
      getDownloads().catch(() => []),
    ]).then(([profileData, subData, connData, payData, dlData]) => {
      setProfile(profileData)
      setSubscription(subData)
      setConnections(connData)
      setPayments(Array.isArray(payData) ? payData : payData?.payments || [])
      setDownloads(Array.isArray(dlData) ? dlData : dlData?.downloads || [])
    }).catch(e => {
      if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
        localStorage.removeItem('token')
        navigate('/login')
        return
      }
      setError(e.message || 'Ошибка загрузки данных')
    }).finally(() => setLoading(false))
  }, [])

  const userId = profile?.id || profile?.heyearth_id || '...'
  const subPlan = subscription?.plan || 'Нет подписки'
  const subValidUntil = subscription?.valid_until ? new Date(subscription.valid_until).toLocaleDateString('ru-RU') : null
  const hasActiveSub = !!subscription
  const vlessUri = connections?.vless_uri || ''
  const socks5Addr = connections?.socks5 || ''
  const httpProxy = connections?.http_proxy || ''

  async function copyText(text, field) {
    try {
      await navigator.clipboard.writeText(text)
      setCopiedField(field)
      setTimeout(() => setCopiedField(''), 2000)
    } catch { /* clipboard unavailable */ }
  }

  function CopyButton({ field, text }) {
    return (
      <button
        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium glass hover:bg-white/10 transition-all duration-200"
        onClick={() => copyText(text, field)}
      >
        {copiedField === field ? <CheckIcon /> : <CopyIcon />}
        {copiedField === field ? 'OK' : 'Копировать'}
      </button>
    )
  }

  /* ─── Loading state ─── */
  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="text-center">
        <div className="w-10 h-10 rounded-full border-2 border-brand/30 border-t-brand animate-spin mx-auto mb-4" />
        <p className="text-text-secondary text-sm">Загрузка...</p>
      </div>
    </div>
  )

  /* ─── Error state ─── */
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
      {/* Background effects */}
      <div className="absolute top-0 left-1/4 w-96 h-96 bg-brand/5 rounded-full blur-[120px] pointer-events-none" />
      <div className="absolute bottom-0 right-1/4 w-96 h-96 bg-accent/5 rounded-full blur-[120px] pointer-events-none" />

      <div className="relative max-w-5xl mx-auto px-6">
        {/* Header */}
        <div className="flex items-center justify-between mb-8 animate-fade-in">
          <div>
            <h1 className="text-2xl md:text-3xl font-bold">Личный кабинет</h1>
            <p className="text-sm text-text-secondary mt-1">Управление аккаунтом и подключениями</p>
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

        {/* Top cards grid */}
        <div className="grid md:grid-cols-2 gap-4 md:gap-6 mb-6">
          {/* HeYearth ID card */}
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
            <p className="text-xl md:text-2xl font-mono font-bold text-brand-light break-all">{userId}</p>
          </div>

          {/* Subscription card */}
          <div className={`relative rounded-2xl p-6 overflow-hidden animate-fade-in-up delay-200 ${
            hasActiveSub
              ? 'glass-card border border-green-500/20'
              : 'glass-card'
          }`}>
            {hasActiveSub && (
              <div className="absolute top-0 right-0 w-32 h-32 bg-green-500/5 rounded-bl-full pointer-events-none" />
            )}
            <div className="flex items-start justify-between mb-3">
              <div className="flex items-center gap-3">
                <div className={`w-10 h-10 rounded-xl flex items-center justify-center ${
                  hasActiveSub ? 'bg-green-500/15' : 'bg-white/5'
                }`}>
                  <svg className={`w-5 h-5 ${hasActiveSub ? 'text-green-400' : 'text-text-secondary'}`} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12c0 1.268-.63 2.39-1.593 3.068a3.745 3.745 0 01-1.043 3.296 3.745 3.745 0 01-3.296 1.043A3.745 3.745 0 0112 21c-1.268 0-2.39-.63-3.068-1.593a3.746 3.746 0 01-3.296-1.043 3.745 3.745 0 01-1.043-3.296A3.745 3.745 0 013 12c0-1.268.63-2.39 1.593-3.068a3.745 3.745 0 011.043-3.296 3.746 3.746 0 013.296-1.043A3.746 3.746 0 0112 3c1.268 0 2.39.63 3.068 1.593a3.746 3.746 0 013.296 1.043 3.746 3.746 0 011.043 3.296A3.745 3.745 0 0121 12z" />
                  </svg>
                </div>
                <p className="text-xs font-medium text-text-secondary uppercase tracking-wider">Подписка</p>
              </div>
              <Link
                to="/payment"
                className="text-xs font-medium text-brand-light hover:text-accent transition-colors"
              >
                {subscription ? 'Продлить' : 'Оформить'}
              </Link>
            </div>
            <p className="text-lg font-semibold">{subPlan}</p>
            {subValidUntil && (
              <div className="flex items-center gap-1.5 mt-1.5">
                <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
                <p className="text-sm text-text-secondary">Действует до {subValidUntil}</p>
              </div>
            )}
            {!hasActiveSub && (
              <Link
                to="/payment"
                className="inline-flex items-center gap-1.5 mt-3 text-sm text-brand-light hover:text-accent transition-colors"
              >
                Оформить подписку
                <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                </svg>
              </Link>
            )}
          </div>
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

          {/* Platform tabs */}
          <div className="flex gap-2 mb-6">
            {tabs.map(tab => (
              <button
                key={tab.id}
                className={`flex items-center gap-2 px-4 py-2.5 rounded-xl text-sm font-medium transition-all duration-300 ${
                  activeTab === tab.id
                    ? 'bg-gradient-to-r from-brand to-brand-dark text-white shadow-lg shadow-brand/15'
                    : 'glass hover:bg-white/10 text-text-secondary hover:text-text-primary'
                }`}
                onClick={() => setActiveTab(tab.id)}
              >
                {tab.icon}
                <span className="hidden sm:inline">{tab.label}</span>
              </button>
            ))}
          </div>

          {/* iOS / macOS */}
          {activeTab === 'apple' && (
            <div className="animate-fade-in">
              {vlessUri ? (
                <div className="mb-5">
                  <p className="text-xs font-medium text-text-secondary uppercase tracking-wider mb-2">VLESS URI</p>
                  <div className="flex items-start gap-3">
                    <code className="flex-1 text-xs bg-white/5 rounded-xl px-4 py-3 break-all border border-white/5 font-mono text-text-secondary leading-relaxed">{vlessUri}</code>
                    <CopyButton field="vless" text={vlessUri} />
                  </div>
                </div>
              ) : (
                <div className="flex items-center gap-3 p-4 rounded-xl bg-white/5 border border-white/5 mb-5">
                  <svg className="w-5 h-5 text-text-secondary flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z" />
                  </svg>
                  <p className="text-text-secondary text-sm">Данные подключения появятся после оформления подписки.</p>
                </div>
              )}
              <div className="text-sm text-text-secondary">
                <p className="font-medium text-text-primary mb-2">Поддерживаемые приложения:</p>
                <div className="flex flex-wrap gap-3">
                  {['Shadowrocket (iOS)', 'Happ (macOS)'].map(app => (
                    <span key={app} className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg glass text-xs">
                      <span className="w-1.5 h-1.5 rounded-full bg-accent" />
                      {app}
                    </span>
                  ))}
                </div>
              </div>
            </div>
          )}

          {/* Windows / Android / Linux */}
          {activeTab === 'other' && (
            <div className="animate-fade-in">
              <p className="text-text-secondary text-sm mb-4">Скачайте приложение HeYearth для вашей платформы:</p>
              {downloads.length > 0 ? (
                <div className="flex flex-wrap gap-3">
                  {downloads.map(dl => (
                    <a
                      key={dl.platform || dl.name}
                      href={dl.url || dl.download_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="group inline-flex items-center gap-2.5 px-5 py-3 rounded-xl bg-gradient-to-r from-brand to-brand-dark text-white font-semibold transition-all duration-300 hover:shadow-lg hover:shadow-brand/25 hover:-translate-y-0.5"
                    >
                      <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" />
                      </svg>
                      {dl.platform || dl.name || 'Скачать'}
                    </a>
                  ))}
                </div>
              ) : (
                <div className="flex items-center gap-3 p-4 rounded-xl bg-white/5 border border-white/5">
                  <svg className="w-5 h-5 text-text-secondary flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z" />
                  </svg>
                  <p className="text-text-secondary text-sm">Загрузки пока недоступны. Скоро добавим.</p>
                </div>
              )}
            </div>
          )}

          {/* Proxy addresses */}
          {(socks5Addr || httpProxy) && (
            <div className="mt-6 pt-6 border-t border-white/5">
              <p className="text-xs font-medium text-text-secondary uppercase tracking-wider mb-4">Прокси-адреса</p>
              <div className="grid sm:grid-cols-2 gap-4">
                {socks5Addr && (
                  <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-xs font-semibold text-accent">SOCKS5</span>
                      <CopyButton field="socks5" text={socks5Addr} />
                    </div>
                    <code className="text-xs font-mono text-text-secondary break-all">{socks5Addr}</code>
                  </div>
                )}
                {httpProxy && (
                  <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between mb-2">
                      <span className="text-xs font-semibold text-accent">HTTP</span>
                      <CopyButton field="http" text={httpProxy} />
                    </div>
                    <code className="text-xs font-mono text-text-secondary break-all">{httpProxy}</code>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>

        {/* ─── Payment history ─── */}
        <div className="glass-card rounded-2xl p-6 md:p-8 animate-fade-in-up delay-400">
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
              <Link to="/payment" className="inline-flex items-center gap-1.5 mt-2 text-sm text-brand-light hover:text-accent transition-colors">
                Оформить подписку
                <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                </svg>
              </Link>
            </div>
          ) : (
            <div className="overflow-x-auto -mx-6 md:-mx-8 px-6 md:px-8">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-xs text-text-secondary uppercase tracking-wider border-b border-white/5">
                    <th className="pb-3 pr-4 font-medium">Дата</th>
                    <th className="pb-3 pr-4 font-medium">Тариф</th>
                    <th className="pb-3 pr-4 font-medium">Сумма</th>
                    <th className="pb-3 font-medium">Статус</th>
                  </tr>
                </thead>
                <tbody>
                  {payments.map(pay => (
                    <tr key={pay.id} className="border-b border-white/5 last:border-0 hover:bg-white/[0.02] transition-colors">
                      <td className="py-3.5 pr-4 text-text-secondary">{formatDate(pay.created_at || pay.date)}</td>
                      <td className="py-3.5 pr-4 font-medium">{pay.plan || '---'}</td>
                      <td className="py-3.5 pr-4 font-medium">{formatAmount(pay.amount)}</td>
                      <td className="py-3.5">
                        <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium ${
                          pay.status === 'succeeded' || pay.status === 'paid'
                            ? 'bg-green-500/15 text-green-400'
                            : 'bg-amber-500/15 text-amber-400'
                        }`}>
                          <span className={`w-1.5 h-1.5 rounded-full ${
                            pay.status === 'succeeded' || pay.status === 'paid' ? 'bg-green-400' : 'bg-amber-400'
                          }`} />
                          {pay.status === 'succeeded' || pay.status === 'paid' ? 'Оплачен' : pay.status || '---'}
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
