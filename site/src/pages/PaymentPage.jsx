import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import useAccount, { invalidateAccount } from '../account/useAccount.js'
import { createPayment, applyPromocode, getEmailPrefs } from '../api/user.js'
import { getQuote, walletPurchase, walletTopup, newRequestId } from '../api/wallet.js'
import PayHeader from '../components/PayHeader.jsx'
import MiniPage from '../components/MiniPage.jsx'
import Alert from '../components/Alert.jsx'
import Badge from '../components/Badge.jsx'
import Button from '../components/Button.jsx'
import Field, { Input } from '../components/Field.jsx'
import Flap from '../components/Flap.jsx'
import Hedgehog from '../components/Hedgehog.jsx'
import Icon from '../components/Icon.jsx'
import PlanCard from '../components/PlanCard.jsx'
import { SkeletonCard, useDelayed } from '../components/Skeleton.jsx'
import { useToast } from '../components/Toast.jsx'
import usePlans from '../hooks/usePlans.js'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { currentSub } from '../lib/account.js'
import { splitPlans, savings, featuredId, planList, termLabel } from '../lib/plans.js'
import { date, kop, money, daysLeft, group } from '../lib/format.js'

// Тарифы → Оформление (spec 09 §9.1–9.2) on one route: /payment is step 1, /payment?plan=<slug> is
// step 2 (a history entry, so «Назад» returns to the grid). Paying: enough on the wallet → buy from
// it; not enough → top up exactly the difference with the purchase attached → /checkout/:id.
export default function PaymentPage() {
  const [params, setParams] = useSearchParams()
  const slug = params.get('plan')
  const plans = usePlans()
  const acc = useAccount(['profile', 'subscription', 'wallet'], { optional: ['subscription', 'wallet'] })
  const sub = currentSub(acc.data.subscription)
  const active = sub?.active ? sub : null
  const chosen = slug ? plans.list.find((p) => p.slug === slug) : null
  const [done, setDone] = useState(null) // { title, text } after a purchase without ЮKassa
  usePageMeta(slug ? 'Оформление — HiEarth' : 'Тарифы — HiEarth', { noindex: true })

  const goStep = (n) => { if (n === 0) setParams({}, { replace: true }) }

  if (done) {
    return (
      <MiniPage kind="res" label="Результат оплаты">
        <Hedgehog mood="happy" size={150} />
        <Badge tone="ok">Оплачено</Badge>
        <h1 className="t-h2">{done.title}</h1>
        {done.text && <p className="muted t-sm">{done.text}</p>}
        <Button variant="primary" block to="/dashboard">В личный кабинет</Button>
      </MiniPage>
    )
  }

  const showStep2 = slug && (chosen || plans.loading)
  return (
    <>
      <PayHeader
        step={showStep2 ? 1 : 0}
        back={showStep2 ? { label: 'Тарифы', onClick: () => goStep(0) } : { to: '/dashboard', label: 'В кабинет' }}
        onStep={goStep}
        planSlug={slug}
      />
      <main id="main" className="container" style={{ maxWidth: showStep2 ? 1040 : undefined, paddingTop: 48, paddingBottom: 80 }}>
        {showStep2
          ? <Review key={chosen?.id ?? slug} plan={chosen} plansLoading={plans.loading} acc={acc} active={active} onChange={() => goStep(0)} onDone={setDone} />
          : <Tariffs plans={plans} active={active} acc={acc} onPick={(p) => setParams({ plan: p.slug })} onDone={setDone} unknownSlug={slug && !chosen && !plans.loading} />}
      </main>
    </>
  )
}

// ── step 1 ────────────────────────────────────────────────────────────────────
function Tariffs({ plans, active, acc, onPick, onDone, unknownSlug }) {
  const { token } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()
  const narrow = useMedia('(max-width: 1024px)')
  const showSkel = useDelayed(plans.loading, 300)
  const { paid, free } = splitPlans(plans.list)
  const save = savings(plans.list)
  const current = active ? paid.find((p) => p.id === active.plan_id || p.slug === active.plan) : null
  const featured = current ? current.id : featuredId(plans.list)
  const allUnlimited = paid.length > 0 && paid.every((p) => !Number(p.traffic_limit_bytes))
  const [pr, setPr] = useState({}) // plan id → proration preview
  const [freeBusy, setFreeBusy] = useState(false)
  const [err, setErr] = useState('')
  const inFlight = useRef(false)

  // bonus days / downgrade rule for each paid plan while a subscription is active
  useEffect(() => {
    if (!token || !active || !paid.length) return undefined
    let alive = true
    Promise.all(paid.map((p) => fetch(`/api/v1/payment/proration-preview?plan_id=${p.id}`, { headers: { Authorization: `Bearer ${token}` } })
      .then((r) => (r.ok ? r.json() : null)).then((d) => [p.id, d]).catch(() => [p.id, null])))
      .then((pairs) => { if (alive) setPr(Object.fromEntries(pairs.filter(([, d]) => d))) })
    return () => { alive = false }
  }, [token, active?.plan_id, paid.length]) // eslint-disable-line react-hooks/exhaustive-deps

  async function activateFree() {
    if (inFlight.current || !free) return
    inFlight.current = true
    setFreeBusy(true)
    setErr('')
    try {
      const d = await createPayment(token, null, `${window.location.origin}/dashboard`, free.id, '', false, '', 'redirect')
      if (d.status !== 'activated') throw new Error('')
      invalidateAccount()
      toast.ok('Подписка активирована')
      navigate('/dashboard')
    } catch (e) {
      setErr(e?.status === 400 && e.message ? e.message : 'Не удалось активировать тариф — попробуйте ещё раз.')
    } finally {
      inFlight.current = false
      setFreeBusy(false)
    }
  }

  const left = active ? daysLeft(active.valid_until) : 0
  const threshold = Object.values(pr)[0]?.early_renewal_days_threshold ?? 3
  const cols = paid.length >= 4 ? 'g-4' : paid.length === 3 ? 'g-3' : 'g-2'

  return (
    <>
      <h1 className="t-h1">Выберите тариф</h1>
      {(allUnlimited || active) && (
        <p className="muted" style={{ marginTop: 6 }}>
          {[allUnlimited && 'Безлимитный трафик во всех тарифах.', active && `Сейчас у вас «${active.plan_name}» до ${date(active.valid_until)}.`].filter(Boolean).join(' ')}
        </p>
      )}
      {unknownSlug && <Alert tone="info" className="mt-4">Такого тарифа сейчас нет — выберите из списка.</Alert>}
      {err && <Alert tone="bad" rise className="mt-4">{err}</Alert>}

      <div style={{ marginTop: 36 }}>
        {plans.error ? (
          <Alert tone="bad" title="Тарифы недоступны.">
            Не удалось загрузить список. <button type="button" className="link" onClick={plans.reload} style={{ background: 'none', border: 0, padding: 0, cursor: 'pointer', font: 'inherit' }}>Обновить</button>
          </Alert>
        ) : plans.loading ? (
          <div className="grid g-4" aria-busy="true" style={{ minHeight: 380 }}>{showSkel && [0, 1, 2, 3].map((i) => <SkeletonCard key={i} lines={4} big />)}</div>
        ) : (
          <div className={`grid ${cols}`} style={paid.length <= 2 ? { maxWidth: 760 } : undefined}>
            {paid.map((p) => {
              const isCur = current && p.id === current.id
              const preview = pr[p.id]
              const blocked = !!preview?.is_downgrade_blocked
              const notes = []
              if (isCur && left <= threshold) notes.push({ tone: 'warn', text: 'Скоро закончится — продлите заранее' })
              if (!isCur && preview?.bonus_days > 0) notes.push({ tone: 'ok', text: `+${preview.bonus_days} бонус-дней за остаток текущего плана` })
              return (
                <PlanCard
                  key={p.id}
                  plan={p}
                  featured={p.id === featured}
                  current={!!isCur}
                  saving={save[p.id]}
                  cta={isCur ? 'Продлить' : 'Выбрать'}
                  onClick={() => onPick(p)}
                  disabled={blocked}
                  disabledNote={blocked && active ? `Переход доступен после ${date(active.valid_until)}` : null}
                  notes={notes}
                />
              )
            })}
          </div>
        )}
      </div>

      {free && !active && !plans.loading && !acc.loading && (
        <div className="card row wrap between gap-4" style={{ marginTop: 16, padding: '20px 24px' }}>
          <span><span className="plan__name">{free.name}</span> <span className="plan__term">· {termLabel(free)}</span><br /><span className="t-sm muted">{planList(free)[0]}</span></span>
          <span className="row gap-4"><b className="t-h3">Бесплатно</b><Button variant="secondary" loading={freeBusy} onClick={activateFree}>Активировать бесплатно</Button></span>
        </div>
      )}

      <div className="grid" style={{ marginTop: 24, gap: 16, gridTemplateColumns: narrow ? '1fr' : '1fr 1fr' }}>
        <div className="card row gap-4">
          <span className="rowi__ic" style={{ background: 'var(--accent-soft)', color: 'var(--accent-fg)' }} aria-hidden="true"><Icon name="wallet" /></span>
          <p className="t-sm" style={{ flex: 1 }}><b>Оплата с кошелька.</b> <span className="muted">Если денег не хватает, на следующем шаге пополним кошелёк ровно на недостающую сумму.</span></p>
        </div>
        <div className="card row gap-4">
          <span className="rowi__ic" aria-hidden="true"><Icon name="lock" /></span>
          <p className="t-sm" style={{ flex: 1 }}><b>Карту не привязываем.</b> <span className="muted">Данные карты вводятся только на защищённой странице ЮKassa.</span></p>
        </div>
      </div>
      {acc.error && acc.error.kind !== 'auth' && <p className="t-xs subtle" style={{ marginTop: 16 }}>Не удалось загрузить данные кабинета — текущий тариф может быть не отмечен.</p>}
    </>
  )
}

// ── step 2 ────────────────────────────────────────────────────────────────────
function Review({ plan, plansLoading, acc, active, onChange, onDone }) {
  const { token } = useAuth()
  const navigate = useNavigate()
  const narrow = useMedia('(max-width: 1024px)')
  const [promo, setPromo] = useState('')
  const [applied, setApplied] = useState('')
  const [promoErr, setPromoErr] = useState('')
  const [quote, setQuote] = useState(null)
  const [quoteBusy, setQuoteBusy] = useState(false)
  const [quoteErr, setQuoteErr] = useState('')
  const [email, setEmail] = useState('')
  const [confirmed, setConfirmed] = useState('')
  const [emailErr, setEmailErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [requestId, setRequestId] = useState(newRequestId)
  const [quoteNonce, setQuoteNonce] = useState(0)
  const inFlight = useRef(false)
  const isFree = plan && Number(plan.price_rub) === 0

  useEffect(() => {
    if (!token) return
    getEmailPrefs(token).then((p) => { if (p?.email) setConfirmed(p.email) }).catch(() => {})
  }, [token])

  // the price with the code, what the wallet covers and what to top up
  useEffect(() => {
    if (!token || !plan || isFree) return undefined
    let alive = true
    setQuote(null) // never pay with a quote of another code or balance
    setQuoteBusy(true)
    getQuote(token, plan.id, applied)
      .then((q) => { if (!alive) return; setQuote(q); setQuoteErr('') })
      .catch((e) => {
        if (!alive || e.kind === 'auth') return
        if (applied) { setPromoErr(`Не удалось применить промокод: ${String(e.message || '').replace(/^./, (c) => c.toLowerCase())}`); setApplied('') }
        else setQuoteErr(e.message || 'Не удалось рассчитать стоимость.')
      })
      .finally(() => { if (alive) setQuoteBusy(false) })
    return () => { alive = false }
  }, [token, plan?.id, applied, quoteNonce]) // eslint-disable-line react-hooks/exhaustive-deps

  const list = quote ? quote.list_price_kop : Number(plan?.price_rub || 0) * 100
  const price = quote ? quote.price_kop : list
  const discount = Math.max(0, list - price)
  const fromWallet = quote ? Math.min(quote.balance_kop, price) : 0
  const topupKop = quote ? quote.topup_kop : 0
  const topupRub = Math.ceil(topupKop / 100)
  const enough = quote && quote.shortfall_kop === 0
  const freeCode = quote?.promo_type === 'free_trial'
  const blocked = !!quote?.is_downgrade_blocked
  const pct = list ? Math.round((discount / list) * 100) : 0

  function applyCode(e) {
    e.preventDefault()
    if (applied) { setApplied(''); setPromo(''); setPromoErr(''); return }
    if (!promo.trim()) { setPromoErr('Введите промокод'); return }
    setPromoErr('')
    setApplied(promo.trim().toUpperCase())
  }

  async function activateFree() {
    if (inFlight.current) return
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      const d = await createPayment(token, null, `${window.location.origin}/dashboard`, plan.id, '', false, '', 'redirect')
      if (d.status !== 'activated') throw new Error('')
      invalidateAccount()
      onDone({ title: 'Подписка активирована', text: d.message || '' })
    } catch (e) {
      setErr(e?.status === 400 && e.message ? e.message : 'Не удалось активировать тариф — попробуйте ещё раз.')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  async function pay() {
    if (isFree) { activateFree(); return }
    if (inFlight.current || !quote || !plan) return
    const mail = (confirmed || email).trim()
    if (!confirmed && mail && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(mail)) { setEmailErr('Проверьте адрес почты — в нём должна быть «@» и домен'); return }
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      if (freeCode) {
        const d = await applyPromocode(token, applied)
        invalidateAccount()
        onDone({ title: 'Промокод активирован', text: d.message || '' })
        return
      }
      if (enough) {
        const d = await walletPurchase(token, plan.id, applied, requestId)
        setRequestId(newRequestId())
        invalidateAccount()
        onDone({ title: 'Подписка активирована', text: `«${d.plan_name}» действует до ${date(d.valid_until)}. На кошельке осталось ${kop(d.balance_kop)}.` })
        return
      }
      const d = await walletTopup(token, { amountRub: topupRub, purchase: { planId: plan.id, promocode: applied }, email: mail, confirmationType: 'embedded' })
      if (d.confirmation_token) {
        sessionStorage.setItem(`checkout_token_${d.payment_id}`, d.confirmation_token)
        sessionStorage.setItem(`checkout_plan_${d.payment_id}`, plan.name || '')
        sessionStorage.setItem(`checkout_meta_${d.payment_id}`, JSON.stringify({ slug: plan.slug, amount_kop: d.amount_kop || topupRub * 100, price_kop: price }))
        try { localStorage.setItem(`he_topup_plan_${d.payment_id}`, plan.slug) } catch { /* storage off */ }
        navigate(`/checkout/${d.payment_id}`)
        return
      }
      if (d.confirmation_url) { window.location.href = d.confirmation_url; return }
      throw new Error('')
    } catch (e) {
      if (e?.kind !== 'network') setRequestId(newRequestId())
      if (e?.status === 409 || e?.status === 402) { setErr(e.message); invalidateAccount('wallet'); acc.reload('wallet'); setQuoteNonce((x) => x + 1) }
      else setErr(e?.status === 400 && e.message ? e.message : 'Не удалось открыть оплату — попробуйте ещё раз.')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  if (!plan) {
    return <div className="grid" style={{ gridTemplateColumns: narrow ? '1fr' : 'minmax(0,1fr) 380px', gap: 24 }}><SkeletonCard lines={3} /><SkeletonCard lines={4} big /></div>
  }

  const btnLabel = isFree ? 'Активировать бесплатно' : quoteBusy ? 'Считаем…'
    : freeCode ? 'Активировать промокод'
      : enough ? `Оплатить с кошелька — ${kop(price)}`
        : `Оплатить ${money(topupRub)}`

  const summary = (
    <aside className="card card--elevated" style={{ position: narrow ? 'static' : 'sticky', top: 92, padding: 26 }}>
      <h2 className="t-h3">К оплате</h2>
      <div style={{ marginTop: 14, opacity: quoteBusy ? 0.5 : 1, transition: 'opacity var(--dur-2) var(--ease-out)' }}>
        <div className="sum"><span className="muted">{plan.name}</span><span className="mono">{kop(list)}</span></div>
        {discount > 0 && <div className="sum rise" style={{ '--d': '0ms' }}><span className="muted">Промокод {applied}</span><span className="mono ok">−{kop(discount)}</span></div>}
        {quote && fromWallet > 0 && !freeCode && <div className="sum"><span className="muted">С кошелька</span><span className="mono">−{kop(fromWallet)}</span></div>}
      </div>
      <div className="divider" style={{ margin: '12px 0 16px' }} />
      {quote && !freeCode && (
        <div className="row between" style={{ alignItems: 'flex-end' }}>
          <span className="t-label">{enough ? 'Спишем с кошелька' : 'Пополнение'}</span>
          <Flap value={enough ? group(price / 100) : topupRub} suffix="₽" size={40} label={enough ? kop(price) : money(topupRub)} />
        </div>
      )}
      {err && <Alert tone="bad" rise className="mt-4">{err}</Alert>}
      <Button variant="primary" size="lg" block loading={busy} disabled={!isFree && (!quote || quoteBusy || blocked)} onClick={pay} style={{ marginTop: 22 }}>{btnLabel}</Button>
      {quote && !enough && !freeCode && <p className="t-xs subtle" style={{ marginTop: 12, textAlign: 'center' }}>Картой или через СБП на защищённой странице ЮKassa. Карту не привязываем.</p>}
    </aside>
  )

  return (
    <>
      <h1 className="t-h1">Оформление</h1>
      <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0, 1fr) 380px', gap: 24, marginTop: 28, alignItems: 'start' }}>
        <div className="stack gap-4">
          <div className="card row between gap-4">
            <span><span className="t-label">Тариф</span><br /><b className="t-h3">{plan.name} · {termLabel(plan)}</b><br /><span className="t-sm subtle">{planList(plan).join(', ')}</span></span>
            <Button variant="ghost" size="sm" onClick={onChange}>Изменить</Button>
          </div>
          <div className="card stack gap-4">
            <form onSubmit={applyCode} noValidate>
              <Field id="pc" label="Промокод" optional error={promoErr || null} hint={applied && discount > 0 ? (quote?.promo_message || `Скидка ${pct}% — минус ${kop(discount)}`) : applied && freeCode ? (quote?.promo_message || 'Промокод даёт бесплатные дни') : null} hintTone="ok">
                <div className="input-group">
                  <Input id="pc" aria-describedby={promoErr || applied ? 'pc-msg' : undefined} value={applied || promo} readOnly={!!applied} placeholder="Например, ЛЕТО10" autoCapitalize="characters" spellCheck={false}
                    onChange={(e) => { setPromo(e.target.value.toUpperCase()); setPromoErr('') }} invalid={!!promoErr} />
                  <Button variant="secondary" type="submit" loading={quoteBusy && !!applied && !quote?.promo_type}>{applied ? 'Убрать' : 'Применить'}</Button>
                </div>
              </Field>
              {applied && quote?.promo_type && <Badge tone="ok" className="pop" style={{ marginTop: 8 }}>Применён</Badge>}
            </form>
            {!confirmed ? (
              <Field id="em" label="Email для чека" optional error={emailErr || null} hint="Используем только для отправки чека.">
                <Input type="email" inputMode="email" autoComplete="email" value={email} placeholder="you@example.ru" onChange={(e) => { setEmail(e.target.value); setEmailErr('') }} />
              </Field>
            ) : (
              <p className="t-sm muted">Чек придёт на <span className="mono" style={{ color: 'var(--fg)' }}>{confirmed}</span>.</p>
            )}
          </div>
          {quoteErr && <Alert tone="bad" title="Не удалось рассчитать стоимость.">{quoteErr === 'Не удалось рассчитать стоимость.' ? 'Обновите страницу.' : quoteErr}</Alert>}
          {blocked && <Alert tone="warn">Сейчас у вас тариф дороже — сменить на более дешёвый можно после окончания текущего{active ? `, ${date(active.valid_until)}` : ''}.</Alert>}
          {quote && !freeCode && !blocked && (enough
            ? <Alert tone="ok" title="Хватает на кошельке.">Спишем {kop(price)} с баланса — без ЮKassa.</Alert>
            : <Alert tone="warn" title={`На кошельке ${kop(quote.balance_kop)}, к оплате ${kop(price)}.`}>Пополним кошелёк ровно на {money(topupRub)} и сразу оплатим подписку.</Alert>)}
          {quote?.bonus_days > 0 && <p className="t-sm ok">+{quote.bonus_days} бонус-дней за остаток текущего плана — подписка будет до {date(quote.new_valid_until)}.</p>}
        </div>
        {summary}
      </div>
    </>
  )
}
