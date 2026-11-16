import { useEffect, useState } from 'react'
import { useNavigate, useSearchParams, Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { getEmailPrefs } from '../api/user.js'
import { getWallet, walletTopup, rub, formatDay } from '../api/wallet.js'
import { renewLine } from '../components/WalletCard.jsx'
import PaymentResult from '../components/PaymentResult.jsx'
import LoadErrorCard from '../components/LoadErrorCard.jsx'
import { CabinetSkeleton } from '../legacy/LegacySkeleton.jsx'

const PRESETS = [300, 900, 3000]

function TxRow({ tx }) {
  const plus = tx.amount_kop > 0
  return (
    <li className="flex items-start justify-between gap-4 py-3 border-b border-brand/8 last:border-0">
      <div className="min-w-0">
        <p className="text-sm">{tx.title}</p>
        <p className="text-xs text-faint mt-0.5">{new Date(tx.created_at).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })}</p>
      </div>
      <div className="text-right flex-shrink-0">
        <p className={`text-sm font-semibold tabular-nums ${plus ? 'text-brand' : ''}`}>{plus ? '+' : '−'}{rub(Math.abs(tx.amount_kop))}</p>
        <p className="text-xs text-faint tabular-nums">остаток {rub(tx.balance_after_kop)}</p>
      </div>
    </li>
  )
}

export default function WalletPage() {
  const { token } = useAuth()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const returned = params.get('topup')

  const [wallet, setWallet] = useState(null)
  const [loadError, setLoadError] = useState('')
  const [amount, setAmount] = useState(PRESETS[0])
  const [custom, setCustom] = useState('')
  const [email, setEmail] = useState('')
  // the receipt address defaults to the one confirmed in the cabinet (E8)
  useEffect(() => {
    if (!token) return
    getEmailPrefs(token).then((p) => { if (p?.email) setEmail((cur) => cur || p.email) }).catch(() => {})
  }, [token])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    getWallet(token).then(setWallet).catch(e => { if (e.kind !== 'auth') setLoadError(e.message) })
  }, [token, returned])

  if (loadError) return <LoadErrorCard message={loadError} />
  if (!wallet) return <CabinetSkeleton />

  const min = wallet.min_topup_rub || 100
  const sum = custom ? parseInt(custom, 10) || 0 : amount
  const line = renewLine(wallet.auto_renew)

  async function topup() {
    setError('')
    if (sum < min) { setError(`Минимальная сумма пополнения — ${min} ₽`); return }
    if (sum > 100000) { setError('Не больше 100 000 ₽ за раз'); return }
    setBusy(true)
    try {
      const d = await walletTopup(token, { amountRub: sum, email: email.trim(), confirmationType: 'embedded' })
      if (d.confirmation_token) {
        sessionStorage.setItem(`checkout_token_${d.payment_id}`, d.confirmation_token)
        sessionStorage.setItem(`checkout_plan_${d.payment_id}`, '')
        navigate(`/checkout/${d.payment_id}`)
        return
      }
      if (d.confirmation_url) { window.location.href = d.confirmation_url; return }
      setError('Не удалось открыть оплату — попробуйте ещё раз.')
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="relative py-10 md:py-16">
      <div className="relative max-w-2xl mx-auto px-4 sm:px-6 space-y-5">
        <div className="flex items-center justify-between">
          <h1 className="serif text-2xl md:text-3xl font-semibold">Кошелёк</h1>
          <Link to="/dashboard" className="text-sm text-text-secondary hover:text-brand transition">← В кабинет</Link>
        </div>

        {returned && <PaymentResult token={token} paymentId={returned} />}

        <div className="card-feat rounded-[22px] p-6">
          <p className="text-sm text-text-secondary">На кошельке</p>
          <p className="serif text-4xl font-semibold tabular-nums mt-1">{rub(wallet.balance_kop)}</p>
          {line && <p className={`text-sm mt-3 ${line.tone === 'warn' ? 'text-accent' : 'text-text-secondary'}`}>{line.text}</p>}
          {wallet.auto_renew && !wallet.auto_renew.free && (
            <p className="text-xs text-faint mt-2">Включить или выключить автопродление — в разделе <Link to="/payments" className="text-brand hover:text-brand-light">«Оплата и подписка»</Link>.</p>
          )}
        </div>

        <div id="topup" className="card rounded-[22px] p-6 scroll-mt-24">
          <h2 className="serif text-lg font-semibold mb-4">Пополнить</h2>
          <div className="grid grid-cols-3 gap-2 mb-3">
            {PRESETS.map(p => (
              <button key={p} type="button" onClick={() => { setAmount(p); setCustom('') }}
                className={`py-3 rounded-xl border text-sm font-semibold tabular-nums transition ${!custom && amount === p ? 'border-brand bg-brand/12 text-brand' : 'border-brand/12 hover:border-brand/30'}`}>
                {p.toLocaleString('ru-RU')} ₽
              </button>
            ))}
          </div>
          <label htmlFor="custom-sum" className="block text-xs text-text-secondary mb-1.5">Своя сумма</label>
          <input id="custom-sum" inputMode="numeric" value={custom} onChange={e => setCustom(e.target.value.replace(/\D/g, '').slice(0, 6))}
            placeholder={`от ${min} ₽`}
            className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20 mb-3" />
          <label htmlFor="topup-email" className="block text-xs text-text-secondary mb-1.5">Email для чека <span className="text-faint">(необязательно)</span></label>
          <input id="topup-email" type="email" inputMode="email" value={email} onChange={e => setEmail(e.target.value)} placeholder="you@mail.ru"
            className="w-full px-4 py-3 rounded-xl bg-white/5 border border-brand/12 text-sm placeholder:text-faint focus:border-brand/50 focus:outline-none focus:ring-2 focus:ring-brand/20" />
          <p className="text-[11px] text-faint mt-1.5 mb-4">Используем только для отправки чека.</p>
          {error && <p className="text-sm text-danger bg-danger/10 border border-danger/20 rounded-xl px-3 py-2 mb-3">{error}</p>}
          <button onClick={topup} disabled={busy || sum <= 0} className="btn-primary w-full !py-3.5 disabled:opacity-50">
            {busy ? 'Готовим оплату…' : `Пополнить на ${sum > 0 ? sum.toLocaleString('ru-RU') : '…'} ₽`}
          </button>
          <p className="text-[11px] text-faint text-center mt-3">Оплата картой или через СБП на защищённой странице ЮKassa.</p>
        </div>

        <div className="card rounded-[22px] p-6">
          <h2 className="serif text-lg font-semibold mb-2">Что такое кошелёк</h2>
          <ul className="text-sm text-text-secondary space-y-1.5 list-disc pl-5">
            <li>Вы пополняете кошелёк картой, а подписка оплачивается с него — сразу или автопродлением.</li>
            <li>Если денег не хватает, при оплате тарифа мы пополним кошелёк ровно на недостающую сумму.</li>
            <li>Деньги на кошельке идут только на услуги HiEarth; подробности — в <Link to="/offer#balance" className="text-brand hover:text-brand-light">оферте</Link>.</li>
          </ul>
        </div>

        <div className="card rounded-[22px] p-6">
          <h2 className="serif text-lg font-semibold mb-2">История</h2>
          {wallet.transactions.length === 0 ? (
            <p className="text-sm text-text-secondary">Пока пусто. Пополните кошелёк — и здесь появятся пополнения и оплаты.</p>
          ) : (
            <ul>{wallet.transactions.map(tx => <TxRow key={tx.id} tx={tx} />)}</ul>
          )}
        </div>
        {wallet.auto_renew && <p className="text-xs text-faint text-center">Подписка действует до {formatDay(wallet.auto_renew.renew_at)}.</p>}
      </div>
    </section>
  )
}
