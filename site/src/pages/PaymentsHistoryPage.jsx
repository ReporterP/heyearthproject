import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getPayments, getProfile, getUserCards, deleteUserCard, toggleAutoRenew, requestRefund } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'

function formatDate(d) {
  if (!d) return '---'
  return new Date(d).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function statusLabel(status) {
  const map = {
    pending: { text: 'Ожидание', color: 'text-yellow-400 bg-yellow-500/10' },
    completed: { text: 'Оплачено', color: 'text-green-400 bg-green-500/10' },
    succeeded: { text: 'Оплачено', color: 'text-green-400 bg-green-500/10' },
    failed: { text: 'Отклонён', color: 'text-red-400 bg-red-500/10' },
    cancelled: { text: 'Отменён', color: 'text-gray-400 bg-gray-500/10' },
    refunded: { text: 'Возврат', color: 'text-orange-400 bg-orange-500/10' },
  }
  const s = map[status] || { text: status, color: 'text-text-secondary bg-white/5' }
  return <span className={`px-2 py-0.5 rounded-md text-xs font-medium ${s.color}`}>{s.text}</span>
}

export default function PaymentsHistoryPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [profile, setProfile] = useState(null)
  const [payments, setPayments] = useState([])
  const [cards, setCards] = useState([])
  const [loading, setLoading] = useState(true)
  const [autoRenew, setAutoRenew] = useState(true)
  const [autoRenewLoading, setAutoRenewLoading] = useState(false)
  const [cancelMsg, setCancelMsg] = useState('')

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    Promise.all([
      getProfile(token).catch(() => null),
      getPayments(token).catch(() => ({ items: [] })),
      getUserCards(token).catch(() => ({ cards: [] })),
    ]).then(([prof, payData, cardsData]) => {
      setProfile(prof)
      setPayments(payData?.items || [])
      setCards(cardsData?.cards || [])
      // Auto-renew from subscription data
    }).finally(() => setLoading(false))
  }, [token])

  const currentPlan = profile?.current_plan || ''
  const validUntil = profile?.valid_until
  const hasActiveSub = !!currentPlan

  async function handleCancelSubscription() {
    if (!confirm('Отменить подписку? Автопродление будет выключено, но доступ сохранится до конца оплаченного периода.')) return
    setAutoRenewLoading(true)
    try {
      // 1. Disable auto-renew
      await toggleAutoRenew(token, false)
      setAutoRenew(false)
      // 2. Remove all saved cards (unbind)
      for (const card of cards) {
        try { await deleteUserCard(token, card.card_id) } catch {}
      }
      setCards([])
      setCancelMsg('Подписка отменена. Доступ сохранится до ' + formatDate(validUntil))
    } catch (e) {
      setCancelMsg('Ошибка: ' + e.message)
    } finally {
      setAutoRenewLoading(false)
    }
  }

  async function handleRefund(paymentId) {
    const reason = prompt('Укажите причину возврата (заявка будет рассмотрена администратором):')
    if (reason === null) return // cancelled
    try {
      const res = await requestRefund(token, paymentId, reason || 'без причины')
      alert(res.message || 'Заявка отправлена')
      const payData = await getPayments(token).catch(() => ({ items: [] }))
      setPayments(payData?.items || [])
    } catch (e) {
      alert(e.message)
    }
  }

  if (loading) return (
    <div className="flex items-center justify-center py-32">
      <div className="w-10 h-10 rounded-full border-2 border-brand/30 border-t-brand animate-spin" />
    </div>
  )

  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-4xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8">
          <div>
            <h1 className="text-2xl md:text-3xl font-bold">Оплата и подписка</h1>
            <p className="text-sm text-text-secondary mt-1">История платежей, карты, управление подпиской</p>
          </div>
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">
            ← Кабинет
          </Link>
        </div>

        {/* Current subscription */}
        {hasActiveSub && (
          <div className="glass-card rounded-2xl p-6 mb-6 border border-green-500/20">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-xs text-text-secondary uppercase tracking-wider mb-1">Текущая подписка</p>
                <p className="text-xl font-bold">{currentPlan}</p>
                {validUntil && (
                  <p className="text-sm text-text-secondary mt-1">Действует до {formatDate(validUntil)}</p>
                )}
              </div>
              <div className="flex flex-col items-end gap-2">
                <Link to="/payment" className="px-4 py-2 rounded-xl bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
                  Сменить тариф
                </Link>
                <button
                  onClick={handleCancelSubscription}
                  disabled={autoRenewLoading}
                  className="text-xs text-red-400 hover:text-red-300 transition"
                >
                  Отменить подписку
                </button>
              </div>
            </div>
            {cancelMsg && (
              <p className="mt-3 text-sm text-amber-400 bg-amber-500/10 px-3 py-2 rounded-lg">{cancelMsg}</p>
            )}
          </div>
        )}

        {/* Saved cards */}
        <div className="glass-card rounded-2xl p-6 mb-6">
          <h2 className="text-lg font-semibold mb-4">Способы оплаты</h2>
          {cards.length === 0 ? (
            <p className="text-sm text-text-secondary">Нет сохранённых карт. Карта привяжется при следующей оплате.</p>
          ) : (
            <div className="space-y-3">
              {cards.map(card => (
                <div key={card.card_id} className="flex items-center justify-between p-3 rounded-xl bg-white/5 border border-white/10">
                  <div className="flex items-center gap-3">
                    <div className="w-10 h-7 rounded bg-gradient-to-br from-blue-500/30 to-purple-500/30 flex items-center justify-center text-[10px] font-bold text-text-secondary">
                      {card.card_type === 'credit' ? 'CR' : 'DB'}
                    </div>
                    <div>
                      <p className="font-mono text-sm">{card.pan}</p>
                      <p className="text-xs text-text-secondary">{card.exp_date || ''}</p>
                    </div>
                  </div>
                  <button
                    onClick={async () => {
                      if (!confirm(`Удалить карту ${card.pan}?`)) return
                      try {
                        await deleteUserCard(token, card.card_id)
                        setCards(cards.filter(c => c.card_id !== card.card_id))
                      } catch (e) { alert(e.message) }
                    }}
                    className="text-xs text-red-400 hover:text-red-300 transition"
                  >
                    Удалить
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Payment history */}
        <div className="glass-card rounded-2xl p-6">
          <h2 className="text-lg font-semibold mb-4">История платежей</h2>
          {payments.length === 0 ? (
            <p className="text-sm text-text-secondary">Нет платежей</p>
          ) : (
            <div className="space-y-3">
              {payments.map(p => (
                <div key={p.id} className="flex items-center justify-between p-4 rounded-xl bg-white/[0.03] border border-white/5 hover:border-white/10 transition">
                  <div className="flex-1">
                    <div className="flex items-center gap-3 mb-1">
                      <span className="text-lg font-bold">{parseFloat(p.amount).toLocaleString('ru-RU')} {p.currency}</span>
                      {statusLabel(p.status)}
                    </div>
                    <p className="text-sm text-text-secondary">{p.notes || p.description || 'Платёж'}</p>
                    <p className="text-xs text-text-secondary mt-0.5">{formatDate(p.created_at)}</p>
                  </div>
                  <div className="flex items-center gap-2">
                    {p.status === 'completed' && (
                      <button
                        onClick={() => handleRefund(p.id)}
                        className="text-xs text-amber-400 hover:text-amber-300 px-2 py-1 rounded-lg bg-amber-500/10 transition"
                      >
                        Запросить возврат
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  )
}
