import { useEffect } from 'react'

const sizeClasses = {
  'max-w-sm':  'sm:max-w-sm',
  'max-w-md':  'sm:max-w-md',
  'max-w-lg':  'sm:max-w-lg',
  'max-w-xl':  'sm:max-w-xl',
  'max-w-2xl': 'sm:max-w-2xl',
  'max-w-3xl': 'sm:max-w-3xl',
  'max-w-4xl': 'sm:max-w-4xl',
}

export function Dialog({ open, onClose, title, children, maxWidth = 'max-w-2xl', icon }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  const sizeClass = sizeClasses[maxWidth] || 'sm:max-w-2xl'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 sm:p-4" onClick={onClose}>
      <div
        className={`bg-overlay shadow-2xl
          w-full h-full sm:h-auto sm:max-h-[90vh]
          rounded-none sm:rounded-[var(--radius-default,0.25rem)]
          sm:border sm:border-default
          p-4 sm:p-6
          overflow-y-auto
          ${sizeClass}`}
        onClick={(e) => e.stopPropagation()}
      >
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
