import { useEffect, useState } from 'react'
import { apiFetch } from '../api/http.js'
import { rub } from '../api/wallet.js'
import { pendingReferral, forgetReferral } from '../lib/referral.js'
import { QR as QRCode } from './QRCode.jsx'

// «Пригласи друга» in the cabinet (cleanup plan R5): the link, copy / share / QR, the
// counters, the rules in two lines — and the «Код друга» field for a new account.
export default function ReferralCard({ token }) {
  const [ref, setRef] = useState(null)
  const [copied, setCopied] = useState(false)
  const [showQR, setShowQR] = useState(false)
  const [code, setCode] = useState(() => pendingReferral())
  const [bindMsg, setBindMsg] = useState('')
  const [bindErr, setBindErr] = useState('')
  const [busy, setBusy] = useState(false)

  function load() {
    apiFetch('/api/v1/referral', { token }).then(setRef).catch(() => setRef(null))
  }
  useEffect(load, [token])

  if (!ref || !ref.enabled) return null

  async function copy() {
    try { await navigator.clipboard.writeText(ref.link); setCopied(true); setTimeout(() => setCopied(false), 2000) } catch { /* no clipboard */ }
  }
  async function share() {
    try {
      await navigator.share({ title: 'HiEarth', text: `Подключайся к HiEarth — по ссылке ты получишь ${rub(ref.friend_kop)} на кошелёк после первой оплаты`, url: ref.link })
    } catch { /* closed */ }
  }
  async function bind(e) {
    e.preventDefault()
    setBindErr(''); setBindMsg('')
    setBusy(true)
    try {
      const d = await apiFetch('/api/v1/referral/bind', { token, method: 'POST', body: { code: code.trim() } })
      setBindMsg(d.message || 'Код принят')
      forgetReferral()
      load()
    } catch (err) {
      setBindErr(err.message)
    } finally {
      setBusy(false)
    }
  }

  const s = ref.stats || {}
  return (
    <div className="card rounded-[22px] p-6 reveal">
      <p className="eyebrow mb-2">Пригласи друга</p>
      <p className="text-sm text-text-secondary mb-4">
        Друг оплатит подписку — вам {rub(ref.referrer_kop)} и другу {rub(ref.friend_kop)} на кошелёк.
        Бонусы идут только на оплату HiEarth.
      </p>
      <div className="flex items-stretch gap-2 mb-3">
        <code className="flex-1 min-w-0 px-3 py-2 rounded-lg bg-white/5 border border-brand/10 text-xs font-mono truncate">{ref.link}</code>
        <button onClick={copy} className="btn-ghost !py-2 !px-3 !text-sm shrink-0">{copied ? '✓' : 'Копировать'}</button>
      </div>
      <div className="flex flex-wrap gap-2 mb-4">
        {typeof navigator !== 'undefined' && navigator.share && <button onClick={share} className="btn-primary !py-2 !px-4 !text-sm">Поделиться</button>}
        <button onClick={() => setShowQR(v => !v)} className="btn-ghost !py-2 !px-4 !text-sm">{showQR ? 'Скрыть QR' : 'QR-код'}</button>
      </div>
      {showQR && <div className="mb-4 flex justify-center"><QRCode data={ref.link} size={168} /></div>}
      <div className="grid grid-cols-3 gap-2 text-center">
        <div className="rounded-xl bg-white/[0.03] border border-brand/10 py-2.5">
          <p className="serif text-lg font-semibold tabular-nums">{s.invited || 0}</p>
          <p className="text-[11px] text-faint">пришли</p>
        </div>
        <div className="rounded-xl bg-white/[0.03] border border-brand/10 py-2.5">
          <p className="serif text-lg font-semibold tabular-nums">{s.paid || 0}</p>
          <p className="text-[11px] text-faint">оплатили</p>
        </div>
        <div className="rounded-xl bg-white/[0.03] border border-brand/10 py-2.5">
          <p className="serif text-lg font-semibold tabular-nums">{rub(s.earned_kop || 0)}</p>
          <p className="text-[11px] text-faint">заработано</p>
        </div>
      </div>
      {ref.can_bind && (
        <form onSubmit={bind} className="mt-5 pt-5 border-t border-brand/10">
          <label htmlFor="friend-code" className="block text-xs text-text-secondary mb-1.5">Вас пригласил друг? Введите его код</label>
          <div className="flex gap-2">
            <input id="friend-code" value={code} onChange={e => setCode(e.target.value.toUpperCase())} placeholder="ABCD2345"
              className="flex-1 min-w-0 px-4 py-2.5 rounded-xl bg-white/5 border border-brand/12 text-sm font-mono uppercase tracking-wider placeholder:text-faint focus:border-brand/50 focus:outline-none" />
            <button disabled={busy || !code.trim()} className="btn-ghost !py-2.5 !px-4 !text-sm disabled:opacity-40">Применить</button>
          </div>
          {bindErr && <p className="text-xs text-danger mt-1.5">{bindErr}</p>}
          {bindMsg && <p className="text-xs text-brand mt-1.5">{bindMsg}</p>}
          <p className="text-[11px] text-faint mt-1.5">Код можно указать в течение 7 дней после регистрации, до первой оплаты.</p>
        </form>
      )}
    </div>
  )
}
