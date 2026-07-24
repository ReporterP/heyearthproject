import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getPayments, getProfile, getUserCards, deleteUserCard, toggleAutoRenew, requestRefund, pauseSubscription, resumeSubscription } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { CabinetSkeleton } from '../components/Skeleton.jsx'

function formatDate(d) {
  if (!d) return '—'
  return new Date(d).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function statusLabel(status) {
  const map = {
    pending: { text: 'Ожидание', color: 'text-accent bg-accent/10' },
    completed: { text: 'Оплачено', color: 'text-brand bg-brand/10' },
    succeeded: { text: 'Оплачено', color: 'text-brand bg-brand/10' },
    failed: { text: 'Отклонён', color: 'text-danger bg-danger/10' },
    cancelled: { text: 'Отменён', color: 'text-faint bg-white/5' },
    refunded: { text: 'Возврат', color: 'text-accent bg-accent/10' },
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
  const [autoRenewLoading, setAutoRenewLoading] = useState(false)
  const [cancelMsg, setCancelMsg] = useState('')

  // Confirm / input modal (replaces native confirm/prompt/alert)
  const [modal, setModal] = useState(null) // { title, message, input?, inputLabel?, confirmLabel, danger?, onConfirm }
  const [modalInput, setModalInput] = useState('')
  const [modalBusy, setModalBusy] = useState(false)

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
    }).finally(() => setLoading(false))
  }, [token])

  const currentPlan = profile?.current_plan || ''
  const validUntil = profile?.valid_until
  const pausedUntil = profile?.paused_until
  const isPaused = pausedUntil ? new Date(pausedUntil) > new Date() : false
  const hasActiveSub = !!currentPlan

  function openModal(cfg) { setModalInput(''); setModal(cfg) }
  function closeModal() { if (!modalBusy) setModal(null) }
  async function confirmModal() {
    if (!modal?.onConfirm) return
    setModalBusy(true)
    try {
      await modal.onConfirm(modalInput)
      setModal(null)
    } catch (e) {
      setCancelMsg(e.message || 'Что-то пошло не так')
      setModal(null)
    } finally {
      setModalBusy(false)
    }
  }

  function handlePause(months) {
    openModal({
      title: 'Приостановить подписку',
      message: `Приостановить на ${months} мес.? Доступ будет закрыт до окончания паузы, но подписка сохранится.`,
      confirmLabel: 'Приостановить',
      onConfirm: async () => {
        await pauseSubscription(token, months)
        const prof = await getProfile(token).catch(() => profile)
        setProfile(prof)
        setCancelMsg(`Подписка приостановлена на ${months} мес.`)
      },
    })
  }

  function handleResume() {
    openModal({
      title: 'Возобновить подписку',
      message: 'Возобновить подписку сейчас? Доступ откроется сразу.',
      confirmLabel: 'Возобновить',
      onConfirm: async () => {
        await resumeSubscription(token)
        const prof = await getProfile(token).catch(() => profile)
        setProfile(prof)
        setCancelMsg('Подписка возобновлена')
      },
    })
  }

  function handleCancelSubscription() {
    openModal({
      title: 'Отменить подписку',
      message: 'Автопродление будет выключено, а сохранённые карты удалены. Доступ сохранится до конца оплаченного периода.',
      confirmLabel: 'Отменить подписку',
      danger: true,
      onConfirm: async () => {
        setAutoRenewLoading(true)
        try {
          await toggleAutoRenew(token, false)
          for (const card of cards) {
            try { await deleteUserCard(token, card.payment_method_id || card.card_id) } catch {}
          }
          setCards([])
          setCancelMsg('Подписка отменена. Доступ сохранится до ' + formatDate(validUntil))
        } finally {
          setAutoRenewLoading(false)
        }
      },
    })
  }

  function handleDeleteCard(card, title) {
    openModal({
      title: 'Удалить способ оплаты',
      message: `Удалить «${title}»? Автопродление по этой карте будет отключено.`,
      confirmLabel: 'Удалить',
      danger: true,
      onConfirm: async () => {
        await deleteUserCard(token, card.payment_method_id)
        setCards(cards.filter(c => (c.payment_method_id || c.id) !== (card.payment_method_id || card.id)))
      },
    })
  }

  function handleRefund(paymentId) {
    openModal({
      title: 'Запросить возврат',
      message: 'Опишите причину возврата — заявку рассмотрит администратор.',
      input: true,
      inputLabel: 'Причина возврата',
      confirmLabel: 'Отправить заявку',
      onConfirm: async (reason) => {
        const res = await requestRefund(token, paymentId, reason || 'без причины')
        const payData = await getPayments(token).catch(() => ({ items: [] }))
        setPayments(payData?.items || [])
        setCancelMsg(res.message || 'Заявка на возврат отправлена')
      },
    })
  }

  if (loading) return <CabinetSkeleton />

  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-4xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8">
          <div>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Оплата и подписка</h1>
            <p className="text-sm text-text-secondary mt-1">История платежей, карты, управление подпиской</p>
          </div>
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">← Кабинет</Link>
        </div>

        {cancelMsg && (
          <div className="mb-6 flex items-start gap-2 p-3 rounded-xl bg-brand/10 border border-brand/20 animate-fade-in">
            <svg className="w-5 h-5 text-brand flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
            <p className="text-sm text-text-primary">{cancelMsg}</p>
          </div>
        )}

        {/* Current subscription */}
        {hasActiveSub && (
          <div className="card rounded-[22px] p-6 mb-6 !border-brand/25">
            <div className="flex items-center justify-between gap-4 flex-wrap">
              <div>
                <p className="text-xs text-faint uppercase tracking-wider mb-1">Текущая подписка</p>
                <p className="serif text-xl font-semibold">{currentPlan}</p>
                {validUntil && <p className="text-sm text-text-secondary mt-1">Действует до {formatDate(validUntil)}</p>}
              </div>
              <div className="flex flex-col items-end gap-2">
                <Link to="/payment" className="btn-primary !py-2 !px-4 !text-sm">Сменить тариф</Link>
                <button onClick={handleCancelSubscription} disabled={autoRenewLoading} className="text-xs text-danger hover:brightness-125 transition">Отменить подписку</button>
              </div>
            </div>
          </div>
        )}

        {/* Pause subscription */}
        {hasActiveSub && (
          <div className="card rounded-[22px] p-6 mb-6">
            {isPaused ? (
              <div>
                <div className="flex items-center gap-2 mb-2">
                  <span className="w-2 h-2 rounded-full bg-accent animate-pulse" />
                  <h2 className="serif text-lg font-semibold">Подписка приостановлена</h2>
                </div>
                <p className="text-sm text-text-secondary mb-4">Доступ закрыт до {formatDate(pausedUntil)}. Карта сохранена, автопродление продолжится после возобновления.</p>
                <button onClick={handleResume} className="btn-primary !py-2 !px-4 !text-sm">Возобновить сейчас</button>
              </div>
            ) : (
              <div>
                <h2 className="serif text-lg font-semibold mb-1">Приостановить подписку</h2>
                <p className="text-sm text-text-secondary mb-4">Если сервис временно не нужен — приостановите на 1–3 мес. Карта сохранится, автопродление не сработает в этот период. Можно возобновить в любой момент.</p>
                <div className="flex flex-wrap gap-2">
                  {[1, 2, 3].map(m => (
                    <button key={m} onClick={() => handlePause(m)} className="btn-ghost !py-2 !px-4 !text-sm">На {m} мес.</button>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}

        {/* Saved cards */}
        <div className="card rounded-[22px] p-6 mb-6">
          <h2 className="serif text-lg font-semibold mb-4">Способы оплаты</h2>
          {cards.length === 0 ? (
            <p className="text-sm text-text-secondary">Нет сохранённых способов оплаты. Привяжутся автоматически, если при оплате отметить «Сохранить для автопродления».</p>
          ) : (
            <div className="space-y-3">
              {cards.map(card => {
                const LABELS = { bank_card: 'Карта', sbp: 'СБП', yoo_money: 'ЮMoney', tinkoff_bank: 'T-Pay', sberbank: 'Сбер', alfabank: 'Alfa' }
                const typeLabel = LABELS[card.type] || card.type || 'Метод'
                const title = card.title || (card.last4 ? `•••• ${card.last4}` : typeLabel)
                return (
                  <div key={card.id || card.payment_method_id} className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-brand/10">
                    <div className="flex items-center gap-3">
                      <div className="w-10 h-7 rounded bg-brand/12 border border-brand/20 flex items-center justify-center text-[10px] font-bold text-brand">{typeLabel.slice(0, 4)}</div>
                      <div>
                        <p className="font-medium text-sm">{title}</p>
                        <p className="text-xs text-text-secondary">{card.card_type || card.issuer || typeLabel}</p>
                      </div>
                    </div>
                    <button onClick={() => handleDeleteCard(card, title)} className="text-xs text-danger hover:brightness-125 transition">Удалить</button>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Payment history */}
        <div className="card rounded-[22px] p-6">
          <h2 className="serif text-lg font-semibold mb-4">История платежей</h2>
          {payments.length === 0 ? (
            <p className="text-sm text-text-secondary">Пока платежей нет.</p>
          ) : (
            <div className="space-y-3">
              {payments.map(p => (
                <Link key={p.id} to={`/payments/${p.id}`} className="group flex items-center justify-between p-4 rounded-xl bg-white/[0.03] border border-brand/8 hover:border-brand/25 hover:bg-white/[0.05] transition">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3 mb-1 flex-wrap">
                      <span className="text-lg font-semibold tabular-nums">{parseFloat(p.amount).toLocaleString('ru-RU')} {p.currency}</span>
                      {statusLabel(p.status)}
                    </div>
                    <p className="text-sm text-text-secondary truncate">{p.notes || p.description || 'Платёж'}</p>
                    <p className="text-xs text-faint mt-0.5">{formatDate(p.created_at)}</p>
                  </div>
                  <div className="flex items-center gap-3 flex-shrink-0 ml-3">
                    {p.status === 'completed' && (
                      <button onClick={e => { e.preventDefault(); e.stopPropagation(); handleRefund(p.id) }} className="text-xs text-accent hover:brightness-110 px-2 py-1 rounded-lg bg-accent/10 transition">Запросить возврат</button>
                    )}
                    <svg className="w-5 h-5 text-faint group-hover:text-brand group-hover:translate-x-1 transition-all" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" /></svg>
                  </div>
                </Link>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Confirm / input modal */}
      {modal && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm animate-fade-in" onClick={closeModal}>
          <div className="glass-strong rounded-[22px] p-7 max-w-sm w-full" onClick={e => e.stopPropagation()}>
            <div className={`w-12 h-12 mx-auto rounded-full flex items-center justify-center mb-4 border ${modal.danger ? 'bg-danger/12 border-danger/25 text-danger' : 'bg-brand/12 border-brand/25 text-brand'}`}>
              <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v4m0 4h.01M10.3 3.9 2 18a2 2 0 001.7 3h16.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z" /></svg>
            </div>
            <h3 className="serif text-lg font-semibold text-center mb-2">{modal.title}</h3>
            <p className="text-sm text-text-secondary text-center mb-5">{modal.message}</p>
            {modal.input && (
              <div className="mb-5">
                {modal.inputLabel && <label htmlFor="modal-input" className="block text-xs font-medium text-text-secondary mb-2">{modal.inputLabel}</label>}
                <textarea
                  id="modal-input"
                  value={modalInput}
                  onChange={e => setModalInput(e.target.value)}
                  rows={3}
                  className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/25 text-sm placeholder:text-faint resize-none"
                  placeholder="Например: передумал, ошибочная оплата…"
                />
              </div>
            )}
            <div className="flex gap-3">
              <button onClick={closeModal} disabled={modalBusy} className="btn-ghost flex-1 !py-2.5 !text-sm disabled:opacity-50">Отмена</button>
              <button
                onClick={confirmModal}
                disabled={modalBusy}
                className={`flex-1 inline-flex items-center justify-center gap-2 !py-2.5 !text-sm rounded-full font-semibold transition-all active:scale-[0.97] disabled:opacity-50 ${modal.danger ? 'bg-danger text-white hover:brightness-110' : 'btn-primary'}`}
              >
                {modalBusy ? 'Подождите…' : modal.confirmLabel}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
