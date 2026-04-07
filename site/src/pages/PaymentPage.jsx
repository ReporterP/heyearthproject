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
  return protocols.map(p => p === 'vless' ? 'VLESS Reality' : p === 'socks' ? 'SOCKS' : p.toUpperCase()).join(' + ')
}

export default function PaymentPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)
  const [selectedPlan, setSelectedPlan] = useState(null)
  const [currentPlanSlug, setCurrentPlanSlug] = useState(null)
  const [validUntil, setValidUntil] = useState(null)
  const [promocode, setPromocode] = useState('')
  const [paying, setPaying] = useState(false)
  const [error, setError] = useState('')

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
      } else if (profile?.current_plan) {
        // Fallback: match by name
        const match = (Array.isArray(plansData) ? plansData : []).find(
          p => p.name === profile.current_plan || p.slug === profile.current_plan
        )
        if (match) setCurrentPlanSlug(match.slug)
      }
      if (profile?.valid_until) setValidUntil(profile.valid_until)
    }).finally(() => setLoading(false))
  }, [token])

  async function handlePay(planId) {
    if (!token) {
      navigate('/login?redirect=/payment')
      return
    }
    setPaying(true)
    setError('')
    try {
      const data = await createPayment(token, null, window.location.origin + '/dashboard', planId, promocode)
      if (data.status === 'activated') {
        // Free plan activated immediately
        navigate('/dashboard')
        return
      }
      if (data.confirmation_url) {
        window.location.href = data.confirmation_url
      } else {
        setError('Не удалось создать платёж. Попробуйте позже.')
      }
    } catch (e) {
      setError(e.message || 'Ошибка создания платежа')
    } finally {
      setPaying(false)
    }
  }

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

        {/* Promocode */}
        <div className="max-w-md mx-auto mb-8 animate-fade-in-up delay-100">
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
        </div>

        {/* Plans grid */}
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5 mb-8">
          {plans.map((plan, idx) => {
            const isSelected = selectedPlan === plan.id
            const isCurrent = currentPlanSlug === plan.slug
            const isFree = plan.price_rub === 0
            const features = []
            features.push(protocolLabel(plan.allowed_protocols))
            features.push(`Трафик: ${formatTraffic(plan.traffic_limit_bytes)}`)
            features.push(`Срок: ${formatDuration(plan.duration_days)}`)
            features.push(`Серверов: ${plan.server_ids?.length || 'все'}`)
            const planFeatures = plan.features ? (typeof plan.features === 'string' ? JSON.parse(plan.features || '{}') : plan.features) : {}
            if (planFeatures.traffic_masking) features.push('Маскировка трафика')
            if (planFeatures.priority_support) features.push('Приоритетная поддержка')

            return (
              <div
                key={plan.id}
                onClick={() => setSelectedPlan(plan.id)}
                className={`relative glass-card rounded-2xl p-6 cursor-pointer transition-all duration-300 animate-fade-in-up ${
                  isCurrent
                    ? 'border-green-500/50 shadow-lg shadow-green-500/10'
                    : isSelected
                      ? 'border-brand/50 shadow-lg shadow-brand/10 -translate-y-1'
                      : 'border-white/10 hover:border-white/20'
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

                <div className="flex items-baseline gap-1 mb-5">
                  <span className="text-3xl font-bold text-brand">{isFree ? 'Бесплатно' : `${plan.price_rub}₽`}</span>
                  {!isFree && <span className="text-sm text-text-secondary">/ {formatDuration(plan.duration_days)}</span>}
                </div>

                <ul className="space-y-2 mb-6">
                  {features.map((f, i) => (
                    <li key={i} className="flex items-start gap-2 text-sm text-text-secondary">
                      <CheckIcon />
                      <span>{f}</span>
                    </li>
                  ))}
                </ul>

                <button
                  onClick={e => { e.stopPropagation(); handlePay(plan.id) }}
                  disabled={paying}
                  className={`w-full py-3 rounded-xl text-sm font-semibold transition-all duration-300 ${
                    isSelected
                      ? 'bg-gradient-to-r from-brand to-brand-dark text-white hover:shadow-lg hover:shadow-brand/25'
                      : 'bg-white/5 text-text-secondary hover:bg-white/10 hover:text-text-primary'
                  } disabled:opacity-50`}
                >
                  {paying ? 'Обработка...' : isCurrent ? 'Продлить' : isFree ? 'Активировать' : 'Оформить'}
                </button>
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
