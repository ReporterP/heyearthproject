import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount, { invalidateAccount } from '../account/useAccount.js'
import { createPayment } from '../api/user.js'
import { walletPurchase, walletTopup, newRequestId } from '../api/wallet.js'
import AppShell, { PageHead } from '../components/AppShell.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import ConnectOrb from '../components/ConnectOrb.jsx'
import EmptyState from '../components/EmptyState.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import { useToast } from '../components/Toast.jsx'
import { ActiveConnect, WaitingConnect, PausedConnect } from '../components/ConnectCard.jsx'
import SubscriptionCard from '../components/SubscriptionCard.jsx'
import WalletCard from '../components/WalletCard.jsx'
import TrafficCard from '../components/TrafficCard.jsx'
import ReferralCard, { useReferral } from '../components/ReferralCard.jsx'
import ChecklistCard from '../components/ChecklistCard.jsx'
import PromoCard from '../components/PromoCard.jsx'
import usePlans from '../hooks/usePlans.js'
import useMtproto from '../hooks/useMtproto.js'
import useMedia, { PHONE } from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { currentSub, subState, readChecklist, writeChecklist } from '../lib/account.js'
import { date, kopNum, money, days as dayWord } from '../lib/format.js'

// Обзор кабинета (spec 08 §8.1): artboards «активна», «нет подписки», «закончилась», «телефон».
// The state comes from the subscription: active · none · expired. Main action — «Добавить в Happ»
// (active), the free week or «Выбрать тариф» (none), renewing (expired).
const span = (n) => ({ gridColumn: `span ${n}` })

export default function DashboardPage() {
  const { token } = useAuth()
  const navigate = useNavigate()
  const toast = useToast()
  const phone = useMedia(PHONE)
  const narrow = useMedia('(max-width: 1024px)')
  usePageMeta('Обзор — HiEarth', { noindex: true })

  const acc = useAccount(['profile', 'subscription', 'traffic', 'subLink', 'wallet'], { optional: ['traffic', 'subLink', 'wallet'] })
  const plans = usePlans()
  const [referral, reloadReferral] = useReferral(token)
  const profile = acc.data.profile
  const sub = currentSub(acc.data.subscription)
  const state = subState(sub)
  const tg = useMtproto(token, state === 'active')
  const wallet = acc.data.wallet
  const showSkel = useDelayed(acc.loading && !profile, 300)

  // «Как начать»
  const [check, setCheck] = useState({})
  useEffect(() => { if (profile?.user_id) setCheck(readChecklist(profile.user_id)) }, [profile?.user_id])
  const toggle = useCallback((n) => setCheck((c) => { const v = { ...c, [n]: !c[n] }; writeChecklist(profile?.user_id, v); return v }), [profile?.user_id])
  const markHapp = () => setCheck((c) => { const v = { ...c, 2: true }; writeChecklist(profile?.user_id, v); return v })

  const reloadAll = () => { invalidateAccount(); acc.reload(); reloadReferral() }

  const plan = sub ? plans.list.find((p) => p.id === sub.plan_id) || plans.list.find((p) => p.slug === sub.plan) : null
  const freePlan = plans.list.find((p) => Number(p.price_rub) === 0) || null

  let body
  if (acc.error && acc.error.kind !== 'auth') {
    body = (
      <EmptyState mood="worried" title="Не удалось загрузить данные" actions={<Button variant="secondary" onClick={() => acc.reload()}>Повторить</Button>}>
        Обновите страницу.
      </EmptyState>
    )
  } else if (!profile) {
    body = (
      <div className="grid g-12" style={{ gap: 16 }} aria-busy="true">
        {showSkel && [7, 5, 4, 5, 3].map((n, i) => <SkeletonCard key={i} lines={i < 2 ? 4 : 3} big={i < 3} style={narrow ? span(12) : span(n)} />)}
      </div>
    )
  } else if (state === 'active') {
    body = <Active {...{ phone, narrow, sub, plan, wallet, acc, tg, referral, reloadReferral, token, check, toggle, markHapp, reloadAll, traffic: acc.data.traffic }} />
  } else if (state === 'none') {
    body = <NoSub {...{ phone, narrow, wallet, freePlan, plansLoading: plans.loading, token, reloadAll, toast, referral, reloadReferral }} />
  } else {
    body = <Expired {...{ phone, narrow, sub, plan, plans, wallet, token, reloadAll, toast, navigate, referral, reloadReferral }} />
  }

  const head = state === 'none' && profile
    ? { title: 'Добро пожаловать', lead: 'Осталось включить подписку — и можно подключаться.' }
    : state === 'active' && profile
      ? { title: 'Обзор', lead: 'Всё работает. Если на мобильном не грузит — обновите подписку в Happ.' }
      : { title: 'Обзор' }

  return (
    <AppShell>
      <PageHead title={head.title} lead={head.lead} />
      {body}
    </AppShell>
  )
}

// cards enter in columns: 0 / 80 / 160 ms (04 §3, cabinet)
const R = ({ d = 0, style, children }) => <div className="rise" style={{ '--d': `${d}ms`, ...style }}>{children}</div>

function Active({ phone, narrow, sub, plan, wallet, acc, tg, referral, reloadReferral, token, check, toggle, markHapp, reloadAll }) {
  const subLink = acc.data.subLink
  const traffic = acc.data.traffic
  const refOn = referral?.enabled
  const checklistOpen = [1, 2, 3].some((n) => !check[n])

  if (narrow) {
    return (
      <div className="stack gap-3">
        <R><ActiveConnect subLink={subLink} tg={tg} phone={phone} onHapp={markHapp} /></R>
        <R d={80}><SubscriptionCard sub={sub} plan={plan} autoRenew={wallet?.auto_renew} phone={phone} started={acc.data.traffic?.subscription?.started_at} /></R>
        {wallet && <R d={160}><WalletCard wallet={wallet} phone={phone} /></R>}
        {traffic && <TrafficCard data={traffic} phone={phone} />}
        {refOn && <ReferralCard token={token} referral={referral} reload={reloadReferral} phone={phone} />}
        {checklistOpen && <ChecklistCard done={check} onToggle={toggle} />}
        <PromoCard onApplied={reloadAll} />
        {phone && <MobileNav />}
      </div>
    )
  }
  return (
    <div className="grid g-12" style={{ gap: 16, alignItems: 'start' }}>
      <R style={span(7)}><ActiveConnect subLink={subLink} tg={tg} onHapp={markHapp} /></R>
      <R d={80} style={{ ...span(5), alignSelf: 'stretch' }}><SubscriptionCard sub={sub} plan={plan} autoRenew={wallet?.auto_renew} started={acc.data.traffic?.subscription?.started_at} /></R>
      {wallet && <R d={160} style={span(4)}><WalletCard wallet={wallet} /></R>}
      {traffic && <div style={span(wallet ? (refOn ? 5 : 8) : (refOn ? 9 : 12))}><TrafficCard data={traffic} /></div>}
      {refOn && <div style={{ ...span(3), alignSelf: 'stretch' }}><ReferralCard token={token} referral={referral} reload={reloadReferral} /></div>}
      {checklistOpen && <div style={span(7)}><ChecklistCard done={check} onToggle={toggle} /></div>}
      <div style={span(checklistOpen ? 5 : 12)}><PromoCard onApplied={reloadAll} /></div>
    </div>
  )
}

function NoSub({ phone, narrow, wallet, freePlan, plansLoading, token, reloadAll, toast, referral, reloadReferral }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [trialGone, setTrialGone] = useState(false)
  const inFlight = useRef(false)
  const trial = freePlan && !trialGone
  const days = Number(freePlan?.duration_days) || 7

  async function activate() {
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      const d = await createPayment(token, null, `${window.location.origin}/dashboard`, freePlan.id, '', false, '', 'redirect')
      if (d.status !== 'activated') throw new Error('')
      toast.ok('Подписка активирована')
      reloadAll()
    } catch (e) {
      // most often the free week has been used already: offer the plans instead
      setErr(e.message && e.status === 400 ? e.message : 'Не удалось активировать тариф — попробуйте ещё раз.')
      if (e.status === 400) setTrialGone(true)
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  const main = (
    <section className="card" style={phone ? { padding: 24 } : { display: 'grid', gridTemplateColumns: narrow ? '1fr' : '200px minmax(0, 1fr)', gap: 32, alignItems: 'center', padding: 32 }}>
      {!phone && !narrow && <ConnectOrb active={false} label="Нет подписки" to="/payment" ariaLabel="Подписки нет — выбрать тариф" />}
      <div className="stack gap-3">
        <Badge tone="plain" style={{ width: 'max-content' }}>Нет активной подписки</Badge>
        {plansLoading ? (
          <span className="skel" style={{ width: '60%', height: 28 }} aria-hidden="true" />
        ) : trial ? (
          <>
            <h2 className="t-h2">Попробуйте неделю бесплатно</h2>
            <p className="muted">Без карты и обязательств. Через {dayWord(days)} выберете тариф — или просто перестанете пользоваться.</p>
            {err && <Alert tone="bad">{err}</Alert>}
            <div className={phone ? 'stack gap-2' : 'row wrap gap-2'} style={{ marginTop: 6 }}>
              <Button variant="primary" size="lg" block={phone} loading={busy} onClick={activate}>Активировать бесплатную неделю</Button>
              <Button variant="ghost" size="lg" block={phone} to="/payment" disabled={busy}>Выбрать тариф</Button>
            </div>
          </>
        ) : (
          <>
            <h2 className="t-h2">Выберите тариф</h2>
            <p className="muted">Подписка оплачивается с кошелька: пополните его картой или через СБП — и подключайтесь.</p>
            {err && <Alert tone="info">{err}</Alert>}
            <div style={{ marginTop: 6 }}><Button variant="primary" size="lg" block={phone} to="/payment">Выбрать тариф</Button></div>
          </>
        )}
      </div>
    </section>
  )
  const walletCard = wallet && <WalletCard wallet={wallet} phone={phone} history={false} note="Пополните картой или через СБП — подписка оплачивается с баланса." />

  if (narrow) {
    return (
      <div className="stack gap-3">
        <R>{main}</R>
        {walletCard && <R d={80}>{walletCard}</R>}
        <R d={160}><WaitingConnect /></R>
        <PromoCard compact onApplied={reloadAll} />
        {referral?.enabled && <ReferralCard token={token} referral={referral} reload={reloadReferral} phone={phone} />}
        {phone && <MobileNav />}
      </div>
    )
  }
  return (
    <div className="grid g-12" style={{ gap: 16, alignItems: 'start' }}>
      <R style={span(8)}>{main}</R>
      {walletCard && <R d={80} style={span(4)}>{walletCard}</R>}
      <R d={160} style={span(8)}><WaitingConnect /></R>
      <div style={span(4)}><PromoCard compact onApplied={reloadAll} /></div>
      {referral?.enabled && <div style={span(12)}><ReferralCard token={token} referral={referral} reload={reloadReferral} /></div>}
    </div>
  )
}

function Expired({ phone, narrow, sub, plan, plans, wallet, token, reloadAll, toast, navigate, referral, reloadReferral }) {
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const inFlight = useRef(false)
  const [requestId, setRequestId] = useState(newRequestId)
  // money math in kopecks: a balance of 99,60 ₽ is not enough for a 100 ₽ plan
  const balanceKop = Number(wallet?.balance_kop) || 0
  const balance = kopNum(balanceKop)
  const price = plan ? Number(plan.price_rub) : Number(sub.price_rub) || 0
  const buyable = !!plan && price > 0
  const shortKop = Math.max(0, price * 100 - balanceKop)
  const short = Math.ceil(shortKop / 100)
  // with a purchase attached the server accepts a top-up from the shortfall itself (min_topup_rub
  // applies to plain top-ups)
  const topup = short
  const wasOn = sub.auto_renew

  const reason = plans.loading
    ? 'Проверяем тариф и баланс…'
    : plans.error && !plan
      ? 'Не удалось загрузить тарифы — обновите их, чтобы продлить подписку.'
      : !buyable
    ? 'Выберите тариф — подписка включится сразу после оплаты.'
    : wasOn && short > 0
      ? `Автопродление не сработало: на кошельке ${money(balance)}, а «${plan.name}» стоит ${money(price)}. Пополните на ${money(short)} — подписка продлится сразу.`
      : wasOn
        ? `На кошельке хватает денег на «${plan.name}». Продлите — подписка включится сразу.`
        : 'Автопродление было выключено. Продлите — подписка включится сразу.'

  async function renew() {
    if (inFlight.current || !buyable) return
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      if (short > 0) {
        const d = await walletTopup(token, { amountRub: topup, purchase: { planId: plan.id, promocode: '' }, confirmationType: 'embedded' })
        if (d.confirmation_token) {
          sessionStorage.setItem(`checkout_token_${d.payment_id}`, d.confirmation_token)
          sessionStorage.setItem(`checkout_plan_${d.payment_id}`, plan.name || '')
          navigate(`/checkout/${d.payment_id}`)
          return
        }
        if (d.confirmation_url) { window.location.href = d.confirmation_url; return }
        throw new Error('')
      }
      await walletPurchase(token, plan.id, '', requestId)
      setRequestId(newRequestId())
      toast.ok('Подписка активирована')
      reloadAll()
    } catch (e) {
      if (e?.kind !== 'network') setRequestId(newRequestId())
      if (e?.status === 402 || e?.status === 409) { setErr(e.message); reloadAll() }
      else setErr(e?.status === 400 && e.message ? e.message : 'Не удалось открыть оплату — попробуйте ещё раз.')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  const main = (
    <section className="card" style={phone ? { padding: 24 } : { display: 'grid', gridTemplateColumns: narrow ? '1fr' : '180px minmax(0, 1fr)', gap: 28, alignItems: 'center', padding: 32 }}>
      {!narrow && <Hedgehog mood="sleep" size={170} />}
      <div className="stack gap-3">
        <Badge tone="warn" style={{ width: 'max-content' }}>Подписка истекла {date(sub.valid_until)}</Badge>
        <h2 className="t-h2">Ёжик уснул — подписка закончилась</h2>
        <p className="muted">{reason}</p>
        {err && <Alert tone="bad" rise>{err}</Alert>}
        <div className={phone ? 'stack gap-2' : 'row wrap gap-2'} style={{ marginTop: 6 }}>
          {plans.loading
            ? <span className="skel" style={{ width: 280, height: 54, borderRadius: 14 }} aria-hidden="true" />
            : plans.error && !plan
              ? <Button variant="secondary" size="lg" block={phone} icon="refresh" onClick={plans.reload}>Обновить тарифы</Button>
              : buyable
            ? <Button variant="primary" size="lg" block={phone} loading={busy} onClick={renew}>{short > 0 ? `Пополнить на ${money(topup)} и продлить` : `Продлить за ${money(price)}`}</Button>
            : <Button variant="primary" size="lg" block={phone} to="/payment">Выбрать тариф</Button>}
          {buyable && <Button variant="ghost" size="lg" block={phone} to="/payment" disabled={busy}>Другой тариф</Button>}
        </div>
      </div>
    </section>
  )
  const walletCard = wallet && (
    <WalletCard wallet={wallet} phone={phone} note={buyable && short > 0 ? `Не хватает ${money(short)} до продления` : undefined} noteTone="warn" />
  )
  if (narrow) {
    return (
      <div className="stack gap-3">
        <R>{main}</R>
        {walletCard && <R d={80}>{walletCard}</R>}
        <R d={160}><PausedConnect phone={phone} /></R>
        {referral?.enabled && <ReferralCard token={token} referral={referral} reload={reloadReferral} phone={phone} />}
        <PromoCard onApplied={reloadAll} />
        {phone && <MobileNav />}
      </div>
    )
  }
  return (
    <div className="grid g-12" style={{ gap: 16, alignItems: 'start' }}>
      <R style={span(8)}>{main}</R>
      {walletCard && <R d={80} style={span(4)}>{walletCard}</R>}
      <R d={160} style={span(12)}><PausedConnect /></R>
      {referral?.enabled && <div style={span(5)}><ReferralCard token={token} referral={referral} reload={reloadReferral} /></div>}
      <div style={span(referral?.enabled ? 7 : 12)}><PromoCard onApplied={reloadAll} /></div>
    </div>
  )
}

// phone: the other sections as a list at the bottom (8.5)
function MobileNav() {
  return (
    <nav className="card" style={{ padding: 6 }} aria-label="Разделы">
      <Link to="/payments" className="side-link">Оплата и подписка</Link>
      <Link to="/settings" className="side-link">Настройки</Link>
      <Link to="/help" className="side-link">Помощь</Link>
    </nav>
  )
}
