import { RotateCcw } from 'lucide-react'
import usePagedData from '../hooks/usePagedData.js'
import { pagedFetch } from '../api/client.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import {
  Badge,
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

const EMAIL_STATUS_VARIANT = {
  sent: 'success',
  pending: 'warning',
  failed: 'danger',
  disabled: 'default',
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

export default function PaymentOpsPage() {
  return (
    <div>
      <PageHeader title="Письма" subtitle="Журнал email-уведомлений клиентам" />
      <EmailLogTab />
    </div>
  )
}
