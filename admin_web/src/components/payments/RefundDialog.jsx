import { useRef, useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { cancelPayment } from '../../api/admin.js'
import {
  REFUND_CONFIRM_WORD, closeUnlessSaving, formatRub, refundOutcome, refundableAmount, validateRefund,
} from '../../utils/refund.js'
import { Button, Dialog, Input } from '../ui/index.js'

// YooKassa's limit for a refund description (the backend refuses longer reasons too).
const REASON_MAX = 250

// Refund through YooKassa: an amount (up to what the server says is left), a reason and the
// typed word ВОЗВРАТ. Money leaves the account and cannot be taken back; the backend also
// requires payments.refund.
//   onDone(message, level)  — the request is settled (level 'success' | 'warn'): close + reload.
//   onRefresh()             — reload the list, the dialog stays open (5xx / no connection: the
//                             amount may be reserved; `payment` then comes back refreshed).
export default function RefundDialog({ payment, onClose, onDone, onRefresh }) {
  const max = refundableAmount(payment)
  const [amountText, setAmountText] = useState(String(max).replace('.', ','))
  const [reason, setReason] = useState('Возврат по обращению пользователя')
  const [confirmText, setConfirmText] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  // A ref, not `saving`: two submits in the same tick both see the stale state.
  const inFlight = useRef(false)

  async function submit(e) {
    e.preventDefault()
    if (inFlight.current) return
    const v = validateRefund({ amountText, maxAmount: max, confirmText })
    if (!v.ok) {
      setError(v.error)
      return
    }
    inFlight.current = true
    setSaving(true)
    setError('')
    let answer
    try {
      answer = { data: await cancelPayment(payment.id, reason.trim() || 'Возврат', v.amount) }
    } catch (err) {
      answer = { error: err }
    }
    inFlight.current = false
    setSaving(false)
    const out = refundOutcome(answer, v.amount)
    if (out.close) {
      onDone(out.message, out.level)
      return
    }
    setError(out.error)
    if (out.reload) onRefresh?.()
  }

  const close = closeUnlessSaving(saving, onClose)

  return (
    <Dialog open onClose={close} title={`Возврат по платежу #${payment.id}`} icon={<RotateCcw className="w-4 h-4" />} maxWidth="max-w-md">
      <form onSubmit={submit} className="space-y-4">
        <p className="text-sm text-secondary">
          Деньги вернутся плательщику через ЮKassa, отменить возврат нельзя. Доступно к возврату:{' '}
          <span className="font-mono tabular-nums text-primary">{formatRub(max)}</span>.
        </p>
        {max <= 0 && (
          <p className="text-sm text-warning">
            Возвращать нечего: сумма уже возвращена или зарезервирована незавершённым возвратом.
          </p>
        )}
        <div>
          <label htmlFor="refund-amount" className="text-xs text-tertiary block mb-1">Сумма, ₽</label>
          <Input id="refund-amount" inputMode="decimal" className="font-mono tabular-nums" value={amountText}
            disabled={saving} onChange={(e) => setAmountText(e.target.value)} />
        </div>
        <div>
          <label htmlFor="refund-reason" className="text-xs text-tertiary block mb-1">Причина (видна в ЮKassa)</label>
          <Input id="refund-reason" maxLength={REASON_MAX} value={reason} disabled={saving}
            onChange={(e) => setReason(e.target.value)} />
        </div>
        <div>
          <label htmlFor="refund-confirm" className="text-xs text-tertiary block mb-1">Для подтверждения введите {REFUND_CONFIRM_WORD}</label>
          <Input id="refund-confirm" autoComplete="off" value={confirmText} disabled={saving}
            onChange={(e) => setConfirmText(e.target.value)} />
        </div>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" disabled={saving} onClick={close}>Отмена</Button>
          <Button type="submit" variant="danger" loading={saving} disabled={max <= 0}>Вернуть деньги</Button>
        </div>
      </form>
    </Dialog>
  )
}
