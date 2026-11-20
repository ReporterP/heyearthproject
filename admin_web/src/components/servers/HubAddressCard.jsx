import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchHubAddresses, startHubAddressChange } from '../../api/admin.js'
import { startPolling } from '../../utils/polling.js'
import { Badge, Button, Card, CardBody, CardHeader, Dialog, Input, Skeleton, Table, Thead, Tbody, Tr, Th, Td } from '../ui/index.js'
import { roleLabel, hubAddrActive } from './hubAddressModel.js'
import HubAddressWizard from './HubAddressWizard.jsx'

const ROLE_VARIANT = { users: 'accent', source: 'info', gateway: 'default', new: 'success', retiring: 'warning', other: 'default' }

/**
 * HubAddressCard — «Адрес хаба» on «Серверы → Hub»: the Hub's addresses with their roles and, while a
 * change runs, its wizard. Polls every 5 s while a step runs or the switch waits, else every 30 s.
 */
export default function HubAddressCard() {
  const { can } = useAuth()
  const toast = useToast()
  const [data, setData] = useState(null)
  const [error, setError] = useState('')
  const [start, setStart] = useState(false)
  const [ip, setIP] = useState('')
  const [starting, setStarting] = useState(false)
  const load = useCallback(async () => {
    try {
      setData(await fetchHubAddresses())
      setError('')
    } catch (e) {
      setError(e?.message || 'ошибка')
    }
  }, [])
  const loadRef = useRef(load)
  loadRef.current = load
  useEffect(() => { load() }, [load])
  const change = data?.change
  const fast = Boolean(change?.running) || ['switching', 'reverting', 'nodes_ready'].includes(change?.state)
  useEffect(() => startPolling(loadRef, fast ? 5000 : 30000, () => document.visibilityState === 'visible'), [fast])

  const submit = async (e) => {
    e.preventDefault()
    setStarting(true)
    try {
      await startHubAddressChange(ip.trim())
      toast.success('Смена начата: адрес добавляется на интерфейс')
      setStart(false)
      setIP('')
      await load()
    } catch (err) {
      toast.error(err?.message || 'Не получилось')
    } finally {
      setStarting(false)
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
          <h3 className="text-sm font-semibold text-primary">Адрес хаба</h3>
          {can('hub.address') && data && !hubAddrActive(change) && (
            <Button variant="secondary" size="sm" onClick={() => setStart(true)}>Сменить адрес</Button>
          )}
        </div>
      </CardHeader>
      <CardBody>
        {!data && !error && <Skeleton className="h-24" />}
        {!data && error && (
          <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
            <span className="text-sm text-danger">Адреса хаба не прочитаны: {error}</span>
            <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={load}>Повторить</Button>
          </div>
        )}
        {data && (
          <div className="space-y-4">
            <Table>
              <Thead><Tr><Th>Адрес</Th><Th>Роль</Th></Tr></Thead>
              <Tbody>
                {(data.addresses || []).map((a) => (
                  <Tr key={a.addr}>
                    <Td mono>{a.addr}/{a.prefix}</Td>
                    <Td><span className="flex flex-wrap gap-1">{a.roles.map((r) => <Badge key={r} variant={ROLE_VARIANT[r]}>{roleLabel(r)}</Badge>)}</span></Td>
                  </Tr>
                ))}
              </Tbody>
            </Table>
            <p className="text-xs text-tertiary">Маршрут по умолчанию: <span className="font-mono">via {data.gateway} dev {data.dev}{data.source ? ` src ${data.source}` : ''}</span></p>
            {change && <HubAddressWizard change={change} onChanged={load} />}
          </div>
        )}
      </CardBody>
      <Dialog open={start} onClose={() => setStart(false)} title="Сменить адрес хаба" maxWidth="max-w-lg">
        <form onSubmit={submit} className="space-y-3">
          <p className="text-sm text-secondary">Сначала закажите у VDSina дополнительный IP на этот же сервер. Лучше из другого диапазона: мобильные операторы режут подсеть VDSina целиком, и адрес из той же подсети может не помочь. Проверка с телефона на шаге 2 это покажет.</p>
          <Input aria-label="Новый адрес" placeholder="198.51.100.20" value={ip} onChange={(e) => setIP(e.target.value)} className="font-mono" autoFocus />
          <p className="text-xs text-tertiary">Адрес добавится на интерфейс и в netplan сразу; пользователи переедут на него только на шаге 5, после проверок.</p>
          <div className="flex flex-col sm:flex-row gap-2 sm:justify-end">
            <Button variant="ghost" type="button" onClick={() => setStart(false)}>Отмена</Button>
            <Button variant="primary" type="submit" loading={starting} disabled={!ip.trim() || starting}>Начать</Button>
          </div>
        </form>
      </Dialog>
    </Card>
  )
}
