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

// Bucket label depends on chart axis: day-mode uses calendar day, year-mode month-YYYY.
function bucketLabel(b, period) {
  if (period === 'year') {
    if (!b.month) return ''
    const [y, m] = b.month.split('-')
    return new Date(Number(y), Number(m) - 1, 1).toLocaleDateString('ru-RU', { month: 'short', year: '2-digit' })
  }
  if (!b.day) return ''
  return new Date(b.day).toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
}

export default function TrafficCard({ data }) {
  const [period, setPeriod] = useState('month')
  const p = data?.periods?.[period]
  const cycle = data?.cycle
  const isUnlimited = !cycle || cycle.limit_bytes === 0
  const buckets = period === 'year' ? (p?.by_month || []) : (p?.by_day || [])
  const periodUsed = p?.used_bytes || 0

  const maxBytes = useMemo(
    () => Math.max(1, ...buckets.map(b => b.bytes || 0)),
    [buckets]
  )

  if (!data) return null

  const showCycleProgress = period === 'month' && cycle && !isUnlimited

  return (
    <div className="glass-card rounded-2xl p-6">
      {/* Header — icon + title + period switcher */}
      <div className="flex items-start justify-between gap-3 flex-wrap mb-4">
        <div className="flex items-center gap-3 min-w-0">
          <div className="w-10 h-10 rounded-xl bg-brand/12 border border-brand/20 flex items-center justify-center flex-shrink-0">
            <svg className="w-5 h-5 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z" />
            </svg>
          </div>
          <div className="min-w-0">
            <h2 className="text-base font-semibold">Трафик</h2>
            <p className="text-xs text-text-secondary truncate">
              {PERIODS.find(po => po.id === period)?.label}
              {period === 'month' && cycle && ` · цикл ${formatDateRu(cycle.started_at)} → ${formatDateRu(cycle.ends_at)}`}
            </p>
          </div>
        </div>

        <div className="inline-flex items-center gap-1 p-1 rounded-xl glass">
          {PERIODS.map(po => (
            <button
              key={po.id}
              onClick={() => setPeriod(po.id)}
              className={`text-xs px-3 py-1.5 rounded-lg transition-all duration-200 ${
                period === po.id
                  ? 'bg-brand text-[#04231a] font-semibold'
                  : 'text-text-secondary hover:text-text-primary hover:bg-white/5'
              }`}
            >
              {po.label}
            </button>
          ))}
        </div>
      </div>

      {/* Big number for selected period */}
      <div className="flex items-baseline justify-between gap-3 flex-wrap mb-2">
        <span className="text-3xl font-bold tabular-nums">
          {formatBytes(periodUsed)}
        </span>
        <span className="text-sm text-text-secondary tabular-nums">
          {showCycleProgress
            ? `/ ${formatBytes(cycle.limit_bytes)} · ${cycle.percent.toFixed(0)}%`
            : isUnlimited ? '/ ∞' : ''}
        </span>
      </div>

      {/* Cycle progress bar — only when period=month AND has limit */}
      {showCycleProgress && (
        <div className="h-1.5 bg-white/5 rounded-full overflow-hidden mb-3">
          <div
            className={`h-full transition-all duration-500 ${
              cycle.exceeded
                ? 'bg-gradient-to-r from-danger to-accent'
                : 'bg-gradient-to-r from-brand-dark to-brand'
            }`}
            style={{ width: `${Math.min(100, cycle.percent)}%` }}
          />
        </div>
      )}

      {/* Bar chart */}
      <div className="mt-4 h-32 flex items-end gap-1">
        {buckets.length === 0 ? (
          <p className="text-xs text-text-secondary self-center mx-auto">Нет данных за период</p>
        ) : (
          buckets.map((b, i) => {
            const pct = (b.bytes || 0) / maxBytes
            const h = Math.max(4, Math.round(pct * 128))
            return (
              <div
                key={i}
                className="flex-1 flex flex-col items-center justify-end gap-1 min-w-0 group cursor-default"
                title={`${bucketLabel(b, period)}: ${formatBytes(b.bytes)}`}
              >
                <div
                  className="w-full bg-gradient-to-t from-brand/40 to-brand/80 group-hover:from-brand/60 group-hover:to-brand rounded-t transition-all"
                  style={{ height: `${h}px` }}
                />
                {buckets.length <= 12 && (
                  <span className="text-[10px] text-text-secondary/60 truncate w-full text-center">
                    {bucketLabel(b, period)}
                  </span>
                )}
              </div>
            )
          })
        )}
      </div>

      {/* Summary row */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-xs text-text-secondary mt-4 pt-4 border-t border-white/5">
        {PERIODS.map(po => (
          <div key={po.id} className={po.id === period ? 'text-text-primary' : ''}>
            {po.label}: <span className="font-semibold tabular-nums">{formatBytes(data.periods?.[po.id]?.used_bytes || 0)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
