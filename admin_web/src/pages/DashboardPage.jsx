import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useT } from '../i18n/index.jsx'
import { fetchOverview, fetchTrafficChart, fetchCapacity, fetchWalletStats } from '../api/admin.js'
import { formatKop } from '../utils/wallet.js'
import StatusBadge from '../components/StatusBadge.jsx'
import CapacityPanel from '../components/servers/CapacityPanel.jsx'
import LaunchChecklist from '../components/servers/LaunchChecklist.jsx'
import { formatBytes } from '../utils/format.js'
import {
  Card,
  PageHeader,
  Skeleton,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
} from '../components/ui/index.js'

function formatRub(amount) {
  return new Intl.NumberFormat('ru-RU', { style: 'currency', currency: 'RUB', maximumFractionDigits: 0 }).format(amount)
}

// Mini bar chart for traffic
function TrafficMiniChart({ data }) {
  if (!data || data.length === 0) return <p className="text-secondary text-sm">Нет данных</p>
  const maxVal = Math.max(...data.map(d => d.upload + d.download), 1)
  return (
    <div className="flex items-end gap-[2px] h-24">
      {data.map((d, i) => {
        const total = d.upload + d.download
        const h = Math.max((total / maxVal) * 100, 2)
        return (
          <div key={i} className="flex-1 flex flex-col items-end h-full group relative">
            <div
              className="w-full rounded-t bg-accent/80 hover:bg-accent transition-colors min-w-[3px]"
              style={{ height: `${h}%` }}
              title={`${d.date}: ${formatBytes(total)}`}
            />
          </div>
        )
      })}
    </div>
  )
}

function ServerStatusDot({ status }) {
  const color = status === 'online' ? 'bg-success' : status === 'offline' ? 'bg-danger' : 'bg-warning'
  return <span className={`inline-block w-2.5 h-2.5 rounded-full ${color}`} />
}

function StatCard({ label, value, sub, to }) {
  const body = (
    <div className="p-5">
      <p className="text-xs text-tertiary uppercase tracking-wider mb-2">{label}</p>
      <p className="text-2xl font-semibold tabular-nums font-mono text-primary">{value ?? '—'}</p>
      {sub != null && (
        <p className={`text-sm mt-1 ${to ? 'text-accent' : 'text-tertiary'}`}>{sub}</p>
      )}
    </div>
  )
  if (to) {
    return (
      <Link to={to} className="block">
        <Card className="hover:border-strong transition-colors">{body}</Card>
      </Link>
    )
  }
  return <Card>{body}</Card>
}

export default function DashboardPage() {
  const { t } = useT()
  const [data, setData] = useState(null)
  const [chart, setChart] = useState(null)
  const [cap, setCap] = useState(null)
  const [wallet, setWallet] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([
      fetchOverview(),
      fetchTrafficChart(30).catch(() => ({ daily: [] })),
      fetchCapacity().catch(() => null),
      fetchWalletStats().catch(() => null),
    ])
      .then(([overview, chartData, capData, walletData]) => {
        setWallet(walletData)
        setData(overview)
        setChart(chartData?.daily || [])
        setCap(capData)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return (
    <div className="space-y-6">
      <PageHeader title={t.dashboardTitle} />
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-4">
        {Array.from({ length: 9 }).map((_, i) => (
          <Skeleton key={i} className="h-24" />
        ))}
      </div>
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <Skeleton className="h-48 lg:col-span-2" />
        <Skeleton className="h-48" />
      </div>
    </div>
  )
  if (error) return <p className="text-danger">{error}</p>

  const cards = [
    { label: t.dashUsers, value: data?.users_count, sub: `${data?.active_users_count ?? 0} ${t.dashActiveUsers}`, to: '/users' },
    { label: t.dashServers, value: data?.servers_count, sub: `${data?.online_servers_count ?? 0} ${t.dashOnlineServers}`, to: '/servers' },
    { label: t.dashTrafficToday, value: formatBytes(data?.traffic_today) },
    { label: t.dashTrafficMonth, value: formatBytes(data?.traffic_month) },
    { label: t.dashRevenueMonth, value: formatRub(data?.revenue_month || 0), to: '/payments' },
    { label: t.dashPayments, value: data?.payments_count, to: '/payments' },
    ...(wallet ? [{
      label: 'На кошельках клиентов', value: formatKop(wallet.liabilities_kop),
      sub: `за месяц: +${formatKop(wallet.topups_month_kop)} / −${formatKop(wallet.spent_month_kop)}`, to: '/payments',
    }] : []),
    { label: t.dashPromocodes, value: data?.promocodes_count, to: '/promocodes' },
    { label: t.dashAdmins, value: data?.admins_count, to: '/admins' },
  ]

  return (
    <div className="space-y-6">
      <PageHeader title={t.dashboardTitle} />

      {/* «Запуск»: the way to a service that works on mobile networks (B6) */}
      <LaunchChecklist />

      {/* KPI cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-4">
        {cards.map(c => (
          <StatCard key={c.label} label={c.label} value={c.value} sub={c.sub} to={c.to} />
        ))}
      </div>

      {/* «Мощность»: when to buy more (B5) */}
      <CapacityPanel rep={cap} compact />

      {/* Traffic chart + Server health */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <Card className="lg:col-span-2 p-5">
          <h3 className="text-lg font-semibold text-primary mb-4">{t.dashTrafficChart}</h3>
          <TrafficMiniChart data={chart} />
          {chart && chart.length > 0 && (
            <div className="flex justify-between mt-2 text-xs text-tertiary">
              <span>{chart[0]?.date}</span>
              <span>{chart[chart.length - 1]?.date}</span>
            </div>
          )}
        </Card>

        <Card className="p-5">
          <h3 className="text-lg font-semibold text-primary mb-4">{t.dashServerHealth}</h3>
          {(!data?.server_health || data.server_health.length === 0) ? (
            <p className="text-secondary text-sm">Нет серверов</p>
          ) : (
            <div className="space-y-3">
              {data.server_health.map(s => (
                <div key={s.id} className="flex items-center gap-3">
                  <ServerStatusDot status={s.status} />
                  <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium text-primary truncate">{s.name}</p>
                    <p className="text-xs text-tertiary">{s.ip_address} {s.country_code && `(${s.country_code})`}</p>
                  </div>
                  <StatusBadge status={s.tunnel_status} />
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>

      {/* Top users by traffic */}
      {data?.top_users && data.top_users.length > 0 && (
        <Card className="p-5">
          <h3 className="text-lg font-semibold text-primary mb-4">{t.dashTopUsers}</h3>
          <Table>
            <Thead>
              <Tr>
                <Th>#</Th>
                <Th>User ID</Th>
                <Th align="right">{t.trafficUpload}</Th>
                <Th align="right">{t.trafficDownload}</Th>
                <Th align="right">{t.trafficTotal}</Th>
              </Tr>
            </Thead>
            <Tbody>
              {data.top_users.map((u, i) => (
                <Tr key={u.user_id}>
                  <Td className="text-tertiary">{i + 1}</Td>
                  <Td mono className="text-xs">{u.username || u.user_id}</Td>
                  <Td align="right" mono className="text-info">{formatBytes(u.upload)}</Td>
                  <Td align="right" mono className="text-info">{formatBytes(u.download)}</Td>
                  <Td align="right" mono className="font-semibold text-primary">{formatBytes(u.total)}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
        </Card>
      )}
    </div>
  )
}
