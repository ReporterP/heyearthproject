import { useState, useEffect } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { getPayments, getProfile, getUserCards, getSubscription, deleteUserCard, toggleAutoRenew, requestRefund, pauseSubscription, resumeSubscription } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { CabinetSkeleton } from '../components/Skeleton.jsx'
import useReveal from '../hooks/useReveal.js'

function formatDate(d) {
  if (!d) return '—'
  return new Date(d).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}
function monthKey(d) {
  if (!d) return 'Ранее'
  const dt = new Date(d)
  return dt.toLocaleDateString('ru-RU', { month: 'long', year: 'numeric' })
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

/* Empty state with a way forward, not a blank area */
function Empty({ icon, title, hint, action }) {
  return (
    <div className="text-center py-8">
      <div className="w-11 h-11 mx-auto rounded-full bg-white/[0.04] border border-brand/10 text-faint flex items-center justify-center mb-3">{icon}</div>
      <p className="text-sm font-medium mb-1">{title}</p>
      <p className="text-xs text-text-secondary max-w-xs mx-auto">{hint}</p>
      {action}
    </div>
  )
}

export default function PaymentsHistoryPage() {
  const navigate = useNavigate()
  const { token } = useAuth()

  const [profile, setProfile] = useState(null)
  const [payments, setPayments] = useState([])
  const [cards, setCards] = useState([])
  const [loading, setLoading] = useState(true)
  const [autoRenew, setAutoRenew] = useState(null)   // synced from API (was: hardcoded true)
  const [autoRenewLoading, setAutoRenewLoading] = useState(false)
  const [notice, setNotice] = useState('')

  const [modal, setModal] = useState(null)
  const [modalInput, setModalInput] = useState('')
  const [modalBusy, setModalBusy] = useState(false)

  useEffect(() => {
    if (!token) { navigate('/login'); return }
    Promise.all([
      getProfile(token).catch(() => null),
      getPayments(token).catch(() => ({ items: [] })),
      getUserCards(token).catch(() => ({ cards: [] })),
      getSubscription(token).catch(() => null),
    ]).then(([prof, payData, cardsData, subData]) => {
      setProfile(prof)
      setPayments(payData?.items || [])
      setCards(cardsData?.cards || [])
      // FIX: auto-renew now reflects the server, so the toggle tells the truth
      const active = subData?.items?.find(s => s.active) || subData?.items?.[0]
      if (active && typeof active.auto_renew === 'boolean') setAutoRenew(active.auto_renew)
    }).finally(() => setLoading(false))
  }, [token])

  useReveal([loading, payments])

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
      setNotice(e.message || 'Что-то пошло не так')
      setModal(null)
    } finally {
      setModalBusy(false)
    }
  }

  /* Auto-renew can now be turned back ON, not only cancelled */
  async function handleToggleAutoRenew(next) {
    setAutoRenewLoading(true)
    try {
      await toggleAutoRenew(token, next)
      setAutoRenew(next)
      setNotice(next ? 'Автопродление включено' : 'Автопродление выключено — подписка не продлится автоматически')
    } catch (e) {
      setNotice(e.message || 'Не удалось изменить автопродление')
    } finally {
      setAutoRenewLoading(false)
    }
  }

  function handlePause(months) {
    openModal({
      title: 'Приостановить подписку',
      message: `Приостановить на ${months} мес.? Доступ будет закрыт до конца паузы, подписка сохранится.`,
      confirmLabel: 'Приостановить',
      onConfirm: async () => {
        await pauseSubscription(token, months)
        setProfile(await getProfile(token).catch(() => profile))
        setNotice(`Подписка приостановлена на ${months} мес.`)
      },
    })
  }

  function handleResume() {
    openModal({
      title: 'Возобновить подписку',
      message: 'Доступ откроется сразу после возобновления.',
      confirmLabel: 'Возобновить',
      onConfirm: async () => {
        await resumeSubscription(token)
        setProfile(await getProfile(token).catch(() => profile))
        setNotice('Подписка возобновлена')
      },
    })
  }

  function handleCancelSubscription() {
    openModal({
      title: 'Отменить подписку',
      message: 'Автопродление выключится, сохранённые карты будут удалены. Доступ сохранится до конца оплаченного периода.',
      confirmLabel: 'Отменить подписку',
      danger: true,
      onConfirm: async () => {
        setAutoRenewLoading(true)
        try {
          await toggleAutoRenew(token, false)
          setAutoRenew(false)
          for (const card of cards) {
            try { await deleteUserCard(token, card.payment_method_id || card.card_id) } catch {}
          }
          setCards([])
          setNotice('Подписка отменена. Доступ сохранится до ' + formatDate(validUntil))
        } finally {
          setAutoRenewLoading(false)
        }
      },
    })
  }

  function handleDeleteCard(card, title) {
    openModal({
      title: 'Удалить способ оплаты',
      message: `Удалить «${title}»? Автопродление по этой карте перестанет работать.`,
      confirmLabel: 'Удалить',
      danger: true,
      onConfirm: async () => {
        await deleteUserCard(token, card.payment_method_id)
        setCards(cards.filter(c => (c.payment_method_id || c.id) !== (card.payment_method_id || card.id)))
        setNotice('Способ оплаты удалён')
      },
    })
  }

  function handleRefund(paymentId) {
    openModal({
      title: 'Запросить возврат',
      message: 'Опишите причину — заявку рассмотрит администратор в течение 5 рабочих дней.',
      input: true,
      inputLabel: 'Причина возврата',
      confirmLabel: 'Отправить заявку',
      onConfirm: async (reason) => {
        const res = await requestRefund(token, paymentId, reason || 'без причины')
        setPayments((await getPayments(token).catch(() => ({ items: [] })))?.items || [])
        setNotice(res.message || 'Заявка на возврат отправлена')
      },
    })
  }

  if (loading) return <CabinetSkeleton />

  // group payments by month for scannability
  const groups = payments.reduce((acc, p) => {
    const k = monthKey(p.created_at)
    ;(acc[k] = acc[k] || []).push(p)
    return acc
  }, {})

  return (
    <section className="relative py-10 md:py-16 overflow-hidden">
      <div className="pointer-events-none absolute top-0 right-1/4 w-96 h-96 bg-brand/6 rounded-full blur-[130px]" />
      <div className="relative max-w-3xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8 gap-3 flex-wrap">
          <div>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Оплата и подписка</h1>
            <p className="text-sm text-text-secondary mt-1">Продление, способы оплаты, история</p>
          </div>
          <Link to="/dashboard" className="btn-ghost !py-2 !px-4 !text-sm">← Кабинет</Link>
        </div>

        {notice && (
          <div role="status" aria-live="polite" className="mb-6 flex items-start gap-2 p-3 rounded-xl bg-brand/10 border border-brand/20 animate-fade-in">
            <svg className="w-5 h-5 text-brand flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
            <p className="text-sm text-text-primary flex-1">{notice}</p>
            <button onClick={() => setNotice('')} aria-label="Скрыть" className="text-faint hover:text-text-secondary"><svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" d="M6 6l12 12M18 6L6 18" /></svg></button>
          </div>
        )}

        {/* ─── Subscription + auto-renew (the two things people come here for) ─── */}
        {hasActiveSub ? (
          <div className="card-feat rounded-[22px] p-6 mb-5 reveal">
            <div className="flex items-start justify-between gap-4 flex-wrap mb-5">
              <div>
                <p className="text-xs text-faint uppercase tracking-wider mb-1">Текущая подписка</p>
                <p className="serif text-xl font-semibold">{currentPlan}</p>
                {isPaused
                  ? <p className="text-sm text-accent mt-1">На паузе до {formatDate(pausedUntil)}</p>
                  : validUntil && <p className="text-sm text-text-secondary mt-1">Действует до {formatDate(validUntil)}</p>}
              </div>
              <Link to="/payment" className="btn-primary !py-2.5 !px-5 !text-sm">Сменить тариф</Link>
            </div>

            {/* auto-renew: real state, and reversible */}
            {autoRenew !== null && (
              <div className="flex items-center justify-between gap-4 p-4 rounded-xl bg-white/[0.03] border border-brand/10 flex-wrap">
                <div className="min-w-0">
                  <p className="text-sm font-medium">Автопродление</p>
                  <p className="text-xs text-text-secondary mt-0.5">
                    {autoRenew
                      ? `Спишем стоимость следующего периода ${validUntil ? formatDate(validUntil).split(',')[0] : 'в день окончания'}`
                      : 'Подписка закончится и не продлится автоматически'}
                  </p>
                </div>
                <button
                  onClick={() => handleToggleAutoRenew(!autoRenew)}
                  disabled={autoRenewLoading}
                  role="switch"
                  aria-checked={autoRenew}
                  aria-label="Автопродление"
                  className={`relative w-12 h-7 rounded-full transition-colors duration-300 flex-shrink-0 disabled:opacity-50 ${autoRenew ? 'bg-brand' : 'bg-white/12'}`}
                >
                  <span className={`absolute top-1 left-1 w-5 h-5 rounded-full bg-white transition-transform duration-300 ${autoRenew ? 'translate-x-5' : ''}`} />
                </button>
              </div>
            )}
          </div>
        ) : (
          <div className="card rounded-[22px] p-6 mb-5 reveal">
            <Empty
              icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}><path strokeLinecap="round" strokeLinejoin="round" d="M2.25 8.25h19.5M2.25 9h19.5m-16.5 5.25h6m-3.75 6h15a2.25 2.25 0 002.25-2.25V6.75A2.25 2.25 0 0019.5 4.5h-15a2.25 2.25 0 00-2.25 2.25v10.5A2.25 2.25 0 004.5 20.25z" /></svg>}
              title="Нет активной подписки"
              hint="Выберите тариф — здесь появятся продление, способы оплаты и история."
              action={<Link to="/payment" className="btn-primary !py-2.5 !text-sm mt-4">Выбрать тариф</Link>}
            />
          </div>
        )}

        {/* ─── Pause ─── */}
        {hasActiveSub && (
          <div className="card rounded-[22px] p-6 mb-5 reveal">
            {isPaused ? (
              <>
                <div className="flex items-center gap-2 mb-2">
                  <span className="w-2 h-2 rounded-full bg-accent animate-pulse" />
                  <h2 className="serif text-base font-semibold">Подписка на паузе</h2>
                </div>
                <p className="text-sm text-text-secondary mb-4">Доступ закрыт до {formatDate(pausedUntil)}. Карта сохранена, автопродление продолжится после возобновления.</p>
                <button onClick={handleResume} className="btn-primary !py-2.5 !px-5 !text-sm">Возобновить сейчас</button>
              </>
            ) : (
              <>
                <h2 className="serif text-base font-semibold mb-1">Приостановить подписку</h2>
                <p className="text-sm text-text-secondary mb-4">Не нужен сервис какое-то время? Поставьте на паузу — карта сохранится, списаний не будет.</p>
                <div className="flex flex-wrap gap-2">
                  {[1, 2, 3].map(m => (
                    <button key={m} onClick={() => handlePause(m)} className="btn-ghost !py-2 !px-4 !text-sm">На {m} мес.</button>
                  ))}
                </div>
              </>
            )}
          </div>
        )}

        {/* ─── Cards ─── */}
        <div className="card rounded-[22px] p-6 mb-5 reveal">
          <h2 className="serif text-base font-semibold mb-4">Способы оплаты</h2>
          {cards.length === 0 ? (
            <Empty
              icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}><rect x="3" y="6" width="18" height="12" rx="2" /><path d="M3 10h18" /></svg>}
              title="Карты не сохранены"
              hint="Отметьте «Сохранить карту» при оплате — тогда подписка сможет продлеваться сама."
            />
          ) : (
            <div className="space-y-2.5">
              {cards.map(card => {
                const LABELS = { bank_card: 'Карта', sbp: 'СБП', yoo_money: 'ЮMoney', tinkoff_bank: 'T-Pay', sberbank: 'Сбер', alfabank: 'Alfa' }
                const typeLabel = LABELS[card.type] || card.type || 'Метод'
                const title = card.title || (card.last4 ? `•••• ${card.last4}` : typeLabel)
                return (
                  <div key={card.id || card.payment_method_id} className="flex items-center justify-between p-3 rounded-xl bg-white/[0.03] border border-brand/10">
                    <div className="flex items-center gap-3 min-w-0">
                      <div className="w-10 h-7 rounded bg-brand/12 border border-brand/20 flex items-center justify-center text-[10px] font-bold text-brand flex-shrink-0">{typeLabel.slice(0, 4)}</div>
                      <div className="min-w-0">
                        <p className="font-medium text-sm truncate">{title}</p>
                        <p className="text-xs text-text-secondary truncate">{card.card_type || card.issuer || typeLabel}</p>
                      </div>
                    </div>
                    <button onClick={() => handleDeleteCard(card, title)} className="text-xs text-danger hover:brightness-125 transition flex-shrink-0 ml-3">Удалить</button>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* ─── History, grouped by month ─── */}
        <div className="card rounded-[22px] p-6 mb-5 reveal">
          <h2 className="serif text-base font-semibold mb-4">История платежей</h2>
          {payments.length === 0 ? (
            <Empty
              icon={<svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.6}><path strokeLinecap="round" strokeLinejoin="round" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 104 0M9 5a2 2 0 014 0m-4 8h6m-6 4h4" /></svg>}
              title="Платежей пока нет"
              hint="Здесь появятся все оплаты и чеки после первой покупки."
            />
          ) : (
            <div className="space-y-6">
              {Object.entries(groups).map(([month, list]) => (
                <div key={month}>
                  <p className="text-[11px] uppercase tracking-wider text-faint mb-2.5">{month}</p>
                  <div className="space-y-2.5">
                    {list.map(p => (
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
                            <button onClick={e => { e.preventDefault(); e.stopPropagation(); handleRefund(p.id) }} className="text-xs text-accent hover:brightness-110 px-2 py-1 rounded-lg bg-accent/10 transition">Возврат</button>
                          )}
                          <svg className="w-5 h-5 text-faint group-hover:text-brand group-hover:translate-x-1 transition-all" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M8.25 4.5l7.5 7.5-7.5 7.5" /></svg>
                        </div>
                      </Link>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* ─── Danger zone ─── */}
        {hasActiveSub && (
          <div className="card rounded-[22px] p-6 reveal !border-danger/15">
            <h2 className="serif text-base font-semibold mb-1">Отменить подписку</h2>
            <p className="text-xs text-text-secondary mb-4">Выключит автопродление и удалит сохранённые карты. Доступ сохранится до {formatDate(validUntil)}.</p>
            <button onClick={handleCancelSubscription} disabled={autoRenewLoading} className="w-full sm:w-auto px-5 py-2.5 rounded-full bg-danger/8 hover:bg-danger/15 border border-danger/20 hover:border-danger/35 text-danger text-sm font-medium transition disabled:opacity-50">
              Отменить подписку
            </button>
          </div>
        )}
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
                  id="modal-input" value={modalInput} onChange={e => setModalInput(e.target.value)} rows={3}
                  className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/25 text-sm placeholder:text-faint resize-none"
                  placeholder="Например: передумал, ошибочная оплата…"
                />
              </div>
            )}
            <div className="flex gap-3">
              <button onClick={closeModal} disabled={modalBusy} className="btn-ghost flex-1 !py-2.5 !text-sm disabled:opacity-50">Отмена</button>
              <button
                onClick={confirmModal} disabled={modalBusy}
                className={`flex-1 inline-flex items-center justify-center !py-2.5 !text-sm rounded-full font-semibold transition-all active:scale-[0.97] disabled:opacity-50 ${modal.danger ? 'bg-danger text-white hover:brightness-110' : 'btn-primary'}`}
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
