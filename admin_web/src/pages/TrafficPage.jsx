import { useEffect, useState, useCallback } from 'react'
import { BarChart3 } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { fetchTrafficUsers, fetchTrafficUserDetail } from '../api/admin.js'
import { formatBytes } from '../utils/format.js'
import {
  Button,
  Card,
  CardBody,
  CardHeader,
  EmptyState,
  Input,
  PageHeader,
  Table,
  Tbody,
  Td,
  Th,
  Thead,
  Tr,
  TableSkeleton,
} from '../components/ui/index.js'

const PERIODS = [
  { key: 'today', label: 'trafficPeriodToday' },
  { key: 'week', label: 'trafficPeriodWeek' },
  { key: 'month', label: 'trafficPeriodMonth' },
]

function TrafficBarChart({ data }) {
  if (!data || data.length === 0) return null
  const maxVal = Math.max(...data.map(d => d.upload + d.download), 1)
  return (
    <div className="mt-4">
      <div className="flex items-end gap-1 h-32">
        {data.map((d, i) => {
          const total = d.upload + d.download
          const h = Math.max((total / maxVal) * 100, 2)
          const upH = total > 0 ? (d.upload / total) * h : 0
          const downH = h - upH
          return (
            <div key={i} className="flex-1 flex flex-col items-stretch group relative" title={`${d.date}\n${formatBytes(d.upload)} / ${formatBytes(d.download)}`}>
              <div className="rounded-t bg-info/70" style={{ height: `${upH}%` }} />
              <div className="bg-accent/70" style={{ height: `${downH}%` }} />
            </div>
          )
        })}
      </div>
      {data.length > 1 && (
        <div className="flex justify-between mt-1 text-xs text-tertiary tabular-nums">
          <span>{data[0]?.date}</span>
          <span>{data[data.length - 1]?.date}</span>
        </div>
      )}
      <div className="flex gap-4 mt-2 text-xs text-tertiary">
        <span className="flex items-center gap-1"><span className="w-3 h-3 bg-info/70 rounded inline-block" /> Upload</span>
        <span className="flex items-center gap-1"><span className="w-3 h-3 bg-accent/70 rounded inline-block" /> Download</span>
      </div>
    </div>
  )
}

export default function TrafficPage() {
  const { t } = useT()
  const [period, setPeriod] = useState('month')
  const [users, setUsers] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [error, setError] = useState('')

  // Детали пользователя
  const [selectedUser, setSelectedUser] = useState(null)
  const [userDetail, setUserDetail] = useState(null)
  const [detailLoading, setDetailLoading] = useState(false)

  const pageSize = 20

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchTrafficUsers({ page, pageSize, search, period })
      .then(data => {
        setUsers(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [page, period, search])

  useEffect(() => { load() }, [load])

  const openUserDetail = (userId) => {
    setSelectedUser(userId)
    setDetailLoading(true)
    fetchTrafficUserDetail(userId, period)
      .then(setUserDetail)
      .catch(() => setUserDetail(null))
      .finally(() => setDetailLoading(false))
  }

  const totalPages = Math.ceil(total / pageSize)

  return (
    <div className="space-y-4">
      <PageHeader
        title={t.trafficTitle}
        subtitle="Использование трафика по пользователям за выбранный период"
      />

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="inline-flex rounded border border-default overflow-hidden">
          {PERIODS.map(p => (
            <button
              key={p.key}
              onClick={() => { setPeriod(p.key); setPage(1) }}
              className={`px-4 py-1.5 text-sm transition-colors ${
                period === p.key
                  ? 'bg-accent text-white'
                  : 'bg-elevated text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {t[p.label]}
            </button>
          ))}
        </div>
        <Input
          type="text"
          placeholder={t.search}
          value={search}
          onChange={e => { setSearch(e.target.value); setPage(1) }}
          className="w-64"
        />
      </div>

      {error && <p className="text-danger">{error}</p>}

      {/* Table */}
      {loading ? (
        <TableSkeleton rows={8} cols={5} />
      ) : users.length === 0 ? (
        <EmptyState
          icon={<BarChart3 size={32} />}
          title={t.trafficNoData || 'Нет данных трафика'}
          description="За выбранный период статистика пуста. Попробуйте другой период или фильтр."
        />
      ) : (
        <Card>
          <Table>
            <Thead>
              <Tr>
                <Th>User ID</Th>
                <Th>Username</Th>
                <Th align="right">{t.trafficUpload}</Th>
                <Th align="right">{t.trafficDownload}</Th>
                <Th align="right">{t.trafficTotal}</Th>
              </Tr>
            </Thead>
            <Tbody>
              {users.map(u => (
                <Tr
                  key={u.user_id}
                  className={`cursor-pointer ${selectedUser === u.user_id ? 'bg-accent/10' : ''}`}
                  onClick={() => openUserDetail(u.user_id)}
                >
                  <Td mono>{u.user_id}</Td>
                  <Td>{u.username || '—'}</Td>
                  <Td align="right" mono><span className="text-info">{formatBytes(u.upload)}</span></Td>
                  <Td align="right" mono><span className="text-accent">{formatBytes(u.download)}</span></Td>
                  <Td align="right" mono className="font-semibold text-primary">{formatBytes(u.total)}</Td>
                </Tr>
              ))}
            </Tbody>
          </Table>

          {/* Pagination */}
          {totalPages > 1 && (
            <div className="flex items-center justify-between px-4 py-3 border-t border-default">
              <span className="text-sm text-tertiary tabular-nums">{total} записей</span>
              <div className="flex items-center gap-1">
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page <= 1}
                  onClick={() => setPage(p => p - 1)}
                  aria-label="Предыдущая"
                >«</Button>
                <span className="px-2 text-sm text-tertiary tabular-nums">{page} / {totalPages}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={page >= totalPages}
                  onClick={() => setPage(p => p + 1)}
                  aria-label="Следующая"
                >»</Button>
              </div>
            </div>
          )}
        </Card>
      )}

      {/* User detail */}
      {selectedUser && (
        <Card>
          <CardHeader>
            <div className="flex items-center justify-between">
              <h3 className="text-base font-semibold text-primary">User <span className="font-mono">{selectedUser}</span></h3>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => { setSelectedUser(null); setUserDetail(null) }}
              >{t.close}</Button>
            </div>
          </CardHeader>
          <CardBody>
            {detailLoading ? (
              <p className="text-tertiary">{t.loading}</p>
            ) : userDetail ? (
              <div>
                <div className="grid grid-cols-3 gap-4 mb-4">
                  <div className="text-center">
                    <p className="text-xs text-tertiary">{t.trafficUpload}</p>
                    <p className="text-xl font-semibold text-info tabular-nums font-mono">{formatBytes(userDetail.upload)}</p>
                  </div>
                  <div className="text-center">
                    <p className="text-xs text-tertiary">{t.trafficDownload}</p>
                    <p className="text-xl font-semibold text-accent tabular-nums font-mono">{formatBytes(userDetail.download)}</p>
                  </div>
                  <div className="text-center">
                    <p className="text-xs text-tertiary">{t.trafficTotal}</p>
                    <p className="text-xl font-semibold text-primary tabular-nums font-mono">{formatBytes(userDetail.total)}</p>
                  </div>
                </div>
                <TrafficBarChart data={userDetail.daily} />
              </div>
            ) : (
              <p className="text-tertiary">{t.trafficNoData}</p>
            )}
          </CardBody>
        </Card>
      )}
    </div>
  )
}
