import { useState } from 'react'
import { Badge, Button, Input } from '../ui/index.js'

// A write-only secret (like the bot token on «Алерты»): GET /settings never returns the value,
// only whether it is set, so the field always starts empty and saving sends only a newly typed
// value (the API rejects an empty secret with 400). On a failed save the typed value stays.
export default function SecretSettingField({ id, label, hint, isSet, onSave }) {
  const [value, setValue] = useState('')
  const [saving, setSaving] = useState(false)
  const hintId = hint ? `${id}-hint` : undefined

  async function save(e) {
    e.preventDefault()
    if (!value.trim() || saving) return
    setSaving(true)
    try {
      await onSave(value)
      setValue('')
    } catch {
      // onSave already showed the error; keep the typed value for another try
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="py-3" onSubmit={save}>
      <div className="flex flex-wrap items-center gap-2 mb-1">
        <label htmlFor={id} className="text-sm font-medium text-primary">{label}</label>
        <Badge variant={isSet ? 'success' : 'warning'}>{isSet ? 'задан' : 'не задан'}</Badge>
      </div>
      {hint && <p id={hintId} className="text-xs text-tertiary mb-2">{hint}</p>}
      <div className="flex flex-col sm:flex-row gap-2">
        <Input id={id} type="password" autoComplete="new-password" spellCheck={false}
          value={value} onChange={(e) => setValue(e.target.value)} aria-describedby={hintId}
          placeholder={isSet ? 'Сохранён на сервере — введите новый, чтобы заменить' : 'Введите значение'}
          className="font-mono sm:flex-1" />
        <Button type="submit" variant="secondary" disabled={!value.trim()} loading={saving} className="w-full sm:w-auto">
          Сохранить
        </Button>
      </div>
    </form>
  )
}
