import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from './Toast.jsx'
import { fetchDialRateSuspects, updateUser } from '../api/admin.js'
import { formatBytes } from '../utils/format.js'
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Skeleton, Table, Thead, Tbody, Tr, Th, Td } from './ui/index.js'

function UserCell({ row }) {
  return row.user_db_id
    ? <Link to={`/users?open=${row.user_db_id}&tab=traffic&search=${encodeURIComponent(row.user_id)}`} className="font-mono text-xs text-primary hover:text-accent">{row.user_id}</Link>
    : <span className="font-mono text-xs text-tertiary">{row.user_id}</span>
}

function BlockCell({ row, canBlock, onBlock }) {
  if (row.is_blocked) return <Badge variant="danger">заблокирован</Badge>
  if (!canBlock || !row.user_db_id) return null
  return <Button size="sm" variant="ghost" className="text-danger" onClick={() => onBlock(row)}>Заблокировать</Button>
}

/**
 * SharingPanel — the dashboard's «Подозрение на раздачу» and «Топ по трафику за 7 дней» (C13),
 * each user with «Заблокировать» (asked first).
 */
export default function SharingPanel() {
  const { can } = useAuth()
  const toast = useToast()
  const canBlock = can('users.write')
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [blocking, setBlocking] = useState(null)
  const [busy, setBusy] = useState(false)
  const load = useCallback(() => {
    fetchDialRateSuspects().then(setData).catch((e) => setError(e?.message || 'ошибка'))
  }, [])
  useEffect(() => { load() }, [load])

  async function block() {
    setBusy(true)
    try {
      await updateUser(blocking.user_db_id, { is_blocked: true })
      toast.success(`${blocking.user_id} заблокирован — подключения закроются в течение минуты`)
      setBlocking(null)
      load()
    } catch (e) {
      toast.error(e.message)
    } finally {
      setBusy(false)
    }
  }

  if (error) return <p role="alert" className="text-xs text-danger">Подозрения не загрузились: {error}</p>
  if (!data) return <Skeleton className="h-40" />
  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <Card>
        <CardHeader><h3 className="text-sm font-semibold text-primary">Подозрение на раздачу (7 дней)</h3></CardHeader>
        <CardBody>
          {data.suspects.length === 0 ? <p className="text-sm text-tertiary">Никто не выходил за порог {data.threshold_per_min} подключений в минуту.</p> : (
            <Table>
              <Thead><Tr><Th>Пользователь</Th><Th>Вердикт</Th><Th align="right" /></Tr></Thead>
              <Tbody>
                {data.suspects.map((r) => (
                  <Tr key={r.user_id}>
                    <Td><UserCell row={r} /></Td>
                    <Td><span className={`text-xs ${r.verdict.suspect ? 'text-warning' : 'text-secondary'}`} title={r.verdict.text}>
                      {r.verdict.suspect ? `похоже: ${r.verdict.days_over} из 7 дней` : `до ${r.verdict.max_minutes_over} мин/сутки`}</span></Td>
                    <Td align="right"><BlockCell row={r} canBlock={canBlock} onBlock={setBlocking} /></Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          )}
        </CardBody>
      </Card>
      <Card>
        <CardHeader><h3 className="text-sm font-semibold text-primary">Топ по трафику (7 дней)</h3></CardHeader>
        <CardBody>
          {data.top_traffic.length === 0 ? <p className="text-sm text-tertiary">Трафика за неделю нет.</p> : (
            <Table>
              <Thead><Tr><Th>Пользователь</Th><Th align="right">Трафик</Th><Th align="right" /></Tr></Thead>
              <Tbody>
                {data.top_traffic.map((r) => (
                  <Tr key={r.user_id}>
                    <Td><UserCell row={r} /></Td>
                    <Td mono align="right">{formatBytes(r.total_bytes)}</Td>
                    <Td align="right"><BlockCell row={r} canBlock={canBlock} onBlock={setBlocking} /></Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
          )}
        </CardBody>
      </Card>
      <Dialog open={!!blocking} onClose={() => setBlocking(null)} title="Заблокировать пользователя" maxWidth="max-w-md">
        <div className="space-y-3 text-sm">
          <p className="text-secondary">Все подключения <span className="font-mono text-primary">{blocking?.user_id}</span> перестанут работать в течение минуты. Подписка и кошелёк сохраняются; разблокировать можно в карточке пользователя.</p>
          <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2">
            <Button variant="ghost" onClick={() => setBlocking(null)}>Отмена</Button>
            <Button variant="danger" loading={busy} onClick={block}>Заблокировать</Button>
          </div>
        </div>
      </Dialog>
    </div>
  )
}
