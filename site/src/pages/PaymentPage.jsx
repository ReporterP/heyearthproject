import { useState } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { createPayment } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'

const CheckIcon = () => (
  <svg className="w-4 h-4 mt-0.5 text-accent flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.5}>
    <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
  </svg>
)

const plans = [
  {
    id: 'standard', name: 'Стандарт', price: '200', period: 'мес', highlighted: false,
    features: ['Эко-маршрутизация трафика', 'Без ограничений по трафику', 'Все устройства', 'Базовая поддержка'],
    icon: (
      <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M12 21a9.004 9.004 0 008.716-6.747M12 21a9.004 9.004 0 01-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 017.843 4.582M12 3a8.997 8.997 0 00-7.843 4.582m15.686 0A11.953 11.953 0 0112 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0121 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0112 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 013 12c0-1.605.42-3.113 1.157-4.418" />
      </svg>
    ),
  },
  {
    id: 'premium', name: 'Премиум', price: '250', period: 'мес', highlighted: true,
    features: ['Все из Стандарт', 'Приоритетная поддержка', 'Маскировка трафика', 'Расширенная аналитика'],
    icon: (
      <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.455 2.456L21.75 6l-1.036.259a3.375 3.375 0 00-2.455 2.456zM16.894 20.567L16.5 21.75l-.394-1.183a2.25 2.25 0 00-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 001.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 001.423 1.423l1.183.394-1.183.394a2.25 2.25 0 00-1.423 1.423z" />
      </svg>
    ),
  },
]

export default function PaymentPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [selectedPlan, setSelectedPlan] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handlePayment() {
    if (!selectedPlan) { setError('Выберите тариф'); return }
    if (!token) { navigate('/login'); return }
    setLoading(true)
    setError('')
    try {
      const returnUrl = window.location.origin + '/dashboard'
      const data = await createPayment(token, selectedPlan, returnUrl)
      if (data.confirmation_url) {
        window.location.href = data.confirmation_url
      } else {
        setError('Не удалось получить ссылку на оплату')
      }
    } catch (e) {
      setError(e.message || 'Ошибка создания платежа')
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="relative py-16 md:py-24 overflow-hidden">
      <div className="absolute top-1/3 -left-32 w-80 h-80 rounded-full bg-brand/8 blur-[100px]" />
      <div className="absolute bottom-1/3 -right-32 w-80 h-80 rounded-full bg-accent/8 blur-[100px]" />

      <div className="relative max-w-3xl mx-auto px-6">
        {/* Back link */}
        <Link
          to="/dashboard"
          className="inline-flex items-center gap-2 mb-6 text-sm text-text-secondary hover:text-text-primary transition-colors animate-fade-in"
        >
          <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5L3 12m0 0l7.5-7.5M3 12h18" />
          </svg>
          Назад в кабинет
        </Link>

        {/* Header */}
        <div className="text-center mb-12 animate-fade-in">
          <div className="inline-flex items-center gap-2 px-4 py-1.5 rounded-full glass text-xs font-medium text-text-secondary mb-6">
            <svg className="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M9 12.75L11.25 15 15 9.75M21 12c0 1.268-.63 2.39-1.593 3.068a3.745 3.745 0 01-1.043 3.296 3.745 3.745 0 01-3.296 1.043A3.745 3.745 0 0112 21c-1.268 0-2.39-.63-3.068-1.593a3.746 3.746 0 01-3.296-1.043 3.745 3.745 0 01-1.043-3.296A3.745 3.745 0 013 12c0-1.268.63-2.39 1.593-3.068a3.745 3.745 0 011.043-3.296 3.746 3.746 0 013.296-1.043A3.746 3.746 0 0112 3c1.268 0 2.39.63 3.068 1.593a3.746 3.746 0 013.296 1.043 3.746 3.746 0 011.043 3.296A3.745 3.745 0 0121 12z" />
            </svg>
            Безопасная оплата через YooKassa
          </div>
          <h1 className="text-3xl md:text-4xl font-bold mb-2">Выберите тариф</h1>
          <p className="text-text-secondary">Подписка активируется мгновенно после оплаты</p>
        </div>

        {/* Plan cards */}
        <div className="grid md:grid-cols-2 gap-6 mb-8">
          {plans.map((p, i) => {
            const isSelected = selectedPlan === p.id
            return (
              <div
                key={p.id}
                className={`group relative p-7 rounded-2xl cursor-pointer transition-all duration-500 hover:-translate-y-1 animate-fade-in-up ${
                  p.highlighted
                    ? 'glass-strong border border-brand/30 glow-brand'
                    : 'glass-card hover:bg-white/[0.06]'
                } ${isSelected ? 'ring-2 ring-brand ring-offset-1 ring-offset-surface' : ''}`}
                onClick={() => setSelectedPlan(p.id)}
              >
                {p.highlighted && (
                  <div className="absolute -top-3.5 left-1/2 -translate-x-1/2">
                    <span className="px-4 py-1 rounded-full bg-gradient-to-r from-brand to-brand-light text-xs font-bold text-white shadow-lg shadow-brand/25">
                      Рекомендуем
                    </span>
                  </div>
                )}

                <div className="absolute top-6 right-6">
                  <div className={`w-6 h-6 rounded-full border-2 flex items-center justify-center transition-all duration-300 ${
                    isSelected
                      ? 'bg-brand border-brand'
                      : 'border-white/20 group-hover:border-white/40'
                  }`}>
                    {isSelected && (
                      <svg className="w-3.5 h-3.5 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3}>
                        <path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" />
                      </svg>
                    )}
                  </div>
                </div>

                <div className={`w-12 h-12 rounded-xl flex items-center justify-center mb-4 ${
                  p.highlighted
                    ? 'bg-gradient-to-br from-brand/20 to-accent/10 text-brand-light'
                    : 'bg-white/5 text-text-secondary'
                }`}>
                  {p.icon}
                </div>

                <h3 className="text-xl font-bold mb-1">{p.name}</h3>
                <div className="flex items-baseline gap-1 mb-6">
                  <span className="text-4xl font-extrabold tracking-tight">{p.price}</span>
                  <span className="text-text-secondary text-sm">&thinsp;&#8381; / {p.period}</span>
                </div>

                <ul className="space-y-3">
                  {p.features.map(feat => (
                    <li key={feat} className="flex items-start gap-3 text-sm text-text-secondary">
                      <CheckIcon />
                      <span>{feat}</span>
                    </li>
                  ))}
                </ul>
              </div>
            )
          })}
        </div>

        {/* Info: promo code on dashboard */}
        <div className="glass-card rounded-2xl p-4 mb-6 animate-fade-in-up">
          <div className="flex items-center gap-3">
            <svg className="w-5 h-5 text-purple-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M9.568 3H5.25A2.25 2.25 0 003 5.25v4.318c0 .597.237 1.17.659 1.591l9.581 9.581c.699.699 1.78.872 2.607.33a18.095 18.095 0 005.223-5.223c.542-.827.369-1.908-.33-2.607L11.16 3.66A2.25 2.25 0 009.568 3z" />
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 6h.008v.008H6V6z" />
            </svg>
            <p className="text-sm text-text-secondary">
              Есть промокод? Активируйте его в <Link to="/dashboard" className="text-brand-light hover:text-accent transition-colors">Личном кабинете</Link>
            </p>
          </div>
        </div>

        {/* Error */}
        {error && (
          <div className="flex items-start gap-2 p-3 rounded-xl bg-red-500/10 border border-red-500/20 mb-6 animate-fade-in">
            <svg className="w-5 h-5 text-red-400 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
            </svg>
            <p className="text-red-400 text-sm">{error}</p>
          </div>
        )}

        {/* Pay button */}
        <button
          disabled={loading || !selectedPlan}
          className="group relative w-full py-4 rounded-xl font-bold text-white text-lg overflow-hidden transition-all duration-300 disabled:opacity-50 hover:shadow-xl hover:shadow-brand/25 hover:-translate-y-0.5 animate-fade-in-up"
          onClick={handlePayment}
        >
          <span className="absolute inset-0 bg-gradient-to-r from-brand to-brand-dark" />
          <span className="absolute inset-0 bg-gradient-to-r from-brand-light to-accent opacity-0 group-hover:opacity-100 transition-opacity duration-500" />
          <span className="relative z-10 flex items-center justify-center gap-2">
            {loading ? (
              <>
                <svg className="w-5 h-5 animate-spin" viewBox="0 0 24 24" fill="none">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
                </svg>
                Перенаправление на оплату...
              </>
            ) : (
              <>
                <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-6 2.25h3m-3.75 3h15a2.25 2.25 0 002.25-2.25V6.75A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25v10.5A2.25 2.25 0 004.5 19.5z" />
                </svg>
                Оплатить {selectedPlan ? `${plans.find(p => p.id === selectedPlan)?.price} \u20BD` : ''}
              </>
            )}
          </span>
        </button>

        <div className="flex items-center justify-center gap-2 mt-4 text-xs text-text-secondary/60">
          <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M16.5 10.5V6.75a4.5 4.5 0 10-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 002.25-2.25v-6.75a2.25 2.25 0 00-2.25-2.25H6.75a2.25 2.25 0 00-2.25 2.25v6.75a2.25 2.25 0 002.25 2.25z" />
          </svg>
          Безопасная оплата. Данные карты защищены.
        </div>
      </div>
    </section>
  )
}
