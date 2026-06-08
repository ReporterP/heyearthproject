import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getProfile, getConnections, getDownloads, getSubscribeLink, getAwgConfig } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import CopyButton, { copyToClipboard } from '../components/CopyButton.jsx'
import TelegramProxyButton from '../components/TelegramProxyButton.jsx'

/* ─── Helpers ─── */

// QR code helper (external API, no deps needed)
const qrUrl = (data) => `https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(data)}`

export default function ConnectionsPage() {
  const navigate = useNavigate()
  const { token, logout } = useAuth()

  const [profile, setProfile] = useState(null)
  const [connections, setConnections] = useState(null)
  const [downloads, setDownloads] = useState({})
  const [subscribeLink, setSubscribeLink] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // Config version update notification
  const [configUpdated, setConfigUpdated] = useState(false)
  // Copy feedback state
  const [copiedField, setCopiedField] = useState('')

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    Promise.all([
      getProfile(token),
      getConnections(token).catch(() => null),
      getDownloads().catch(() => ({ downloads: {} })),
      getSubscribeLink(token).catch(() => null),
    ]).then(([profileData, connData, dlData, subLinkData]) => {
      setProfile(profileData)
      setConnections(connData)
      setDownloads(dlData?.downloads || {})
      setSubscribeLink(subLinkData)
      // Check if config_version changed
      if (connData?.config_version) {
        const savedVersion = localStorage.getItem('heyearth_config_version')
        if (savedVersion && savedVersion !== String(connData.config_version)) {
          setConfigUpdated(true)
        }
        localStorage.setItem('heyearth_config_version', String(connData.config_version))
      }
    }).catch(e => {
      if (e.message?.includes('401') || e.message?.includes('Unauthorized')) {
        logout()
        navigate('/login')
        return
      }
      setError(e.message || 'Ошибка загрузки данных')
    }).finally(() => setLoading(false))
  }, [])

  const canConnect = connections?.can_connect
  const vlessReality = connections?.vless_reality || ''
  const amneziaAvailable = connections?.amnezia_available

  // Palma (AmneziaWG) — lazy-loaded config, only fetched when the card expands.
  const [awg, setAwg] = useState(null)
  const [awgLoading, setAwgLoading] = useState(false)
  const [awgError, setAwgError] = useState('')

  async function copyText(text, field) {
    const ok = await copyToClipboard(text)
    if (ok) {
      setCopiedField(field)
      setTimeout(() => setCopiedField(''), 2000)
    }
  }

  async function loadAwg() {
    if (awg || awgLoading) return // fetch once
    setAwgLoading(true)
    setAwgError('')
    try {
      const data = await getAwgConfig(token)
      setAwg(data)
    } catch (e) {
      setAwgError(e.message || 'Не удалось загрузить конфигурацию')
    } finally {
      setAwgLoading(false)
    }
  }

  function downloadAwgConf() {
    if (!awg?.config_text) return
    const blob = new Blob([awg.config_text], { type: 'text/plain' })
    const href = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = href
    a.download = 'palma.conf'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(href)
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
            <h1 className="text-2xl md:text-3xl font-bold">Подключение и настройка</h1>
            <p className="text-sm text-text-secondary mt-1">Данные для подключения и инструкции</p>
          </div>
          <Link
            to="/dashboard"
            className="inline-flex items-center gap-2 px-4 py-2 rounded-xl glass hover:bg-white/10 text-sm text-text-secondary hover:text-text-primary transition-all duration-200"
          >
            <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" />
            </svg>
            Кабинет
          </Link>
        </div>

        {/* ─── Connections ─── */}
        <div className="glass-card rounded-2xl p-6 md:p-8 mb-6 animate-fade-in-up delay-100">
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
                <Link to="/payment" className="inline-flex items-center gap-1.5 mt-2 text-sm text-brand-light hover:text-accent transition-colors">
                  Оформить подписку
                  <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                    <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                  </svg>
                </Link>
              </div>
            </div>
          ) : (
            <>
              {/* Config update notification */}
              {configUpdated && subscribeLink?.subscribe_url && (
                <div className="p-4 rounded-xl bg-amber-500/10 border border-amber-500/20 mb-5 animate-fade-in">
                  <div className="flex items-start gap-3">
                    <svg className="w-5 h-5 text-amber-400 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                      <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                    </svg>
                    <div className="flex-1">
                      <p className="text-amber-400 text-sm font-medium">Конфигурация обновилась</p>
                      <p className="text-xs text-text-secondary mt-1">Параметры подключения были автоматически оптимизированы. Обновите подписку в вашем приложении (Happ, v2rayNG, Streisand) для применения новых настроек.</p>
                      <div className="flex items-center gap-3 mt-3">
                        <button onClick={() => { copyText(subscribeLink.subscribe_url, 'sub-update'); setConfigUpdated(false); }}
                          className="px-4 py-2 rounded-lg bg-amber-500/20 text-amber-300 text-sm font-medium hover:bg-amber-500/30 transition">
                          Скопировать ссылку подписки
                        </button>
                        <button onClick={() => setConfigUpdated(false)}
                          className="text-xs text-text-secondary hover:text-text-primary transition">
                          Скрыть
                        </button>
                      </div>
                    </div>
                  </div>
                </div>
              )}

              <TelegramProxyButton token={token} />

              {/* ─── Connection cards: VLESS ─── */}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-5">
                {vlessReality && (
                  <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5">
                    <div className="flex items-center justify-between mb-2">
                      <div className="flex items-center gap-2">
                        <span className="px-2 py-0.5 rounded-md bg-violet-500/15 text-violet-400 text-xs font-bold">Прокси</span>
                        <span className="text-[10px] text-green-400 bg-green-500/10 px-1.5 py-0.5 rounded">оптимизированный</span>
                      </div>
                      <CopyButton text={vlessReality} />
                    </div>
                    <code className="text-[11px] font-mono text-text-primary break-all select-all line-clamp-2">{vlessReality}</code>
                    <p className="text-xs text-text-secondary mt-1.5">Оптимизированная маршрутизация. Для Happ, v2rayNG, Streisand</p>
                    <details className="mt-3">
                      <summary className="text-xs text-brand cursor-pointer hover:text-brand-light transition">QR-код</summary>
                      <img src={qrUrl(vlessReality)} alt="QR" className="mt-2 rounded-lg bg-white p-2 w-[180px] h-[180px]" loading="lazy" />
                    </details>
                  </div>
                )}
              </div>

              {/* ─── Subscription URL (for V2Ray/Clash clients) ─── */}
              {subscribeLink?.subscribe_url && (
                <div className="p-4 rounded-xl bg-white/[0.03] border border-white/5 mb-5">
                  <div className="flex items-center justify-between mb-2">
                    <div className="flex items-center gap-2">
                      <span className="px-2 py-0.5 rounded-md bg-amber-500/15 text-amber-400 text-xs font-bold">Подписка</span>
                      <span className="text-[10px] text-text-secondary">V2RayNG / Happ / Streisand / Clash</span>
                    </div>
                    <CopyButton text={subscribeLink.subscribe_url} />
                  </div>
                  <code className="text-[11px] font-mono text-text-primary break-all select-all">{subscribeLink.subscribe_url}</code>
                  <p className="text-xs text-text-secondary mt-1.5">Вставьте эту ссылку в приложение как подписку -- настройки обновятся автоматически</p>
                  <details className="mt-3">
                    <summary className="text-xs text-brand cursor-pointer hover:text-brand-light transition">QR-код подписки</summary>
                    <img src={qrUrl(subscribeLink.subscribe_url)} alt="QR" className="mt-2 rounded-lg bg-white p-2 w-[180px] h-[180px]" loading="lazy" />
                  </details>
                </div>
              )}

              {/* ─── Пальма (AmneziaWG) — only for amnezia_wg_enabled plans ─── */}
              {amneziaAvailable && (
                <div className="p-4 rounded-xl bg-gradient-to-br from-emerald-500/[0.06] to-cyan-500/[0.04] border border-emerald-500/15 mb-5">
                  <div className="flex items-center gap-2 mb-2">
                    <span className="px-2 py-0.5 rounded-md bg-emerald-500/15 text-emerald-300 text-xs font-bold">🌴 Пальма</span>
                    <span className="text-[10px] text-text-secondary">AmneziaWG · максимальная маскировка</span>
                  </div>
                  <p className="text-xs text-text-secondary mb-3">
                    Отдельный протокол для приложения AmneziaWG. Скачайте конфиг или отсканируйте QR-код.
                  </p>

                  <details className="group" onToggle={(e) => { if (e.currentTarget.open) loadAwg() }}>
                    <summary className="inline-flex items-center gap-1.5 text-sm text-emerald-300 cursor-pointer hover:text-emerald-200 transition select-none">
                      <svg className="w-4 h-4 transition-transform group-open:rotate-90" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" />
                      </svg>
                      Показать конфигурацию
                    </summary>

                    <div className="mt-4">
                      {awgLoading && (
                        <div className="flex items-center gap-2 text-xs text-text-secondary">
                          <div className="w-4 h-4 rounded-full border-2 border-emerald-400/30 border-t-emerald-400 animate-spin" />
                          Загрузка конфигурации...
                        </div>
                      )}

                      {awgError && (
                        <p className="text-xs text-red-400">{awgError}</p>
                      )}

                      {awg?.config_text && (
                        <div className="space-y-4">
                          <div className="flex flex-col sm:flex-row gap-4 items-start">
                            <img
                              src={qrUrl(awg.config_text)}
                              alt="QR конфигурации Пальмы"
                              className="rounded-lg bg-white p-2 w-[180px] h-[180px] flex-shrink-0"
                              loading="lazy"
                            />
                            <div className="flex-1 min-w-0">
                              {awg.endpoint && (
                                <p className="text-xs text-text-secondary mb-1">
                                  Сервер: <code className="font-mono text-text-primary">{awg.endpoint}</code>
                                </p>
                              )}
                              {awg.ip && (
                                <p className="text-xs text-text-secondary mb-3">
                                  Ваш IP: <code className="font-mono text-text-primary">{awg.ip}</code>
                                </p>
                              )}
                              <div className="flex flex-wrap gap-2">
                                <button
                                  onClick={downloadAwgConf}
                                  className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium bg-emerald-500/15 text-emerald-300 hover:bg-emerald-500/25 transition"
                                >
                                  <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                                    <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5M16.5 12L12 16.5m0 0L7.5 12m4.5 4.5V3" />
                                  </svg>
                                  Скачать .conf
                                </button>
                                {awg.deeplink && (
                                  <a
                                    href={awg.deeplink}
                                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium glass hover:bg-white/10 transition"
                                  >
                                    Открыть в AmneziaWG
                                  </a>
                                )}
                                <CopyButton text={awg.config_text} label="Копировать конфиг" />
                              </div>
                              <a
                                href="https://github.com/amnezia-vpn/amnezia-client/releases"
                                target="_blank"
                                rel="noopener noreferrer"
                                className="inline-flex items-center gap-1 mt-3 text-xs text-brand-light hover:text-accent transition"
                              >
                                Скачать приложение AmneziaWG
                                <svg className="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" />
                                </svg>
                              </a>
                            </div>
                          </div>
                        </div>
                      )}
                    </div>
                  </details>
                </div>
              )}

            </>
          )}
        </div>

        {/* ─── Downloads ─── */}
        {Object.keys(downloads).length > 0 && (
          <div className="glass-card rounded-2xl p-6 md:p-8 mb-6 animate-fade-in-up delay-200">
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
      </div>
    </section>
  )
}
