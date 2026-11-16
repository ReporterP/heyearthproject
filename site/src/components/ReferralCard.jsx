import { useEffect, useRef, useState } from 'react'
import { apiFetch } from '../api/http.js'
import { pendingReferral, forgetReferral } from '../lib/referral.js'
import { copyText } from '../lib/clipboard.js'
import { kop } from '../lib/format.js'
import Button from './Button.jsx'
import Field, { Input } from './Field.jsx'
import Hedgehog from './Hedgehog.jsx'
import QRDialog from './QRCode.jsx'
import { useToast } from './Toast.jsx'

// «Пригласить друга» (spec 08 §8.1 item 14): the terms, three counters, «Скопировать ссылку».
// Kept from the previous cabinet: «Поделиться» (system share sheet), the link's QR code, and the
// «Код друга» field for a young account (/api/v1/referral can_bind).
export function useReferral(token) {
  const [ref, setRef] = useState(null)
  const load = () => apiFetch('/api/v1/referral', { token }).then(setRef).catch(() => setRef(null))
  useEffect(() => { if (token) load() }, [token]) // eslint-disable-line react-hooks/exhaustive-deps
  return [ref, load]
}

export default function ReferralCard({ token, referral, reload, phone }) {
  const toast = useToast()
  const [copied, setCopied] = useState(false)
  const [qr, setQr] = useState(false)
  const timer = useRef(null)
  useEffect(() => () => clearTimeout(timer.current), [])
  if (!referral || !referral.enabled) return null

  const me = kop(referral.referrer_kop).replace(/\u00a0₽$/, '')
  const friend = kop(referral.friend_kop).replace(/\u00a0₽$/, '')
  const s = referral.stats || {}

  async function copy() {
    if (await copyText(referral.link)) {
      setCopied(true)
      toast.ok('Ссылка скопирована')
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setCopied(false), 2000)
    } else {
      toast.bad('Не удалось скопировать ссылку')
    }
  }
  async function share() {
    try { await navigator.share({ title: 'HiEarth', text: `Подключайся к HiEarth — по ссылке тебе придёт ${friend} ₽ на кошелёк после первой оплаты`, url: referral.link }) } catch { /* closed */ }
  }
  const canShare = typeof navigator !== 'undefined' && !!navigator.share

  if (phone) {
    return (
      <section id="referral" className="card row gap-3">
        <Hedgehog mood="happy" size={56} style={{ flex: 'none' }} />
        <span className="t-sm" style={{ flex: 1 }}><b>Пригласить друга</b><br /><span className="subtle">{me === friend ? `По ${me} ₽ вам и ему` : `Вам ${me} ₽, ему ${friend} ₽`}</span></span>
        <Button variant="secondary" size="sm" onClick={copy} aria-live="polite">{copied ? '✓ Скопировано' : 'Ссылка'}</Button>
        {referral.can_bind && <div style={{ flexBasis: '100%' }}><BindCode token={token} onDone={reload} /></div>}
      </section>
    )
  }

  return (
    <section id="referral" className="card" style={{ display: 'flex', flexDirection: 'column' }}>
      <div className="card__head"><h2 className="card__title">Пригласить друга</h2></div>
      <p className="t-sm">
        {me === friend ? <>Друг оплатит подписку — вам и ему по <b>{me} ₽</b>.</> : <>Друг оплатит подписку — вам <b>{me} ₽</b>, ему <b>{friend} ₽</b>.</>}
      </p>
      <div className="grid g-3" style={{ margin: '16px 0', gap: 8, textAlign: 'center', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))' }}>
        <div><p className="t-h3 mono">{s.invited || 0}</p><p className="t-xs subtle">пришли</p></div>
        <div><p className="t-h3 mono">{s.paid || 0}</p><p className="t-xs subtle">оплатили</p></div>
        <div><p className={`t-h3 mono ${s.earned_kop ? 'ok' : ''}`}>{kop(s.earned_kop).replace(/\u00a0₽$/, '')}</p><p className="t-xs subtle">₽ получено</p></div>
      </div>
      <div className="stack gap-2" style={{ marginTop: 'auto' }}>
        <Button variant="secondary" block onClick={copy} aria-live="polite">{copied ? '✓ Скопировано' : 'Скопировать ссылку'}</Button>
        <div className="row gap-2" style={{ justifyContent: 'center' }}>
          {canShare && <Button variant="ghost" size="sm" onClick={share}>Поделиться</Button>}
          <Button variant="ghost" size="sm" onClick={() => setQr(true)}>QR-код</Button>
        </div>
      </div>
      {referral.can_bind && <BindCode token={token} onDone={reload} />}
      <QRDialog open={qr} onClose={() => setQr(false)} data={referral.link} title="Ссылка-приглашение" text="Отсканируйте камерой телефона — ссылка откроет страницу приглашения в HiEarth." />
    </section>
  )
}

// «Вас пригласил друг? Введите его код» — 7 days after registration, before the first payment.
function BindCode({ token, onDone }) {
  const toast = useToast()
  const [code, setCode] = useState(() => pendingReferral())
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function bind(e) {
    e.preventDefault()
    if (busy) return
    if (!code.trim()) { setErr('Укажите код друга'); return }
    setBusy(true)
    setErr('')
    try {
      const d = await apiFetch('/api/v1/referral/bind', { token, method: 'POST', body: { code: code.trim() } })
      forgetReferral()
      toast.ok(d.message || 'Код друга принят')
      onDone?.()
    } catch (e2) {
      setErr(e2.message)
    } finally {
      setBusy(false)
    }
  }
  return (
    <form onSubmit={bind} noValidate style={{ marginTop: 16, paddingTop: 16, borderTop: '1px solid var(--line)' }}>
      <Field id="friend-code" label="Вас пригласил друг? Введите его код" error={err || null} hint="Код можно указать в течение 7 дней после регистрации, до первой оплаты.">
        <div className="input-group">
          <Input id="friend-code" aria-describedby="friend-code-msg" mono value={code} placeholder="ABCD2345" autoCapitalize="characters" spellCheck={false}
            onChange={(e) => { setCode(e.target.value.toUpperCase()); setErr('') }} invalid={!!err} />
          <Button variant="secondary" type="submit" loading={busy}>Применить</Button>
        </div>
      </Field>
    </form>
  )
}
