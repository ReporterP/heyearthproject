import { useState, useEffect, useRef } from 'react'
import { useNavigate, Link } from 'react-router-dom'
import { createPayment, getProfile, applyPromocode, getEmailPrefs } from '../api/user.js'
import { getQuote, walletPurchase, walletTopup, newRequestId, rub, formatDay } from '../api/wallet.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { Skeleton } from '../components/Skeleton.jsx'
import useReveal from '../hooks/useReveal.js'

const CheckIcon = ({ className = 'w-4 h-4 mt-0.5 text-brand flex-shrink-0' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2.4} strokeLinecap="round" strokeLinejoin="round"><path d="M5 12l5 5L20 6" /></svg>
)
const LockIcon = () => (
  <svg className="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}><rect x="4" y="10" width="16" height="11" rx="2" /><path d="M8 10V7a4 4 0 018 0v3" /></svg>
)
const Tree = ({ className = 'w-6 h-6' }) => (
  <svg className={className} fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7} strokeLinecap="round" strokeLinejoin="round"><path d="M9 22V4M15 22V6M6 9c0-2 2-3 3-3M18 11c0-2-2-4-3-4" /></svg>
)

function formatTraffic(bytes) {
  if (!bytes) return 'Безлимит'
  const gb = bytes / (1024 * 1024 * 1024)
  return gb >= 1 ? `${gb.toFixed(0)} ГБ` : `${(bytes / (1024 * 1024)).toFixed(0)} МБ`
}
function formatDuration(days) {
  if (days >= 365) { const y = Math.round(days / 365); return `${y} ${y === 1 ? 'год' : y < 5 ? 'года' : 'лет'}` }
  if (days >= 30) return `${Math.round(days / 30)} мес.`
  return `${days} дн.`
}

function protocolLabel(protocols) {
  if (!protocols || protocols.includes('all')) return 'Все протоколы'
  // User-facing names only — technical core identifiers are never surfaced (§7).
  const labelMap = { vless: 'Прокси', hysteria2: 'Скоростной', xhttp: 'Стабильный канал', all: 'Все протоколы', socks: '', http: '' }
  const labels = protocols.map(p => (labelMap[p] !== undefined ? labelMap[p] : 'Современный')).filter(Boolean)
  const uniq = [...new Set(labels)]
  return uniq.length ? uniq.join(' + ') : 'Современные протоколы'
}

export default function PaymentPage() {
  const navigate = useNavigate()
  const { token } = useAuth()
  const checkoutRef = useRef(null)

  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)
  const [selectedPlan, setSelectedPlan] = useState(null)
  const [currentPlanSlug, setCurrentPlanSlug] = useState(null)
  const [currentSortOrder, setCurrentSortOrder] = useState(-1)
  const [validUntil, setValidUntil] = useState(null)
  const [promocode, setPromocode] = useState('')
  const [appliedPromo, setAppliedPromo] = useState('')
  const [email, setEmail] = useState('')
  // the receipt address defaults to the one confirmed in the cabinet (E8)
  useEffect(() => {
    if (!token) return
    getEmailPrefs(token).then((p) => { if (p?.email) setEmail((cur) => cur || p.email) }).catch(() => {})
  }, [token])
  const [quote, setQuote] = useState(null)
  const [quoteLoading, setQuoteLoading] = useState(false)
  const [promoError, setPromoError] = useState('')
  const [topupRub, setTopupRub] = useState(0)
  const [walletConfirm, setWalletConfirm] = useState(false)
  const [requestId, setRequestId] = useState(newRequestId)
  const [done, setDone] = useState(null) // successful wallet purchase / free activation
  const [paying, setPaying] = useState(false)
  const [error, setError] = useState('')
  const [prorations, setProrations] = useState({})
  const [prorationsLoading, setProrationsLoading] = useState(false)
  const [earlyRenewalThreshold, setEarlyRenewalThreshold] = useState(3)
  const [switchConfirm, setSwitchConfirm] = useState(null)
  const [loadError, setLoadError] = useState(false)

  useEffect(() => {
    const promises = [fetch('/api/v1/plans').then(r => r.json()).catch(() => null)]
    if (token) promises.push(getProfile(token).catch(() => null))
    Promise.all(promises).then(([plansData, profile]) => {
      if (!Array.isArray(plansData)) { setLoadError(true); setPlans([]) }
      else setPlans(plansData)
      const list = Array.isArray(plansData) ? plansData : []
      if (profile?.current_plan_slug) {
        setCurrentPlanSlug(profile.current_plan_slug)
        const match = list.find(p => p.slug === profile.current_plan_slug)
        if (match) setCurrentSortOrder(match.sort_order ?? -1)
      } else if (profile?.current_plan) {
        const match = list.find(p => p.name === profile.current_plan || p.slug === profile.current_plan)
        if (match) { setCurrentPlanSlug(match.slug); setCurrentSortOrder(match.sort_order ?? -1) }
      }
      if (profile?.valid_until) setValidUntil(profile.valid_until)
    }).finally(() => setLoading(false))
  }, [token])

  useEffect(() => {
    if (!token || !validUntil || plans.length === 0) return
    const paid = plans.filter(p => p.price_rub > 0)
    setProrationsLoading(true)
    Promise.all(
      paid.map(p => fetch(`/api/v1/payment/proration-preview?plan_id=${p.id}`, { headers: { Authorization: 'Bearer ' + token } })
        .then(r => r.ok ? r.json() : null).then(d => [p.id, d]).catch(() => [p.id, null]))
    ).then(pairs => {
      const map = {}
      pairs.forEach(([id, d]) => { if (d) map[id] = d })
      setProrations(map)
      const firstWithThreshold = pairs.find(([, d]) => d && typeof d.early_renewal_days_threshold === 'number')
      if (firstWithThreshold) setEarlyRenewalThreshold(firstWithThreshold[1].early_renewal_days_threshold)
    }).finally(() => setProrationsLoading(false))
  }, [token, validUntil, plans])

  useReveal([plans, loading])

  // The price of the chosen plan with the applied code, and the wallet balance.
  useEffect(() => {
    if (!token || selectedPlan == null) { setQuote(null); return }
    let stop = false
    setQuoteLoading(true)
    getQuote(token, selectedPlan, appliedPromo).then(q => {
      if (stop) return
      setQuote(q)
      setPromoError('')
      setTopupRub(Math.ceil((q.topup_kop || 0) / 100))
    }).catch(e => {
      if (stop || e.kind === 'auth') return
      if (appliedPromo) { setPromoError(e.message); setAppliedPromo('') } else setError(e.message)
    }).finally(() => { if (!stop) setQuoteLoading(false) })
    return () => { stop = true }
  }, [token, selectedPlan, appliedPromo])

  // One flow (cleanup plan W3): free — activate; a free-days code — redeem it; enough on
  // the wallet — confirm and pay from it; not enough — top up the shortfall (or more) and
  // the payment buys the plan right away.
  async function handlePay(planId) {
    setSwitchConfirm(null)
    if (!token) { navigate('/login?redirect=/payment'); return }
    const plan = plans.find(p => p.id === planId)
    if (!plan) return
    setError('')
    if (plan.price_rub === 0) return activateFree(planId)
    if (!quote) return
    if (quote.promo_type === 'free_trial') return redeemCode()
    if (quote.shortfall_kop === 0) { setWalletConfirm(true); return }
    return topupAndBuy(planId)
  }

  async function activateFree(planId) {
    setPaying(true)
    try {
      const data = await createPayment(token, null, window.location.origin + '/dashboard', planId, '', false, '', 'redirect')
      if (data.status === 'activated') { setDone({ title: 'Тариф активирован', text: data.message || '' }); return }
      setError('Не удалось активировать тариф — попробуйте ещё раз.')
    } catch (e) { setError(e.message) } finally { setPaying(false) }
  }

  async function redeemCode() {
    setPaying(true)
    try {
      const d = await applyPromocode(token, appliedPromo)
      setDone({ title: 'Промокод активирован', text: d.message || '' })
    } catch (e) { setError(e.message) } finally { setPaying(false) }
  }

  async function payFromWallet() {
    setWalletConfirm(false)
    setPaying(true)
    try {
      const d = await walletPurchase(token, selectedPlan, appliedPromo, requestId)
      setRequestId(newRequestId())
      setDone({ title: 'Подписка активирована', text: `«${d.plan_name}» действует до ${formatDay(d.valid_until)}. На кошельке осталось ${rub(d.balance_kop)}.` })
    } catch (e) { setError(e.message) } finally { setPaying(false) }
  }

  async function topupAndBuy(planId) {
    const min = Math.ceil((quote.topup_kop || 0) / 100)
    if (topupRub < min) { setError(`Пополнение — не меньше ${min} ₽, чтобы хватило на тариф`); return }
    setPaying(true)
    try {
      const plan = plans.find(p => p.id === planId)
      const d = await walletTopup(token, { amountRub: topupRub, purchase: { planId, promocode: appliedPromo }, email: email.trim(), confirmationType: 'embedded' })
      if (d.confirmation_token) {
        sessionStorage.setItem(`checkout_token_${d.payment_id}`, d.confirmation_token)
        sessionStorage.setItem(`checkout_plan_${d.payment_id}`, plan?.name || '')
        navigate(`/checkout/${d.payment_id}`)
        return
      }
      if (d.confirmation_url) { window.location.href = d.confirmation_url; return }
      setError('Не удалось открыть оплату — попробуйте ещё раз.')
    } catch (e) { setError(e.message) } finally { setPaying(false) }
  }

  // Selecting a card scrolls the checkout panel into view (spatial continuity).
  function selectPlan(plan) {
    setSelectedPlan(plan.id)
    setError('')
    setDone(null)
    requestAnimationFrame(() => {
      checkoutRef.current?.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    })
  }

  // Single primary CTA — confirms a switch when replacing an active sub.
  function submitPayment() {
    if (selectedPlan == null) return
    const plan = plans.find(p => p.id === selectedPlan)
    if (!plan) return
    const isCurrent = currentPlanSlug === plan.slug
    if (currentPlanSlug && !isCurrent) { setSwitchConfirm(plan.id); return }
    handlePay(plan.id)
  }

  const daysRemaining = (() => {
    if (!validUntil) return Infinity
    const ms = new Date(validUntil).getTime() - Date.now()
    return Math.max(0, Math.ceil(ms / (24 * 60 * 60 * 1000)))
  })()
  const showEarlyRenewalBadge = currentPlanSlug && daysRemaining <= earlyRenewalThreshold

  const chosen = plans.find(p => p.id === selectedPlan) || null
  const chosenPr = chosen ? (prorations[chosen.id] || {}) : {}
  const chosenIsFree = chosen ? chosen.price_rub === 0 : false
  const chosenIsCurrent = chosen ? currentPlanSlug === chosen.slug : false
  const chosenTotal = quote && chosen && quote.plan_id === chosen.id ? quote.price_kop / 100 : (chosen ? chosen.price_rub : 0)
  const isFreeCode = quote?.promo_type === 'free_trial'
  const enough = quote && quote.shortfall_kop === 0
  const ctaLabel = chosenIsFree ? 'Активировать бесплатно'
    : isFreeCode ? 'Активировать промокод'
    : !quote ? 'Считаем…'
    : enough ? `Оплатить с кошелька — ${rub(quote.price_kop)}`
    : `Пополнить на ${topupRub} ₽ и оплатить`

  /* ── Loading: skeleton grid (no layout jump) ── */
  if (loading) return (
    <section className="relative py-10 md:py-16">
      <div className="max-w-5xl mx-auto px-4 sm:px-6">
        <div className="text-center mb-10 space-y-3">
          <Skeleton className="h-4 w-24 mx-auto !rounded-full" />
          <Skeleton className="h-9 w-64 mx-auto" />
          <Skeleton className="h-4 w-80 mx-auto" />
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5">
          {[0, 1, 2].map(i => <Skeleton key={i} className="h-[22rem] !rounded-[22px]" />)}
        </div>
      </div>
    </section>
  )

  return (
    <section className="relative py-10 md:py-16 overflow-hidden" style={{ paddingBottom: 'max(6rem, env(safe-area-inset-bottom))' }}>
      <div className="pointer-events-none absolute top-0 left-1/4 w-96 h-96 bg-brand/6 rounded-full blur-[130px]" />

      <div className="relative max-w-5xl mx-auto px-4 sm:px-6">
        <div className="text-center mb-8">
          <p className="eyebrow mb-4">Тарифы</p>
          <h1 className="serif text-3xl md:text-4xl font-semibold mb-3">Выберите тариф</h1>
          <p className="text-text-secondary max-w-lg mx-auto">
            Оплата идёт с кошелька: если на нём не хватает, пополним ровно на нужную сумму и сразу оформим подписку.
          </p>
        </div>

        {/* ── Step indicator (progress feedback) ── */}
        <div className="flex items-center justify-center gap-3 mb-9" aria-label="Шаги оформления">
          {[{ n: 1, label: 'Тариф' }, { n: 2, label: 'Оплата' }].map((s, i) => {
            const done = s.n === 1 && chosen
            const active = (s.n === 1 && !chosen) || (s.n === 2 && chosen)
            return (
              <div key={s.n} className="flex items-center gap-3">
                <div className="flex items-center gap-2.5">
                  <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-bold transition-all duration-300 ${
                    done ? 'bg-brand text-[#04231a]' : active ? 'border-2 border-brand text-brand' : 'bg-white/5 text-faint border border-brand/12'
                  }`}>
                    {done ? <CheckIcon className="w-3.5 h-3.5" /> : s.n}
                  </div>
                  <span className={`text-sm font-medium ${active || done ? 'text-text-primary' : 'text-faint'}`}>{s.label}</span>
                </div>
                {i === 0 && <div className={`w-10 h-0.5 rounded-full transition-colors duration-300 ${chosen ? 'bg-brand' : 'bg-brand/15'}`} />}
              </div>
            )
          })}
        </div>

        {/* ── Plans: selection only (no competing buy buttons) ── */}
        {loadError || plans.length === 0 ? (
          <div className="card rounded-[22px] p-10 text-center max-w-md mx-auto">
            <div className="w-12 h-12 mx-auto rounded-full bg-accent/12 border border-accent/25 text-accent flex items-center justify-center mb-4">
              <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v4m0 4h.01M10.3 3.9 2 18a2 2 0 001.7 3h16.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z" /></svg>
            </div>
            <h2 className="serif text-lg font-semibold mb-2">Тарифы недоступны</h2>
            <p className="text-sm text-text-secondary mb-5">Не удалось загрузить список тарифов. Попробуйте обновить страницу.</p>
            <button onClick={() => window.location.reload()} className="btn-primary !py-2.5 !text-sm">Обновить</button>
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-5 mb-10">
            {plans.map((plan, idx) => {
              const isSelected = selectedPlan === plan.id
              const isCurrent = currentPlanSlug === plan.slug
              const isFree = plan.price_rub === 0
              const isDowngrade = currentSortOrder >= 0 && !isCurrent && (plan.sort_order ?? 0) < currentSortOrder
              const pr = prorations[plan.id] || {}
              const blocked = pr.is_downgrade_blocked === true
              const unavailable = isDowngrade || blocked
              const validUntilDate = validUntil ? new Date(validUntil).toLocaleDateString('ru-RU') : ''

              const features = [
                protocolLabel(plan.allowed_protocols),
                `Трафик: ${formatTraffic(plan.traffic_limit_bytes)}`,
                `Срок: ${formatDuration(plan.duration_days)}`,
                `Серверов: ${plan.server_ids?.length || 'все'}`,
              ]
              const planFeatures = plan.features ? (typeof plan.features === 'string' ? JSON.parse(plan.features || '{}') : plan.features) : {}
              if (planFeatures.traffic_masking) features.push('Приватная маршрутизация')
              if (planFeatures.priority_support) features.push('Приоритетная поддержка')
              if (plan.direct_routing_enabled) features.push('Доступ к РФ-сервисам')

              return (
                <button
                  key={plan.id}
                  type="button"
                  onClick={() => !unavailable && selectPlan(plan)}
                  disabled={unavailable}
                  aria-pressed={isSelected}
                  className={`relative text-left rounded-[22px] p-6 reveal transition-all duration-300 ${
                    unavailable ? 'card opacity-50 cursor-not-allowed'
                      : isSelected ? 'card-feat cursor-pointer ring-2 ring-brand/40'
                      : 'card card-hover cursor-pointer'
                  }`}
                  style={{ transitionDelay: `${Math.min(idx * 70, 220)}ms` }}
                >
                  {isCurrent && (
                    <span className="absolute -top-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-full bg-brand text-[#04231a] text-[10px] font-bold uppercase tracking-wider whitespace-nowrap">Текущий план</span>
                  )}

                  <div className="flex items-start justify-between gap-2 mb-1">
                    <h3 className="serif text-lg font-semibold">{plan.name}</h3>
                    <span className={`flex-shrink-0 w-5 h-5 rounded-full border-2 grid place-items-center transition-colors ${isSelected ? 'border-brand bg-brand' : 'border-brand/25'}`}>
                      {isSelected && <svg className="w-3 h-3 text-[#04231a]" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={3.5}><path strokeLinecap="round" strokeLinejoin="round" d="M5 12l5 5L20 6" /></svg>}
                    </span>
                  </div>
                  <p className="text-xs text-text-secondary mb-4">{plan.description}</p>

                  {/* price — fixed-height row so async proration can't shift layout */}
                  <div className="min-h-[4.25rem] mb-4">
                    <div className="flex items-baseline gap-1.5">
                      <span className="serif text-3xl font-semibold tabular-nums">{isFree ? 'Бесплатно' : `${plan.price_rub}₽`}</span>
                      {!isFree && <span className="text-sm text-text-secondary">/ {formatDuration(plan.duration_days)}</span>}
                    </div>
                    <div className="mt-1.5 min-h-[1.5rem]">
                      {prorationsLoading && !isFree ? <Skeleton className="h-4 w-40" /> : null}
                    </div>
                  </div>

                  <ul className="space-y-2 mb-4">
                    {features.map((f, i) => (
                      <li key={i} className="flex items-start gap-2 text-sm text-text-secondary"><CheckIcon /><span>{f}</span></li>
                    ))}
                  </ul>

                  {pr.bonus_days > 0 && !unavailable && !isCurrent && (
                    <p className="text-xs text-brand">+{pr.bonus_days} бонус-дней за остаток текущего плана</p>
                  )}
                  {isCurrent && showEarlyRenewalBadge && (
                    <p className="text-xs text-accent flex items-center gap-1.5">
                      <svg className="w-3.5 h-3.5 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.8}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" strokeLinecap="round" /></svg>
                      Скоро закончится — продлите заранее
                    </p>
                  )}
                  {/* explain WHY a plan is unavailable, don't just grey it out */}
                  {unavailable && (
                    <p className="text-xs text-faint flex items-start gap-1.5">
                      <LockIcon />
                      <span>{blocked && validUntilDate ? `Переход доступен после ${validUntilDate}` : 'Сейчас у вас более старший тариф'}</span>
                    </p>
                  )}
                </button>
              )
            })}
          </div>
        )}

        {/* ── Step 2: checkout — inputs asked only once a plan is chosen ── */}
        <div ref={checkoutRef} className="scroll-mt-24">
          {done ? (
            <div className="card p-8 text-center !border-brand/30 animate-fade-in-up" role="status">
              <div className="w-14 h-14 mx-auto mb-4 rounded-full bg-brand/15 border border-brand/25 text-brand flex items-center justify-center">
                <CheckIcon className="w-8 h-8" />
              </div>
              <h2 className="serif text-xl font-semibold mb-1">{done.title}</h2>
              <p className="text-sm text-text-secondary mb-6">{done.text}</p>
              <Link to="/dashboard" className="btn-primary !py-2.5 !text-sm">В личный кабинет</Link>
            </div>
          ) : chosen ? (
            <div className="grid lg:grid-cols-[1.25fr_.9fr] gap-5 animate-fade-in-up">
              {/* left: inputs */}
              <div className="card rounded-[22px] p-6">
                <h2 className="serif text-lg font-semibold mb-1">Оформление</h2>
                <p className="text-xs text-text-secondary mb-5">Данные карты вводятся только на защищённой странице ЮKassa</p>

                <div className="space-y-4">
                  {!chosenIsFree && (
                    <div>
                      <label htmlFor="promo" className="block text-xs font-medium text-text-secondary mb-1.5">Промокод <span className="text-faint">(если есть)</span></label>
                      <div className="flex gap-2">
                        <input
                          id="promo" value={promocode} onChange={e => { setPromocode(e.target.value.toUpperCase()); setPromoError('') }}
                          placeholder="ЛЕС10"
                          className="flex-1 min-w-0 px-4 py-3 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20 font-mono uppercase tracking-wider"
                        />
                        {appliedPromo && appliedPromo === promocode.trim() ? (
                          <button onClick={() => { setAppliedPromo(''); setPromocode('') }} className="btn-ghost !py-2.5 !px-4 !text-sm">Убрать</button>
                        ) : (
                          <button onClick={() => setAppliedPromo(promocode.trim())} disabled={!promocode.trim()} className="btn-ghost !py-2.5 !px-4 !text-sm disabled:opacity-40">Применить</button>
                        )}
                      </div>
                      {promoError && <p className="text-xs text-danger mt-1.5">{promoError}</p>}
                      {!promoError && quote?.promo_message && appliedPromo && <p className="text-xs text-brand mt-1.5">{quote.promo_message}</p>}
                    </div>
                  )}

                  {!chosenIsFree && !isFreeCode && quote && !enough && (
                    <div>
                      <label htmlFor="pay-email" className="block text-xs font-medium text-text-secondary mb-1.5">Email для чека <span className="text-faint">(необязательно)</span></label>
                      <input
                        id="pay-email" value={email} onChange={e => setEmail(e.target.value)} type="email" inputMode="email" placeholder="you@mail.ru"
                        className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20"
                      />
                      <p className="text-[11px] text-faint mt-1.5">Используем только для отправки чека.</p>
                    </div>
                  )}
                </div>
              </div>

              {/* right: sticky summary + the single primary CTA */}
              <div className="card-feat rounded-[22px] p-6 lg:sticky lg:top-24 h-fit">
                <div className="flex items-center gap-3 mb-5">
                  <span className="w-11 h-11 rounded-xl bg-brand/15 border border-brand/25 text-brand flex items-center justify-center flex-shrink-0"><Tree className="w-6 h-6" /></span>
                  <div className="min-w-0">
                    <p className="serif text-lg font-semibold truncate">{chosen.name}</p>
                    <p className="text-xs text-text-secondary">подписка на {formatDuration(chosen.duration_days)}</p>
                  </div>
                </div>

                <div className="space-y-2 py-4 border-y border-brand/10 text-sm">
                  <div className="flex justify-between text-text-secondary">
                    <span>{chosen.name}</span>
                    <span className="tabular-nums">{chosenIsFree ? '0 ₽' : `${chosen.price_rub} ₽`}</span>
                  </div>
                  {quote && quote.list_price_kop > quote.price_kop && !isFreeCode && (
                    <div className="flex justify-between text-brand">
                      <span>Промокод {appliedPromo}</span>
                      <span className="tabular-nums">−{rub(quote.list_price_kop - quote.price_kop)}</span>
                    </div>
                  )}
                  {(quote?.bonus_days || chosenPr.bonus_days) > 0 && (
                    <div className="flex justify-between text-brand">
                      <span>Бонусные дни за остаток тарифа</span>
                      <span className="tabular-nums">+{quote?.bonus_days || chosenPr.bonus_days} дн.</span>
                    </div>
                  )}
                  {!chosenIsFree && !isFreeCode && quote && (
                    <div className="flex justify-between text-text-secondary">
                      <span>На кошельке</span>
                      <span className="tabular-nums">{rub(quote.balance_kop)}</span>
                    </div>
                  )}
                  {quote?.new_valid_until && !chosenIsFree && !isFreeCode && (
                    <div className="flex justify-between text-text-secondary">
                      <span>Подписка будет до</span>
                      <span className="tabular-nums">{formatDay(quote.new_valid_until)}</span>
                    </div>
                  )}
                </div>

                {chosenIsFree || isFreeCode ? (
                  <div className="flex items-baseline justify-between my-5">
                    <span className="text-sm text-text-secondary">К оплате</span>
                    <span className="serif text-3xl font-semibold">Бесплатно</span>
                  </div>
                ) : quoteLoading && !quote ? (
                  <Skeleton className="h-10 w-full my-5" />
                ) : enough ? (
                  <div className="flex items-baseline justify-between my-5">
                    <span className="text-sm text-text-secondary">Спишем с кошелька</span>
                    <span className="serif text-3xl font-semibold tabular-nums">{rub(quote.price_kop)}</span>
                  </div>
                ) : quote ? (
                  <div className="my-5">
                    <p className="text-sm text-text-secondary mb-3">
                      На кошельке {rub(quote.balance_kop)}. Пополним на нужную сумму и сразу оплатим подписку.
                    </p>
                    <div className="flex items-center gap-2 flex-wrap">
                      <input
                        aria-label="Сумма пополнения, ₽" inputMode="numeric" value={topupRub}
                        onChange={e => setTopupRub(parseInt(e.target.value.replace(/\D/g, '') || '0', 10))}
                        className="w-28 px-3 py-2.5 rounded-xl bg-white/5 border border-brand/15 text-lg font-semibold tabular-nums focus:border-brand/50 focus:outline-none"
                      />
                      <span className="text-sm text-text-secondary">₽</span>
                      <button type="button" onClick={() => setTopupRub(v => v + 100)} className="btn-ghost !py-2 !px-3 !text-sm">+100</button>
                      <button type="button" onClick={() => setTopupRub(v => v + 500)} className="btn-ghost !py-2 !px-3 !text-sm">+500</button>
                    </div>
                    {topupRub * 100 > quote.topup_kop && (
                      <p className="text-xs text-faint mt-2">После оплаты на кошельке останется {rub(quote.balance_kop + topupRub * 100 - quote.price_kop)}.</p>
                    )}
                  </div>
                ) : null}

                {error && <p className="text-sm text-danger bg-danger/10 border border-danger/20 rounded-xl px-3 py-2 mb-3">{error}</p>}

                <button onClick={submitPayment} disabled={paying || (!chosenIsFree && !quote)} className="btn-primary w-full !py-3.5 disabled:opacity-50">
                  {paying ? 'Готовим…' : ctaLabel}
                </button>

                <p className="flex items-center justify-center gap-1.5 text-[11px] text-faint mt-3.5 text-center">
                  <LockIcon /> Оплата защищена · без скрытых списаний
                </p>
              </div>
            </div>
          ) : (
            plans.length > 0 && (
              <div className="card rounded-[22px] p-8 text-center border-dashed">
                <p className="text-sm text-text-secondary">Выберите тариф выше — здесь появится оформление и итоговая сумма.</p>
              </div>
            )
          )}
        </div>

        <div className="text-center mt-10">
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">← Вернуться в личный кабинет</Link>
        </div>
      </div>

      {/* ── Sticky mobile summary bar (thumb-reachable primary action) ── */}
      {chosen && !done && (
        <div className="lg:hidden fixed bottom-0 inset-x-0 z-40 glass-strong border-t border-brand/15 px-4 py-3 flex items-center gap-3" style={{ paddingBottom: 'max(0.75rem, env(safe-area-inset-bottom))' }}>
          <div className="min-w-0 flex-1">
            <p className="text-xs text-text-secondary truncate">{chosen.name}</p>
            <p className="serif text-lg font-semibold tabular-nums leading-tight">{chosenIsFree || isFreeCode ? 'Бесплатно' : `${chosenTotal} ₽`}</p>
          </div>
          <button onClick={submitPayment} disabled={paying || (!chosenIsFree && !quote)} className="btn-primary !py-3 !px-6 disabled:opacity-50 flex-shrink-0">
            {paying ? '…' : chosenIsFree || isFreeCode ? 'Активировать' : enough ? 'Оплатить' : 'Пополнить и оплатить'}
          </button>
        </div>
      )}

      {/* Wallet confirm: what leaves the wallet, what stays, until when */}
      {walletConfirm && quote && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm animate-fade-in" onClick={() => setWalletConfirm(false)}>
          <div className="glass-strong rounded-[22px] p-7 max-w-sm w-full text-center" onClick={e => e.stopPropagation()} role="dialog" aria-modal="true">
            <h3 className="serif text-lg font-semibold mb-2">Оплатить с кошелька?</h3>
            <p className="text-sm text-text-secondary mb-6">
              Спишем {rub(quote.price_kop)} с кошелька. Останется {rub(quote.balance_kop - quote.price_kop)}.
              {quote.new_valid_until ? ` Подписка до ${formatDay(quote.new_valid_until)}.` : ''}
            </p>
            <div className="flex gap-3">
              <button onClick={() => setWalletConfirm(false)} className="btn-ghost flex-1 !py-2.5 !text-sm">Отмена</button>
              <button onClick={payFromWallet} className="btn-primary flex-1 !py-2.5 !text-sm">Оплатить</button>
            </div>
          </div>
        </div>
      )}

      {/* Switch-confirm modal */}
      {switchConfirm != null && (
        <div className="fixed inset-0 z-[100] flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm animate-fade-in" onClick={() => setSwitchConfirm(null)}>
          <div className="glass-strong rounded-[22px] p-7 max-w-sm w-full text-center" onClick={e => e.stopPropagation()}>
            <div className="w-12 h-12 mx-auto rounded-full bg-accent/12 border border-accent/25 text-accent flex items-center justify-center mb-4">
              <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.7}><path strokeLinecap="round" strokeLinejoin="round" d="M12 9v4m0 4h.01M10.3 3.9 2 18a2 2 0 001.7 3h16.6a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0z" /></svg>
            </div>
            <h3 className="serif text-lg font-semibold mb-2">Сменить тариф?</h3>
            <p className="text-sm text-text-secondary mb-6">Текущая подписка будет заменена новым тарифом. Неиспользованный остаток учтён в сумме.</p>
            <div className="flex gap-3">
              <button onClick={() => setSwitchConfirm(null)} className="btn-ghost flex-1 !py-2.5 !text-sm">Отмена</button>
              <button onClick={() => handlePay(switchConfirm)} className="btn-primary flex-1 !py-2.5 !text-sm">Продолжить</button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
