import { useT } from '../i18n/index.jsx'

export default function ConfirmDialog({ open, title, message, onConfirm, onCancel, loading }) {
  const { t } = useT()
  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onCancel}>
      <div
        className="bg-surface-card rounded-xl shadow-xl border border-white/10 w-full max-w-sm m-4 p-5"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-lg font-semibold mb-2">{title || t.delete}</h2>
        <p className="text-sm text-text-secondary mb-5">{message}</p>
        <div className="flex justify-end gap-2">
          <button
            onClick={onCancel}
            className="px-4 py-2 rounded-lg text-sm text-text-secondary hover:text-text-primary transition"
          >
            {t.cancel}
          </button>
          <button
            onClick={onConfirm}
            disabled={loading}
            className="px-4 py-2 rounded-lg bg-red-600 text-white text-sm font-medium hover:bg-red-700 transition disabled:opacity-50"
          >
            {loading ? t.loading : t.delete}
          </button>
        </div>
      </div>
    </div>
  )
}
