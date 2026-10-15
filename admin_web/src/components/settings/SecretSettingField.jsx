import { useState } from 'react'
import { prepareSecret } from '../../utils/secret.js'
import { Badge, Button, Input } from '../ui/index.js'

// A write-only secret (like the bot token on «Алерты»): GET /settings never returns the value,
// only whether it is set, so the field always starts empty and saving sends only a newly typed
// value, trimmed (prepareSecret); an all-whitespace value is refused like the API refuses an
// empty one. On a failed save the typed value stays. disabledReason (e.g. no settings.write)
// locks the field and says why.
export default function SecretSettingField({ id, label, hint, isSet, onSave, disabledReason = '' }) {
  const [value, setValue] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const disabled = !!disabledReason
  const hintId = hint ? `${id}-hint` : undefined
  const errorId = error ? `${id}-error` : undefined
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined

  async function save(e) {
    e.preventDefault()
    if (disabled || !value || saving) return
    const secret = prepareSecret(value)
    if (secret.error) {
      setError(secret.error)
      return
    }
    setSaving(true)
    try {
      await onSave(secret.value)
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
          value={value} onChange={(e) => { setValue(e.target.value); setError('') }}
          aria-describedby={describedBy} aria-invalid={error ? true : undefined} error={!!error}
          disabled={disabled} title={disabledReason || undefined}
          placeholder={isSet ? 'Сохранён на сервере — введите новый, чтобы заменить' : 'Введите значение'}
          className="font-mono sm:flex-1" />
        <Button type="submit" variant="secondary" disabled={disabled || !value} loading={saving}
          title={disabledReason || undefined} className="w-full sm:w-auto">
          Сохранить
        </Button>
      </div>
      {error && <p id={errorId} role="alert" className="text-xs text-danger mt-1">{error}</p>}
    </form>
  )
}
