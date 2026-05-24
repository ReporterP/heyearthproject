import { useEffect, useState, useCallback } from 'react'
import { FileText } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { fetchAuditLog } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import {
  Button,
  Input,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  TableSkeleton,
  EmptyState,
  Badge,
} from '../components/ui/index.js'

const ACTION_VARIANT = {
  create_user: 'success',
  update_user: 'info',
  delete_user: 'danger',
  create_server: 'success',
  update_server: 'info',
  delete_server: 'danger',
  update_setting: 'warning',
}

function formatTime(iso) {
  if (!iso) return '—'
  try {
    return new Date(iso).toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
  } catch { return iso }
}

export default function AuditLogPage() {
  const { t } = useT()
  const [state, update] = useUrlState({ action: '', admin: '', from: '', to: '', page: 1 })
  const [entries, setEntries] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 25

  const { action, admin, from, to, page } = state

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    const params = { page, pageSize }
    if (action) params.action = action
    if (admin) params.admin = admin
    if (from) params.from = from
    if (to) params.to = to
    fetchAuditLog(params)
      .then(data => {
        setEntries(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [page, action, admin, from, to])

  useEffect(() => { load() }, [load])

  const totalPages = Math.ceil(total / pageSize)
  const hasFilters = action || admin || from || to

  return (
    <div className="space-y-4">
      <PageHeader title={t.navAuditLog} subtitle="История действий администраторов" />

      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-3">
        <Input
          type="text"
          placeholder="Действие (например, create_user)"
          value={action}
          onChange={e => update({ action: e.target.value, page: 1 })}
          className="w-full sm:flex-1 sm:max-w-xs"
        />
        <Input
          type="text"
          placeholder="Админ (username)"
          value={admin}
          onChange={e => update({ admin: e.target.value, page: 1 })}
          className="w-full sm:w-48"
        />
        <Input
          type="date"
          value={from}
          onChange={e => update({ from: e.target.value, page: 1 })}
          className="w-full sm:w-44"
          aria-label="С даты"
        />
        <Input
          type="date"
          value={to}
          onChange={e => update({ to: e.target.value, page: 1 })}
          className="w-full sm:w-44"
          aria-label="По дату"
        />
      </div>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={7} />
      ) : entries.length === 0 ? (
        <EmptyState
          icon={<FileText className="w-12 h-12" />}
          title={hasFilters ? 'Ничего не найдено' : 'Журнал пуст'}
          description={hasFilters ? 'Попробуйте изменить фильтры' : 'Действия администраторов будут записываться сюда'}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>Время</Th>
              <Th>Админ</Th>
              <Th>Действие</Th>
              <Th>Тип</Th>
              <Th>ID цели</Th>
              <Th>IP</Th>
              <Th>Детали</Th>
            </Tr>
          </Thead>
          <Tbody>
            {entries.map(e => (
              <Tr key={e.id}>
                <Td className="text-xs text-tertiary whitespace-nowrap">
                  {formatTime(e.created_at)}
                </Td>
                <Td>{e.admin_username || `#${e.admin_id}`}</Td>
                <Td>
                  <Badge variant={ACTION_VARIANT[e.action] || 'default'}>{e.action}</Badge>
                </Td>
                <Td className="text-tertiary">{e.target_type || '—'}</Td>
                <Td mono className="text-xs">{e.target_id || '—'}</Td>
                <Td className="text-tertiary text-xs">{e.ip_address || '—'}</Td>
                <Td className="text-xs text-tertiary max-w-xs truncate">
                  {e.details && e.details !== '{}' && typeof e.details === 'object'
                    ? JSON.stringify(e.details)
                    : '—'}
                </Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={page <= 1} onClick={() => update({ page: page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={page >= totalPages} onClick={() => update({ page: page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
        </div>
      )}
    </div>
  )
}
