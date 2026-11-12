import { useCallback, useEffect, useRef, useState } from 'react'
import { Server, Plus, KeyRound } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useUrlState } from '../hooks/useUrlState.js'
import { fetchNodes } from '../api/admin.js'
import { startPolling, requestSequencer } from '../utils/polling.js'
import { formatDate, formatRelativeRu } from '../utils/format.js'
import {
  Button, Badge, PageHeader, Tabs, TabPanel, Table, Thead, Tbody, Tr, Th, Td, TableSkeleton, EmptyState,
} from '../components/ui/index.js'
import {
  ROLE_TABS, PRIORITY_HINT, nodeState, pathView, pathsStale, unconfirmedKeys, keyTypeLabel, hostKeyFile,
} from '../components/nodes/nodeModel.js'
import AddNodeWizard from '../components/nodes/AddNodeWizard.jsx'
import NodeDrawer from '../components/nodes/NodeDrawer.jsx'

const POLL_MS = 15000
const PLANS_HINT = 'Без привязки нода получает только общий пул; тарифы с привязкой ходят только через свои ноды'

function PathCell({ path, enabled = true, stale }) {
  if (stale) return <span className="text-xs text-tertiary" title="Нода отключена — пути не проверяются">—</span>
  const v = pathView(path, { enabled })
  const live = !v.off // a switched-off path has no RTT or check time to show
  const title = [v.hint, live && path?.error].filter(Boolean).join(' · ')
  return (
    <div className="flex items-center gap-2 whitespace-nowrap" title={title || undefined}>
      <Badge variant={v.variant}>{v.label}</Badge>
      {live && path?.rtt_ms > 0 && <span className="font-mono tabular-nums text-xs text-secondary">{path.rtt_ms} мс</span>}
      {live && path?.checked_at && (
        <span className="font-mono tabular-nums text-xs text-tertiary" title={`Проверено ${formatDate(path.checked_at)}`}>
          {formatRelativeRu(path.checked_at)}
        </span>
      )}
    </div>
  )
}

// HostKeyNotice: nodes the Hub will not SSH into until the owner compares the fingerprint with the
// provider's console. The confirmation itself lives in the node card.
function HostKeyNotice({ nodes, manage, onOpen }) {
  if (nodes.length === 0) return null
  return (
    <div role="status" className="mb-4 rounded border border-warning/30 bg-warning/10 px-3 py-2">
      <div className="flex items-start gap-2">
        <KeyRound className="w-4 h-4 text-warning mt-0.5 shrink-0" aria-hidden="true" />
        <div className="flex-1 min-w-0">
          <p className="text-sm text-primary">Не подтверждён отпечаток ключа SSH</p>
          <p className="text-xs text-secondary mt-0.5">
            Пока отпечаток не сверен с консолью хостера, Hub не подключается к серверу: настройка, проверка и отключение недоступны. Трафик пользователей это не останавливает.
          </p>
          <ul className="mt-2 space-y-2">
            {nodes.map((n) => {
              const type = n.host_key?.key_type
              const file = hostKeyFile(type)
              return (
                <li key={n.id} className="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
                  <div className="flex-1 min-w-0 text-xs">
                    <span className="text-primary">{n.name || `Нода ${n.id}`}</span>
                    {type && <span className="text-tertiary"> · {keyTypeLabel(type)}</span>}
                    {n.host_key?.fingerprint && <code className="block font-mono text-secondary break-all">{n.host_key.fingerprint}</code>}
                    {file && <span className="block text-tertiary">В консоли хостера: <code className="font-mono">ssh-keygen -lf {file}</code></span>}
                  </div>
                  {manage && (
                    <Button size="sm" variant="secondary" onClick={() => onOpen(n.id)} className="w-full sm:w-auto">Подтвердить отпечаток</Button>
                  )}
                </li>
              )
            })}
          </ul>
          {!manage && <p className="text-xs text-tertiary mt-2">Подтвердить может администратор с правом настройки нод.</p>}
        </div>
      </div>
    </div>
  )
}

export default function NodesPage() {
  const { can } = useAuth()
  const manage = can('nodes.provision')
  // ?role=exit|relay&node=<id>&add=1 — add=1 opens the wizard with the tab's role (phase 3
  // «Входы» opens the reserve-entry wizard as /nodes?role=relay&add=1).
  const [f, setF] = useUrlState({ role: 'exit', node: '', add: '' })
  const role = f.role === 'relay' ? 'relay' : 'exit'
  const [items, setItems] = useState(null)
  const [error, setError] = useState('')
  const [nextRequest] = useState(() => requestSequencer())

  // A poll answer for the previous role (a slow request finishing after a tab switch) is dropped;
  // a failed refresh keeps the last list and says so instead of turning it into «нод нет».
  const load = useCallback(async () => {
    const isCurrent = nextRequest()
    try {
      const d = await fetchNodes(role)
      if (!isCurrent()) return
      setItems(d.items || [])
      setError('')
    } catch (e) {
      if (!isCurrent()) return
      setError(e.message)
    }
  }, [role, nextRequest])

  useEffect(() => {
    setItems(null) // another role: never show the other tab's rows as this one's
    setError('')
    load()
  }, [load])
  const loadRef = useRef(load)
  useEffect(() => { loadRef.current = load }, [load])
  useEffect(() => startPolling(loadRef, POLL_MS, () => document.visibilityState === 'visible'), [])

  const openNode = (id) => setF({ node: String(id) })
  const current = items?.find((n) => String(n.id) === f.node)
  const canAdd = manage
  const wizard = manage && f.add === '1'
  const exitTab = role === 'exit'
  const addButton = (
    <Button icon={<Plus className="w-4 h-4" />} onClick={() => setF({ add: '1' })}>{role === 'relay' ? 'Добавить вход в РФ' : 'Добавить ноду'}</Button>
  )

  return (
    <div>
      <PageHeader title="Ноды" subtitle="Выходы за рубежом и резервные входы в РФ: состояние, оба пути туннеля, настройка без SSH"
        actions={canAdd && addButton} />
      <Tabs id="node-role" label="Роль ноды" tabs={ROLE_TABS} value={role}
        onChange={(r) => setF({ role: r, node: '' })} className="mb-4" />
      <TabPanel tabsId="node-role" tabKey={role}>
        {items !== null && error && <p role="alert" className="text-sm text-danger mb-4">Не удалось обновить: {error}</p>}
        {items !== null && <HostKeyNotice nodes={unconfirmedKeys(items)} manage={manage} onOpen={openNode} />}

        {items === null ? (
          error ? (
            <EmptyState icon={<Server className="w-8 h-8" />} title="Не удалось загрузить ноды" description={error}
              action={<Button variant="secondary" onClick={load}>Повторить</Button>} />
          ) : <TableSkeleton rows={3} cols={exitTab ? 6 : 2} />
        ) : items.length === 0 ? (
          <EmptyState icon={<Server className="w-8 h-8" />}
            title={exitTab ? 'Выходов пока нет' : 'Резервных входов пока нет'}
            description={exitTab
              ? 'Купите VPS за рубежом (Ubuntu или Debian, x86_64) и добавьте его — мастер всё настроит и проверит сам.'
              : 'Мобильные операторы режут адрес Hub. Купите VPS в России у другого провайдера (не там, где Hub) и добавьте его — мастер проверит адрес с телефона, настроит пересылку на Hub и включит вход для всех.'}
            action={canAdd && addButton} />
        ) : (
          <>
            <Table>
              <Thead>
                <Tr>
                  <Th>Нода</Th>
                  <Th>Состояние</Th>
                  {exitTab && (
                    <>
                      <Th>Основной туннель</Th>
                      <Th>Запасной TCP</Th>
                      <Th align="right"><span title={PRIORITY_HINT}>Приоритет</span></Th>
                      <Th align="right"><span title={PLANS_HINT}>Тарифов</span></Th>
                    </>
                  )}
                </Tr>
              </Thead>
              <Tbody>
                {items.map((n) => {
                  const st = nodeState(n)
                  const stale = pathsStale(n)
                  return (
                    <Tr key={n.id} onClick={() => openNode(n.id)} className="cursor-pointer">
                      <Td>
                        <button type="button" onClick={(e) => { e.stopPropagation(); openNode(n.id) }}
                          className="block text-left text-primary hover:text-accent rounded focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent">
                          {n.name || `Нода ${n.id}`}
                        </button>
                        <div className="font-mono text-xs text-tertiary whitespace-nowrap">{n.country_code} · {n.ip_address}</div>
                      </Td>
                      <Td><span title={st.hint}><Badge variant={st.variant}>{st.label}</Badge></span></Td>
                      {exitTab && (
                        <>
                          <Td><PathCell path={n.paths?.hy} stale={stale} /></Td>
                          <Td><PathCell path={n.paths?.tcp} enabled={n.tunnel_fallback_enabled !== false} stale={stale} /></Td>
                          <Td mono align="right">{n.load_weight}</Td>
                          <Td mono align="right">{n.plan_ids?.length ?? 0}</Td>
                        </>
                      )}
                    </Tr>
                  )
                })}
              </Tbody>
            </Table>
            {exitTab && (
              <p className="text-xs text-tertiary mt-2">
                <span className="text-secondary">Приоритет.</span> {PRIORITY_HINT}. Список обновляется каждые {POLL_MS / 1000} с.
              </p>
            )}
          </>
        )}
      </TabPanel>

      {wizard && (
        <AddNodeWizard initialRole={role}
          onClose={() => { setF({ add: '' }); load() }}
          onOpenNode={(id) => { setF({ add: '', node: String(id) }); load() }} />
      )}
      {current && <NodeDrawer node={current} onClose={() => setF({ node: '' })} onChanged={load} />}
    </div>
  )
}
