import { useState, useEffect, useMemo } from 'react'
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
    { id: 'apple', label: 'iOS / macOS' },
    { id: 'other', label: 'Windows / Android / Linux' },
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

  if (loading) return <div className="text-center py-20 text-text-secondary">Загрузка...</div>
  if (error) return (
    <div className="text-center py-20">
      <p className="text-red-400 mb-4">{error}</p>
      <button className="px-6 py-2 rounded-xl bg-brand hover:bg-brand-dark text-white transition" onClick={() => window.location.reload()}>Повторить</button>
    </div>
  )

  return (
    <section className="py-16">
      <div className="max-w-4xl mx-auto px-6">
        <div className="flex items-center justify-between mb-10">
          <h1 className="text-3xl font-bold">Личный кабинет</h1>
          <Link to="/settings" className="text-sm text-brand-light hover:text-brand transition">Настройки</Link>
        </div>

        {/* HeYearth ID */}
        <div className="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <p className="text-sm text-text-secondary mb-1">Ваш HeYearth ID</p>
          <div className="flex items-center gap-3">
            <span className="text-2xl font-mono font-bold text-brand-light">{userId}</span>
            <button className="px-3 py-1 rounded-lg text-xs border border-white/10 hover:border-white/20 transition" onClick={() => copyText(userId, 'id')}>
              {copiedField === 'id' ? 'Скопировано!' : 'Копировать'}
            </button>
          </div>
        </div>

        {/* Subscription */}
        <div className="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <div className="flex items-center justify-between mb-2">
            <p className="text-sm text-text-secondary">Подписка</p>
            <Link to="/payment" className="text-sm text-brand-light hover:text-brand transition">
              {subscription ? 'Продлить' : 'Оформить'}
            </Link>
          </div>
          <p className="text-lg font-semibold">{subPlan}</p>
          {subValidUntil && <p className="text-sm text-text-secondary mt-1">Действует до {subValidUntil}</p>}
        </div>

        {/* Connections */}
        <div className="p-6 rounded-2xl bg-surface-card border border-white/5 mb-6">
          <h2 className="text-lg font-semibold mb-4">Подключение</h2>

          {/* Tabs */}
          <div className="flex gap-2 mb-6">
            {tabs.map(tab => (
              <button
                key={tab.id}
                className={`px-4 py-2 rounded-lg text-sm transition ${
                  activeTab === tab.id
                    ? 'bg-brand text-white'
                    : 'bg-surface border border-white/10 text-text-secondary hover:text-text-primary'
                }`}
                onClick={() => setActiveTab(tab.id)}
              >
                {tab.label}
              </button>
            ))}
          </div>

          {/* iOS / macOS */}
          {activeTab === 'apple' && (
            <div>
              {vlessUri && (
                <div className="mb-4">
                  <p className="text-sm text-text-secondary mb-1">VLESS URI</p>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 text-xs bg-surface rounded-lg px-3 py-2 break-all border border-white/5">{vlessUri}</code>
                    <button className="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0" onClick={() => copyText(vlessUri, 'vless')}>
                      {copiedField === 'vless' ? 'OK' : 'Копировать'}
                    </button>
                  </div>
                </div>
              )}
              <div className="text-sm text-text-secondary space-y-1">
                <p>Поддерживаемые приложения:</p>
                <ul className="list-disc list-inside ml-2 space-y-1">
                  <li>Shadowrocket (iOS)</li>
                  <li>Happ (macOS)</li>
                </ul>
              </div>
              {!vlessUri && (
                <div className="text-text-secondary text-sm">Данные подключения появятся после оформления подписки.</div>
              )}
            </div>
          )}

          {/* Windows / Android / Linux */}
          {activeTab === 'other' && (
            <div>
              <p className="text-text-secondary text-sm mb-4">Скачайте приложение HeYearth для вашей платформы:</p>
              {downloads.length > 0 ? (
                <div className="flex flex-wrap gap-3">
                  {downloads.map(dl => (
                    <a
                      key={dl.platform || dl.name}
                      href={dl.url || dl.download_url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-2 px-5 py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition"
                    >
                      {dl.platform || dl.name || 'Скачать'}
                    </a>
                  ))}
                </div>
              ) : (
                <p className="text-text-secondary text-sm">Загрузки пока недоступны. Скоро добавим.</p>
              )}
            </div>
          )}

          {/* Proxy addresses */}
          {(socks5Addr || httpProxy) && (
            <div className="mt-6 pt-6 border-t border-white/5">
              <h3 className="text-sm font-semibold mb-3">Прокси</h3>
              {socks5Addr && (
                <div className="mb-3">
                  <p className="text-xs text-text-secondary mb-1">SOCKS5</p>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 text-xs bg-surface rounded-lg px-3 py-2 border border-white/5">{socks5Addr}</code>
                    <button className="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0" onClick={() => copyText(socks5Addr, 'socks5')}>
                      {copiedField === 'socks5' ? 'OK' : 'Копировать'}
                    </button>
                  </div>
                </div>
              )}
              {httpProxy && (
                <div>
                  <p className="text-xs text-text-secondary mb-1">HTTP</p>
                  <div className="flex items-center gap-2">
                    <code className="flex-1 text-xs bg-surface rounded-lg px-3 py-2 border border-white/5">{httpProxy}</code>
                    <button className="px-3 py-2 rounded-lg text-xs border border-white/10 hover:border-white/20 transition shrink-0" onClick={() => copyText(httpProxy, 'http')}>
                      {copiedField === 'http' ? 'OK' : 'Копировать'}
                    </button>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        {/* Payment history */}
        <div className="p-6 rounded-2xl bg-surface-card border border-white/5">
          <h2 className="text-lg font-semibold mb-4">История платежей</h2>
          {payments.length === 0 ? (
            <div className="text-text-secondary text-sm">Платежей пока нет.</div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-text-secondary border-b border-white/5">
                    <th className="pb-2 pr-4 font-medium">Дата</th>
                    <th className="pb-2 pr-4 font-medium">Тариф</th>
                    <th className="pb-2 pr-4 font-medium">Сумма</th>
                    <th className="pb-2 font-medium">Статус</th>
                  </tr>
                </thead>
                <tbody>
                  {payments.map(pay => (
                    <tr key={pay.id} className="border-b border-white/5 last:border-0">
                      <td className="py-3 pr-4">{formatDate(pay.created_at || pay.date)}</td>
                      <td className="py-3 pr-4">{pay.plan || '---'}</td>
                      <td className="py-3 pr-4">{formatAmount(pay.amount)}</td>
                      <td className="py-3">
                        <span className={`px-2 py-0.5 rounded-full text-xs ${
                          pay.status === 'succeeded' || pay.status === 'paid'
                            ? 'bg-green-500/20 text-green-300'
                            : 'bg-yellow-500/20 text-yellow-300'
                        }`}>
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
