import { useEffect, useState } from 'react'

/*
 * B2B analytics dashboard for HiEarth admins.
 *
 * Endpoint: GET /api/admin/b2b-analytics
 *   - Counts by status (active/inactive/suspended/total)
 *   - MRR (sum of debits last 30d) + revenue intake (sum of credits last 30d)
 *   - Top 10 tenants by traffic (bytes) and by spend (RUB debits)
 *   - Last 10 wallet credits as an activity feed
 *
 * Permission: tenants.read.
 */

function fmtMoney(n) { return new Intl.NumberFormat('ru-RU', { maximumFractionDigits: 0 }).format(n || 0) + ' ₽' }
function fmtBytes(n) {
  if (!n) return '0 B'
  const k = 1024, units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(n) / Math.log(k))
  return parseFloat((n / Math.pow(k, i)).toFixed(1)) + ' ' + units[i]
}
function fmtDate(s) { try { return new Date(s).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }) } catch { return s } }

export default function B2BAnalyticsPage() {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    fetch('/api/admin/b2b-analytics', { headers: { Authorization: 'Bearer ' + localStorage.getItem('admin_token') } })
      .then(r => r.json().then(d => { if (!r.ok) throw new Error(d.error || 'load failed'); return d }))
      .then(setData)
      .catch(e => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <p className="text-text-secondary">Загрузка…</p>
  if (error || !data) return <p className="text-red-400">{error || 'Нет данных'}</p>

  const c = data.counts || {}
  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">B2B Аналитика</h2>

      {/* KPI cards */}
      <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
        <Kpi label="Всего компаний" value={c.total || 0} />
        <Kpi label="Активных" value={c.active || 0} color="text-green-400" />
        <Kpi label="Неактивных" value={c.inactive || 0} color="text-amber-400" />
        <Kpi label="Приостановлено" value={c.suspended || 0} color="text-red-400" />
        <Kpi label="B2B-юзеров" value={data.members_total || 0} />
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Kpi label="Доход за 30д (списания)" value={fmtMoney(data.mrr_30d)} color="text-green-400" big />
        <Kpi label="Пополнения за 30д" value={fmtMoney(data.credits_30d)} color="text-brand-light" big />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {/* Top by spend */}
        <div className="bg-surface-card rounded-xl border border-white/10 p-5">
          <h3 className="text-base font-semibold mb-3">Топ-10 по тратам (30д)</h3>
          {(!data.top_by_spend || data.top_by_spend.length === 0) ? (
            <p className="text-sm text-text-secondary">Пока нет списаний.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-text-secondary text-xs">
                  <th className="text-left pb-2">Компания</th>
                  <th className="text-right pb-2">Юзеры</th>
                  <th className="text-right pb-2">Списано</th>
                  <th className="text-right pb-2">Баланс</th>
                </tr>
              </thead>
              <tbody>
                {data.top_by_spend.map(t => (
                  <tr key={t.id} className="border-t border-white/5">
                    <td className="py-2"><span className={`mr-2 inline-block w-2 h-2 rounded-full ${t.status === 'active' ? 'bg-green-400' : t.status === 'suspended' ? 'bg-red-400' : 'bg-amber-400'}`} />{t.name}</td>
                    <td className="text-right">{t.members}</td>
                    <td className="text-right font-medium text-red-400">{fmtMoney(t.metric_rub)}</td>
                    <td className="text-right text-text-secondary">{fmtMoney(t.balance)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        {/* Top by traffic */}
        <div className="bg-surface-card rounded-xl border border-white/10 p-5">
          <h3 className="text-base font-semibold mb-3">Топ-10 по трафику (30д)</h3>
          {(!data.top_by_traffic || data.top_by_traffic.length === 0) ? (
            <p className="text-sm text-text-secondary">Пока нет трафика по тенантам.</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="text-text-secondary text-xs">
                  <th className="text-left pb-2">Компания</th>
                  <th className="text-right pb-2">Юзеры</th>
                  <th className="text-right pb-2">Трафик</th>
                </tr>
              </thead>
              <tbody>
                {data.top_by_traffic.map(t => (
                  <tr key={t.id} className="border-t border-white/5">
                    <td className="py-2">{t.name}</td>
                    <td className="text-right">{t.members}</td>
                    <td className="text-right font-medium text-cyan-400">{fmtBytes(t.metric_bytes)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {/* Recent topups activity */}
      <div className="bg-surface-card rounded-xl border border-white/10 p-5">
        <h3 className="text-base font-semibold mb-3">Последние пополнения</h3>
        {(!data.recent_topups || data.recent_topups.length === 0) ? (
          <p className="text-sm text-text-secondary">Пока нет пополнений.</p>
        ) : (
          <div className="space-y-2">
            {data.recent_topups.map(tx => (
              <div key={tx.id} className="flex items-center justify-between gap-3 p-3 rounded-lg bg-white/[0.03] border border-white/5">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium truncate">{tx.tenant_name}</p>
                  <p className="text-[11px] text-text-secondary truncate">{tx.description}</p>
                </div>
                <span className="text-sm font-semibold text-green-400 whitespace-nowrap">+{fmtMoney(tx.amount)}</span>
                <span className="text-[11px] text-text-secondary whitespace-nowrap">{fmtDate(tx.created_at)}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function Kpi({ label, value, color, big }) {
  return (
    <div className="bg-surface-card rounded-xl border border-white/10 p-4">
      <p className="text-xs text-text-secondary mb-1">{label}</p>
      <p className={`${big ? 'text-2xl' : 'text-xl'} font-bold ${color || 'text-text-primary'}`}>{value}</p>
    </div>
  )
}
