import { useId, useState } from 'react'
import { Button, Dialog, Input, Select } from '../ui/index.js'
import { manualPaymentBody, MANUAL_STATUSES } from '../../utils/manualPayment.js'

/**
 * ManualPaymentDialog — a record of money taken outside YooKassa (cash, a transfer): the user,
 * the amount in roubles, the status and a note. It does not extend a subscription; only manual
 * records are created and edited here (C7). Props: dialog {mode, item?}, onClose, onSave(body).
 */
export default function ManualPaymentDialog({ dialog, onClose, onSave }) {
  const uid = useId()
  const edit = dialog.mode === 'edit'
  const it = dialog.item || {}
  const [form, setForm] = useState({
    userDbId: edit ? String(it.user_db_id || '') : '', amount: edit ? String(it.amount ?? '') : '',
    status: edit ? it.status : 'succeeded', notes: edit ? it.notes || '' : '',
  })
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))

  async function submit(e) {
    e.preventDefault()
    const { body, error: err } = manualPaymentBody(form, dialog.mode)
    setError(err)
    if (err) return
    setSaving(true)
    try {
      await onSave(body)
    } catch (ex) {
      setError(ex?.message || 'Не удалось сохранить')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onClose={onClose} title={edit ? `Ручной платёж #${it.id}` : 'Ручной платёж'} maxWidth="max-w-md">
      <form onSubmit={submit} className="space-y-4">
        <p className="text-xs text-tertiary">
          Запись для учёта денег, принятых не через ЮKassa (наличные, перевод). Подписку она не продлевает — продлите её в карточке
          пользователя. Платежи ЮKassa здесь не правятся.
        </p>
        {!edit && (
          <div>
            <label htmlFor={`${uid}-u`} className="block text-xs text-secondary mb-1">Пользователь — ID в базе (из карточки пользователя)</label>
            <Input id={`${uid}-u`} value={form.userDbId} inputMode="numeric" onChange={(e) => set({ userDbId: e.target.value })} className="font-mono tabular-nums" autoFocus />
          </div>
        )}
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div>
            <label htmlFor={`${uid}-a`} className="block text-xs text-secondary mb-1">Сумма, ₽</label>
            <Input id={`${uid}-a`} value={form.amount} inputMode="decimal" onChange={(e) => set({ amount: e.target.value })} className="font-mono tabular-nums" />
          </div>
          <div>
            <label htmlFor={`${uid}-s`} className="block text-xs text-secondary mb-1">Статус</label>
            <Select id={`${uid}-s`} value={form.status} onChange={(e) => set({ status: e.target.value })}>
              {MANUAL_STATUSES.map((m) => <option key={m.value} value={m.value}>{m.label}</option>)}
            </Select>
          </div>
        </div>
        <div>
          <label htmlFor={`${uid}-n`} className="block text-xs text-secondary mb-1">Примечание</label>
          <Input id={`${uid}-n`} value={form.notes} onChange={(e) => set({ notes: e.target.value })} placeholder="Например: наличные, 13.11" />
        </div>
        {error && <p role="alert" className="text-sm text-danger">{error}</p>}
        <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onClose}>Отмена</Button>
          <Button type="submit" loading={saving}>{edit ? 'Сохранить' : 'Записать'}</Button>
        </div>
      </form>
    </Dialog>
  )
}
