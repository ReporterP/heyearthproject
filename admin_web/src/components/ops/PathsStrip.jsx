import { Card } from '../ui/index.js'
import { pathTone, ageLabel } from './opsModel.js'

const DOT = {
  ok: 'bg-success',
  warn: 'bg-warning',
  danger: 'bg-danger',
  muted: 'bg-transparent border border-strong',
}
const TEXT = {
  ok: 'text-primary',
  warn: 'text-warning',
  danger: 'text-danger',
  muted: 'text-tertiary',
}
const STATE_WORD = { ok: 'работает', warn: 'внимание', down: 'не работает', off: 'выключен', unknown: 'нет данных' }

/**
 * PathsStrip — the health board: every path a user's traffic or money takes, one cell each.
 * Props: paths [{id, label, state, detail, age_sec}].
 */
export default function PathsStrip({ paths }) {
  return (
    <Card>
      <ul className="flex flex-wrap gap-x-5 gap-y-1 px-4 py-2.5" aria-label="Состояние путей">
        {paths.map((p) => {
          const tone = pathTone(p.state)
          const word = STATE_WORD[p.state] || p.state
          return (
            <li key={p.id} className="flex items-center gap-1.5 h-7 min-w-0"
              title={[`${p.label}: ${word}`, p.detail, `проверено ${ageLabel(p.age_sec)} назад`].filter(Boolean).join('\n')}>
              <span aria-hidden="true" className={`h-2 w-2 shrink-0 rounded-full ${DOT[tone]}`} />
              <span className={`text-sm whitespace-nowrap ${TEXT[tone]}`}>{p.label}</span>
              <span className="sr-only">{word}</span>
              {p.state !== 'ok' && p.detail && (
                <span className="text-xs text-tertiary truncate max-w-[16rem]">{p.detail}</span>
              )}
              {p.state === 'ok' && (
                <span className="text-xs font-mono tabular-nums text-tertiary">{ageLabel(p.age_sec)}</span>
              )}
            </li>
          )
        })}
      </ul>
    </Card>
  )
}
