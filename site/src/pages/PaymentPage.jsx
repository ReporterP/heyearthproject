import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { createPayment, getProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'

const CheckIcon = () => (
  <svg className="w-4 h-4 mt-0.5 text-accent flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
  </svg>
)

function formatTraffic(bytes) {
  if (!bytes) return 'Безлимит'
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(0)} ГБ` : `${(bytes / (1024 * 1024)).toFixed(0)} МБ`
}

function formatDuration(days) {
  if (days >= 365) return `${Math.round(days / 365)} год`
  if (days >= 30) return `${Math.round(days / 30)} мес.`
  return `${days} дн.`
}

function protocolLabel(protocols) {
  if (!protocols || protocols.includes('all')) return 'Все протоколы'
  // User-facing names. Technical protocol identifiers are mapped to
  // friendly Russian labels — we don't expose «VLESS» / «Hysteria2»
  // / «XHTTP» in marketing because users don't know what those mean.
  const labelMap = {
    vless: 'Прокси',
    hysteria2: 'Скоростной',
    xhttp: 'Anti-DPI',
    all: 'Все протоколы',
    // Legacy entries that may still appear on old plans in the DB.
    // Filtered out (empty string) so they don't surface in the UI;
    // admin should migrate those plans via the protocols editor.
    socks: '',
    http: '',
  }
  return protocols.map(p => labelMap[p] !== undefined ? labelMap[p] : p.toUpperCase()).filter(Boolean).join(' + ')
}

export default function PaymentPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)
  const [selectedPlan, setSelectedPlan] = useState(null)
  const [currentPlanSlug, setCurrentPlanSlug] = useState(null)
  const [currentSortOrder, setCurrentSortOrder] = useState(-1)
  const [validUntil, setValidUntil] = useState(null)
  const [promocode, setPromocode] = useState('')
  const [email, setEmail] = useState('')
  const [saveCard, setSaveCard] = useState(false)
  const [paying, setPaying] = useState(false)
  const [error, setError] = useState('')
  const [prorations, setProrations] = useState({}) // planId → {original_price, proration_credit, final_price}
  const [earlyRenewalThreshold, setEarlyRenewalThreshold] = useState(3)

  useEffect(() => {
    const promises = [
      fetch('/api/v1/plans').then(r => r.json()).catch(() => []),
    ]
    // Fetch user profile to show current plan
    if (token) {
      promises.push(getProfile(token).catch(() => null))
    }
    Promise.all(promises).then(([plansData, profile]) => {
      setPlans(Array.isArray(plansData) ? plansData : [])
      if (profile?.current_plan_slug) {
        setCurrentPlanSlug(profile.current_plan_slug)
        const match = (Array.isArray(plansData) ? plansData : []).find(p => p.slug === profile.current_plan_slug)
        if (match) setCurrentSortOrder(match.sort_order ?? -1)
      } else if (profile?.current_plan) {
        const match = (Array.isArray(plansData) ? plansData : []).find(
          p => p.name === profile.current_plan || p.slug === profile.current_plan
        )
        if (match) {
          setCurrentPlanSlug(match.slug)
          setCurrentSortOrder(match.sort_order ?? -1)
        }
      }
      if (profile?.valid_until) setValidUntil(profile.valid_until)
    }).finally(() => setLoading(false))
  }, [token])

  // Proration preview для каждого платного тарифа, когда есть активная подписка
  useEffect(() => {
    if (!token || !validUntil || plans.length === 0) return
    const paid = plans.filter(p => p.price_rub > 0)
    Promise.all(
      paid.map(p => fetch(`/api/v1/payment/proration-preview?plan_id=${p.id}`, {
        headers: { Authorization: 'Bearer ' + token }
      }).then(r => r.ok ? r.json() : null).then(d => [p.id, d]).catch(() => [p.id, null]))
    ).then(pairs => {
      const map = {}
      pairs.forEach(([id, d]) => { if (d) map[id] = d })
      setProrations(map)
      // All responses carry the same early_renewal_days_threshold (backend
      // setting is global); pick the first response that includes it.
      const firstWithThreshold = pairs.find(([, d]) => d && typeof d.early_renewal_days_threshold === 'number')
      if (firstWithThreshold) {
        setEarlyRenewalThreshold(firstWithThreshold[1].early_renewal_days_threshold)
      }
    })
  }, [token, validUntil, plans])

  async function handlePay(planId) {
    if (!token) {
      navigate('/login?redirect=/payment')
      return
    }
    setPaying(true)
    setError('')
    try {
      // Для веба используем embedded widget (юзер не покидает hiearth.ru),
      // для нестандартных клиентов (Flutter) — старый redirect flow.
      const data = await createPayment(
        token, null, window.location.origin + '/dashboard',
        planId, promocode, saveCard, email, 'embedded'
      )
      if (data.status === 'activated') {
        // Free plan activated immediately
        navigate('/dashboard')
        return
      }
      const plan = plans.find(p => p.id === planId)
      if (plan) {
        sessionStorage.setItem('payment_pending', JSON.stringify({ name: plan.name, price: plan.price_rub }))
      }
      // Embedded flow: есть confirmation_token → /checkout страница с виджетом
      if (data.confirmation_token) {
        sessionStorage.setItem(`checkout_token_${data.payment_id}`, data.confirmation_token)
        sessionStorage.setItem(`checkout_plan_${data.payment_id}`, plan?.name || '')
        navigate(`/checkout/${data.payment_id}`)
        return
      }
      // Fallback: redirect flow (старый путь для Flutter)
      if (data.confirmation_url) {
        window.location.href = data.confirmation_url
        return
      }
      setError('Не удалось создать платёж. Попробуйте позже.')
    } catch (e) {
      setError(e.message || 'Ошибка создания платежа')
    } finally {
      setPaying(false)
    }
  }

  // Computed daysRemaining for «Продлить заранее» badge — recomputed every render.
  const daysRemaining = (() => {
    if (!validUntil) return Infinity
    const ms = new Date(validUntil).getTime() - Date.now()
    return Math.max(0, Math.ceil(ms / (24 * 60 * 60 * 1000)))
  })()
  const showEarlyRenewalBadge = currentPlanSlug && daysRemaining <= earlyRenewalThreshold

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-10 h-10 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
    </div>
  )

  return (
    <section className="relative py-10 md:py-16">
      <div className="absolute top-0 left-1/4 w-96 h-96 bg-brand/5 rounded-full blur-[120px] pointer-events-none" />
      <div className="absolute bottom-0 right-1/4 w-96 h-96 bg-accent/5 rounded-full blur-[120px] pointer-events-none" />

      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">
        <div className="text-center mb-10 animate-fade-in">
          <h1 className="text-3xl md:text-4xl font-bold mb-3">Выберите тариф</h1>
          <p className="text-text-secondary max-w-lg mx-auto">
            Гибкие подписки с разными протоколами и серверами. Все тарифы с автоматическим продлением.
          </p>
        </div>

        {/* Promocode + Email + Save card */}
        <div className="max-w-lg mx-auto mb-8 animate-fade-in-up delay-100 space-y-3">
          <div className="flex gap-2">
            <input
              value={promocode}
              onChange={e => setPromocode(e.target.value)}
              placeholder="Промокод (если есть)"
              className="flex-1 px-4 py-2.5 rounded-xl bg-white/5 border border-white/10 text-sm placeholder-text-secondary focus:border-brand/50 focus:outline-none"
            />
            {promocode && (
              <button onClick={() => setPromocode('')} className="px-3 py-2 rounded-xl bg-surface-alt text-text-secondary text-sm hover:text-text-primary transition">
                ✕
              </button>
            )}
          </div>
          <div>
            <input
              value={email}
              onChange={e => setEmail(e.target.value)}
              type="email"
              placeholder="Email для чека (необязательно)"
              className="w-full px-4 py-2.5 rounded-xl bg-white/5 border border-white/10 text-sm placeholder-text-secondary focus:border-brand/50 focus:outline-none"
            />
            <p className="text-[11px] text-text-secondary mt-1 ml-1">Мы не храним вашу почту — она используется только для отправки чека</p>
          </div>
          <label className="flex items-center gap-2 cursor-pointer text-sm text-text-secondary">
            <input
              type="checkbox"
              checked={saveCard}
              onChange={e => setSaveCard(e.target.checked)}
              className="rounded border-white/20"
            />
            Сохранить карту для автопродления
          </label>
        </div>

        {/* Plans grid */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5 mb-8">
          {plans.map((plan, idx) => {
            const isSelected = selectedPlan === plan.id
            const isCurrent = currentPlanSlug === plan.slug
            const isFree = plan.price_rub === 0
            // Block downgrade: can't buy a plan with lower sort_order than current
            // Same sort_order = allowed (horizontal switch between equal plans)
            const isDowngrade = currentSortOrder >= 0 && !isCurrent && (plan.sort_order ?? 0) < currentSortOrder
            const features = []
            features.push(protocolLabel(plan.allowed_protocols))
            features.push(`Трафик: ${formatTraffic(plan.traffic_limit_bytes)}`)
            features.push(`Срок: ${formatDuration(plan.duration_days)}`)
            features.push(`Серверов: ${plan.server_ids?.length || 'все'}`)
            const planFeatures = plan.features ? (typeof plan.features === 'string' ? JSON.parse(plan.features || '{}') : plan.features) : {}
            if (planFeatures.traffic_masking) features.push('Маскировка трафика')
            if (planFeatures.priority_support) features.push('Приоритетная поддержка')
            if (plan.direct_routing_enabled) features.push('Доступ к РФ-сервисам')

            return (
              <div
                key={plan.id}
                onClick={() => !isDowngrade && !isCurrent && setSelectedPlan(plan.id)}
                className={`relative glass-card rounded-2xl p-6 transition-all duration-300 animate-fade-in-up ${
                  isDowngrade
                    ? 'border-white/5 opacity-40 cursor-not-allowed'
                    : isCurrent
                      ? 'border-green-500/40 cursor-default'
                      : isSelected
                        ? 'border-brand/40 cursor-pointer'
                        : 'border-white/10 hover:border-white/20 cursor-pointer'
                }`}
                style={{ animationDelay: `${idx * 100}ms` }}
              >
                {isCurrent && (
                  <div className="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-green-500 text-white text-[10px] font-bold uppercase tracking-wider">
                    Текущий план
                  </div>
                )}
                {isSelected && !isCurrent && (
                  <div className="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-brand text-white text-[10px] font-bold uppercase tracking-wider">
                    Выбрано
                  </div>
                )}

                <h3 className="text-lg font-bold mb-1">{plan.name}</h3>
                <p className="text-xs text-text-secondary mb-4">{plan.description}</p>

                <div className="flex items-baseline gap-1 mb-1">
                  <span className="text-2xl font-semibold text-text-primary">
                    {isFree ? 'Бесплатно' : `${prorations[plan.id]?.final_price ?? plan.price_rub}₽`}
                  </span>
                  {!isFree && <span className="text-sm text-text-secondary">/ {formatDuration(plan.duration_days)}</span>}
                </div>
                {!isFree && prorations[plan.id]?.proration_credit > 0 && (
                  <div className="mb-4 text-xs text-amber-400 bg-amber-500/10 px-2 py-1 rounded-lg inline-flex items-center gap-1.5">
                    <span className="text-text-secondary line-through">{plan.price_rub}₽</span>
                    <span>−{prorations[plan.id].proration_credit}₽ (остаток текущего тарифа)</span>
                  </div>
                )}
                {!isFree && !prorations[plan.id]?.proration_credit && <div className="mb-4" />}

                <ul className="space-y-2 mb-6">
                  {features.map((f, i) => (
                    <li key={i} className="flex items-start gap-2 text-sm text-text-secondary">
                      <CheckIcon />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>

                {(() => {
                  const pr = prorations[plan.id] || {}
                  const blocked = pr.is_downgrade_blocked === true
                  const bonus = pr.bonus_days || 0
                  const validUntilDate = validUntil ? new Date(validUntil).toLocaleDateString('ru-RU') : ''
                  const dr = daysRemaining
                  const dayWord = dr === 1 ? 'день' : dr < 5 ? 'дня' : 'дней'

                  return (
                    <>
                      {/* Bonus days hint — only when conversion will happen */}
                      {bonus > 0 && !blocked && !isCurrent && !isDowngrade && (
                        <p className="text-xs text-accent mb-2">
                          +{bonus} бонус-дней (за {pr.days_remaining} оставшихся дней текущего плана)
                        </p>
                      )}

                      {/* Early renewal badge on current plan */}
                      {isCurrent && showEarlyRenewalBadge && (
                        <p className="text-xs text-amber-400 mb-2">
                          ⏰ Подписка скоро закончится — продлите заранее (осталось {dr} {dayWord})
                        </p>
                      )}

                      {/* Buy / disabled button */}
                      {blocked ? (
                        <button
                          disabled
                          title={`Доступно после ${validUntilDate}`}
                          className="w-full py-2.5 rounded-xl text-sm font-medium bg-white/5 text-text-secondary/50 cursor-not-allowed disabled:opacity-50"
                        >
                          Доступно после {validUntilDate}
                        </button>
                      ) : (
                        <button
                          onClick={e => {
                            e.stopPropagation()
                            if (isDowngrade || isCurrent) return
                            // Warn about plan switch if user has active sub
                            if (currentPlanSlug && !isCurrent) {
                              if (!confirm('При оплате нового тарифа ваша текущая подписка будет заменена. Продолжить?')) return
                            }
                            handlePay(plan.id)
                          }}
                          disabled={paying || isDowngrade}
                          className={`w-full py-2.5 rounded-xl text-sm font-medium transition-all duration-200 ${
                            isDowngrade
                              ? 'bg-white/5 text-text-secondary/50 cursor-not-allowed'
                              : isCurrent
                                ? 'bg-green-500/15 text-green-400 hover:bg-green-500/25'
                                : 'bg-white/5 text-text-primary hover:bg-brand/20 hover:text-brand-light border border-white/5 hover:border-brand/30'
                          } disabled:opacity-50`}
                        >
                          {isDowngrade ? 'Недоступно' : paying ? 'Обработка...' : isCurrent ? 'Продлить' : isFree ? 'Активировать' : 'Оформить'}
                        </button>
                      )}
                    </>
                  )
                })()}
              </div>
            )
          })}
        </div>

        {error && (
          <div className="max-w-md mx-auto p-4 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm text-center mb-6">
            {error}
          </div>
        )}

        <div className="text-center">
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">
            ← Вернуться в личный кабинет
          </Link>
        </div>
      </div>
    </section>
  )
}
