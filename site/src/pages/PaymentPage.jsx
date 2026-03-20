import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { createPayment } from '../api/user.js'

const CheckIcon = () => (
  <svg className="w-4 h-4 mt-0.5 text-brand-light flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
    <path fillRule="evenodd" d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z" clipRule="evenodd" />
  </svg>
)

const plans = [
  {
    id: 'standard', name: 'Стандарт', price: '200', period: 'мес', highlighted: false,
    features: ['Эко-маршрутизация трафика', 'Без ограничений по трафику', 'Все устройства'],
  },
  {
    id: 'premium', name: 'Премиум', price: '250', period: 'мес', highlighted: true,
    features: ['Все из Стандарт', 'Приоритетная поддержка', 'Максимальная оптимизация маршрутов'],
  },
]

export default function PaymentPage() {
  const navigate = useNavigate()
  const token = localStorage.getItem('token')

  const [selectedPlan, setSelectedPlan] = useState('')
  const [promoCode, setPromoCode] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  async function handlePayment() {
    if (!selectedPlan) { setError('Выберите тариф'); return }
    if (!token) { navigate('/login'); return }
    setLoading(true)
    setError('')
    try {
      const returnUrl = window.location.origin + '/dashboard'
      const data = await createPayment(token, selectedPlan, returnUrl, promoCode.trim())
      if (data.payment_url || data.confirmation_url) {
        window.location.href = data.payment_url || data.confirmation_url
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
    <section className="py-24">
      <div className="max-w-3xl mx-auto px-6">
        <h1 className="text-3xl font-bold text-center mb-2">Выберите тариф</h1>
        <p className="text-text-secondary text-center mb-12">Оплата через YooKassa. Подписка активируется мгновенно.</p>

        <div className="grid md:grid-cols-2 gap-8 mb-10">
          {plans.map(p => (
            <div
              key={p.id}
              className={`relative p-8 rounded-2xl border transition cursor-pointer ${
                p.highlighted
                  ? 'bg-gradient-to-b from-brand/10 to-transparent border-brand/40 shadow-xl shadow-brand/10'
                  : 'bg-surface-card border-white/5 hover:border-white/10'
              } ${selectedPlan === p.id ? 'ring-2 ring-brand' : ''}`}
              onClick={() => setSelectedPlan(p.id)}
            >
              {p.highlighted && (
                <div className="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-0.5 bg-brand rounded-full text-xs font-semibold">
                  Рекомендуем
                </div>
              )}
              <h3 className="text-xl font-bold mb-1">{p.name}</h3>
              <div className="flex items-baseline gap-1 mb-6">
                <span className="text-4xl font-extrabold">{p.price}</span>
                <span className="text-text-secondary">&thinsp;&#8381; / {p.period}</span>
              </div>
              <ul className="space-y-3 mb-6">
                {p.features.map(feat => (
                  <li key={feat} className="flex items-start gap-2 text-sm text-text-secondary">
                    <CheckIcon />
                    <span>{feat}</span>
                  </li>
                ))}
              </ul>
              <div className={`w-5 h-5 rounded-full border-2 mx-auto transition ${
                selectedPlan === p.id ? 'bg-brand border-brand' : 'border-white/20'
              }`} />
            </div>
          ))}
        </div>

        {/* Promo code */}
        <div className="mb-8">
          <label className="block text-sm text-text-secondary mb-2">Промокод (необязательно)</label>
          <input
            type="text"
            value={promoCode}
            onChange={e => setPromoCode(e.target.value)}
            placeholder="Введите промокод"
            className="w-full px-4 py-3 rounded-xl bg-surface-card border border-white/10 focus:border-brand/50 focus:outline-none transition"
          />
        </div>

        {error && <p className="text-red-400 text-sm mb-4">{error}</p>}

        <button
          disabled={loading || !selectedPlan}
          className="w-full py-3 rounded-xl bg-brand hover:bg-brand-dark text-white font-semibold transition disabled:opacity-50"
          onClick={handlePayment}
        >
          {loading ? 'Перенаправление...' : 'Оплатить'}
        </button>
      </div>
    </section>
  )
}
