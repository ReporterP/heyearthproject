import { useEffect, useRef, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import PaymentResult from '../components/PaymentResult.jsx'

// YooKassa widget mounts here. Платёж и confirmation_token создаёт предыдущая страница
// PaymentPage и передаёт через location state или sessionStorage.
//
// Документация: https://yookassa.ru/developers/payment-acceptance/integration-scenarios/widget/basics
// Скрипт: https://yookassa.ru/checkout-widget/v1/checkout-widget.js

const YOOKASSA_SCRIPT = 'https://yookassa.ru/checkout-widget/v1/checkout-widget.js'

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const existing = document.querySelector(`script[src="${src}"]`)
    if (existing) {
      // Скрипт уже добавлен — если globals ещё не готовы, ждём
      if (window.YooMoneyCheckoutWidget) { resolve(); return }
      existing.addEventListener('load', () => resolve())
      existing.addEventListener('error', () => reject(new Error('Не удалось загрузить виджет')))
      // На случай если load уже произошёл до регистрации listener — полинг
      const poll = setInterval(() => {
        if (window.YooMoneyCheckoutWidget) {
          clearInterval(poll); resolve()
        }
      }, 100)
      setTimeout(() => { clearInterval(poll); if (!window.YooMoneyCheckoutWidget) reject(new Error('Таймаут загрузки виджета')) }, 10000)
      return
    }
    const s = document.createElement('script')
    s.src = src
    s.async = true
    s.onload = () => {
      // Ждём пока YooMoneyCheckoutWidget появится в window
      const poll = setInterval(() => {
        if (window.YooMoneyCheckoutWidget) { clearInterval(poll); resolve() }
      }, 50)
      setTimeout(() => { clearInterval(poll); if (!window.YooMoneyCheckoutWidget) reject(new Error('Виджет не инициализировался')) }, 10000)
    }
    s.onerror = () => reject(new Error('Не удалось загрузить виджет ЮKassa'))
    document.body.appendChild(s)
  })
}

export default function CheckoutPage() {
  const { paymentId } = useParams()
  const { token: authToken } = useAuth()
  const containerRef = useRef(null)
  const widgetRef = useRef(null)

  // loading | ready | error | result (the payment is being or was paid: PaymentResult polls)
  const [status, setStatus] = useState('loading')
  const [resumed, setResumed] = useState(false)
  const [error, setError] = useState('')
  const [planName, setPlanName] = useState('')

  useEffect(() => {
    // Токен + название тарифа передаёт PaymentPage через sessionStorage
    const token = sessionStorage.getItem(`checkout_token_${paymentId}`)
    const plan = sessionStorage.getItem(`checkout_plan_${paymentId}`) || ''
    setPlanName(plan)

    if (!token) {
      // The widget token is gone (refresh / new tab). The payment still exists: show
      // its real state (paid — the result; not paid — a way to start again).
      setResumed(true)
      setStatus('result')
      return
    }

    let cancelled = false

    loadScript(YOOKASSA_SCRIPT).then(() => {
      if (cancelled) return
      if (!window.YooMoneyCheckoutWidget) {
        setError('Виджет ЮKassa не доступен. Проверьте соединение и попробуйте снова.')
        setStatus('error')
        return
      }

      try {
        const checkout = new window.YooMoneyCheckoutWidget({
          confirmation_token: token,
          return_url: window.location.origin + `/wallet?topup=${paymentId}`,
          error_callback(e) {
            console.error('[yookassa-widget]', e)
            setError(e?.error?.description || 'Ошибка оплаты')
            setStatus('error')
          },
          customization: {
            colors: {
              controlPrimary: '#10b981', // brand emerald («Лес в ночи»)
              background: '#0a130f',     // ночной лес
            },
          },
        })

        // success / complete: the client paid. The widget closes and PaymentResult
        // polls our API until the webhook has credited the wallet (and bought the plan).
        const done = () => {
          sessionStorage.removeItem(`checkout_token_${paymentId}`)
          sessionStorage.removeItem(`checkout_plan_${paymentId}`)
          try { checkout.destroy() } catch { /* already gone */ }
          setStatus('result')
        }
        checkout.on('success', done)
        checkout.on('complete', done)

        checkout.on('fail', () => {
          setError('Оплата не прошла. Попробуйте другую карту или способ.')
          setStatus('error')
        })

        checkout.render('yookassa-checkout-container')
        widgetRef.current = checkout
        setStatus('ready')
      } catch (e) {
        console.error('[checkout]', e)
        setError(e.message || 'Не удалось инициализировать виджет')
        setStatus('error')
      }
    }).catch((e) => {
      if (!cancelled) {
        setError(e.message)
        setStatus('error')
      }
    })

    return () => {
      cancelled = true
      try { widgetRef.current?.destroy() } catch {}
    }
  }, [paymentId])

  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-xl mx-auto px-4 sm:px-6">
        <div className="flex items-center justify-between mb-8">
          <div>
            <h1 className="serif text-2xl md:text-3xl font-semibold">Оплата</h1>
            {planName && (
              <p className="text-sm text-text-secondary mt-1">Тариф «{planName}»</p>
            )}
          </div>
          <Link to="/payment" className="text-sm text-text-secondary hover:text-brand transition">
            ← К выбору тарифа
          </Link>
        </div>

        {status === 'loading' && (
          <div className="card p-8 flex items-center justify-center min-h-[400px]">
            <div className="text-center">
              <div className="inline-block w-10 h-10 rounded-full border-2 border-brand/25 border-t-brand animate-spin mb-3" />
              <p className="text-sm text-text-secondary">Подготавливаем форму оплаты…</p>
            </div>
          </div>
        )}

        {status === 'error' && (
          <div className="card p-6 !border-danger/30">
            <div className="flex items-start gap-3">
              <div className="w-10 h-10 rounded-full bg-danger/15 flex items-center justify-center shrink-0">
                <svg className="w-5 h-5 text-danger" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
                  <path strokeLinecap="round" strokeLinejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
              <div className="flex-1">
                <h2 className="serif font-semibold mb-1">Не удалось открыть оплату</h2>
                <p className="text-sm text-text-secondary mb-2">{error}</p>
                <p className="text-xs text-faint mb-4">
                  Деньги не списаны. Если платёж уже был создан, его можно продолжить или проверить статус в истории.
                </p>
                <div className="flex flex-wrap gap-2.5">
                  <Link to="/payment" className="btn-primary !py-2.5 !text-sm">Выбрать тариф заново</Link>
                  {paymentId && <Link to={`/payments/${paymentId}`} className="btn-ghost !py-2.5 !text-sm">Открыть этот платёж</Link>}
                  <Link to="/payments" className="btn-ghost !py-2.5 !text-sm">История платежей</Link>
                </div>
              </div>
            </div>
          </div>
        )}

        {status === 'result' && <PaymentResult token={authToken} paymentId={paymentId} resumeHint={resumed} />}

        {/* Container для виджета — существует всегда чтобы YooKassa могла его найти */}
        <div
          id="yookassa-checkout-container"
          ref={containerRef}
          className={status === 'ready' ? 'glass-card rounded-2xl p-4 md:p-6 min-h-[600px]' : 'hidden'}
        />

        <p className="text-xs text-text-secondary text-center mt-6">
          Оплата проходит через защищённую систему ЮKassa. Мы не получаем данные вашей карты.
        </p>
      </div>
    </section>
  )
}
