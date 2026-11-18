import { useEffect, useId, useState } from 'react'
import { Button, Input, Switch } from '../ui/index.js'
import { checkSetting, shownValue } from './settingsModel.js'

/**
 * SettingField — one key of «Настройки»: a switch saved at once, or a field with «Сохранить»
 * (checked as the API checks it, settingsModel.js). canEdit=false: read-only.
 */
export default function SettingField({ field, settings, canEdit, onSave }) {
  const id = useId()
  const current = shownValue(field, settings)
  const [value, setValue] = useState(current)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  useEffect(() => { setValue(current); setError('') }, [current])

  async function save(v) {
    const { value: checked, error: err } = checkSetting(field, v)
    setError(err)
    if (err) return
    setSaving(true)
    try {
      await onSave(field.key, checked)
    } catch (e) {
      setError(e?.message || 'Не удалось сохранить')
    } finally {
      setSaving(false)
    }
  }

  if (field.kind === 'bool') {
    return (
      <div className="py-3">
        <Switch checked={current === 'true'} disabled={!canEdit || saving} label={field.label} description={field.hint}
          onChange={(on) => save(on ? 'true' : 'false')} />
        {error && <p role="alert" className="text-xs text-danger mt-1">{error}</p>}
      </div>
    )
  }
  const numeric = field.kind === 'int'
  return (
    <form className="py-3" onSubmit={(e) => { e.preventDefault(); save(value) }}>
      <label htmlFor={id} className="block text-sm text-primary mb-1">{field.label}</label>
      {field.hint && <p className="text-xs text-tertiary mb-2">{field.hint}</p>}
      <div className="flex flex-col sm:flex-row gap-2">
        <Input id={id} value={value} disabled={!canEdit || saving} inputMode={numeric ? 'numeric' : undefined}
          onChange={(e) => { setValue(e.target.value); setError('') }} aria-invalid={error ? true : undefined}
          className={`w-full sm:flex-1 ${numeric || field.kind === 'host' || field.kind === 'inn' || field.kind === 'slug' ? 'font-mono tabular-nums' : ''}`} />
        {canEdit && <Button type="submit" variant="secondary" loading={saving} disabled={value === current} className="w-full sm:w-auto">Сохранить</Button>}
      </div>
      {error && <p role="alert" className="text-xs text-danger mt-1">{error}</p>}
    </form>
  )
}
