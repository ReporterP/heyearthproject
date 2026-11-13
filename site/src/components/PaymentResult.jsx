import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getTopupStatus, rub, formatDay } from '../api/wallet.js'

// What happened to a top-up payment (and the purchase it was made for). Polls the API
// instead of «обновите страницу позже»: ЮKassa reports the payment to us by webhook a few
// seconds after the client pays.
export default function PaymentResult({ token, paymentId, onRetry, resumeHint = false }) {
  const [st, setSt] = useState(null)
  const [timedOut, setTimedOut] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let stop = false
    let tries = 0
    async function tick() {
      tries += 1
      try {
        const s = await getTopupStatus(token, paymentId)
        if (stop) return
        setSt(s)
        setError('')
        const purchasePending = s.purchase && s.purchase.status === 'pending'
        const settled = s.status === 'canceled' || (s.paid && !purchasePending)
        if (settled) return
      } catch (e) {
        if (stop) return
        if (e.kind === 'auth') return
        setError(e.message)
      }
      if (tries >= 45) { setTimedOut(true); return }
      setTimeout(tick, 2000)
    }
    tick()
    return () => { stop = true }
  }, [token, paymentId])

  const card = (tone, title, body, actions) => (
    <div className={`card p-7 text-center ${tone === 'ok' ? '!border-brand/30' : tone === 'bad' ? '!border-danger/30' : ''}`} role="status">
      <div className={`w-14 h-14 mx-auto mb-4 rounded-full flex items-center justify-center ${tone === 'ok' ? 'bg-brand/15 text-brand' : tone === 'bad' ? 'bg-danger/15 text-danger' : 'bg-accent/12 text-accent'}`}>
        {tone === 'ok'
          ? <svg className="w-8 h-8" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M5 13l4 4L19 7" /></svg>
          : tone === 'bad'
            ? <svg className="w-7 h-7" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" d="M6 6l12 12M18 6L6 18" /></svg>
            : <span className="w-6 h-6 rounded-full border-2 border-accent/30 border-t-accent animate-spin" />}
      </div>
      <h2 className="serif text-xl font-semibold mb-2">{title}</h2>
      <p className="text-sm text-text-secondary mb-6 max-w-sm mx-auto">{body}</p>
      <div className="flex flex-wrap gap-2.5 justify-center">{actions}</div>
    </div>
  )

  if (!st) {
    return error
      ? card('wait', 'Проверяем платёж…', error, <button className="btn-ghost !py-2.5 !text-sm" onClick={() => window.location.reload()}>Повторить</button>)
      : card('wait', 'Проверяем платёж…', 'Это займёт несколько секунд.', null)
  }

  const toCabinet = <Link key="cab" to="/dashboard" className="btn-primary !py-2.5 !text-sm">В личный кабинет</Link>
  const toWallet = <Link key="wal" to="/wallet" className="btn-ghost !py-2.5 !text-sm">Кошелёк</Link>

  if (st.status === 'canceled') {
    return card('bad', 'Оплата не прошла', 'Деньги не списаны. Попробуйте ещё раз — можно другой картой или через СБП.',
      [onRetry
        ? <button key="r" className="btn-primary !py-2.5 !text-sm" onClick={onRetry}>Попробовать снова</button>
        : <Link key="r" to="/payment" className="btn-primary !py-2.5 !text-sm">Попробовать снова</Link>, toWallet])
  }
  if (!st.paid) {
    const again = <Link key="again" to="/payment" className="btn-ghost !py-2.5 !text-sm">Начать оплату заново</Link>
    return timedOut
      ? card('wait', 'Платёж ещё не подтверждён', 'Если вы оплатили — деньги появятся на кошельке, как только ЮKassa подтвердит оплату, страницу можно закрыть. Если не оплатили — начните заново.', [toCabinet, again])
      : card('wait', 'Ждём подтверждения оплаты…', 'Обычно это несколько секунд.', resumeHint ? [again] : null)
  }
  const p = st.purchase
  if (p && p.status === 'done') {
    return card('ok', 'Подписка активирована',
      `«${p.plan_name}» действует до ${formatDay(p.valid_until)}. На кошельке осталось ${rub(st.balance_kop)}.`, [toCabinet])
  }
  if (p && p.status === 'failed') {
    return card('wait', `Кошелёк пополнен на ${st.amount_rub} ₽`,
      `Подписку оплатите кнопкой ниже — деньги уже на кошельке. ${p.reason ? `Причина: ${p.reason}` : ''}`,
      [<Link key="pay" to="/payment" className="btn-primary !py-2.5 !text-sm">Оплатить подписку с кошелька</Link>, toWallet])
  }
  if (p && p.status === 'pending') {
    return timedOut
      ? card('wait', 'Оплата получена', 'Подписку оформляем — обновите кабинет через минуту.', [toCabinet])
      : card('wait', 'Оплата получена, оформляем подписку…', 'Ещё пара секунд.', null)
  }
  return card('ok', 'Кошелёк пополнен', `На кошельке ${rub(st.balance_kop)}.`, [toCabinet, toWallet])
}
