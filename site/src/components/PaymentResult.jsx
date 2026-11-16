import { useEffect, useState } from 'react'
import { getTopupStatus } from '../api/wallet.js'
import { getPaymentDetail } from '../api/user.js'
import { invalidateAccount } from '../account/useAccount.js'
import MiniPage from './MiniPage.jsx'
import Badge from './Badge.jsx'
import Button from './Button.jsx'
import ConnectOrb from './ConnectOrb.jsx'
import Flap from './Flap.jsx'
import Hedgehog from './Hedgehog.jsx'
import Timeline from './Timeline.jsx'
import { date, kop, time } from '../lib/format.js'
import usePageMeta from '../hooks/usePageMeta.js'

// Результат оплаты (spec 09 §9.4, artboard «Результат оплаты»): after ЮKassa (return to
// /wallet?topup=<id>) or the embedded widget's «success». Polls GET /api/v1/wallet/topup/:id every
// 3 s up to 2 minutes; then «pending» stays with the text that the page can be closed.
const POLL_MS = 3000
const POLL_MAX = 40

export default function PaymentResult({ token, paymentId }) {
  const [st, setSt] = useState(null)
  const [created, setCreated] = useState('')
  const [timedOut, setTimedOut] = useState(false)
  const [err, setErr] = useState(false)
  usePageMeta('Результат оплаты — HiEarth', { noindex: true })

  useEffect(() => {
    let stop = false
    let tries = 0
    let t
    async function tick() {
      tries += 1
      try {
        const s = await getTopupStatus(token, paymentId)
        if (stop) return
        setSt(s)
        setErr(false)
        const purchasePending = s.purchase && s.purchase.status === 'pending'
        if (s.status === 'canceled' || (s.paid && !purchasePending)) { invalidateAccount(); return }
      } catch (e) {
        if (stop || e.kind === 'auth') return
        if (e.status === 404) { setErr('missing'); return }
        setErr(true)
      }
      if (tries >= POLL_MAX) { setTimedOut(true); return }
      t = setTimeout(tick, POLL_MS)
    }
    tick()
    getPaymentDetail(token, paymentId).then((d) => { if (!stop) setCreated(d.created_at || '') }).catch(() => {})
    return () => { stop = true; clearTimeout(t) }
  }, [token, paymentId])

  const forPlan = !!st?.purchase
  const again = forPlan ? '/payment' : '/wallet'
  const amountRub = st ? Math.round(parseFloat(st.amount_rub || '0')) : 0

  if (err === 'missing') {
    return (
      <MiniPage kind="res" label="Платёж не найден">
        <Hedgehog mood="worried" size={150} />
        <h1 className="t-h2">Платёж не найден</h1>
        <p className="muted t-sm">Платёж не найден или принадлежит другому пользователю.</p>
        <Button variant="secondary" block to="/payments">История платежей</Button>
      </MiniPage>
    )
  }

  // pending (also before the first answer)
  if (!st || (!st.paid && st.status !== 'canceled') || (st.paid && st.purchase?.status === 'pending')) {
    const paidWaitingPlan = st?.paid && st.purchase?.status === 'pending'
    return (
      <MiniPage kind="res" label="Результат оплаты">
        <ConnectOrb active={false} small ariaLabel="Проверяем оплату" />
        <Badge tone="warn" className="swap">Ожидание</Badge>
        <h1 className="t-h2 swap">{paidWaitingPlan ? 'Оплата получена, оформляем подписку…' : 'Ждём подтверждения оплаты…'}</h1>
        <p className="muted t-sm" role="status">
          {err ? 'Не удаётся проверить платёж — продолжаем пробовать. ' : ''}
          {timedOut
            ? 'Платёж ещё не подтверждён. Если вы оплатили — деньги появятся на кошельке, как только ЮKassa подтвердит оплату, страницу можно закрыть.'
            : 'Обычно это несколько секунд. Страницу можно закрыть — деньги появятся на кошельке, как только ЮKassa подтвердит оплату.'}
        </p>
        <div style={{ alignSelf: 'stretch', textAlign: 'left', marginTop: 10 }}>
          <Timeline items={[
            { title: 'Платёж создан', sub: created ? time(created) : undefined, state: 'done' },
            paidWaitingPlan
              ? { title: 'Оплата получена', sub: 'деньги на кошельке', state: 'done' }
              : { title: 'Ждём подтверждения от банка', sub: 'проверяем автоматически', state: 'now' },
            { title: forPlan ? 'Подписка оформлена' : 'Деньги на кошельке', state: paidWaitingPlan ? 'now' : undefined },
          ]} />
        </div>
        <Button variant="ghost" size="sm" to={again}>Начать оплату заново</Button>
      </MiniPage>
    )
  }

  if (st.status === 'canceled') {
    return (
      <MiniPage kind="res" label="Результат оплаты">
        <Hedgehog mood="worried" size={150} />
        <Badge tone="bad">Не прошла</Badge>
        <h1 className="t-h2">Оплата не прошла</h1>
        <p className="muted t-sm">Деньги не списаны. Попробуйте ещё раз — можно другой картой или через СБП.</p>
        <Button variant="primary" block to={again}>Попробовать снова</Button>
        <Button variant="ghost" size="sm" to="/dashboard">В личный кабинет</Button>
      </MiniPage>
    )
  }

  // paid: the purchase went through, failed, or there was none
  const p = st.purchase
  if (p && p.status === 'failed') {
    return (
      <MiniPage kind="res" label="Результат оплаты">
        <Hedgehog mood="idle" size={150} />
        <Badge tone="ok">Оплачено</Badge>
        <h1 className="t-h2">Кошелёк пополнен на {amountRub}&nbsp;₽</h1>
        <p className="muted t-sm">Подписку оплатите кнопкой ниже — деньги уже на кошельке.{p.reason ? ` Причина: ${p.reason}` : ''}</p>
        <Button variant="primary" block to="/payment">Оплатить подписку с кошелька</Button>
        <Button variant="ghost" size="sm" to="/wallet">Кошелёк</Button>
      </MiniPage>
    )
  }
  return (
    <MiniPage kind="res" label="Результат оплаты">
      <Hedgehog mood="happy" size={150} className="pop" />
      <Badge tone="ok">Оплачено</Badge>
      <h1 className="t-h2">{p ? 'Подписка активирована' : 'Кошелёк пополнен'}</h1>
      <p className="muted t-sm">
        {p
          ? `Кошелёк пополнен на ${amountRub} ₽, с него оплачен «${p.plan_name}»${p.valid_until ? ` — действует до ${date(p.valid_until)}` : ''}.`
          : `На кошельке ${kop(st.balance_kop)}.`}
      </p>
      <Flap value={amountRub} suffix="₽" size={40} tone="accent" delayMs={200} label={`${amountRub} рублей`} />
      <Button variant="primary" block to="/dashboard">В личный кабинет</Button>
      <Button variant="ghost" size="sm" to={`/payments/${paymentId}`}>Открыть чек</Button>
    </MiniPage>
  )
}

