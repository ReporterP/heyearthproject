import { useEffect, useState, useCallback } from 'react'
import { useT } from '../i18n/index.jsx'
import { fetchTrafficUsers, fetchTrafficUserDetail } from '../api/admin.js'
import { formatBytes } from '../utils/format.js'

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
              <div className="rounded-t bg-cyan-500/70" style={{ height: `${upH}%` }} />
              <div className="bg-blue-500/70" style={{ height: `${downH}%` }} />
            </div>
          )
        })}
      </div>
      {data.length > 1 && (
        <div className="flex justify-between mt-1 text-xs text-text-secondary">
          <span>{data[0]?.date}</span>
          <span>{data[data.length - 1]?.date}</span>
        </div>
      )}
      <div className="flex gap-4 mt-2 text-xs text-text-secondary">
        <span className="flex items-center gap-1"><span className="w-3 h-3 bg-cyan-500/70 rounded inline-block" /> Upload</span>
        <span className="flex items-center gap-1"><span className="w-3 h-3 bg-blue-500/70 rounded inline-block" /> Download</span>
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
      <h2 className="text-2xl font-bold">{t.trafficTitle}</h2>

      {/* Фильтры */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex rounded-lg overflow-hidden border border-white/10">
          {PERIODS.map(p => (
            <button
              key={p.key}
              onClick={() => { setPeriod(p.key); setPage(1) }}
              className={`px-4 py-2 text-sm transition-colors ${
                period === p.key
                  ? 'bg-brand text-white'
                  : 'bg-surface-card text-text-secondary hover:text-white'
              }`}
            >
              {t[p.label]}
            </button>
          ))}
        </div>
        <input
          type="text"
          placeholder={t.search}
          value={search}
          onChange={e => { setSearch(e.target.value); setPage(1) }}
          className="px-3 py-2 rounded-lg bg-surface-card border border-white/10 text-sm w-64"
        />
      </div>

      {error && <p className="text-red-400">{error}</p>}

      {/* Таблица */}
      <div className="bg-surface-card rounded-xl border border-white/10 overflow-hidden">
        {loading ? (
          <p className="p-6 text-text-secondary">{t.loading}</p>
        ) : users.length === 0 ? (
          <p className="p-6 text-text-secondary">{t.trafficNoData}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-text-secondary border-b border-white/10">
                  <th className="text-left py-3 px-4">User ID</th>
                  <th className="text-left py-3 px-4">Username</th>
                  <th className="text-right py-3 px-4">{t.trafficUpload}</th>
                  <th className="text-right py-3 px-4">{t.trafficDownload}</th>
                  <th className="text-right py-3 px-4">{t.trafficTotal}</th>
                </tr>
              </thead>
              <tbody>
                {users.map(u => (
                  <tr
                    key={u.user_id}
                    className={`border-b border-white/5 hover:bg-white/5 cursor-pointer transition-colors ${
                      selectedUser === u.user_id ? 'bg-brand/10' : ''
                    }`}
                    onClick={() => openUserDetail(u.user_id)}
                  >
                    <td className="py-3 px-4 font-mono text-xs">{u.user_id}</td>
                    <td className="py-3 px-4">{u.username || '\u2014'}</td>
                    <td className="py-3 px-4 text-right text-cyan-400">{formatBytes(u.upload)}</td>
                    <td className="py-3 px-4 text-right text-blue-400">{formatBytes(u.download)}</td>
                    <td className="py-3 px-4 text-right font-semibold">{formatBytes(u.total)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* Пагинация */}
        {totalPages > 1 && (
          <div className="flex items-center justify-between px-4 py-3 border-t border-white/10">
            <span className="text-sm text-text-secondary">
              {total} записей
            </span>
            <div className="flex gap-1">
              <button
                disabled={page <= 1}
                onClick={() => setPage(p => p - 1)}
                className="px-3 py-1 rounded text-sm bg-white/5 disabled:opacity-30 hover:bg-white/10"
              >
                &laquo;
              </button>
              <span className="px-3 py-1 text-sm">{page} / {totalPages}</span>
              <button
                disabled={page >= totalPages}
                onClick={() => setPage(p => p + 1)}
                className="px-3 py-1 rounded text-sm bg-white/5 disabled:opacity-30 hover:bg-white/10"
              >
                &raquo;
              </button>
            </div>
          </div>
        )}
      </div>

      {/* Детали пользователя */}
      {selectedUser && (
        <div className="bg-surface-card rounded-xl border border-white/10 p-5">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-lg font-semibold">
              {selectedUser}
            </h3>
            <button
              onClick={() => { setSelectedUser(null); setUserDetail(null) }}
              className="text-text-secondary hover:text-white text-sm"
            >
              {t.close}
            </button>
          </div>
          {detailLoading ? (
            <p className="text-text-secondary">{t.loading}</p>
          ) : userDetail ? (
            <div>
              <div className="grid grid-cols-3 gap-4 mb-4">
                <div className="text-center">
                  <p className="text-xs text-text-secondary">{t.trafficUpload}</p>
                  <p className="text-xl font-bold text-cyan-400">{formatBytes(userDetail.upload)}</p>
                </div>
                <div className="text-center">
                  <p className="text-xs text-text-secondary">{t.trafficDownload}</p>
                  <p className="text-xl font-bold text-blue-400">{formatBytes(userDetail.download)}</p>
                </div>
                <div className="text-center">
                  <p className="text-xs text-text-secondary">{t.trafficTotal}</p>
                  <p className="text-xl font-bold">{formatBytes(userDetail.total)}</p>
                </div>
              </div>
              <TrafficBarChart data={userDetail.daily} />
            </div>
          ) : (
            <p className="text-text-secondary">{t.trafficNoData}</p>
          )}
        </div>
      )}
    </div>
  )
}
