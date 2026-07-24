import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getPaymentDetail } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'

function formatDate(s) {
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString('ru-RU', {
      day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit',
    })
  } catch { return s }
}

function statusBadge(status) {
  const map = {
    pending:      { text: 'Ожидает оплаты', cls: 'bg-accent/12 text-accent border-accent/30' },
    succeeded:    { text: 'Оплачено',        cls: 'bg-brand/12 text-brand border-brand/30' },
    completed:    { text: 'Оплачено',        cls: 'bg-brand/12 text-brand border-brand/30' },
    waiting_for_capture: { text: 'Ожидает подтверждения', cls: 'bg-brand/10 text-brand border-brand/25' },
    canceled:     { text: 'Отменён',         cls: 'bg-white/5 text-faint border-brand/10' },
    cancelled:    { text: 'Отменён',         cls: 'bg-white/5 text-faint border-brand/10' },
    failed:       { text: 'Ошибка',          cls: 'bg-danger/12 text-danger border-danger/30' },
    refunded:     { text: 'Возврат',         cls: 'bg-accent/12 text-accent border-accent/30' },
  }
  const s = map[status] || { text: status || '—', cls: 'bg-white/5 text-text-secondary border-brand/10' }
  return <span className={`inline-flex px-2.5 py-1 rounded-lg text-xs font-medium border ${s.cls}`}>{s.text}</span>
}

const METHOD_LABELS = {
  bank_card: 'Банковская карта',
  sbp: 'СБП',
  yoo_money: 'ЮMoney',
  tinkoff_bank: 'T-Pay',
  sberbank: 'Сбер',
  alfabank: 'Альфа-Банк',
}

export default function PaymentDetailPage() {
  const { id } = useParams()
  const { token } = useAuth()
  const [payment, setPayment] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!token) return
    getPaymentDetail(token, id)
      .then(setPayment)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false))
  }, [id, token])

  if (loading) {
    return (
      <section className="py-16 flex items-center justify-center">
        <div className="w-10 h-10 rounded-full border-2 border-brand/25 border-t-brand animate-spin" />
      </section>
    )
  }

  if (error || !payment) {
    return (
      <section className="max-w-xl mx-auto px-4 py-16">
        <div className="card rounded-[22px] p-6 !border-danger/20">
          <h1 className="serif text-lg font-semibold text-danger mb-2">Платёж не найден</h1>
          <p className="text-sm text-text-secondary mb-4">{error || 'Платёж с таким ID не найден или принадлежит другому пользователю.'}</p>
          <Link to="/payments" className="text-sm text-brand hover:text-brand-light">← К истории платежей</Link>
        </div>
      </section>
    )
  }

  const details = payment.payment_method_details || {}
  const methodLabel = METHOD_LABELS[payment.payment_method_type] || payment.payment_method_type || null
  const refunded = parseFloat(payment.refunded_amount || 0)
  const hasRefund = refunded > 0

  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-2xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8">
          <div>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Платёж #{payment.id}</h1>
            <p className="text-sm text-text-secondary mt-1">{payment.description || 'Платёж'}</p>
          </div>
          <Link to="/payments" className="text-sm text-text-secondary hover:text-brand transition">← История</Link>
        </div>

        {/* ─── Summary ─── */}
        <div className="card rounded-[22px] p-6 mb-4">
          <div className="flex items-start justify-between mb-5">
            <div>
              <p className="text-xs text-faint uppercase tracking-wider mb-1">Сумма</p>
              <p className="serif text-3xl font-semibold tabular-nums">
                {parseFloat(payment.amount).toLocaleString('ru-RU', { minimumFractionDigits: 2 })} {payment.currency}
              </p>
              {hasRefund && (
                <p className="text-sm text-accent mt-1">Возвращено: {refunded.toLocaleString('ru-RU', { minimumFractionDigits: 2 })} {payment.currency}</p>
              )}
            </div>
            <div className="flex flex-col items-end gap-2">
              {statusBadge(payment.status)}
              {payment.test && <span className="inline-flex px-2 py-0.5 rounded bg-accent/10 text-accent text-[10px] font-bold">TEST</span>}
            </div>
          </div>

          {payment.cancellation_reason && (
            <div className="mb-4 px-3 py-2 rounded-lg bg-danger/5 border border-danger/20">
              <p className="text-xs text-text-secondary">Причина отмены</p>
              <p className="text-sm text-danger">{payment.cancellation_reason}</p>
            </div>
          )}

          {payment.status === 'pending' && payment.confirmation_url && (
            <a href={payment.confirmation_url} className="btn-primary w-full !py-3">
              Продолжить оплату
              <svg className="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5L21 12m0 0l-7.5 7.5M21 12H3" /></svg>
            </a>
          )}
        </div>

        {/* ─── Details ─── */}
        <div className="card rounded-[22px] p-6 mb-4">
          <h2 className="serif text-base font-semibold mb-4">Детали</h2>
          <dl className="space-y-3 text-sm">
            <div className="flex justify-between items-start gap-3">
              <dt className="text-text-secondary">Создан</dt>
              <dd className="text-right">{formatDate(payment.created_at)}</dd>
            </div>
            {payment.paid_at && (
              <div className="flex justify-between items-start gap-3">
                <dt className="text-text-secondary">Оплачен</dt>
                <dd className="text-right text-brand">{formatDate(payment.paid_at)}</dd>
              </div>
            )}
            {methodLabel && (
              <div className="flex justify-between items-start gap-3">
                <dt className="text-text-secondary">Способ оплаты</dt>
                <dd className="text-right">
                  {methodLabel}
                  {details.last4 && <span className="text-text-secondary"> •••• {details.last4}</span>}
                  {details.card_type && <span className="text-text-secondary"> ({details.card_type})</span>}
                </dd>
              </div>
            )}
            {payment.payer_email && (
              <div className="flex justify-between items-start gap-3">
                <dt className="text-text-secondary">Email для чека</dt>
                <dd className="text-right font-mono text-xs">{payment.payer_email}</dd>
              </div>
            )}
            {payment.yookassa_payment_id && (
              <div className="flex justify-between items-start gap-3">
                <dt className="text-text-secondary">ID в ЮKassa</dt>
                <dd className="text-right font-mono text-xs break-all">{payment.yookassa_payment_id}</dd>
              </div>
            )}
          </dl>
        </div>

        {/* ─── Чек 54-ФЗ ─── */}
        <div className="card rounded-[22px] p-6">
          <h2 className="serif text-base font-semibold mb-3">Чек (54-ФЗ)</h2>
          {payment.receipt_registered ? (
            <div className="flex items-start gap-3 p-3 rounded-lg bg-brand/10 border border-brand/20">
              <svg className="w-5 h-5 text-brand flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M4.5 12.75l6 6 9-13.5" /></svg>
              <div className="flex-1">
                <p className="text-sm text-brand font-medium">Чек зарегистрирован в ОФД</p>
                <p className="text-xs text-text-secondary mt-1">
                  {payment.payer_email
                    ? <>Копия отправлена на <strong className="text-text-primary">{payment.payer_email}</strong>.</>
                    : <>Чек передан в ОФД от ЮKassa.</>}
                  {' '}Если письмо не пришло, проверьте папку «Спам».
                </p>
              </div>
            </div>
          ) : payment.status === 'succeeded' || payment.status === 'completed' ? (
            <div className="flex items-start gap-3 p-3 rounded-lg bg-accent/10 border border-accent/20">
              <svg className="w-5 h-5 text-accent flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" /></svg>
              <div>
                <p className="text-sm text-accent font-medium">Чек обрабатывается</p>
                <p className="text-xs text-text-secondary mt-1">Чек ещё регистрируется в ОФД. Обычно занимает до нескольких минут.</p>
              </div>
            </div>
          ) : (
            <p className="text-sm text-text-secondary">Чек будет выдан после успешной оплаты.</p>
          )}
        </div>
      </div>
    </section>
  )
}
