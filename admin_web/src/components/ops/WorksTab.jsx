import { useEffect, useRef, useState } from 'react'
import { useToast } from '../Toast.jsx'
import { cancelOpsRun, fetchOpsWorks } from '../../api/admin.js'
import { startPolling } from '../../utils/polling.js'
import { formatDate } from '../../utils/format.js'
import { Badge, Button, Card, CardBody, CardHeader, EmptyState, Table, TableSkeleton, Tbody, Td, Th, Thead, Tr } from '../ui/index.js'
import { factsAge, nodeIssues, roleLabel, sortNodes } from './worksModel.js'

const POLL_MS = 60000

/**
 * WorksTab — «Операции → Работы»: every node with what its daily facts job read (kernel, boot,
 * pending reboot, security updates, password login, disk) and the age of that data, the ones that
 * need attention first; below, the actions planned for the night window with «Отменить».
 * Props: canCancel (the admin may cancel planned runs), onAction(id, target) — opens the action
 * dialog (node.reboot) when the console has it.
 */
export default function WorksTab({ canCancel, onAction }) {
  const toast = useToast()
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(0)
  const loadRef = useRef(() => {})

  async function load() {
    try {
      setData(await fetchOpsWorks())
      setError('')
    } catch (e) { setError(e.message) }
  }
  loadRef.current = load
  useEffect(() => {
    load()
    return startPolling(loadRef, POLL_MS, () => !document.hidden)
  }, [])

  async function cancel(run) {
    setBusy(run.id)
    try {
      await cancelOpsRun(run.id)
      toast.success('Запланированное отменено')
      await load()
    } catch (e) { toast.error(e.message) } finally { setBusy(0) }
  }

  if (error && !data) return <EmptyState title="«Работы» не загрузились" description={error} action={<Button size="sm" variant="secondary" onClick={load}>Повторить</Button>} />
  if (!data) return <TableSkeleton rows={5} cols={6} />

  const now = Date.now()
  const nodes = sortNodes(data.nodes || [], now)
  const scheduled = data.scheduled || []

  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
          <span className="text-sm font-medium text-primary">Узлы</span>
          <span className="text-xs text-tertiary sm:ml-auto">сведения собираются в 06:00 МСК и после каждого verify</span>
        </CardHeader>
        <CardBody className="p-0">
          <Table>
            <Thead>
              <Tr>
                <Th>Узел</Th>
                <Th>Ядро</Th>
                <Th>Загружен</Th>
                <Th align="right">Обновления</Th>
                <Th align="right">Диск</Th>
                <Th>Сведения</Th>
                <Th>Внимание</Th>
                {onAction && <Th align="right" />}
              </Tr>
            </Thead>
            <Tbody>
              {nodes.map(n => {
                const f = n.facts
                const age = factsAge(f?.collected_at, now)
                const issues = nodeIssues(n, now)
                return (
                  <Tr key={`${n.role}-${n.id}`}>
                    <Td>
                      <span className="text-primary">{n.name}</span>
                      <span className="ml-2 text-xs text-tertiary">{roleLabel(n.role)}{n.id ? ` #${n.id}` : ''}</span>
                    </Td>
                    <Td mono><span className="whitespace-nowrap">{f?.kernel || '—'}</span></Td>
                    <Td mono><span className="whitespace-nowrap">{f?.boot_at ? formatDate(f.boot_at) : '—'}</span></Td>
                    <Td mono align="right">{f ? f.security_updates : '—'}</Td>
                    <Td mono align="right">{f ? `${f.disk_pct} %` : '—'}</Td>
                    <Td><span className={`whitespace-nowrap ${age.stale ? 'text-warning' : 'text-secondary'}`}>{age.label}</span></Td>
                    <Td>
                      {issues.length === 0
                        ? <span className="text-tertiary">—</span>
                        : <div className="flex flex-wrap gap-1">{issues.map(i => <Badge key={i} variant="warning">{i}</Badge>)}</div>}
                    </Td>
                    {onAction && (
                      <Td align="right">
                        {n.role !== 'hub' && n.lifecycle === 'online' && (
                          <Button size="sm" variant="ghost" onClick={() => onAction('node.reboot', String(n.id))}>
                            Перезагрузить…
                          </Button>
                        )}
                      </Td>
                    )}
                  </Tr>
                )
              })}
            </Tbody>
          </Table>
        </CardBody>
      </Card>

      <Card>
        <CardHeader className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
          <span className="text-sm font-medium text-primary">Запланировано в окно</span>
          <span className="text-xs text-tertiary sm:ml-auto">окно {data.best_window || '04:00–05:30'} МСК</span>
        </CardHeader>
        <CardBody className="p-0">
          {scheduled.length === 0 ? (
            <EmptyState className="py-6" title="Ничего не запланировано" description="Действие с кнопкой «В окно» встанет сюда и выполнится ночью, когда соединения рвать можно." />
          ) : (
            <ul className="divide-y divide-default">
              {scheduled.map(r => (
                <li key={r.id} className="px-4 py-2 flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3 text-sm">
                  <span className="font-mono text-primary">{r.action}{r.target ? ` · ${r.target}` : ''}</span>
                  <Badge variant={r.impact === 'K3' || r.impact === 'K2' ? 'danger' : 'default'}>{r.impact}</Badge>
                  <span className="text-secondary">{formatDate(r.scheduled_for)} · {r.started_by}</span>
                  {canCancel && (
                    <Button size="sm" variant="ghost" className="sm:ml-auto" loading={busy === r.id} onClick={() => cancel(r)}>Отменить</Button>
                  )}
                </li>
              ))}
            </ul>
          )}
        </CardBody>
      </Card>
    </div>
  )
}
