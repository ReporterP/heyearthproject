import { Check } from 'lucide-react'

/**
 * Stepper — wizard progress; a vertical list below sm.
 * Props: steps (titles), current (index of the active step; ≥ steps.length = all done), className (default 'mb-5').
 * The active step carries aria-current="step"; finished steps are announced as such.
 */
export function Stepper({ steps, current, className = 'mb-5' }) {
  return (
    <ol className={`flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-0 ${className}`} aria-label="Шаги">
      {steps.map((title, i) => {
        const done = i < current
        const active = i === current
        return (
          <li key={i} className="flex items-center gap-2 sm:flex-1 min-w-0" aria-current={active ? 'step' : undefined}>
            <span aria-hidden="true"
              className={`flex-shrink-0 w-6 h-6 rounded border flex items-center justify-center font-mono tabular-nums text-[11px] transition-colors
                ${done ? 'bg-accent/15 border-accent/40 text-accent' : active ? 'border-accent text-accent' : 'border-default text-tertiary'}`}>
              {done ? <Check className="w-3.5 h-3.5" /> : i + 1}
            </span>
            <span className={`text-xs truncate ${active ? 'text-primary font-medium' : done ? 'text-secondary' : 'text-tertiary'}`}>
              <span className="sr-only">{`Шаг ${i + 1}: `}</span>
              {title}
              {done && <span className="sr-only"> — готово</span>}
            </span>
            {i < steps.length - 1 && <span className="hidden sm:block flex-1 border-t border-default mx-2" aria-hidden="true" />}
          </li>
        )
      })}
    </ol>
  )
}
