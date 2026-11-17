import { useMemo, useState } from 'react'
import { Segmented, Progress } from './Controls.jsx'
import { trafficParts, traffic, date } from '../lib/format.js'

// «Трафик» (spec 08 §8.1 item 13, 02 §23 bars). GET /api/v1/user/traffic returns every period at
// once, so switching the period is instant (the new bars grow in with `swap`). The current bucket is
// green; a bar's native title gives its date and volume; a hidden table repeats the values.
const PERIODS = [
  { value: 'day', label: 'День' },
  { value: 'week', label: 'Неделя' },
  { value: 'month', label: 'Месяц' },
  { value: 'year', label: 'Год' },
]
const MONTHS = ['янв', 'фев', 'мар', 'апр', 'май', 'июн', 'июл', 'авг', 'сен', 'окт', 'ноя', 'дек']

function label(b) {
  if (b.month) { const [y, m] = b.month.split('-'); return `${MONTHS[Number(m) - 1]} ${y}` }
  return date(b.day)
}

export default function TrafficCard({ data, phone }) {
  const [period, setPeriod] = useState('month')
  const p = data?.periods?.[phone ? 'month' : period]
  const buckets = useMemo(() => (p ? (p.by_month || p.by_day || []) : []), [p])
  const max = Math.max(1, ...buckets.map((b) => b.bytes || 0))
  const cycle = data?.cycle
  const limited = cycle && Number(cycle.limit_bytes) > 0
  const [num, unit] = trafficParts(p?.used_bytes || 0)

  const bars = buckets.length ? (
    <>
      <div key={phone ? 'm' : period} className="bars swap" style={{ marginTop: phone ? 0 : 16, height: phone ? 56 : 72 }} aria-label={phone ? 'Трафик по дням за месяц' : 'Трафик по дням'} role="img">
        {buckets.map((b, i) => (
          <span
            key={b.day || b.month || i}
            className={i === buckets.length - 1 ? 'is-now' : ''}
            style={{ '--i': i, height: `${Math.max(4, Math.round(((b.bytes || 0) / max) * 100))}%` }}
            title={`${label(b)}: ${traffic(b.bytes)}`}
          />
        ))}
      </div>
      <table className="sr">
        <caption>Трафик по периодам</caption>
        <tbody>{buckets.map((b, i) => <tr key={i}><th scope="row">{label(b)}</th><td>{traffic(b.bytes)}</td></tr>)}</tbody>
      </table>
    </>
  ) : (
    <p className="t-sm subtle" style={{ marginTop: 16 }}>За этот период трафика нет.</p>
  )

  if (phone) {
    return (
      <section className="card">
        <div className="card__head"><h2 className="card__title">Трафик за месяц</h2><b className="mono">{num} {unit}</b></div>
        {bars}
      </section>
    )
  }
  return (
    <section className="card">
      <div className="card__head">
        <h2 className="card__title">Трафик</h2>
        <Segmented label="Период" value={period} onChange={setPeriod} options={PERIODS} />
      </div>
      <p><span className="t-h1 mono" style={{ fontSize: 36 }}>{num}</span> <span className="muted">{unit} · {limited ? `из ${traffic(cycle.limit_bytes)}` : 'без лимита'}</span></p>
      {limited && period === 'month' && <div style={{ marginTop: 10 }}><Progress value={Math.min(100, cycle.percent || 0)} warn={cycle.exceeded} label="Израсходовано от лимита" /></div>}
      {bars}
    </section>
  )
}
