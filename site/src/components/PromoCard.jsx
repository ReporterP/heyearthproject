import { useRef, useState } from 'react'
import { applyPromocode } from '../api/user.js'
import { useAuth } from '../auth/AuthContext.jsx'
import Button from './Button.jsx'
import Field, { Input } from './Field.jsx'
import Hedgehog from './Hedgehog.jsx'
import { useToast } from './Toast.jsx'

// «Промокод» in the cabinet (spec 08 §8.1 item 16): POST /api/v1/payment/apply-promocode; success —
// toast + the cabinet reloads (flaps flip to the new values); a refused code — field error with the
// server's reason. `compact` — the «none» state's small empty block with «Есть промокод?» and «ОК».
export default function PromoCard({ onApplied, compact = false, mood = 'idle' }) {
  const { token } = useAuth()
  const toast = useToast()
  const [code, setCode] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const inFlight = useRef(false)

  async function submit(e) {
    e.preventDefault()
    if (inFlight.current) return
    if (!code.trim()) { setErr('Введите промокод'); return }
    inFlight.current = true
    setBusy(true)
    setErr('')
    try {
      await applyPromocode(token, code.trim())
      setCode('')
      toast.ok('Промокод активирован')
      onApplied?.()
    } catch (e2) {
      setErr(e2.message ? `Не удалось применить промокод: ${e2.message.replace(/^./, (c) => c.toLowerCase())}` : 'Не удалось применить промокод')
    } finally {
      inFlight.current = false
      setBusy(false)
    }
  }

  const form = (
    <form onSubmit={submit} noValidate style={{ width: '100%' }}>
      <Field id="promo" label="Промокод" srLabel error={err || null} hint={compact ? null : 'Есть код? Активируйте подписку или бонус.'}>
        <div className="input-group">
          <Input id="promo" aria-describedby={err || !compact ? 'promo-msg' : undefined} value={code} placeholder={compact ? 'Код' : 'Например, ЛЕТО10'} autoCapitalize="characters" spellCheck={false}
            onChange={(e) => { setCode(e.target.value.toUpperCase()); setErr('') }} invalid={!!err} />
          <Button variant="secondary" type="submit" loading={busy}>{compact ? 'ОК' : 'Активировать'}</Button>
        </div>
      </Field>
    </form>
  )

  if (compact) {
    return (
      <section className="card">
        <div className="empty" style={{ padding: '4px 0 0' }}>
          <Hedgehog mood={mood} size={96} />
          <h2 className="empty__t" style={{ fontSize: 16 }}>Есть промокод?</h2>
          {form}
        </div>
      </section>
    )
  }
  return (
    <section className="card">
      <div className="card__head"><h2 className="card__title">Промокод</h2></div>
      {form}
    </section>
  )
}
