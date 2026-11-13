import { useT } from '../i18n/index.jsx'

export default function CrudDialog({ open, title, onClose, onSubmit, loading, children }) {
  const { t } = useT()
  if (!open) return null

  function handleSubmit(e) {
    e.preventDefault()
    onSubmit()
  }

  return (
    <div className="fixed inset-0 z-40 flex items-center justify-center bg-overlay/80" onClick={onClose}>
      <div
        className="bg-elevated rounded-lg border border-default w-full max-w-md max-h-[80vh] overflow-y-auto m-4"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-4 border-b border-default">
          <h2 className="text-lg font-semibold">{title}</h2>
          <button onClick={onClose} className="text-secondary hover:text-primary transition">
            <svg className="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}><path strokeLinecap="round" strokeLinejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
          </button>
        </div>
        <form onSubmit={handleSubmit} className="px-5 py-4 space-y-4">
          {children}
          <div className="flex justify-end gap-2 pt-2">
            <button type="button" onClick={onClose} className="px-4 py-2 rounded-lg text-sm text-secondary hover:text-primary transition">
              {t.cancel}
            </button>
            <button type="submit" disabled={loading} className="px-4 py-2 rounded-lg bg-accent text-white text-sm font-medium hover:bg-accent-hover transition disabled:opacity-50">
              {loading ? t.loading : t.save}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

export function FormField({ label, children }) {
  return (
    <div>
      <label className="block text-sm text-secondary mb-1">{label}</label>
      {children}
    </div>
  )
}

export function TextInput({ value, onChange, placeholder, type = 'text', ...props }) {
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-full px-3 py-2 rounded-lg bg-canvas border border-default text-primary placeholder-text-secondary text-sm focus:border-accent focus:ring-1 focus:ring-accent outline-none"
      {...props}
    />
  )
}

export function TextArea({ value, onChange, placeholder, rows = 3, ...props }) {
  return (
    <textarea
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      rows={rows}
      className="w-full px-3 py-2 rounded-lg bg-canvas border border-default text-primary placeholder-text-secondary text-sm focus:border-accent focus:ring-1 focus:ring-accent outline-none resize-y"
      {...props}
    />
  )
}

export function Checkbox({ label, checked, onChange }) {
  return (
    <label className="flex items-center gap-2 cursor-pointer">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="rounded border-default bg-canvas text-accent focus:ring-accent"
      />
      <span className="text-sm text-primary">{label}</span>
    </label>
  )
}

export function SelectInput({ value, onChange, options, placeholder }) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className="w-full px-3 py-2 rounded-lg bg-canvas border border-default text-primary text-sm focus:border-accent focus:ring-1 focus:ring-accent outline-none"
    >
      {placeholder && <option value="">{placeholder}</option>}
      {options.map(o => (
        <option key={o.value} value={o.value}>{o.label}</option>
      ))}
    </select>
  )
}
