import { useEffect, useState, useCallback } from 'react'
import { useT } from '../i18n/index.jsx'
import { fetchAuditLog } from '../api/admin.js'

const ACTION_COLORS = {
  create_user: 'bg-green-500/20 text-green-400',
  update_user: 'bg-blue-500/20 text-blue-400',
  delete_user: 'bg-red-500/20 text-red-400',
  create_server: 'bg-green-500/20 text-green-400',
  update_server: 'bg-blue-500/20 text-blue-400',
  delete_server: 'bg-red-500/20 text-red-400',
  update_setting: 'bg-yellow-500/20 text-yellow-400',
}

function formatTime(iso) {
  if (!iso) return '\u2014'
  try {
    return new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
  } catch { return iso }
}

export default function AuditLogPage() {
  const { t } = useT()
  const [entries, setEntries] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 25

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchAuditLog({ page, pageSize, search })
      .then(data => {
        setEntries(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [page, search])

  useEffect(() => { load() }, [load])

  const totalPages = Math.ceil(total / pageSize)

  return (
    <div className="space-y-4">
      <h2 className="text-2xl font-bold">{t.navAuditLog}</h2>

      <div className="flex items-center gap-3">
        <input
          type="text"
          placeholder={t.search}
          value={search}
          onChange={e => { setSearch(e.target.value); setPage(1) }}
          className="px-3 py-2 rounded-lg bg-surface-card border border-white/10 text-sm w-80"
        />
      </div>

      {error && <p className="text-red-400">{error}</p>}

      <div className="bg-surface-card rounded-xl border border-white/10 overflow-hidden">
        {loading ? (
          <p className="p-6 text-text-secondary">{t.loading}</p>
        ) : entries.length === 0 ? (
          <p className="p-6 text-text-secondary">Записей не найдено</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-text-secondary border-b border-white/10">
                  <th className="text-left py-3 px-4">Время</th>
                  <th className="text-left py-3 px-4">Админ</th>
                  <th className="text-left py-3 px-4">Действие</th>
                  <th className="text-left py-3 px-4">Тип</th>
                  <th className="text-left py-3 px-4">ID цели</th>
                  <th className="text-left py-3 px-4">IP</th>
                  <th className="text-left py-3 px-4">Детали</th>
                </tr>
              </thead>
              <tbody>
                {entries.map(e => (
                  <tr key={e.id} className="border-b border-white/5 hover:bg-white/5">
                    <td className="py-2 px-4 text-xs text-text-secondary whitespace-nowrap">
                      {formatTime(e.created_at)}
                    </td>
                    <td className="py-2 px-4">{e.admin_username || `#${e.admin_id}`}</td>
                    <td className="py-2 px-4">
                      <span className={`text-xs px-2 py-0.5 rounded ${ACTION_COLORS[e.action] || 'bg-white/10 text-text-secondary'}`}>
                        {e.action}
                      </span>
                    </td>
                    <td className="py-2 px-4 text-text-secondary">{e.target_type || '\u2014'}</td>
                    <td className="py-2 px-4 font-mono text-xs">{e.target_id || '\u2014'}</td>
                    <td className="py-2 px-4 text-text-secondary text-xs">{e.ip_address || '\u2014'}</td>
                    <td className="py-2 px-4 text-xs text-text-secondary max-w-xs truncate">
                      {e.details && e.details !== '{}' && typeof e.details === 'object'
                        ? JSON.stringify(e.details)
                        : '\u2014'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {totalPages > 1 && (
          <div className="flex items-center justify-between px-4 py-3 border-t border-white/10">
            <span className="text-sm text-text-secondary">{total} записей</span>
            <div className="flex gap-1">
              <button disabled={page <= 1} onClick={() => setPage(p => p - 1)} className="px-3 py-1 rounded text-sm bg-white/5 disabled:opacity-30 hover:bg-white/10">&laquo;</button>
              <span className="px-3 py-1 text-sm">{page} / {totalPages}</span>
              <button disabled={page >= totalPages} onClick={() => setPage(p => p + 1)} className="px-3 py-1 rounded text-sm bg-white/5 disabled:opacity-30 hover:bg-white/10">&raquo;</button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
