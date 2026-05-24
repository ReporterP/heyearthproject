import { useEffect, useRef } from 'react'
import { Check } from 'lucide-react'
import { useTheme } from '../theme/ThemeContext.jsx'
import { themes } from '../theme/themes.js'
import { accents } from '../theme/accents.js'

const previewPalettes = {
  linear:    { canvas: 'oklch(0.16 0.012 260)', elevated: 'oklch(0.20 0.013 260)', text: 'oklch(0.97 0.005 260)' },
  brutalist: { canvas: 'oklch(0.08 0 0)',       elevated: 'oklch(0.12 0 0)',       text: 'oklch(0.98 0 0)' },
  notion:    { canvas: 'oklch(0.98 0.005 80)',  elevated: 'oklch(1 0 0)',          text: 'oklch(0.20 0.005 80)' },
  terminal:  { canvas: 'oklch(0.06 0 0)',       elevated: 'oklch(0.10 0 0)',       text: 'oklch(0.92 0 0)' },
}

export default function ThemePicker({ open, onClose, anchor = 'footer' }) {
  const { theme, accent, setTheme, setAccent } = useTheme()
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose() }
    const onMouseDown = (e) => {
      if (ref.current && !ref.current.contains(e.target)) onClose()
    }
    window.addEventListener('keydown', onKey)
    window.addEventListener('mousedown', onMouseDown)
    return () => {
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('mousedown', onMouseDown)
    }
  }, [open, onClose])

  if (!open) return null

  const positionCls = anchor === 'left-collapsed'
    ? 'left-full ml-2 bottom-0'
    : 'left-2 right-2 bottom-full mb-2'

  return (
    <div
      ref={ref}
      className={`absolute ${positionCls} z-40 rounded border border-default bg-overlay shadow-2xl p-3 w-72`}
    >
      <div className="text-[10px] uppercase tracking-wider text-tertiary mb-2">Тема</div>
      <div className="grid grid-cols-2 gap-2 mb-4">
        {themes.map((tm) => (
          <ThemeTile
            key={tm.id}
            themeId={tm.id}
            label={tm.label}
            active={tm.id === theme}
            onSelect={() => setTheme(tm.id)}
          />
        ))}
      </div>

      <div className="text-[10px] uppercase tracking-wider text-tertiary mb-2">Акцент</div>
      <div className="flex flex-wrap gap-1.5">
        {accents.map((a) => (
          <button
            key={a.id}
            type="button"
            onClick={() => setAccent(a.id)}
            aria-label={a.label}
            title={a.label}
            className={`w-6 h-6 rounded-full flex items-center justify-center transition-transform hover:scale-110 ${
              a.id === accent ? 'ring-2 ring-primary scale-110' : ''
            }`}
            style={{ background: a.hex }}
          >
            {a.id === accent && <Check className="w-3 h-3 text-white" />}
          </button>
        ))}
      </div>

      <div className="mt-3 pt-2 border-t border-default text-[10px] text-tertiary">
        Изменения сохраняются автоматически
      </div>
    </div>
  )
}

function ThemeTile({ themeId, label, active, onSelect }) {
  const p = previewPalettes[themeId] || previewPalettes.linear
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-label={`Выбрать тему ${label}`}
      className={`relative rounded border p-2 text-left transition-colors ${
        active ? 'border-accent ring-1 ring-accent/40' : 'border-default hover:border-strong'
      }`}
    >
      <div className="flex gap-0.5 mb-1.5">
        <div className="w-3 h-3 rounded-sm" style={{ background: p.canvas }} />
        <div className="w-3 h-3 rounded-sm" style={{ background: p.elevated }} />
        <div className="w-3 h-3 rounded-sm" style={{ background: p.text }} />
        <div className="w-3 h-3 rounded-sm" style={{ background: 'var(--color-accent)' }} />
      </div>
      <div className="text-xs font-medium text-primary">{label}</div>
      {active && <Check className="absolute top-1 right-1 w-3 h-3 text-accent" />}
    </button>
  )
}
