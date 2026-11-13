import { useEffect, useState } from 'react'
import { fetchUserDialRate } from '../../api/admin.js'
import { Badge, Skeleton } from '../ui/index.js'

/**
 * DialRateCard — the user's connections over 7 days (C13): minutes a day above the per-minute
 * threshold and the peak, with the «похоже на раздачу» verdict. The estimate counts outgoing
 * traffic, so long video calls look alike — the owner decides, nothing is blocked by itself.
 */
export default function DialRateCard({ userId }) {
  const [d, setD] = useState(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let alive = true
    fetchUserDialRate(userId).then((r) => { if (alive) setD(r) }).catch((e) => { if (alive) setError(e?.message || 'ошибка') })
    return () => { alive = false }
  }, [userId])
  if (error) return <p className="text-xs text-danger">Подключения не загрузились: {error}</p>
  if (!d) return <Skeleton className="h-28" />
  const max = Math.max(60, ...d.days.map((x) => x.minutes_over))
  return (
    <section className="rounded border border-default p-3">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 mb-2">
        <h3 className="text-sm font-semibold text-primary">Подключения за 7 дней</h3>
        <Badge variant={d.verdict.suspect ? 'warning' : 'success'}>{d.verdict.suspect ? 'похоже на раздачу' : 'в норме'}</Badge>
      </div>
      <div className="flex items-end gap-1 h-16" role="img" aria-label="Минут в сутки выше порога">
        {d.days.map((x) => (
          <div key={x.day} className="flex-1 flex flex-col items-center justify-end h-full" title={`${x.day}: выше порога ${x.minutes_over} мин, пик ${x.peak_dials}/мин`}>
            <div className={`w-full rounded-t ${x.minutes_over >= 60 ? 'bg-warning' : 'bg-accent/50'}`} style={{ height: `${Math.max(2, (x.minutes_over / max) * 100)}%` }} />
          </div>
        ))}
      </div>
      <div className="flex gap-1 mt-1">
        {d.days.map((x) => <span key={x.day} className="flex-1 text-center font-mono tabular-nums text-[10px] text-tertiary">{x.day.slice(8)}.{x.day.slice(5, 7)}</span>)}
      </div>
      <p className="text-xs text-secondary mt-2">{d.verdict.text}</p>
      <p className="text-[11px] text-tertiary mt-1">Столбик — минут в сутки, когда подключений было больше {d.threshold_per_min} в минуту (оценка по исходящему трафику).</p>
    </section>
  )
}
