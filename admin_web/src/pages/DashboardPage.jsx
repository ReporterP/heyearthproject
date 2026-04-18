import { useEffect, useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { fetchOverview, fetchTrafficChart } from '../api/admin.js'
import StatCard from '../components/StatCard.jsx'
import StatusBadge from '../components/StatusBadge.jsx'
import { formatBytes } from '../utils/format.js'

function formatRub(amount) {
  return new Intl.NumberFormat('ru-RU', { style: 'currency', currency: 'RUB', maximumFractionDigits: 0 }).format(amount)
}

// Мини-бар-чарт для трафика
function TrafficMiniChart({ data }) {
  if (!data || data.length === 0) return <p className="text-text-secondary text-sm">Нет данных</p>
  const maxVal = Math.max(...data.map(d => d.upload + d.download), 1)
  return (
    <div className="flex items-end gap-[2px] h-24">
      {data.map((d, i) => {
        const total = d.upload + d.download
        const h = Math.max((total / maxVal) * 100, 2)
        return (
          <div key={i} className="flex-1 flex flex-col items-center group relative">
            <div
              className="w-full rounded-t bg-brand/80 hover:bg-brand transition-colors min-w-[3px]"
              style={{ height: `${h}%` }}
              title={`${d.date}: ${formatBytes(total)}`}
            />
          </div>
        )
      })}
    </div>
  )
}

// Статус-индикатор сервера
function ServerStatusDot({ status }) {
  const color = status === 'online' ? 'bg-green-500' : status === 'offline' ? 'bg-red-500' : 'bg-yellow-500'
  return <span className={`inline-block w-2.5 h-2.5 rounded-full ${color}`} />
}

export default function DashboardPage() {
  const { t } = useT()
  const [data, setData] = useState(null)
  const [chart, setChart] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([
      fetchOverview(),
      fetchTrafficChart(30).catch(() => ({ daily: [] })),
    ])
      .then(([overview, chartData]) => {
        setData(overview)
        setChart(chartData?.daily || [])
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <p className="text-text-secondary">{t.loading}</p>
  if (error) return <p className="text-red-400">{error}</p>

  // Cards with `to` become clickable; sub-text in brand blue signals that.
  // Cards without `to` keep sub-text muted so it doesn't masquerade as a link.
  const cards = [
    { label: t.dashUsers, value: data?.users_count, sub: `${data?.active_users_count ?? 0} ${t.dashActiveUsers}`, to: '/users' },
    { label: t.dashServers, value: data?.servers_count, sub: `${data?.online_servers_count ?? 0} ${t.dashOnlineServers}`, to: '/servers' },
    { label: t.dashTrafficToday, value: formatBytes(data?.traffic_today), to: '/traffic' },
    { label: t.dashTrafficMonth, value: formatBytes(data?.traffic_month), to: '/traffic' },
    { label: t.dashRevenueMonth, value: formatRub(data?.revenue_month || 0), to: '/payments' },
    { label: t.dashPayments, value: data?.payments_count, to: '/payments' },
    // Groups is hidden from the sidebar but the /groups route still works:
    // linking the card keeps the number useful without re-adding a nav entry.
    { label: t.dashGroups, value: data?.groups_count, to: '/groups' },
    { label: t.dashPromocodes, value: data?.promocodes_count, to: '/promocodes' },
    { label: t.dashActiveReleases, value: data?.active_releases, to: '/releases' },
    { label: t.dashAdmins, value: data?.admins_count, to: '/admins' },
  ]

  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">{t.dashboardTitle}</h2>

      {/* Карточки */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-4">
        {cards.map(c => (
          <StatCard key={c.label} label={c.label} value={c.value} sub={c.sub} to={c.to} />
        ))}
      </div>

      {/* График трафика + Здоровье серверов */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* График трафика */}
        <div className="lg:col-span-2 bg-surface-card rounded-xl border border-white/10 p-5">
          <h3 className="text-lg font-semibold mb-4">{t.dashTrafficChart}</h3>
          <TrafficMiniChart data={chart} />
          {chart && chart.length > 0 && (
            <div className="flex justify-between mt-2 text-xs text-text-secondary">
              <span>{chart[0]?.date}</span>
              <span>{chart[chart.length - 1]?.date}</span>
            </div>
          )}
        </div>

        {/* Здоровье серверов */}
        <div className="bg-surface-card rounded-xl border border-white/10 p-5">
          <h3 className="text-lg font-semibold mb-4">{t.dashServerHealth}</h3>
          {(!data?.server_health || data.server_health.length === 0) ? (
            <p className="text-text-secondary text-sm">Нет серверов</p>
          ) : (
            <div className="space-y-3">
              {data.server_health.map(s => (
                <div key={s.id} className="flex items-center gap-3">
                  <ServerStatusDot status={s.status} />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium truncate">{s.name}</p>
                    <p className="text-xs text-text-secondary">{s.ip_address} {s.country_code && `(${s.country_code})`}</p>
                  </div>
                  <StatusBadge status={s.tunnel_status} />
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Топ пользователей по трафику */}
      {data?.top_users && data.top_users.length > 0 && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-5">
          <h3 className="text-lg font-semibold mb-4">{t.dashTopUsers}</h3>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-text-secondary border-b border-white/10">
                  <th className="text-left py-2 px-3">#</th>
                  <th className="text-left py-2 px-3">User ID</th>
                  <th className="text-right py-2 px-3">{t.trafficUpload}</th>
                  <th className="text-right py-2 px-3">{t.trafficDownload}</th>
                  <th className="text-right py-2 px-3">{t.trafficTotal}</th>
                </tr>
              </thead>
              <tbody>
                {data.top_users.map((u, i) => (
                  <tr key={u.user_id} className="border-b border-white/5 hover:bg-white/5">
                    <td className="py-2 px-3 text-text-secondary">{i + 1}</td>
                    <td className="py-2 px-3 font-mono text-xs">
                      {u.username || u.user_id}
                    </td>
                    <td className="py-2 px-3 text-right text-cyan-400">{formatBytes(u.upload)}</td>
                    <td className="py-2 px-3 text-right text-blue-400">{formatBytes(u.download)}</td>
                    <td className="py-2 px-3 text-right font-semibold">{formatBytes(u.total)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
