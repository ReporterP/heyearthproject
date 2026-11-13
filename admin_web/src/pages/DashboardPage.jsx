import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useT } from '../i18n/index.jsx'
import { fetchOverview, fetchTrafficChart, fetchCapacity, fetchWalletStats } from '../api/admin.js'
import { formatKop } from '../utils/wallet.js'
import StatusBadge from '../components/StatusBadge.jsx'
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

// ── Capacity widget ──
const VERDICT_MAP = {
  ok: ['bg-success/15 text-success', '🟢 Запас есть'],
  warn: ['bg-warning/15 text-warning', '🟡 Близко к пределу'],
  critical: ['bg-danger/15 text-danger', '🔴 Пора масштабировать'],
  unknown: ['bg-overlay text-tertiary', 'н/д'],
}

function VerdictChip({ verdict }) {
  const [cls, text] = VERDICT_MAP[verdict] || VERDICT_MAP.unknown
  return <span className={`px-2 py-0.5 rounded text-xs font-medium whitespace-nowrap ${cls}`}>{text}</span>
}

function CapacityBar({ label, pct, value, tone = 'accent' }) {
  const toneClass = { ok: 'bg-success', warn: 'bg-warning', critical: 'bg-danger', accent: 'bg-accent' }[tone] || 'bg-accent'
  return (
    <div>
      <div className="flex justify-between text-xs mb-1">
        <span className="text-tertiary">{label}</span>
        <span className="text-secondary font-mono tabular-nums">{value}</span>
      </div>
      <div className="h-1.5 rounded-full bg-canvas overflow-hidden">
        <div className={`h-full ${toneClass} transition-all`} style={{ width: `${Math.min(Math.max(pct || 0, 0), 100)}%` }} />
      </div>
    </div>
  )
}

function CapacityWidget({ cap }) {
  if (!cap) return null
  const h = cap.hub || {}
  const e = cap.exit || {}
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <Card className="p-5">
        <div className="flex items-center justify-between gap-2 mb-4">
          <h3 className="text-lg font-semibold text-primary">Ёмкость Hub</h3>
          <VerdictChip verdict={h.verdict} />
        </div>
        <div className="space-y-3">
          <CapacityBar label="CPU load" tone={h.verdict}
            pct={h.cpu_pct} value={`${h.cpu_load ?? '—'} / ${h.cpu_cores ?? '—'} ядра (${h.cpu_pct ?? '—'}%)`} />
          {h.ram_pct != null && <CapacityBar label="RAM" tone="accent" pct={h.ram_pct} value={`${h.ram_pct}%`} />}
          {h.traffic_gb_day != null && <CapacityBar label="Трафик/день" tone="accent"
            pct={h.traffic_pct} value={`${h.traffic_gb_day} / ${h.quota_gb_day} GB`} />}
          {h.max_subs != null && <CapacityBar label="Активные подписки" tone={h.verdict}
            pct={h.fill_pct} value={`${h.active_subs} / ~${h.max_subs}`} />}
        </div>
        {h.cost_per_sub != null && (
          <div className="flex justify-between mt-4 pt-3 border-t border-default text-xs">
            <span className="text-tertiary">Себестоимость / юзер</span>
            <span className="text-secondary font-mono tabular-nums">{h.cost_per_sub} ₽/мес</span>
          </div>
        )}
      </Card>

      <Card className="p-5">
        <div className="flex items-center justify-between gap-2 mb-4">
          <h3 className="text-lg font-semibold text-primary truncate">Exit · {e.name || 'н/д'}</h3>
          <VerdictChip verdict={e.online ? e.verdict : 'unknown'} />
        </div>
        {e.online ? (
          <div className="space-y-3">
            {e.cpu_pct != null && <CapacityBar label="CPU" tone={e.verdict} pct={e.cpu_pct} value={`${e.cpu_pct}%`} />}
            {e.ram_pct != null && <CapacityBar label="RAM" tone="accent" pct={e.ram_pct} value={`${e.ram_pct}%`} />}
            <div className="flex justify-between text-xs pt-1">
              <span className="text-tertiary">Активные соединения</span>
              <span className="text-secondary font-mono tabular-nums">{e.active_connections ?? '—'}</span>
            </div>
          </div>
        ) : (
          <p className="text-secondary text-sm">Сервер офлайн или метрики недоступны</p>
        )}
      </Card>
    </div>
  )
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
    { label: t.dashServers, value: data?.servers_count, sub: `${data?.online_servers_count ?? 0} ${t.dashOnlineServers}`, to: '/nodes' },
    { label: t.dashTrafficToday, value: formatBytes(data?.traffic_today), to: '/traffic' },
    { label: t.dashTrafficMonth, value: formatBytes(data?.traffic_month), to: '/traffic' },
    { label: t.dashRevenueMonth, value: formatRub(data?.revenue_month || 0), to: '/payments' },
    { label: t.dashPayments, value: data?.payments_count, to: '/payments' },
    ...(wallet ? [{
      label: 'На кошельках клиентов', value: formatKop(wallet.liabilities_kop),
      sub: `за месяц: +${formatKop(wallet.topups_month_kop)} / −${formatKop(wallet.spent_month_kop)}`, to: '/payments',
    }] : []),
    { label: t.dashPromocodes, value: data?.promocodes_count, to: '/promocodes' },
    { label: t.dashActiveReleases, value: data?.active_releases, to: '/releases' },
    { label: t.dashAdmins, value: data?.admins_count, to: '/admins' },
  ]

  return (
    <div className="space-y-6">
      <PageHeader title={t.dashboardTitle} />

      {/* KPI cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-4">
        {cards.map(c => (
          <StatCard key={c.label} label={c.label} value={c.value} sub={c.sub} to={c.to} />
        ))}
      </div>

      {/* Infrastructure capacity / headroom */}
      <CapacityWidget cap={cap} />

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
