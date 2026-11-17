import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { resumeTopup } from '../api/wallet.js'
import PayHeader from '../components/PayHeader.jsx'
import Alert from '../components/Alert.jsx'
import Button from '../components/Button.jsx'
import Flap from '../components/Flap.jsx'
import useMedia from '../hooks/useMedia.js'
import usePageMeta from '../hooks/usePageMeta.js'
import { kop, kopNum } from '../lib/format.js'

// Оплата /checkout/:paymentId (spec 09 §9.3, artboard «Оплата»): the embedded ЮKassa widget on a white
// plate, the amount on the right. The confirmation token comes from the previous step (sessionStorage,
// this tab only); after a reload or in a new tab the API continues the same payment (resume); a payment
// that cannot go on any more goes straight to its result. No primary button of ours: the widget's
// button confirms the payment.
const SCRIPT = 'https://yookassa.ru/checkout-widget/v1/checkout-widget.js'
const READY_TIMEOUT_MS = 10000

// loads the widget script once; a failed tag is removed so «Открыть этот платёж» really retries
function loadWidget(isCancelled) {
  return new Promise((resolve, reject) => {
    if (window.YooMoneyCheckoutWidget) { resolve(); return }
    let s = document.querySelector(`script[src="${SCRIPT}"]`)
    if (!s) {
      s = document.createElement('script')
      s.src = SCRIPT
      s.async = true
      document.body.appendChild(s)
    }
    const started = Date.now()
    const fail = (why) => { clearInterval(poll); s.remove(); reject(new Error(why)) }
    const poll = setInterval(() => {
      if (isCancelled()) { clearInterval(poll); reject(new Error('cancelled')); return }
      if (window.YooMoneyCheckoutWidget) { clearInterval(poll); resolve() }
      else if (Date.now() - started > READY_TIMEOUT_MS) fail('timeout')
    }, 60)
    s.addEventListener('error', () => fail('load'), { once: true })
  })
}

function readMeta(id) {
  try { return JSON.parse(sessionStorage.getItem(`checkout_meta_${id}`) || 'null') || {} } catch { return {} }
}

export default function CheckoutPage() {
  const { paymentId } = useParams()
  const { token } = useAuth()
  const navigate = useNavigate()
  const narrow = useMedia('(max-width: 1024px)')
  usePageMeta('Оплата — HiEarth', { noindex: true })
  const [state, setState] = useState('loading') // loading | ready | error
  const [attempt, setAttempt] = useState(0)
  const [declined, setDeclined] = useState(false)
  const widget = useRef(null)
  const planName = sessionStorage.getItem(`checkout_plan_${paymentId}`) || ''
  const meta = readMeta(paymentId)
  const toResult = () => navigate(`/wallet?topup=${paymentId}`, { replace: true })

  useEffect(() => {
    let cancelled = false
    setState('loading')
    const saved = attempt === 0 ? sessionStorage.getItem(`checkout_token_${paymentId}`) : null
    const tokenReady = saved
      ? Promise.resolve({ confirmation_token: saved })
      : resumeTopup(token, paymentId).catch((e) => (e.status === 409 || e.status === 404 ? {} : { error: e }))

    tokenReady.then((res) => {
      if (cancelled) return null
      if (res.error) { setState('error'); return null }
      if (res.confirmation_url) { window.location.assign(res.confirmation_url); return null }
      if (!res.confirmation_token) { toResult(); return null } // paid, canceled or expired: show what happened
      sessionStorage.setItem(`checkout_token_${paymentId}`, res.confirmation_token)
      return loadWidget(() => cancelled).then(() => {
        if (cancelled) return
        try {
          const w = new window.YooMoneyCheckoutWidget({
            confirmation_token: res.confirmation_token,
            return_url: `${window.location.origin}/wallet?topup=${paymentId}`,
            // the widget is drawn by ЮKassa on its white plate; our accent for its buttons (01 §10)
            customization: { colors: { controlPrimary: '#22C55E', controlPrimaryContent: '#052E14', background: '#FFFFFF' } },
            error_callback() { if (!cancelled) setState('error') },
          })
          const done = () => {
            sessionStorage.removeItem(`checkout_token_${paymentId}`)
            try { w.destroy() } catch { /* gone */ }
            toResult()
          }
          w.on('success', done)
          w.on('complete', done)
          // a declined card: the widget stays, the person can try another card or СБП
          w.on('fail', () => { if (!cancelled) setDeclined(true) })
          w.render('yookassa-checkout-container')
          widget.current = w
          setState('ready')
        } catch {
          setState('error')
        }
      }).catch(() => { if (!cancelled) setState('error') })
    })
    return () => {
      cancelled = true
      try { widget.current?.destroy() } catch { /* gone */ }
      widget.current = null
    }
  }, [paymentId, token, attempt]) // eslint-disable-line react-hooks/exhaustive-deps

  const amount = meta.amount_kop ? kopNum(meta.amount_kop) : null
  const aside = (
    <aside className="stack gap-3">
      {amount !== null && (
        <div className="card card--elevated">
          <span className="t-label">Пополнение кошелька</span>
          <div style={{ marginTop: 10 }}><Flap value={amount} suffix="₽" size={40} label={`${amount} рублей`} /></div>
          {planName && meta.price_kop ? <p className="t-sm muted" style={{ marginTop: 12 }}>После оплаты сразу спишем {kop(meta.price_kop)} с кошелька за «{planName}».</p> : null}
        </div>
      )}
      {declined && state === 'ready' && <Alert tone="bad" title="Оплата не прошла." rise>Деньги не списаны. Попробуйте другую карту или способ.</Alert>}
      <Alert tone="plain" icon="shield">Данные карты вводятся только в форме ЮKassa. HiEarth их не видит и не хранит.</Alert>
      {state === 'error' && (
        <div className="card stack gap-3">
          <Alert tone="bad" title="Не удалось связаться с оплатой.">Обновите страницу через минуту.</Alert>
          <div className="row wrap gap-2">
            <Button variant="secondary" size="sm" onClick={() => setAttempt((a) => a + 1)}>Открыть этот платёж</Button>
            <Button variant="ghost" size="sm" to="/payment">Выбрать тариф заново</Button>
          </div>
        </div>
      )}
    </aside>
  )

  return (
    <>
      <PayHeader step={2} back={{ to: meta.slug ? `/payment?plan=${encodeURIComponent(meta.slug)}` : '/payment', label: 'Оформление' }} planSlug={meta.slug} />
      <main id="main" className="container" style={{ maxWidth: 1040, paddingTop: 48, paddingBottom: 80 }}>
        <h1 className="sr">Оплата</h1>
        <div className="grid" style={{ gridTemplateColumns: narrow ? 'minmax(0,1fr)' : 'minmax(0, 1fr) 340px', gap: 24, alignItems: 'start' }}>
          {narrow && aside}
          <div className="widget" role="region" aria-label="Форма оплаты ЮKassa" aria-busy={state === 'loading'} style={{ position: 'relative', padding: state === 'ready' ? 8 : 28 }}>
            {state === 'loading' && <div style={{ position: 'absolute', inset: 0, display: 'grid', placeItems: 'center' }}><span className="spinner" role="status" aria-label="Готовим оплату" /></div>}
            <div id="yookassa-checkout-container" />
          </div>
          {!narrow && aside}
        </div>
      </main>
    </>
  )
}
