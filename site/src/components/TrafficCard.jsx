import { useState, useMemo } from 'react'

const PERIODS = [
  { id: 'day',   label: 'День' },
  { id: 'week',  label: 'Неделя' },
  { id: 'month', label: 'Месяц' },
  { id: 'year',  label: 'Год' },
]

function formatBytes(n) {
  if (!n) return '0 B'
  const k = 1024, units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(n) / Math.log(k))
  return parseFloat((n / Math.pow(k, i)).toFixed(1)) + ' ' + units[i]
}

function formatDateRu(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export default function TrafficCard({ data }) {
  const [period, setPeriod] = useState('month')
  const p = data?.periods?.[period]
  const cycle = data?.cycle
  const isUnlimited = !cycle || cycle.limit_bytes === 0
  const buckets = period === 'year' ? (p?.by_month || []) : (p?.by_day || [])

  const maxBytes = useMemo(
    () => Math.max(1, ...buckets.map(b => b.bytes || 0)),
    [buckets]
  )

  if (!data) return null

  return (
    <div className="rounded-lg border border-default bg-elevated p-4 space-y-3">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <h2 className="text-base font-semibold text-primary">Трафик</h2>
        <div className="flex items-center gap-1">
          {PERIODS.map(po => (
            <button
              key={po.id}
              onClick={() => setPeriod(po.id)}
              className={`text-xs px-2.5 py-1 rounded-md transition ${
                period === po.id
                  ? 'bg-accent text-canvas font-medium'
                  : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {po.label}
            </button>
          ))}
        </div>
      </div>

      {cycle && (
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-3 flex-wrap">
            <span className="text-2xl font-semibold text-primary tabular-nums">
              {formatBytes(cycle.used_bytes)}
            </span>
            <span className="text-sm text-tertiary tabular-nums">
              / {isUnlimited ? '∞' : formatBytes(cycle.limit_bytes)}
              {!isUnlimited && ` · ${cycle.percent.toFixed(0)}%`}
            </span>
          </div>
          {!isUnlimited && (
            <div className="h-1.5 bg-canvas rounded-full overflow-hidden">
              <div
                className={`h-full transition-all ${cycle.exceeded ? 'bg-danger' : 'bg-info'}`}
                style={{ width: `${Math.min(100, cycle.percent)}%` }}
              />
            </div>
          )}
          <p className="text-xs text-tertiary">
            Цикл: {formatDateRu(cycle.started_at)} → {formatDateRu(cycle.ends_at)}
          </p>
        </div>
      )}

      <div className="h-24 flex items-end gap-0.5">
        {buckets.length === 0 ? (
          <p className="text-xs text-tertiary self-center">Нет данных за период</p>
        ) : (
          buckets.map((b, i) => {
            const h = Math.max(2, Math.round((b.bytes / maxBytes) * 96))
            const label = period === 'year' ? b.month : b.day
            return (
              <div
                key={i}
                className="flex-1 bg-info/30 hover:bg-info/60 transition rounded-sm relative group"
                style={{ height: `${h}px` }}
                title={`${label}: ${formatBytes(b.bytes)}`}
              />
            )
          })
        )}
      </div>

      <div className="grid grid-cols-3 gap-3 text-xs text-secondary pt-2 border-t border-default">
        <div>День: <span className="text-primary font-medium tabular-nums">{formatBytes(data.periods?.day?.used_bytes || 0)}</span></div>
        <div>Неделя: <span className="text-primary font-medium tabular-nums">{formatBytes(data.periods?.week?.used_bytes || 0)}</span></div>
        <div>Год: <span className="text-primary font-medium tabular-nums">{formatBytes(data.periods?.year?.used_bytes || 0)}</span></div>
      </div>
    </div>
  )
}
