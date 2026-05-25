import { useEffect } from 'react'
import { X } from 'lucide-react'

const WIDTH_CLASSES = {
  md:   'sm:max-w-md',
  lg:   'sm:max-w-lg',
  xl:   'sm:max-w-xl',
  '2xl':'sm:max-w-2xl',
}

/**
 * Drawer — right-side slide-in panel above md, fullscreen modal on <md.
 *
 * Props:
 *   open       boolean
 *   onClose    () => void   — called on backdrop click, ESC, ✕
 *   title      string | ReactNode
 *   subtitle   string (optional)
 *   actions    ReactNode (optional) — header right-side, e.g. quick buttons
 *   width      'md' | 'lg' | 'xl' | '2xl'   default 'xl' (≈576px)
 *   children   ReactNode — body content (scrollable)
 *
 * Behavior:
 *   - ESC closes via window keydown listener.
 *   - Backdrop click closes (desktop only — on mobile the drawer is the screen).
 *   - Body scroll locked while open; original overflow restored on close/unmount.
 *   - Hairline border-l on the left edge above md.
 *   - aria-modal="true", role="dialog", aria-label from title (when string).
 */
export function Drawer({ open, onClose, title, subtitle, actions, width = 'xl', children }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose?.() }
    window.addEventListener('keydown', onKey)
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      window.removeEventListener('keydown', onKey)
      document.body.style.overflow = prev
    }
  }, [open, onClose])

  if (!open) return null

  const widthClass = WIDTH_CLASSES[width] || WIDTH_CLASSES.xl

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      {/* Backdrop — desktop only. On mobile the drawer is fullscreen so no dim layer is needed. */}
      <div
        className="absolute inset-0 bg-canvas/80 backdrop-blur-sm hidden sm:block"
        onClick={onClose}
        aria-hidden="true"
      />
      <aside
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : 'Drawer'}
        className={`relative bg-elevated flex flex-col
          w-full h-full
          sm:h-screen sm:border-l sm:border-default
          ${widthClass}`}
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-start gap-3 p-4 border-b border-default flex-shrink-0">
          <div className="flex-1 min-w-0">
            {typeof title === 'string'
              ? <h2 className="text-base font-semibold text-primary tracking-tight truncate">{title}</h2>
              : title}
            {subtitle && (
              <p className="text-xs text-tertiary mt-0.5 truncate">{subtitle}</p>
            )}
          </div>
          {actions && (
            <div className="flex items-center gap-1 flex-shrink-0">
              {actions}
            </div>
          )}
          <button
            type="button"
            onClick={onClose}
            aria-label="Закрыть"
            className="p-1 rounded hover:bg-hover text-secondary hover:text-primary flex-shrink-0"
          >
            <X className="w-4 h-4" />
          </button>
        </header>
        <div className="flex-1 overflow-y-auto">{children}</div>
      </aside>
    </div>
  )
}
