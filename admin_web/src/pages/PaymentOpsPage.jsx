import { useState } from 'react'
import { RotateCcw } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { pagedFetch, apiFetch } from '../api/client.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import {
  Badge,
  Button,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  EmptyState,
} from '../components/ui/index.js'

function fetchEmailLog(params) { return pagedFetch('/email-log', params) }
function fetchRetryQueue(params) { return pagedFetch('/payment-retries', params) }
function cancelRetry(id, reason) { return apiFetch(`/payment-retries/${id}/cancel`, { method: 'POST', body: { reason } }) }

const EMAIL_STATUS_VARIANT = {
  sent: 'success',
  pending: 'warning',
  failed: 'danger',
  disabled: 'default',
}
const RETRY_STATUS_VARIANT = {
  pending: 'warning',
  in_progress: 'info',
  succeeded: 'success',
  exhausted: 'danger',
  canceled: 'default',
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
        <EmptyState icon={<RotateCcw className="w-12 h-12" />} title="Журнал email пуст" description="Транзакционные письма появятся здесь после первой отправки." />
      ) : (
        <div className="mt-4">
          <Table>
            <Thead>
              <Tr>
                <Th>ID</Th>
                <Th>Email</Th>
                <Th>Шаблон</Th>
                <Th>Статус</Th>
                <Th>Отправлено</Th>
                <Th>Ошибка</Th>
              </Tr>
            </Thead>
            <Tbody>
              {paged.items.map(item => (
                <Tr key={item.id}>
                  <Td mono>{item.id}</Td>
                  <Td mono>{item.email}</Td>
                  <Td>{item.template}</Td>
                  <Td>
                    <Badge variant={EMAIL_STATUS_VARIANT[item.status] || 'default'}>{item.status}</Badge>
                  </Td>
                  <Td><span className="text-tertiary tabular-nums">{formatDate(item.sent_at || item.created_at)}</span></Td>
                  <Td><span className="text-danger/80 text-xs max-w-[300px] truncate inline-block" title={item.error}>{item.error || ''}</span></Td>
                </Tr>
              ))}
            </Tbody>
          </Table>
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
        <EmptyState icon={<RotateCcw className="w-12 h-12" />} title="Очередь retries пуста" description="Все recurring платежи прошли успешно." />
      ) : (
        <div className="mt-4">
          <Table>
            <Thead>
              <Tr>
                <Th>ID</Th>
                <Th>User</Th>
                <Th>План</Th>
                <Th>Попытка</Th>
                <Th>Статус</Th>
                <Th>Следующая</Th>
                <Th>Grace до</Th>
                <Th>Ошибка</Th>
                {can('payments.write') && <Th></Th>}
              </Tr>
            </Thead>
            <Tbody>
              {paged.items.map(r => (
                <Tr key={r.id}>
                  <Td mono>{r.id}</Td>
                  <Td mono>{r.user_id_text?.slice(0, 12)}…</Td>
                  <Td>{r.plan_name}</Td>
                  <Td mono>{r.attempt}/{r.max_attempts}</Td>
                  <Td>
                    <Badge variant={RETRY_STATUS_VARIANT[r.status] || 'default'}>{r.status}</Badge>
                  </Td>
                  <Td><span className="text-tertiary tabular-nums">{r.status === 'pending' ? formatDate(r.next_retry_at) : '—'}</span></Td>
                  <Td><span className="text-tertiary tabular-nums">{r.grace_until ? formatDate(r.grace_until) : '—'}</span></Td>
                  <Td><span className="text-danger/80 text-xs max-w-[300px] truncate inline-block" title={r.last_error}>{r.last_error || ''}</span></Td>
                  {can('payments.write') && (
                    <Td>
                      {['pending', 'in_progress'].includes(r.status) && (
                        <Button variant="ghost" size="sm" onClick={() => handleCancel(r.id)}>Отменить</Button>
                      )}
                    </Td>
                  )}
                </Tr>
              ))}
            </Tbody>
          </Table>
        </div>
      )}
    </div>
  )
}

export default function PaymentOpsPage() {
  const [tab, setTab] = useState('email')
  return (
    <div>
      <PageHeader
        title="Операции платежей"
        subtitle="Журнал email-уведомлений и очередь повторных списаний"
      />

      <div className="flex gap-2 mb-5 border-b border-default">
        <button
          onClick={() => setTab('email')}
          className={`px-4 py-2 text-sm font-medium transition border-b-2 -mb-px ${tab === 'email' ? 'border-accent text-accent' : 'border-transparent text-secondary hover:text-primary'}`}
        >
          Email log
        </button>
        <button
          onClick={() => setTab('retries')}
          className={`px-4 py-2 text-sm font-medium transition border-b-2 -mb-px ${tab === 'retries' ? 'border-accent text-accent' : 'border-transparent text-secondary hover:text-primary'}`}
        >
          Retry queue (dunning)
        </button>
      </div>

      {tab === 'email' ? <EmailLogTab /> : <RetryQueueTab />}
    </div>
  )
}
