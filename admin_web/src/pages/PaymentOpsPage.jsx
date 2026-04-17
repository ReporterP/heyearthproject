import { useState } from 'react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { pagedFetch, apiFetch } from '../api/client.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'

function fetchEmailLog(params) { return pagedFetch('/email-log', params) }
function fetchRetryQueue(params) { return pagedFetch('/payment-retries', params) }
function cancelRetry(id, reason) { return apiFetch(`/payment-retries/${id}/cancel`, { method: 'POST', body: { reason } }) }

const EMAIL_STATUS_COLORS = {
  sent: 'bg-green-500/20 text-green-400',
  pending: 'bg-yellow-500/20 text-yellow-400',
  failed: 'bg-red-500/20 text-red-400',
  disabled: 'bg-gray-500/20 text-gray-400',
}
const RETRY_STATUS_COLORS = {
  pending: 'bg-yellow-500/20 text-yellow-400',
  in_progress: 'bg-blue-500/20 text-blue-400',
  succeeded: 'bg-green-500/20 text-green-400',
  exhausted: 'bg-red-500/20 text-red-400',
  canceled: 'bg-gray-500/20 text-gray-400',
}

function formatDate(s) {
  if (!s) return '—'
  return new Date(s).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function EmailLogTab() {
  const paged = usePagedData(fetchEmailLog)
  return (
    <div>
      <SearchPaginationBar search={paged.search} onSearch={paged.doSearch} page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage} loading={paged.loading} />
      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary text-center py-12">Журнал email пуст</p>
      ) : (
        <div className="overflow-x-auto mt-4">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left text-xs uppercase">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">Email</th>
                <th className="py-2 px-3">Шаблон</th>
                <th className="py-2 px-3">Статус</th>
                <th className="py-2 px-3">Отправлено</th>
                <th className="py-2 px-3">Ошибка</th>
              </tr>
            </thead>
            <tbody>
              {paged.items.map(item => (
                <tr key={item.id} className="border-b border-white/5 hover:bg-white/5">
                  <td className="py-2 px-3">{item.id}</td>
                  <td className="py-2 px-3 font-mono text-xs">{item.email}</td>
                  <td className="py-2 px-3 text-xs">{item.template}</td>
                  <td className="py-2 px-3">
                    <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${EMAIL_STATUS_COLORS[item.status] || 'bg-gray-500/20 text-gray-400'}`}>{item.status}</span>
                  </td>
                  <td className="py-2 px-3 text-xs text-text-secondary whitespace-nowrap">{formatDate(item.sent_at || item.created_at)}</td>
                  <td className="py-2 px-3 text-xs text-red-400/80 max-w-[300px] truncate" title={item.error}>{item.error || ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function RetryQueueTab() {
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchRetryQueue)

  async function handleCancel(id) {
    const reason = prompt('Причина отмены:', 'admin_manual_cancel')
    if (!reason) return
    try {
      await cancelRetry(id, reason)
      toast.success('Retry отменён')
      paged.reload()
    } catch (e) { toast.error(e.message) }
  }

  return (
    <div>
      <SearchPaginationBar search={paged.search} onSearch={paged.doSearch} page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage} loading={paged.loading} />
      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary text-center py-12">Очередь retries пуста — все recurring платежи успешны</p>
      ) : (
        <div className="overflow-x-auto mt-4">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left text-xs uppercase">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">User</th>
                <th className="py-2 px-3">План</th>
                <th className="py-2 px-3">Попытка</th>
                <th className="py-2 px-3">Статус</th>
                <th className="py-2 px-3">Следующая</th>
                <th className="py-2 px-3">Grace до</th>
                <th className="py-2 px-3">Ошибка</th>
                {can('payments.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {paged.items.map(r => (
                <tr key={r.id} className="border-b border-white/5 hover:bg-white/5">
                  <td className="py-2 px-3">{r.id}</td>
                  <td className="py-2 px-3 font-mono text-xs">{r.user_id_text?.slice(0, 12)}…</td>
                  <td className="py-2 px-3">{r.plan_name}</td>
                  <td className="py-2 px-3">{r.attempt}/{r.max_attempts}</td>
                  <td className="py-2 px-3">
                    <span className={`inline-block px-2 py-0.5 rounded text-xs font-medium ${RETRY_STATUS_COLORS[r.status] || 'bg-gray-500/20 text-gray-400'}`}>{r.status}</span>
                  </td>
                  <td className="py-2 px-3 text-xs text-text-secondary whitespace-nowrap">{r.status === 'pending' ? formatDate(r.next_retry_at) : '—'}</td>
                  <td className="py-2 px-3 text-xs text-text-secondary whitespace-nowrap">{r.grace_until ? formatDate(r.grace_until) : '—'}</td>
                  <td className="py-2 px-3 text-xs text-red-400/80 max-w-[300px] truncate" title={r.last_error}>{r.last_error || ''}</td>
                  {can('payments.write') && (
                    <td className="py-2 px-3">
                      {['pending', 'in_progress'].includes(r.status) && (
                        <button onClick={() => handleCancel(r.id)} className="text-red-400 text-xs hover:underline">Отменить</button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default function PaymentOpsPage() {
  const [tab, setTab] = useState('email')
  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h2 className="text-2xl font-bold">Операции платежей</h2>
          <p className="text-sm text-text-secondary mt-1">Журнал email-уведомлений и очередь повторных списаний</p>
        </div>
      </div>

      <div className="flex gap-2 mb-5 border-b border-white/10">
        <button
          onClick={() => setTab('email')}
          className={`px-4 py-2 text-sm font-medium transition border-b-2 -mb-px ${tab === 'email' ? 'border-brand text-brand' : 'border-transparent text-text-secondary hover:text-text-primary'}`}
        >
          Email log
        </button>
        <button
          onClick={() => setTab('retries')}
          className={`px-4 py-2 text-sm font-medium transition border-b-2 -mb-px ${tab === 'retries' ? 'border-brand text-brand' : 'border-transparent text-text-secondary hover:text-text-primary'}`}
        >
          Retry queue (dunning)
        </button>
      </div>

      {tab === 'email' ? <EmailLogTab /> : <RetryQueueTab />}
    </div>
  )
}
