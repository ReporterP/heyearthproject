import { useState } from 'react'
import { useT } from '../i18n/index.jsx'

export default function SettingCard({ label, hint, value, onSave, type = 'text' }) {
  const { t } = useT()
  const [input, setInput] = useState(value ?? '')
  const [saving, setSaving] = useState(false)

  async function handleSave() {
    setSaving(true)
    try {
      await onSave(input)
    } finally {
      setSaving(false)
    }
  }

  const changed = input !== (value ?? '')

  if (type === 'toggle') {
    return (
      <div className="flex items-center justify-between py-3">
        <div>
          <p className="text-sm font-medium">{label}</p>
          {hint && <p className="text-xs text-text-secondary">{hint}</p>}
        </div>
        <button
          onClick={() => onSave(value === 'true' ? 'false' : 'true')}
          className={`w-10 h-6 rounded-full transition relative ${value === 'true' ? 'bg-brand' : 'bg-white/20'}`}
        >
          <span className={`absolute top-0.5 w-5 h-5 rounded-full bg-white transition-transform ${value === 'true' ? 'left-[18px]' : 'left-0.5'}`} />
        </button>
      </div>
    )
  }

  return (
    <div className="py-3">
      <p className="text-sm font-medium mb-1">{label}</p>
      {hint && <p className="text-xs text-text-secondary mb-2">{hint}</p>}
      <div className="flex gap-2">
        <input
          type={type}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          className="flex-1 px-3 py-1.5 rounded-lg bg-surface-alt border border-white/10 text-text-primary text-sm focus:border-brand focus:ring-1 focus:ring-brand outline-none"
        />
        <button
          onClick={handleSave}
          disabled={!changed || saving}
          className="px-3 py-1.5 rounded-lg bg-brand text-white text-sm hover:bg-brand-dark transition disabled:opacity-50"
        >
          {saving ? t.loading : t.save}
        </button>
      </div>
    </div>
  )
}
