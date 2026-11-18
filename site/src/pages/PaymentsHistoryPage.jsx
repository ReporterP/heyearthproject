import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount, { invalidateAccount } from '../account/useAccount.js'
import { getPayments, toggleAutoRenew, requestRefund } from '../api/user.js'
import AppShell, { PageHead } from '../components/AppShell.jsx'
import Alert from '../components/Alert.jsx'
import { paymentBadge } from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import Dialog, { DialogActions } from '../components/Dialog.jsx'
import EmptyState from '../components/EmptyState.jsx'
import Field, { Input } from '../components/Field.jsx'
import ListRow, { Group } from '../components/ListRow.jsx'
import { Switch, Select } from '../components/Controls.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import { useToast } from '../components/Toast.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { currentSub } from '../lib/account.js'
import { date, dateTime, daysLeft, days as dayWord, kop, money, monthName, signed } from '../lib/format.js'

// Оплата и подписка (spec 09 §9.6, artboard «Оплата и подписка»): the current subscription with
// auto-renewal, the payments grouped by month, «Запросить возврат». The «Все / Пополнения / Подписки»
// filter is not built: every ЮKassa payment is a wallet top-up (plans are paid from the wallet).
const paid = (s) => s === 'succeeded' || s === 'completed'

function paymentTitle(p) {
  const n = String(p.notes || p.description || '')
  return /пополнен/i.test(n) || !n ? 'Пополнение кошелька' : n.replace(/^HiEarth\s*—\s*/i, '')
}

function groupKey(iso) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return 'Ранее'
  const months = (new Date().getFullYear() - d.getFullYear()) * 12 + new Date().getMonth() - d.getMonth()
  return months <= 2 ? monthName(d) : 'Ранее'
}

export default function PaymentsHistoryPage() {
  const { token } = useAuth()
  const toast = useToast()
  const narrow = useMedia('(max-width: 640px)')
  usePageMeta('Оплата и подписка — HiEarth', { noindex: true })
  const acc = useAccount(['subscription', 'wallet'], { optional: ['wallet'] })
  const sub = currentSub(acc.data.subscription)
  const active = sub?.active ? sub : null
  const wallet = acc.data.wallet
  const ar = wallet?.auto_renew

  const [payments, setPayments] = useState(null)
  const [payErr, setPayErr] = useState(false)
  const loadPayments = () => {
    setPayErr(false)
    getPayments(token).then((d) => setPayments(d?.items || [])).catch((e) => { if (e.kind !== 'auth') setPayErr(true) })
  }
  useEffect(() => { if (token) loadPayments() }, [token]) // eslint-disable-line react-hooks/exhaustive-deps
  const showSkel = useDelayed((acc.loading && !acc.data.subscription) || (!payments && !payErr), 300)

  // auto-renewal switch: optimistic, disabled while the request runs, rolled back on error
  const [renew, setRenew] = useState(null)
  const [renewBusy, setRenewBusy] = useState(false)
  useEffect(() => { if (active) setRenew(ar ? ar.enabled : !!active.auto_renew) }, [active?.id, ar?.enabled]) // eslint-disable-line react-hooks/exhaustive-deps
  async function flip(next) {
    const prev = renew
    setRenew(next)
    setRenewBusy(true)
    try {
      await toggleAutoRenew(token, next)
      invalidateAccount('subscription', 'wallet')
      toast.ok(next ? 'Автопродление включено' : 'Автопродление выключено — подписка не продлится автоматически')
    } catch {
      setRenew(prev)
      toast.bad('Не удалось изменить автопродление')
    } finally {
      setRenewBusy(false)
    }
  }

  // refund request
  const refundable = useMemo(() => (payments || []).filter((p) => paid(p.status)), [payments])
  const [refundOpen, setRefundOpen] = useState(false)
  const [refundId, setRefundId] = useState('')
  const [reason, setReason] = useState('')
  const [refundBusy, setRefundBusy] = useState(false)
  const [refundErr, setRefundErr] = useState('')
  const inFlight = useRef(false)
  function openRefund() { setRefundId(String(refundable[0]?.id || '')); setReason(''); setRefundErr(''); setRefundOpen(true) }
  async function sendRefund(e) {
    e.preventDefault()
    if (inFlight.current || !refundId) return
    inFlight.current = true
    setRefundBusy(true)
    setRefundErr('')
    try {
      await requestRefund(token, Number(refundId), reason.trim() || 'без причины')
      setRefundOpen(false)
      toast.ok('Заявка на возврат отправлена')
    } catch (e2) {
      setRefundErr(e2?.status === 400 || e2?.status === 404 ? e2.message : 'Не удалось отправить заявку — попробуйте ещё раз.')
    } finally {
      inFlight.current = false
      setRefundBusy(false)
    }
  }
  const chosen = refundable.find((p) => String(p.id) === refundId)

  // subscription card
  let subCard = null
  if (acc.error && acc.error.kind !== 'auth' && !acc.data.subscription) {
    subCard = <Alert tone="bad" title="Не удалось загрузить подписку.">Обновите страницу. <button type="button" className="link" onClick={() => acc.reload()} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Повторить</button></Alert>
  } else if (!acc.data.subscription && acc.loading) {
    subCard = showSkel ? <SkeletonCard lines={3} big /> : <div style={{ minHeight: 180 }} />
  } else if (!active) {
    subCard = (
      <section className="card">
        <EmptyState mood="search" title="Нет активной подписки" actions={<Button variant="primary" to="/payment">Выбрать тариф</Button>}>
          Выберите тариф — здесь появятся продление и история платежей.
        </EmptyState>
      </section>
    )
  } else {
    const left = daysLeft(active.valid_until)
    const price = ar ? kop(ar.price_kop) : money(active.price_rub)
    subCard = (
      <section className="card rise" style={{ padding: narrow ? 22 : 28 }}>
        <div className="row wrap between gap-4">
          <div>
            <span className="card__title">Текущая подписка</span>
            <h2 className="t-h2" style={{ marginTop: 6 }}>{active.plan_name} · до {date(active.valid_until)}</h2>
            <p className={`t-sm ${left <= 3 ? 'warn' : 'muted'}`} style={{ marginTop: 4 }}>Осталось {dayWord(left)}</p>
          </div>
          <div className="row wrap gap-2">
            <Button variant="secondary" to="/payment">Сменить тариф</Button>
            <Button variant="primary" to={`/payment?plan=${encodeURIComponent(active.plan || '')}`}>Продлить</Button>
          </div>
        </div>
        <div className="row between gap-4" style={{ marginTop: 22, paddingTop: 20, borderTop: '1px solid var(--line)' }}>
          {ar?.free ? (
            <span><b>Автопродление</b><br /><span className="t-sm subtle">Подписка по промокоду продлевается бесплатно.</span></span>
          ) : (
            <>
              <label htmlFor="renew" style={{ cursor: 'pointer' }}>
                <b>Автопродление</b><br />
                <span className="t-sm subtle">
                  {renew
                    ? <>За сутки до окончания спишем {price} с кошелька.{ar && !ar.enough ? <> На кошельке {kop(wallet.balance_kop)} — <Link to="/wallet" className="link">пополнить</Link></> : null}</>
                    : 'Подписка закончится и не продлится автоматически.'}
                </span>
              </label>
              <Switch id="renew" checked={!!renew} disabled={renewBusy || renew === null} onChange={flip} />
            </>
          )}
        </div>
      </section>
    )
  }

  // payments history
  let history
  if (payErr) {
    history = <Alert tone="bad" title="Не удалось загрузить платежи.">Обновите страницу. <button type="button" className="link" onClick={loadPayments} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Повторить</button></Alert>
  } else if (!payments) {
    history = <div aria-busy="true" style={{ minHeight: 160 }}>{showSkel && [0, 1, 2].map((i) => <div key={i} className="skel" style={{ height: 52, marginTop: i ? 10 : 0 }} />)}</div>
  } else if (!payments.length) {
    history = <EmptyState title="Платежей пока нет">Здесь появятся оплаты картой и чеки к ним. Покупки с кошелька — в разделе <Link to="/wallet" className="link">«Кошелёк»</Link>.</EmptyState>
  } else {
    const groups = []
    for (const p of payments) {
      const k = groupKey(p.created_at)
      const g = groups.find((x) => x.k === k)
      if (g) g.items.push(p)
      else groups.push({ k, items: [p] })
    }
    history = (
      <div className="swap">
        {groups.map((g) => (
          <div key={g.k}>
            <Group>{g.k}</Group>
            <div className="rows">
              {g.items.map((p) => {
                const b = p.status === 'refunded' ? { tone: 'info', text: 'Возврат' } : paymentBadge(p.status)
                const amount = Number(p.amount) || 0
                return (
                  <ListRow
                    key={p.id}
                    icon={p.status === 'refunded' ? 'refund' : 'topup'}
                    title={paymentTitle(p)}
                    sub={[dateTime(p.created_at), paid(p.status) ? 'оплачено' : null].filter(Boolean).join(' · ')}
                    amount={paid(p.status) ? signed(amount) : money(amount)}
                    amountTone={paid(p.status) ? 'ok' : 'subtle'}
                    badge={b}
                    to={`/payments/${p.id}`}
                  />
                )
              })}
            </div>
          </div>
        ))}
        {refundable.length > 0 && (
          <div className="row wrap between gap-2" style={{ marginTop: 16, paddingTop: 16, borderTop: '1px solid var(--line)' }}>
            <span className="t-sm subtle">Нашли ошибку в списании?</span>
            <Button variant="ghost" size="sm" icon="refund" onClick={openRefund}>Запросить возврат</Button>
          </div>
        )}
      </div>
    )
  }

  return (
    <AppShell>
      <div style={{ maxWidth: 1080 }}>
        <PageHead title="Оплата и подписка" lead="Продление, автопродление, история и чеки." />
        {subCard}
        <section className="card" style={{ marginTop: 16 }}>
          <div className="card__head"><h2 className="card__title">История платежей</h2></div>
          {history}
        </section>
      </div>

      <Dialog open={refundOpen} onClose={() => setRefundOpen(false)} title="Запросить возврат" busy={refundBusy} closeOnOverlay={!reason}>
        <form onSubmit={sendRefund} noValidate>
          <p className="muted t-sm" style={{ marginTop: 8 }}>Опишите причину — заявку рассмотрит администратор в течение 5 рабочих дней.</p>
          {refundable.length > 1 && (
            <div className="field" style={{ marginTop: 16 }}>
              <span className="field__label">Платёж</span>
              <Select label="Платёж" value={refundId} onChange={setRefundId} options={refundable.map((p) => ({ value: String(p.id), label: `${dateTime(p.created_at)} · ${money(p.amount)}` }))} />
            </div>
          )}
          {chosen && (
            <div className="rows" style={{ marginTop: 12 }}>
              <ListRow icon="topup" title={paymentTitle(chosen)} sub={dateTime(chosen.created_at)} amount={money(chosen.amount)} badge={paymentBadge(chosen.status)} />
            </div>
          )}
          <Field id="reason" label="Причина возврата" optional className="mt-4">
            <Input as="textarea" value={reason} placeholder="Например: передумал, ошибочная оплата…" onChange={(e) => setReason(e.target.value)} />
          </Field>
          {refundErr && <Alert tone="bad" rise className="mt-4">{refundErr}</Alert>}
          <DialogActions>
            <Button variant="ghost" onClick={() => setRefundOpen(false)} disabled={refundBusy}>Отмена</Button>
            <Button variant="primary" type="submit" loading={refundBusy}>{refundBusy ? 'Подождите…' : 'Отправить заявку'}</Button>
          </DialogActions>
        </form>
      </Dialog>
    </AppShell>
  )
}

