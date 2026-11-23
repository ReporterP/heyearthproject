import {
  RotateCw, Rocket, Power, Bot, TriangleAlert, CircleCheck, UserCog, Activity, Wrench, StickyNote,
} from 'lucide-react'
import { Card, CardHeader, EmptyState } from '../ui/index.js'
import { formatDate } from '../../utils/format.js'
import { feedIcon, impactLabel } from './opsModel.js'

const ICONS = {
  restart: RotateCw, deploy: Rocket, power: Power, auto: Bot, alert: TriangleAlert, resolved: CircleCheck,
  admin: UserCog, probe: Activity, maintenance: Wrench, note: StickyNote,
}
const ICON_TONE = { restart: 'text-warning', power: 'text-warning', alert: 'text-danger', resolved: 'text-success' }
const IMPACT_TONE = { K1: 'text-info', K2: 'text-warning', K3: 'text-danger' }

const time = (iso) => {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
}
const day = (iso) => new Date(iso).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })

/**
 * OpsFeed — what changed: deploys, restarts, settings, alerts, in one list, newest first.
 * Props: items [{at, source, kind, actor, target, summary, impact, ok}], error, hours.
 */
export default function OpsFeed({ items, error, hours = 24 }) {
  let lastDay = ''
  return (
    <Card>
      <CardHeader className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-primary">Лента</h3>
        <span className="text-xs text-tertiary font-mono tabular-nums">{hours} ч</span>
      </CardHeader>
      {error && <p role="alert" className="px-4 py-2 text-xs text-danger">{error}</p>}
      {items.length === 0 && !error ? (
        <EmptyState className="py-8" title="За это время ничего не менялось" description="Здесь появляются выкатки, перезапуски VPN, изменения настроек и тревоги." />
      ) : (
        <ul className="max-h-[32rem] overflow-y-auto" aria-label="Лента изменений">
          {items.map((it, i) => {
            const name = feedIcon(it)
            const Icon = ICONS[name] || StickyNote
            const d = day(it.at)
            const showDay = d !== lastDay
            lastDay = d
            return (
              <li key={`${it.at}-${i}`}>
                {showDay && <div className="px-4 pt-2 pb-1 text-[11px] font-mono tabular-nums text-tertiary">{d}</div>}
                <div className="flex items-start gap-2 min-h-9 px-4 py-1.5 border-t border-default first:border-t-0" title={formatDate(it.at)}>
                  <span className="w-10 shrink-0 text-xs font-mono tabular-nums text-tertiary pt-0.5">{time(it.at)}</span>
                  <Icon aria-hidden="true" className={`w-3.5 h-3.5 mt-0.5 shrink-0 ${ICON_TONE[name] || 'text-tertiary'}`} />
                  <span className="min-w-0 flex-1 text-sm text-primary break-words">
                    {it.summary}
                    {it.target && it.source !== 'ops' && <span className="text-tertiary font-mono text-xs"> {it.target}</span>}
                    {it.actor && it.actor !== 'system' && <span className="text-tertiary text-xs"> · {it.actor}</span>}
                  </span>
                  {it.impact && it.impact !== 'K0' && (
                    <span className={`shrink-0 text-xs font-mono ${IMPACT_TONE[it.impact] || 'text-tertiary'}`} title={impactLabel(it.impact)}>
                      {it.impact.replace('K', 'К')}
                    </span>
                  )}
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </Card>
  )
}
