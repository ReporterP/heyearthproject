import { useEffect, useState } from 'react'
import { Activity } from 'lucide-react'
import { fetchServerLoad } from '../../api/admin.js'
import { Button, Skeleton, EmptyState } from '../ui/index.js'
import { LOAD_RANGES, chartGeometry, fmtMbps, fmtInt } from './serversModel.js'

const W = 300
const H = 72

function fmtAxis(at, range) {
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return ''
  return range === '7d'
    ? d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' })
    : d.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' })
}

// Chart: one metric over the range — series [{pick, className, fill?}], all on one scale.
function Chart({ title, points, series, max, unit, format = (v) => String(v), range }) {
  const geos = series.map((s) => chartGeometry(points, s.pick, { width: W, height: H, max }))
  const top = Math.max(...geos.map((g) => g.top))
  const scaled = max == null && geos.length > 1 ? series.map((s) => chartGeometry(points, s.pick, { width: W, height: H, max: top })) : geos
  const peak = Math.max(...scaled.map((g) => g.peak))
  const empty = scaled.every((g) => g.empty)
  return (
    <div className="rounded border border-default bg-elevated p-3 min-w-0">
      <div className="flex items-baseline justify-between gap-2 mb-2">
        <span className="text-xs text-secondary">{title}</span>
        <span className="font-mono tabular-nums text-xs text-tertiary">
          {empty ? '—' : <>сейчас <span className="text-primary">{format(scaled[0].last)}{unit}</span> · пик {format(peak)}{unit}</>}
        </span>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="w-full h-16 block" role="img"
        aria-label={`${title}: пик ${format(peak)}${unit}`}>
        <line x1="0" y1={H - 0.5} x2={W} y2={H - 0.5} className="stroke-[var(--color-border-default)]" strokeWidth="1" vectorEffect="non-scaling-stroke" />
        <line x1="0" y1="0.5" x2={W} y2="0.5" className="stroke-[var(--color-border-default)]" strokeWidth="1" strokeDasharray="2 3" vectorEffect="non-scaling-stroke" />
        {scaled.map((g, i) => (
          <g key={i} className={series[i].className}>
            {series[i].fill && <path d={g.area} fill="currentColor" fillOpacity="0.12" />}
            <path d={g.line} fill="none" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
          </g>
        ))}
      </svg>
      <div className="flex justify-between mt-1 font-mono tabular-nums text-[10px] text-tertiary">
        <span>{points.length ? fmtAxis(points[0].at, range) : ''}</span>
        <span>шкала до {format(max ?? top)}{unit}</span>
        <span>{points.length ? fmtAxis(points[points.length - 1].at, range) : ''}</span>
      </div>
    </div>
  )
}

/**
 * LoadCharts — «Нагрузка» of a server or the Hub (id 'hub'): CPU, RAM, the channel and the
 * connections over 24 h (10-minute points) or 7 days (hours). GET /servers/{id}/load.
 */
export default function LoadCharts({ id, relay = false }) {
  const [range, setRange] = useState('24h')
  const [state, setState] = useState({ points: null, error: '' })

  useEffect(() => {
    let alive = true
    setState({ points: null, error: '' })
    fetchServerLoad(id, range)
      .then((r) => { if (alive) setState({ points: r?.points || [], error: '' }) })
      .catch((e) => { if (alive) setState({ points: null, error: e?.message || 'ошибка' }) })
    return () => { alive = false }
  }, [id, range])

  const pts = state.points
  return (
    <div>
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-2 mb-3">
        <p className="text-xs text-tertiary">Точка — {range === '7d' ? 'час' : '10 минут'}: среднее CPU и RAM, пик канала (зелёный — вход, жёлтый — выход) и подключений.</p>
        <div role="group" aria-label="Период" className="flex gap-1">
          {LOAD_RANGES.map((r) => (
            <Button key={r.key} size="sm" variant={range === r.key ? 'secondary' : 'ghost'} aria-pressed={range === r.key}
              onClick={() => setRange(r.key)}>{r.label}</Button>
          ))}
        </div>
      </div>
      {state.error && <p role="alert" className="text-sm text-danger mb-2">Не удалось загрузить нагрузку: {state.error}</p>}
      {pts === null && !state.error && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-28" />)}</div>
      )}
      {pts !== null && pts.length === 0 && (
        <EmptyState icon={<Activity className="w-8 h-8" />} title="Данных пока нет"
          description="Сборщик опрашивает сервер раз в минуту по SSH; первые точки появятся через несколько минут после настройки." />
      )}
      {pts !== null && pts.length > 0 && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Chart title="CPU" points={pts} max={100} unit=" %" range={range} format={(v) => (v == null ? '—' : Math.round(v))}
            series={[{ pick: (p) => p.cpu_max, className: 'text-accent/40' }, { pick: (p) => p.cpu, className: 'text-accent', fill: true }]} />
          <Chart title="RAM" points={pts} max={100} unit=" %" range={range} format={(v) => (v == null ? '—' : Math.round(v))}
            series={[{ pick: (p) => p.mem, className: 'text-info', fill: true }]} />
          <Chart title="Канал, Мбит/с" points={pts} unit="" range={range} format={(v) => (v == null ? '—' : fmtMbps(v))}
            series={[{ pick: (p) => p.rx_mbps, className: 'text-success', fill: true }, { pick: (p) => p.tx_mbps, className: 'text-warning' }]} />
          {relay ? (
            <Chart title="conntrack, % таблицы" points={pts} max={100} unit=" %" range={range} format={(v) => (v == null ? '—' : Math.round(v))}
              series={[{ pick: (p) => p.conntrack_pct, className: 'text-warning', fill: true }]} />
          ) : (
            <Chart title="Подключения TCP" points={pts} unit="" range={range} format={(v) => (v == null ? '—' : fmtInt(v))}
              series={[{ pick: (p) => p.connections, className: 'text-accent', fill: true }]} />
          )}
        </div>
      )}
    </div>
  )
}
