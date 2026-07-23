import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { createPayment, getProfile } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import useReveal from '../hooks/useReveal.js'

const CheckIcon = () => (
  <svg className="w-4 h-4 mt-0.5 text-brand flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" strokeLinejoin="round">
    <path d="M5 12l5 5L20 6" />
  </svg>
)

function formatTraffic(bytes) {
  if (!bytes) return 'Безлимит'
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(0)} ГБ` : `${(bytes / (1024 * 1024)).toFixed(0)} МБ`
}
function formatDuration(days) {
  if (days >= 365) { const y = Math.round(days / 365); return `${y} ${y === 1 ? 'год' : y < 5 ? 'года' : 'лет'}` }
  if (days >= 30) return `${Math.round(days / 30)} мес.`
  return `${days} дн.`
}

function protocolLabel(protocols) {
  if (!protocols || protocols.includes('all')) return 'Все протоколы'
  // User-facing names only. Technical core identifiers are never surfaced
  // (§7 branding) — unknown ids collapse to a neutral label, never raw names.
  const labelMap = {
    vless: 'Прокси',
    hysteria2: 'Скоростной',
    xhttp: 'Стабильный канал',
    all: 'Все протоколы',
    socks: '', http: '',
  }
  const labels = protocols.map(p => (labelMap[p] !== undefined ? labelMap[p] : 'Современный')).filter(Boolean)
  const uniq = [...new Set(labels)]
  return uniq.length ? uniq.join(' + ') : 'Современные протоколы'
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
  const [prorations, setProrations] = useState({})
  const [earlyRenewalThreshold, setEarlyRenewalThreshold] = useState(3)
  const [switchConfirm, setSwitchConfirm] = useState(null) // planId pending a switch-confirm

  useEffect(() => {
    const promises = [fetch('/api/v1/plans').then(r => r.json()).catch(() => [])]
    if (token) promises.push(getProfile(token).catch(() => null))
    Promise.all(promises).then(([plansData, profile]) => {
      setPlans(Array.isArray(plansData) ? plansData : [])
      if (profile?.current_plan_slug) {
        setCurrentPlanSlug(profile.current_plan_slug)
        const match = (Array.isArray(plansData) ? plansData : []).find(p => p.slug === profile.current_plan_slug)
        if (match) setCurrentSortOrder(match.sort_order ?? -1)
      } else if (profile?.current_plan) {
        const match = (Array.isArray(plansData) ? plansData : []).find(p => p.name === profile.current_plan || p.slug === profile.current_plan)
        if (match) { setCurrentPlanSlug(match.slug); setCurrentSortOrder(match.sort_order ?? -1) }
      }
      if (profile?.valid_until) setValidUntil(profile.valid_until)
    }).finally(() => setLoading(false))
  }, [token])

  useEffect(() => {
    if (!token || !validUntil || plans.length === 0) return
    const paid = plans.filter(p => p.price_rub > 0)
    Promise.all(
      paid.map(p => fetch(`/api/v1/payment/proration-preview?plan_id=${p.id}`, { headers: { Authorization: 'Bearer ' + token } })
        .then(r => r.ok ? r.json() : null).then(d => [p.id, d]).catch(() => [p.id, null]))
    ).then(pairs => {
      const map = {}
      pairs.forEach(([id, d]) => { if (d) map[id] = d })
      setProrations(map)
      const firstWithThreshold = pairs.find(([, d]) => d && typeof d.early_renewal_days_threshold === 'number')
      if (firstWithThreshold) setEarlyRenewalThreshold(firstWithThreshold[1].early_renewal_days_threshold)
    })
  }, [token, validUntil, plans])

  useReveal([plans, loading])

  async function handlePay(planId) {
    setSwitchConfirm(null)
    if (!token) { navigate('/login?redirect=/payment'); return }
    setPaying(true)
    setError('')
    try {
      const data = await createPayment(token, null, window.location.origin + '/dashboard', planId, promocode, saveCard, email, 'embedded')
      if (data.status === 'activated') { navigate('/dashboard'); return }
      const plan = plans.find(p => p.id === planId)
      if (plan) sessionStorage.setItem('payment_pending', JSON.stringify({ name: plan.name, price: plan.price_rub }))
      if (data.confirmation_token) {
        sessionStorage.setItem(`checkout_token_${data.payment_id}`, data.confirmation_token)
        sessionStorage.setItem(`checkout_plan_${data.payment_id}`, plan?.name || '')
        navigate(`/checkout/${data.payment_id}`)
        return
      }
      if (data.confirmation_url) { window.location.href = data.confirmation_url; return }
      setError('Не удалось создать платёж. Попробуйте позже.')
    } catch (e) {
      setError(e.message || 'Не удалось создать платёж. Попробуйте позже.')
    } finally {
      setPaying(false)
    }
  }

  // click a buy button: confirm switch if replacing an active sub, else pay
  function onBuy(plan, isCurrent, isDowngrade) {
    if (isDowngrade || isCurrent) { if (isCurrent) handlePay(plan.id); return }
    if (currentPlanSlug && !isCurrent) { setSwitchConfirm(plan.id); return }
    handlePay(plan.id)
  }

  const daysRemaining = (() => {
    if (!validUntil) return Infinity
    const ms = new Date(validUntil).getTime() - Date.now()
    return Math.max(0, Math.ceil(ms / (24 * 60 * 60 * 1000)))
  })()
  const showEarlyRenewalBadge = currentPlanSlug && daysRemaining <= earlyRenewalThreshold

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-10 h-10 rounded-full border-2 border-brand/25 border-t-brand animate-spin" />
    </div>
  )

  return (
    <section className="relative py-10 md:py-16 overflow-hidden">
      <div className="pointer-events-none absolute top-0 left-1/4 w-96 h-96 bg-brand/6 rounded-full blur-[130px]" />
      <div className="pointer-events-none absolute bottom-0 right-1/4 w-96 h-96 bg-accent/6 rounded-full blur-[130px]" />

      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">
        <div className="text-center mb-10">
          <p className="eyebrow mb-4">Тарифы</p>
          <h1 className="serif text-3xl md:text-4xl font-semibold mb-3">Выберите тариф</h1>
          <p className="text-text-secondary max-w-lg mx-auto">
            Гибкие подписки с разными каналами и серверами. Все тарифы с автоматическим продлением — его можно отключить.
          </p>
        </div>

        {/* Promocode + Email + Save card */}
        <div className="max-w-lg mx-auto mb-9 space-y-3">
          <div className="flex gap-2">
            <input
              value={promocode}
              onChange={e => setPromocode(e.target.value)}
              placeholder="Промокод (если есть)"
              className="flex-1 px-4 py-2.5 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20"
            />
            {promocode && (
              <button onClick={() => setPromocode('')} aria-label="Очистить промокод" className="px-3 py-2 rounded-xl bg-surface-alt border border-brand/12 text-text-secondary text-sm hover:text-text-primary transition">
                <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" d="M6 6l12 12M18 6L6 18" /></svg>
              </button>
            )}
          </div>
          <div>
            <input
              value={email}
              onChange={e => setEmail(e.target.value)}
              type="email"
              placeholder="Email для чека (необязательно)"
              className="w-full px-4 py-2.5 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20"
            />
            <p className="text-[11px] text-faint mt-1 ml-1">Мы не храним вашу почту — она используется только для отправки чека</p>
          </div>
          <label className="flex items-center gap-2 cursor-pointer text-sm text-text-secondary">
            <input type="checkbox" checked={saveCard} onChange={e => setSaveCard(e.target.checked)} className="accent-brand w-4 h-4" />
            Сохранить карту для автопродления
          </label>
        </div>

        {/* Plans grid */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5 mb-8">
          {plans.map((plan, idx) => {
            const isSelected = selectedPlan === plan.id
            const isCurrent = currentPlanSlug === plan.slug
            const isFree = plan.price_rub === 0
            const isDowngrade = currentSortOrder >= 0 && !isCurrent && (plan.sort_order ?? 0) < currentSortOrder
            const features = []
            features.push(protocolLabel(plan.allowed_protocols))
            features.push(`Трафик: ${formatTraffic(plan.traffic_limit_bytes)}`)
            features.push(`Срок: ${formatDuration(plan.duration_days)}`)
            features.push(`Серверов: ${plan.server_ids?.length || 'все'}`)
            const planFeatures = plan.features ? (typeof plan.features === 'string' ? JSON.parse(plan.features || '{}') : plan.features) : {}
            if (planFeatures.traffic_masking) features.push('Приватная маршрутизация')
            if (planFeatures.priority_support) features.push('Приоритетная поддержка')
            if (plan.direct_routing_enabled) features.push('Доступ к РФ-сервисам')

            const pr = prorations[plan.id] || {}
            const blocked = pr.is_downgrade_blocked === true
            const bonus = pr.bonus_days || 0
            const validUntilDate = validUntil ? new Date(validUntil).toLocaleDateString('ru-RU') : ''
            const dr = daysRemaining
            const dayWord = dr === 1 ? 'день' : dr < 5 ? 'дня' : 'дней'

            return (
              <div
                key={plan.id}
                onClick={() => !isDowngrade && !isCurrent && setSelectedPlan(plan.id)}
                className={`relative rounded-[22px] p-6 reveal transition-colors duration-300 ${
                  isDowngrade ? 'card opacity-45 cursor-not-allowed'
                    : isCurrent ? 'card !border-brand/45 cursor-default'
                    : isSelected ? 'card-feat cursor-pointer'
                    : 'card card-hover cursor-pointer'
                }`}
                style={{ transitionDelay: `${Math.min(idx * 70, 220)}ms` }}
              >
                {isCurrent && (
                  <div className="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-brand text-[#04231a] text-[10px] font-bold uppercase tracking-wider">Текущий план</div>
                )}
                {isSelected && !isCurrent && (
                  <div className="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-accent text-[#2a1e02] text-[10px] font-bold uppercase tracking-wider">Выбрано</div>
                )}

                <h3 className="serif text-lg font-semibold mb-1">{plan.name}</h3>
                <p className="text-xs text-text-secondary mb-4">{plan.description}</p>

                <div className="flex items-baseline gap-1 mb-1">
                  <span className="serif text-3xl font-semibold text-text-primary tabular-nums">
                    {isFree ? 'Бесплатно' : `${pr.final_price ?? plan.price_rub}₽`}
                  </span>
                  {!isFree && <span className="text-sm text-text-secondary">/ {formatDuration(plan.duration_days)}</span>}
                </div>
                {!isFree && pr.proration_credit > 0 && (
                  <div className="mb-4 text-xs text-accent bg-accent/10 px-2 py-1 rounded-lg inline-flex items-center gap-1.5">
                    <span className="text-faint line-through">{plan.price_rub}₽</span>
                    <span>−{pr.proration_credit}₽ (остаток текущего тарифа)</span>
                  </div>
                )}
                {!isFree && !pr.proration_credit && <div className="mb-4" />}

                <ul className="space-y-2 mb-6">
                  {features.map((f, i) => (
                    <li key={i} className="flex items-start gap-2 text-sm text-text-secondary"><CheckIcon /><span>{f}</span></li>
                  ))}
                </ul>

                {bonus > 0 && !blocked && !isCurrent && !isDowngrade && (
                  <p className="text-xs text-brand mb-2">+{bonus} бонус-дней (за {pr.days_remaining} оставшихся дней текущего плана)</p>
                )}
                {isCurrent && showEarlyRenewalBadge && (
                  <p className="text-xs text-accent mb-2 flex items-center gap-1.5">
                    <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" strokeLinecap="round" /></svg>
                    Подписка скоро закончится — продлите заранее (осталось {dr} {dayWord})
                  </p>
                )}

                {blocked ? (
                  <button disabled title={`Доступно после ${validUntilDate}`} className="w-full py-2.5 rounded-xl text-sm font-medium bg-white/5 text-faint cursor-not-allowed">
                    Доступно после {validUntilDate}
                  </button>
                ) : (
                  <button
                    onClick={e => { e.stopPropagation(); onBuy(plan, isCurrent, isDowngrade) }}
                    disabled={paying || isDowngrade}
                    className={
                      isDowngrade ? 'w-full py-2.5 rounded-xl text-sm font-medium bg-white/5 text-faint cursor-not-allowed'
                        : isCurrent ? 'btn-ghost w-full !py-2.5 !text-sm'
                        : (isSelected ? 'btn-primary w-full !py-2.5 !text-sm' : 'btn-ghost w-full !py-2.5 !text-sm')
                    }
                  >
                    {isDowngrade ? 'Недоступно' : paying ? 'Обработка…' : isCurrent ? 'Продлить' : isFree ? 'Активировать' : 'Оформить'}
                  </button>
                )}
              </div>
            )
          })}
        </div>

        {error && (
          <div className="max-w-md mx-auto p-4 rounded-xl bg-danger/10 border border-danger/20 text-danger text-sm text-center mb-6">{error}</div>
        )}

        <div className="text-center">
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">← Вернуться в личный кабинет</Link>
        </div>
      </div>

      {/* Switch-confirm modal (replaces native confirm) */}
      {switchConfirm != null && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm animate-fade-in" onClick={() => setSwitchConfirm(null)}>
          <div className="glass-strong rounded-[22px] p-7 max-w-sm w-full text-center" onClick={e => e.stopPropagation()}>
            <div className="w-12 h-12 mx-auto rounded-full bg-accent/12 border border-accent/25 text-accent flex items-center justify-center mb-4">
              <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v4m0 4h.01M10.3 3.9 2 18a2 2 0 001.7 3h16.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z" /></svg>
            </div>
            <h3 className="serif text-lg font-semibold mb-2">Сменить тариф?</h3>
            <p className="text-sm text-text-secondary mb-6">Текущая подписка будет заменена новым тарифом. Неиспользованный остаток учитывается при оплате.</p>
            <div className="flex gap-3">
              <button onClick={() => setSwitchConfirm(null)} className="btn-ghost flex-1 !py-2.5 !text-sm">Отмена</button>
              <button onClick={() => handlePay(switchConfirm)} className="btn-primary flex-1 !py-2.5 !text-sm">Продолжить</button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
