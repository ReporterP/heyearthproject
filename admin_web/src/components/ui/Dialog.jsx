import { useEffect } from 'react'

export function Dialog({ open, onClose, title, children, maxWidth = 'max-w-2xl', icon }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60" onClick={onClose}>
      <div className={`bg-overlay rounded-[var(--radius-default,0.25rem)] border border-default p-6 shadow-2xl w-full ${maxWidth} max-h-[90vh] overflow-y-auto`}
           onClick={(e) => e.stopPropagation()}>
        {title && (
          <div className="flex items-center gap-2 mb-4">
            {icon && <div className="text-tertiary">{icon}</div>}
            <h2 className="text-lg font-semibold text-primary tracking-tight">{title}</h2>
          </div>
        )}
        {children}
      </div>
    </div>
  )
}
