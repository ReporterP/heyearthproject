import { useEffect, useState } from 'react'
import { useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { getPaymentDetail } from '../api/user.js'
import NarrowHeader, { Back } from '../components/NarrowHeader.jsx'
import Alert from '../components/Alert.jsx'
import Badge, { paymentBadge } from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import Flap from '../components/Flap.jsx'
import Timeline from '../components/Timeline.jsx'
import Skeleton from '../components/Skeleton.jsx'
import { useToast } from '../components/Toast.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { copyText } from '../lib/clipboard.js'
import { dateTime, money } from '../lib/format.js'

// Платёж /payments/:id (spec 09 §9.7, artboard «Платёж»): what happened, the details, the receipt.
// A payment still waiting is polled every 3 s (as on the result page).
const METHOD = { bank_card: 'Банковская карта', sbp: 'СБП', yoo_money: 'ЮMoney', tinkoff_bank: 'T-Pay', sberbank: 'Сбер', alfabank: 'Альфа-Банк' }
const isPaid = (s) => s === 'succeeded' || s === 'completed' || s === 'refunded'

function method(p) {
  const m = METHOD[p.payment_method_type] || p.payment_method_type || ''
  const last4 = p.payment_method_details?.card?.last4
  return m ? `${m}${last4 ? ` · •••• ${last4}` : ''}` : ''
}
const shortId = (s) => (String(s).length > 12 ? `${String(s).slice(0, 4)}…${String(s).slice(-4)}` : String(s))

export default function PaymentDetailPage() {
  const { id } = useParams()
  const { token } = useAuth()
  const toast = useToast()
  const narrow = useMedia('(max-width: 640px)')
  usePageMeta('Платёж — HiEarth', { noindex: true })
  const [p, setP] = useState(null)
  const [err, setErr] = useState(null) // 'missing' | 'failed'

  useEffect(() => {
    let stop = false
    let t
    const load = () => getPaymentDetail(token, id)
      .then((d) => {
        if (stop) return
        setP(d)
        setErr(null)
        if (d.status === 'pending' || d.status === 'waiting_for_capture') t = setTimeout(load, 3000)
      })
      .catch((e) => { if (!stop && e.kind !== 'auth') setErr(e.status === 404 || e.status === 400 ? 'missing' : 'failed') })
    load()
    return () => { stop = true; clearTimeout(t) }
  }, [token, id])

  async function copyYk() {
    if (await copyText(p.yookassa_payment_id)) toast.ok('Скопировано')
  }

  let body
  if (err === 'missing') {
    body = (
      <div className="stack gap-4">
        <Alert tone="bad" title="Платёж не найден">или принадлежит другому пользователю.</Alert>
        <div><Back to="/payments" label="История платежей" /></div>
      </div>
    )
  } else if (err) {
    body = <Alert tone="bad" title="Не удалось загрузить платёж.">Обновите страницу.</Alert>
  } else if (!p) {
    body = <div className="stack gap-4" aria-busy="true"><Skeleton w={220} h={14} /><Skeleton w={160} h={44} /></div>
  } else {
    const paidNow = isPaid(p.status)
    const failed = p.status === 'canceled' || p.status === 'cancelled' || p.status === 'failed'
    const waiting = p.status === 'pending' || p.status === 'waiting_for_capture'
    const refunded = Number(p.refunded_amount) || 0
    const badge = paymentBadge(p.status)
    const steps = [
      { title: 'Платёж создан', sub: dateTime(p.created_at), state: 'done' },
      failed
        ? { title: 'Платёж не прошёл', sub: p.cancellation_reason ? 'отменён банком или ЮKassa' : 'не оплачен', state: 'done' }
        : { title: paidNow ? 'Оплачен' : 'Ждём подтверждения от банка', sub: paidNow ? [dateTime(p.paid_at), method(p).toLowerCase()].filter(Boolean).join(' · ') : 'проверяем автоматически', state: paidNow ? 'done' : 'now' },
      ...(failed ? [] : [{ title: 'Чек зарегистрирован в ОФД', sub: p.receipt_registered ? (p.payer_email ? `копия на ${p.payer_email}` : undefined) : paidNow ? 'обычно до нескольких минут' : undefined, state: p.receipt_registered ? 'done' : paidNow ? 'now' : undefined }]),
      ...(refunded > 0 ? [{ title: `Возвращено ${money(refunded)}`, state: 'done' }] : []),
    ]
    const receipt = p.receipt_registered
      ? { tone: 'ok', badge: 'Передан в ОФД' }
      : paidNow ? { tone: 'warn', badge: 'Обрабатывается' } : { tone: 'plain', badge: 'После оплаты' }

    body = (
      <>
        <div className="row wrap between gap-4" style={{ alignItems: 'flex-end' }}>
          <div>
            <h1 className="t-sm subtle" style={{ fontWeight: 400 }}>Платёж · {/пополнен/i.test(p.description || 'пополнение') ? 'пополнение кошелька' : p.description}</h1>
            <div style={{ marginTop: 8 }}><Flap value={Math.round(Number(p.amount) || 0)} suffix="₽" size={narrow ? 36 : 48} label={money(p.amount)} /></div>
          </div>
          <div className="row gap-2">
            {p.test && <Badge tone="plain">Тестовый платёж</Badge>}
            <Badge tone={badge.tone} className="swap" style={{ height: 30, fontSize: 13, padding: '0 12px' }}>{badge.text}</Badge>
          </div>
        </div>
        {waiting && p.confirmation_url && (
          <div style={{ marginTop: 16 }}><Button variant="secondary" href={p.confirmation_url}>Продолжить оплату</Button></div>
        )}
        <div className="grid" style={{ gridTemplateColumns: narrow ? '1fr' : '1fr 1fr', gap: 16, marginTop: 32, alignItems: 'start' }}>
          <section className="card">
            <h2 className="card__title">Что происходило</h2>
            <div style={{ marginTop: 18 }}><Timeline items={steps} /></div>
          </section>
          <section className="card">
            <h2 className="card__title">Детали</h2>
            <dl className="kv" style={{ gridTemplateColumns: narrow ? '1fr' : '140px 1fr', marginTop: 18, fontSize: 14 }}>
              <dt>Создан</dt><dd>{dateTime(p.created_at)}</dd>
              {method(p) && <><dt>Способ оплаты</dt><dd>{method(p)}</dd></>}
              {p.payer_email && <><dt>Email для чека</dt><dd className="mono" style={{ wordBreak: 'break-all' }}>{p.payer_email}</dd></>}
              {p.yookassa_payment_id && (
                <><dt>ID в ЮKassa</dt><dd className="row gap-2"><span className="mono t-sm" title={p.yookassa_payment_id}>{shortId(p.yookassa_payment_id)}</span><Button variant="ghost" size="sm" onClick={copyYk} style={{ height: 26 }}>Копировать</Button></dd></>
              )}
              {refunded > 0 && <><dt>Возвращено</dt><dd>{money(refunded)}</dd></>}
            </dl>
          </section>
          <section className="card" style={narrow ? undefined : { gridColumn: 'span 2' }}>
            <div className="card__head"><h2 className="card__title">Чек (54-ФЗ)</h2><Badge tone={receipt.tone}>{receipt.badge}</Badge></div>
            {p.receipt_registered ? (
              <p className="muted t-sm">Чек передан в ОФД от ЮKassa.{p.payer_email ? <> Копия отправлена на <span className="mono">{p.payer_email}</span>.</> : null}</p>
            ) : paidNow ? (
              <Alert tone="warn" title="Чек обрабатывается.">Обычно до нескольких минут.</Alert>
            ) : (
              <Alert tone="info">Чек будет выдан после успешной оплаты.</Alert>
            )}
          </section>
        </div>
      </>
    )
  }

  return (
    <>
      <NarrowHeader back={{ to: '/payments', label: 'История платежей' }} />
      <main id="main" className="container" style={{ maxWidth: 960, paddingTop: 48, paddingBottom: 80 }}>
        {body}
      </main>
    </>
  )
}
