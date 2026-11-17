import { useState, useEffect, useCallback } from 'react'
import { useT } from '../../i18n/index.jsx'
import { fetchUserTraffic, fetchUserPayments } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { Badge } from '../ui/index.js'
import TrafficCard from './TrafficCard.jsx'
import DialRateCard from './DialRateCard.jsx'

export default function TrafficPaymentsTab({ user }) {
  const { t } = useT()
  const [traffic, setTraffic] = useState(null)
  const [payments, setPayments] = useState([])
  const [loadingT, setLoadingT] = useState(true)
  const [loadingP, setLoadingP] = useState(true)
  const [error, setError] = useState('')

  const loadTraffic = useCallback(() => {
    setLoadingT(true)
    fetchUserTraffic(user.id)
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

  return (
    <div className="space-y-6">
      {/* ── Traffic section ─────────────────────────────────────── */}
      <section>
        {loadingT ? (
          <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
        ) : !traffic ? (
          <p className="text-sm text-tertiary">{t.drawerNoTraffic || 'Нет данных о трафике.'}</p>
        ) : (
          <TrafficCard data={traffic} />
        )}
      </section>

      <DialRateCard userId={user.id} />

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
