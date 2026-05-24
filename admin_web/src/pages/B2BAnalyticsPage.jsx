import { useEffect, useState } from 'react'
import { TrendingUp } from 'lucide-react'
import {
  Card,
  CardBody,
  CardHeader,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  EmptyState,
} from '../components/ui/index.js'

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

  if (loading) return <p className="text-secondary">Загрузка…</p>
  if (error || !data) {
    return (
      <div>
        <PageHeader title="B2B Аналитика" subtitle="Сводка по тенантам, выручке и активности" />
        <EmptyState icon={<TrendingUp className="w-12 h-12" />} title="Нет данных" description={error || 'Не удалось загрузить аналитику'} />
      </div>
    )
  }

  const c = data.counts || {}
  return (
    <div className="space-y-6">
      <PageHeader title="B2B Аналитика" subtitle="Сводка по тенантам, выручке и активности" />

      {/* KPI cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4">
        <Kpi label="Всего компаний" value={c.total || 0} />
        <Kpi label="Активных" value={c.active || 0} color="text-success" />
        <Kpi label="Неактивных" value={c.inactive || 0} color="text-warning" />
        <Kpi label="Приостановлено" value={c.suspended || 0} color="text-danger" />
        <Kpi label="B2B-юзеров" value={data.members_total || 0} />
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <Kpi label="Доход за 30д (списания)" value={fmtMoney(data.mrr_30d)} color="text-success" big />
        <Kpi label="Пополнения за 30д" value={fmtMoney(data.credits_30d)} color="text-info" big />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {/* Top by spend */}
        <Card>
          <CardHeader>
            <h3 className="text-base font-semibold text-primary">Топ-10 по тратам (30д)</h3>
          </CardHeader>
          <CardBody>
            {(!data.top_by_spend || data.top_by_spend.length === 0) ? (
              <p className="text-sm text-secondary">Пока нет списаний.</p>
            ) : (
              <Table>
                <Thead>
                  <Tr>
                    <Th>Компания</Th>
                    <Th align="right">Юзеры</Th>
                    <Th align="right">Списано</Th>
                    <Th align="right">Баланс</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {data.top_by_spend.map(t => (
                    <Tr key={t.id}>
                      <Td>
                        <span className={`mr-2 inline-block w-2 h-2 rounded-full ${t.status === 'active' ? 'bg-success' : t.status === 'suspended' ? 'bg-danger' : 'bg-warning'}`} />
                        {t.name}
                      </Td>
                      <Td align="right" mono>{t.members}</Td>
                      <Td align="right" mono><span className="text-danger font-medium">{fmtMoney(t.metric_rub)}</span></Td>
                      <Td align="right" mono><span className="text-secondary">{fmtMoney(t.balance)}</span></Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </CardBody>
        </Card>

        {/* Top by traffic */}
        <Card>
          <CardHeader>
            <h3 className="text-base font-semibold text-primary">Топ-10 по трафику (30д)</h3>
          </CardHeader>
          <CardBody>
            {(!data.top_by_traffic || data.top_by_traffic.length === 0) ? (
              <p className="text-sm text-secondary">Пока нет трафика по тенантам.</p>
            ) : (
              <Table>
                <Thead>
                  <Tr>
                    <Th>Компания</Th>
                    <Th align="right">Юзеры</Th>
                    <Th align="right">Трафик</Th>
                  </Tr>
                </Thead>
                <Tbody>
                  {data.top_by_traffic.map(t => (
                    <Tr key={t.id}>
                      <Td>{t.name}</Td>
                      <Td align="right" mono>{t.members}</Td>
                      <Td align="right" mono><span className="text-info font-medium">{fmtBytes(t.metric_bytes)}</span></Td>
                    </Tr>
                  ))}
                </Tbody>
              </Table>
            )}
          </CardBody>
        </Card>
      </div>

      {/* Recent topups activity */}
      <Card>
        <CardHeader>
          <h3 className="text-base font-semibold text-primary">Последние пополнения</h3>
        </CardHeader>
        <CardBody>
          {(!data.recent_topups || data.recent_topups.length === 0) ? (
            <p className="text-sm text-secondary">Пока нет пополнений.</p>
          ) : (
            <div className="space-y-2">
              {data.recent_topups.map(tx => (
                <div key={tx.id} className="flex items-center justify-between gap-3 p-3 rounded bg-canvas border border-default">
                  <div className="min-w-0 flex-1">
                    <p className="text-sm font-medium text-primary truncate">{tx.tenant_name}</p>
                    <p className="text-[11px] text-tertiary truncate">{tx.description}</p>
                  </div>
                  <span className="text-sm font-semibold text-success whitespace-nowrap tabular-nums">+{fmtMoney(tx.amount)}</span>
                  <span className="text-[11px] text-tertiary whitespace-nowrap tabular-nums">{fmtDate(tx.created_at)}</span>
                </div>
              ))}
            </div>
          )}
        </CardBody>
      </Card>
    </div>
  )
}

function Kpi({ label, value, color, big }) {
  return (
    <Card className="p-4">
      <p className="text-xs text-tertiary mb-1">{label}</p>
      <p className={`${big ? 'text-2xl' : 'text-xl'} font-semibold tabular-nums ${color || 'text-primary'}`}>{value}</p>
    </Card>
  )
}
