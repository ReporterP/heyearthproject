import { useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { cancelPayment } from '../../api/admin.js'
import { REFUND_CONFIRM_WORD, formatRub, refundableAmount, validateRefund } from '../../utils/refund.js'
import { Button, Dialog, Input } from '../ui/index.js'

// Refund through YooKassa: an amount (up to what is left), a reason and the typed word ВОЗВРАТ.
// Money leaves the account and cannot be taken back; the backend also requires payments.refund.
export default function RefundDialog({ payment, onClose, onDone }) {
  const max = refundableAmount(payment)
  const [amountText, setAmountText] = useState(String(max).replace('.', ','))
  const [reason, setReason] = useState('Возврат по обращению пользователя')
  const [confirmText, setConfirmText] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(e) {
    e.preventDefault()
    const v = validateRefund({ amountText, maxAmount: max, confirmText })
    if (!v.ok) {
      setError(v.error)
      return
    }
    setSaving(true)
    setError('')
    try {
      const res = await cancelPayment(payment.id, reason.trim() || 'Возврат', v.amount)
      onDone(`Возврат ${formatRub(v.amount)} создан, статус ЮKassa: ${res.status || 'pending'}`)
    } catch (err) {
      setError(err.message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onClose={onClose} title={`Возврат по платежу #${payment.id}`} icon={<RotateCcw className="w-4 h-4" />} maxWidth="max-w-md">
      <form onSubmit={submit} className="space-y-4">
        <p className="text-sm text-secondary">
          Деньги вернутся плательщику через ЮKassa, отменить возврат нельзя. Доступно к возврату:{' '}
          <span className="font-mono tabular-nums text-primary">{formatRub(max)}</span>.
        </p>
        <div>
          <label htmlFor="refund-amount" className="text-xs text-tertiary block mb-1">Сумма, ₽</label>
          <Input id="refund-amount" inputMode="decimal" className="font-mono tabular-nums" value={amountText}
            onChange={(e) => setAmountText(e.target.value)} />
        </div>
        <div>
          <label htmlFor="refund-reason" className="text-xs text-tertiary block mb-1">Причина (видна в ЮKassa)</label>
          <Input id="refund-reason" value={reason} onChange={(e) => setReason(e.target.value)} />
        </div>
        <div>
          <label htmlFor="refund-confirm" className="text-xs text-tertiary block mb-1">Для подтверждения введите {REFUND_CONFIRM_WORD}</label>
          <Input id="refund-confirm" autoComplete="off" value={confirmText} onChange={(e) => setConfirmText(e.target.value)} />
        </div>
        {error && <p className="text-sm text-danger">{error}</p>}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>Отмена</Button>
          <Button type="submit" variant="danger" loading={saving}>Вернуть деньги</Button>
        </div>
      </form>
    </Dialog>
  )
}
