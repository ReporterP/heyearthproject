import { useState, useEffect, useCallback, useMemo } from 'react'
import { useT } from '../../i18n/index.jsx'
import { fetchUserTraffic, fetchUserPayments } from '../../api/admin.js'
import { formatBytes, formatDate } from '../../utils/format.js'
import { Badge } from '../ui/index.js'

export default function TrafficPaymentsTab({ user }) {
  const { t } = useT()
  const [traffic, setTraffic] = useState(null)
  const [payments, setPayments] = useState([])
  const [loadingT, setLoadingT] = useState(true)
  const [loadingP, setLoadingP] = useState(true)
  const [error, setError] = useState('')

  const loadTraffic = useCallback(() => {
    setLoadingT(true)
    fetchUserTraffic(user.id, 30)
      .then(setTraffic)
      .catch(err => setError(err.message))
      .finally(() => setLoadingT(false))
  }, [user.id])

  const loadPayments = useCallback(() => {
    setLoadingP(true)
    fetchUserPayments(user.id)
      .then(data => setPayments(data.items || []))
      .catch(err => setError(err.message))
      .finally(() => setLoadingP(false))
  }, [user.id])

  useEffect(() => { loadTraffic(); loadPayments() }, [loadTraffic, loadPayments])

  const maxDayBytes = useMemo(() => {
    if (!traffic?.by_day?.length) return 0
    return Math.max(...traffic.by_day.map(d => d.bytes || 0), 1)
  }, [traffic])

  const usedPct = traffic && traffic.limit_bytes > 0
    ? Math.min(100, Math.round((traffic.total_bytes / traffic.limit_bytes) * 100))
    : null

  return (
    <div className="space-y-6">
      {/* ── Traffic section ─────────────────────────────────────── */}
      <section>
        <header className="flex items-center justify-between mb-2">
          <h3 className="text-sm font-semibold text-primary">{t.drawerTrafficTitle || 'Трафик за 30 дней'}</h3>
          {traffic && (
            <span className="text-xs text-tertiary font-mono tabular-nums">
              {formatBytes(traffic.total_bytes)}
              {traffic.limit_bytes > 0 && ` / ${formatBytes(traffic.limit_bytes)} (${usedPct}%)`}
            </span>
          )}
        </header>

        {loadingT ? (
          <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
        ) : !traffic || !traffic.by_day?.length ? (
          <p className="text-sm text-tertiary">{t.drawerNoTraffic || 'За последние 30 дней трафика нет.'}</p>
        ) : (
          <>
            {/* Daily bar chart — pure CSS */}
            <div className="flex items-end gap-px h-24 bg-canvas border border-default rounded p-2">
              {traffic.by_day.map(d => {
                const pct = maxDayBytes > 0 ? (d.bytes / maxDayBytes) * 100 : 0
                return (
                  <div
                    key={d.day}
                    title={`${d.day}: ${formatBytes(d.bytes)}`}
                    className="flex-1 bg-info/60 hover:bg-info rounded-sm transition min-w-0"
                    style={{ height: `${Math.max(pct, 2)}%` }}
                  />
                )
              })}
            </div>

            {/* Limit overall progress bar */}
            {traffic.limit_bytes > 0 && (
              <div className="mt-2">
                <div className="h-1.5 bg-canvas border border-default rounded-full overflow-hidden">
                  <div
                    className={`h-full transition-all ${usedPct >= 90 ? 'bg-danger' : usedPct >= 70 ? 'bg-warning' : 'bg-info'}`}
                    style={{ width: `${usedPct}%` }}
                  />
                </div>
              </div>
            )}

            {/* By-country breakdown (v1: always empty) */}
            <div className="mt-4">
              <h4 className="text-xs uppercase tracking-wide text-tertiary mb-2">
                {t.drawerByCountry || 'По странам'}
              </h4>
              {traffic.by_country?.length > 0 ? (
                <div className="space-y-1">
                  {traffic.by_country.map(c => (
                    <div key={c.country} className="flex items-center justify-between text-sm">
                      <span className="text-primary">{c.country}</span>
                      <span className="font-mono text-tertiary tabular-nums">{formatBytes(c.bytes)}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-tertiary italic">
                  {t.drawerByCountryEmpty || 'Распределение по странам пока недоступно'}
                </p>
              )}
            </div>
          </>
        )}
      </section>

      {/* ── Payments section ────────────────────────────────────── */}
      <section>
        <h3 className="text-sm font-semibold text-primary mb-2">{t.drawerPaymentsTitle || 'Платежи'}</h3>
        {loadingP ? (
          <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
        ) : payments.length === 0 ? (
          <p className="text-sm text-tertiary">{t.drawerNoPayments || 'Платежей нет.'}</p>
        ) : (
          <ul className="space-y-1">
            {payments.map(p => {
              const amount = Number(p.amount_rub ?? p.amount ?? 0)
              const positive = amount >= 0
              const variant = p.status === 'succeeded' ? 'success' : p.status === 'failed' ? 'danger' : p.status === 'pending' ? 'warning' : 'default'
              return (
                <li key={p.id} className="flex items-center gap-2 py-1.5 border-b border-default last:border-b-0 text-sm">
                  <span className="text-xs text-tertiary font-mono w-32 shrink-0">{formatDate(p.created_at)}</span>
                  <span className={`font-mono tabular-nums font-semibold w-16 shrink-0 ${positive ? 'text-success' : 'text-warning'}`}>
                    {positive ? '+' : ''}{amount}₽
                  </span>
                  <span className="text-secondary flex-1 truncate">
                    {p.provider || 'YooKassa'} · {p.description || p.purpose || p.plan_name || '—'}
                  </span>
                  <Badge variant={variant}>{p.status}</Badge>
                </li>
              )
            })}
          </ul>
        )}
      </section>

      {error && <p className="text-danger text-sm">{error}</p>}
    </div>
  )
}
