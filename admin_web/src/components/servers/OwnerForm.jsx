import { useEffect, useId, useState } from 'react'
import { saveServerOwner } from '../../api/admin.js'
import { useToast } from '../Toast.jsx'
import { Button, Input } from '../ui/index.js'
import { OWNER_FIELDS, ownerForm, ownerBody } from './serversModel.js'

/**
 * OwnerForm — the numbers only the owner knows (hoster's traffic quota, price, port speed, cores):
 * «Мощность» counts the quota, the N+1 reserve and the cost per user with them.
 * PUT /servers/{id}/capacity (servers.write); id 'hub' = the Hub. detectedCores: the hint.
 */
export default function OwnerForm({ id, owner, detectedCores, canEdit, onSaved }) {
  const toast = useToast()
  const uid = useId()
  const [form, setForm] = useState(() => ownerForm(owner))
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const ownerKey = JSON.stringify(owner || {})
  useEffect(() => { setForm(ownerForm(owner)); setError('') }, [ownerKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const dirty = JSON.stringify(form) !== JSON.stringify(ownerForm(owner))
  async function save(e) {
    e.preventDefault()
    const { body, error: err } = ownerBody(form)
    setError(err)
    if (err) return
    setSaving(true)
    try {
      await saveServerOwner(id, body)
      toast.success('Сохранено — «Мощность» пересчитает вердикт')
      await onSaved?.()
    } catch (ex) {
      setError(ex?.message || 'Не удалось сохранить')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={save} className="space-y-3">
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
        {OWNER_FIELDS.map((f) => (
          <div key={f.key}>
            <label htmlFor={`${uid}-${f.key}`} className="block text-xs text-secondary mb-1">{f.label}</label>
            <Input id={`${uid}-${f.key}`} value={form[f.key]} inputMode={f.decimal ? 'decimal' : 'numeric'} disabled={!canEdit || saving}
              placeholder={f.key === 'cpu_cores' && detectedCores ? String(detectedCores) : f.key === 'port_mbps' ? '1000' : ''}
              onChange={(e) => setForm({ ...form, [f.key]: e.target.value })} className="font-mono tabular-nums" />
            <p className="text-xs text-tertiary mt-1">
              {f.key === 'cpu_cores' && detectedCores ? `Пусто — ${detectedCores} (видит сервер)` : f.hint}
            </p>
          </div>
        ))}
      </div>
      {error && <p role="alert" className="text-sm text-danger">{error}</p>}
      {canEdit ? (
        <div className="flex flex-col sm:flex-row gap-2">
          <Button type="submit" loading={saving} disabled={!dirty || saving}>Сохранить</Button>
          {dirty && <Button type="button" variant="ghost" onClick={() => { setForm(ownerForm(owner)); setError('') }}>Отменить</Button>}
        </div>
      ) : (
        <p className="text-xs text-tertiary">Изменить может администратор с правом «Servers Write».</p>
      )}
    </form>
  )
}
