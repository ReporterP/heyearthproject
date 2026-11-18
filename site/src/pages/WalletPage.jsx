import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount from '../account/useAccount.js'
import { getEmailPrefs } from '../api/user.js'
import { walletTopup } from '../api/wallet.js'
import AppShell, { PageHead } from '../components/AppShell.jsx'
import Alert from '../components/Alert.jsx'
import Button from '../components/Button.jsx'
import EmptyState from '../components/EmptyState.jsx'
import Field, { Input } from '../components/Field.jsx'
import Flap from '../components/Flap.jsx'
import ListRow from '../components/ListRow.jsx'
import PaymentResult from '../components/PaymentResult.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import useMedia, { CABINET_NARROW } from '../hooks/useMedia.js'
import { isMiniApp } from '../lib/telegram.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { currentSub } from '../lib/account.js'
import { date, dateTime, kop, kopNum, group } from '../lib/format.js'
import { payErrorText } from '../lib/payError.js'

// Кошелёк (spec 09 §9.5, artboard «Кошелёк»). /wallet?topup=<id> is where ЮKassa returns: it shows the
// payment's result instead (9.4).
const AMOUNTS = [300, 500, 1000, 2000]
const MAX = 100000

export default function WalletPage() {
  const [params] = useSearchParams()
  const { token } = useAuth()
  const returned = params.get('topup')
  if (returned) return <PaymentResult token={token} paymentId={returned} />
  return <Wallet />
}

// a wallet transaction → a history row (05 §4, §6)
function txRow(tx) {
  const plus = tx.amount_kop > 0
  const amt = `${plus ? '+' : '−'}${kop(Math.abs(tx.amount_kop))}`
  const base = { title: tx.title, amount: amt, amountTone: plus ? 'ok' : '' }
  switch (tx.kind) {
    case 'topup': return { ...base, icon: 'topup', sub: dateTime(tx.created_at), badge: { tone: 'ok', text: 'Оплачено' }, to: tx.payment_id ? `/payments/${tx.payment_id}` : undefined }
    case 'purchase':
    case 'auto_renew': return { ...base, icon: 'receipt', sub: `${dateTime(tx.created_at)} · с кошелька`, badge: { tone: 'ok', text: 'Оплачено' } }
    case 'referral_bonus':
    case 'referral_welcome': return { ...base, icon: 'gift', sub: dateTime(tx.created_at), badge: { tone: 'ok', text: 'Зачислено' } }
    case 'refund': return { ...base, icon: 'refund', sub: dateTime(tx.created_at), badge: { tone: 'info', text: 'Возврат' }, to: tx.payment_id ? `/payments/${tx.payment_id}` : undefined }
    default: return { ...base, icon: 'wallet', sub: dateTime(tx.created_at) }
  }
}

function Wallet() {
  const { token } = useAuth()
  const navigate = useNavigate()
  const narrow = useMedia(CABINET_NARROW) // one column beside the side menu (01 §3)
  const tight = useMedia('(max-width: 1024px)')
  usePageMeta('Кошелёк — HiEarth', { noindex: true })
  const acc = useAccount(['wallet', 'subscription'], { optional: ['subscription'] })
  const wallet = acc.data.wallet
  const sub = currentSub(acc.data.subscription)
  const showSkel = useDelayed(acc.loading && !wallet, 300)
  const [pick, setPick] = useState(null) // a quick amount
  const [own, setOwn] = useState('')
  const [ownFocus, setOwnFocus] = useState(false)
  const [email, setEmail] = useState('')
  const [err, setErr] = useState('')
  const [fieldErr, setFieldErr] = useState('')
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)

  useEffect(() => {
    if (!token) return
    getEmailPrefs(token).then((p) => { if (p?.email) setEmail((c) => c || p.email) }).catch(() => {})
  }, [token])

  // default: 500 ₽, or the quick amount right above what the renewal is missing
  useEffect(() => {
    if (!wallet || pick !== null || own) return
    const ar = wallet.auto_renew
    const short = ar && ar.enabled && !ar.free && !ar.enough ? kopNum(ar.shortfall_kop) : 0
    setPick(short ? (AMOUNTS.find((a) => a >= short) || AMOUNTS[AMOUNTS.length - 1]) : 500)
  }, [wallet]) // eslint-disable-line react-hooks/exhaustive-deps

  const min = Number(wallet?.min_topup_rub) || 100
  const ownNum = parseInt(own, 10) || 0
  const sum = own ? ownNum : pick || 0
  const ownError = own && (ownNum < min ? `Минимальная сумма пополнения — ${min} ₽` : ownNum > MAX ? 'Не больше 100 000 ₽ за раз' : '')

  async function topup(e) {
    e.preventDefault()
    if (inFlight.current || !sum) return
    if (ownError) { setFieldErr(ownError); return }
    if (email.trim() && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) { setErr('Проверьте адрес почты — в нём должна быть «@» и домен'); return }
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      const d = await walletTopup(token, { amountRub: sum, email: email.trim(), confirmationType: 'embedded' })
      if (d.confirmation_token) {
        sessionStorage.setItem(`checkout_token_${d.payment_id}`, d.confirmation_token)
        sessionStorage.setItem(`checkout_plan_${d.payment_id}`, '')
        sessionStorage.setItem(`checkout_meta_${d.payment_id}`, JSON.stringify({ amount_kop: d.amount_kop || sum * 100 }))
        navigate(`/checkout/${d.payment_id}`)
        return
      }
      if (d.confirmation_url) { window.location.href = d.confirmation_url; return }
      throw new Error('')
    } catch (e2) {
      setErr(payErrorText(e2))
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  let body
  if (acc.error && acc.error.kind !== 'auth' && !wallet) {
    body = <EmptyState mood="worried" title="Не удалось загрузить данные" actions={<Button variant="secondary" onClick={() => acc.reload()}>Повторить</Button>}>Обновите страницу.</EmptyState>
  } else if (!wallet) {
    body = <div className="stack gap-4" aria-busy="true">{showSkel && [0, 1].map((i) => <SkeletonCard key={i} lines={4} big={!i} />)}</div>
  } else {
    const ar = wallet.auto_renew
    const note = ar?.free ? 'Подписка по промокоду продлевается бесплатно'
      : ar?.enabled ? `автопродление ${date(ar.renew_at)} · ${kop(ar.price_kop)}`
        : sub?.active ? `Подписка действует до ${date(sub.valid_until)}` : ''
    const rows = (wallet.transactions || []).slice(0, 5)
    const bal = kopNum(wallet.balance_kop)

    body = (
      <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0, 1fr) 360px', gap: 16, alignItems: 'start' }}>
        <div className="stack gap-4">
          <form id="topup" className="card rise" style={{ padding: tight ? 22 : 30 }} onSubmit={topup} noValidate>
            <div className="row wrap between gap-2" style={{ alignItems: 'flex-end' }}>
              <span className="card__title">На кошельке</span>
              {note && <span className="t-sm subtle">{note}</span>}
            </div>
            <div style={{ marginTop: 12 }}><Flap value={bal} suffix="₽" size={tight ? 40 : 56} label={`${bal} рублей на кошельке`} /></div>
            <hr className="divider" style={{ margin: '26px 0 22px' }} />
            {isMiniApp() ? (
              <div className="stack gap-4">
                <span className="t-label">Оплата в Telegram</span>
                <p className="muted t-sm">В Telegram подписку оплачивают звёздами — дни добавятся к текущей. Автопродление списывает подписку с этого баланса.</p>
                <div><Button variant="primary" size="lg" block={tight} to="/payment">Оплатить звёздами</Button></div>
              </div>
            ) : (<>
            <span className="t-label" id="amounts-l">Пополнить</span>
            <div className="amounts" role="radiogroup" aria-labelledby="amounts-l" style={{ marginTop: 10 }}>
              {AMOUNTS.map((a) => (
                <span key={a} style={{ display: 'contents' }}>
                  <input type="radio" id={`amt-${a}`} name="amount" checked={!own && pick === a} onChange={() => { setPick(a); setOwn(''); setFieldErr('') }} />
                  <label htmlFor={`amt-${a}`}>{group(a)}&nbsp;₽</label>
                </span>
              ))}
            </div>
            <div className="grid" style={{ gridTemplateColumns: tight ? '1fr' : '1fr 1fr', gap: 12, marginTop: 14 }}>
              <Field id="own" label="Своя сумма" error={fieldErr || null}>
                <Input id="own" mono inputMode="numeric" placeholder="до 100 000 ₽ за раз"
                  value={ownFocus || !own ? own : group(ownNum)}
                  onFocus={() => setOwnFocus(true)}
                  onBlur={() => { setOwnFocus(false); if (ownError) setFieldErr(ownError) }}
                  onChange={(e) => { const v = e.target.value.replace(/\D/g, '').slice(0, 6); setOwn(v); setFieldErr('') }} />
              </Field>
              <Field id="re" label="Email для чека" optional>
                <Input id="re" type="email" inputMode="email" autoComplete="email" value={email} placeholder="you@example.ru" onChange={(e) => { setEmail(e.target.value); setErr('') }} />
              </Field>
            </div>
            {err && <Alert tone="bad" rise className="mt-4">{err}</Alert>}
            <div className="row wrap gap-4" style={{ marginTop: 20 }}>
              <Button variant="primary" size="lg" type="submit" block={tight} loading={busy} disabled={!sum}>{sum ? `Пополнить на ${group(sum)} ₽` : 'Пополнить'}</Button>
              <span className="t-sm subtle">На защищённой странице ЮKassa</span>
            </div>
            </>)}
          </form>

          <section id="history" className="card">
            <div className="card__head"><h2 className="card__title">История</h2><Link to="/payments" className="t-sm link">Все платежи</Link></div>
            {rows.length
              ? <div className="rows">{rows.map((tx) => <ListRow key={tx.id} {...txRow(tx)} />)}</div>
              : <EmptyState small title="Пока пусто">Пополните кошелёк — и здесь появятся пополнения и оплаты.</EmptyState>}
          </section>
        </div>

        <aside className="card card--elevated stack gap-3">
          <h2 className="t-h3">Что такое кошелёк</h2>
          <p className="t-sm muted">Вы пополняете кошелёк картой, а подписка оплачивается с него — сразу или автопродлением.</p>
          <p className="t-sm muted">Если денег не хватает, при оплате тарифа мы пополним кошелёк ровно на недостающую сумму.</p>
          <p className="t-sm muted">Деньги на кошельке идут на услуги HiEarth. Неиспользованные можно вернуть по заявке — подробности в <Link to="/refund" className="link">условиях возврата</Link>.</p>
          <hr className="divider" />
          <p className="t-sm muted">Включить или выключить автопродление — в разделе <Link to="/payments" className="link">«Оплата и подписка»</Link>.</p>
        </aside>
      </div>
    )
  }

  return (
    <AppShell>
      <div style={{ maxWidth: 1120 }}>
        <PageHead title="Кошелёк" lead="Пополняете картой или СБП — подписка оплачивается с баланса." />
        {body}
      </div>
    </AppShell>
  )
}
