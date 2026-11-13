import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Server, Plus, KeyRound, RefreshCw } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useUrlState } from '../hooks/useUrlState.js'
import { fetchNodes, fetchCapacity } from '../api/admin.js'
import { startPolling, requestSequencer } from '../utils/polling.js'
import { formatDate, formatRelativeRu } from '../utils/format.js'
import {
  Button, Badge, PageHeader, Tabs, TabPanel, Table, Thead, Tbody, Tr, Th, Td, TableSkeleton, EmptyState, Skeleton,
} from '../components/ui/index.js'
import { PRIORITY_HINT, nodeState, pathView, pathsStale, unconfirmedKeys, keyTypeLabel, hostKeyFile } from '../components/nodes/nodeModel.js'
import { isLastExit } from '../components/nodes/drawerModel.js'
import { SERVER_TABS, serverTab, loadView, flag, countryName } from '../components/servers/serversModel.js'
import AddNodeWizard from '../components/nodes/AddNodeWizard.jsx'
import NodeDrawer from '../components/nodes/NodeDrawer.jsx'
import EntriesPanel from '../components/entries/EntriesPanel.jsx'
import HubPanel from '../components/servers/HubPanel.jsx'
import CapacityPanel from '../components/servers/CapacityPanel.jsx'

const POLL_MS = 15000
const PLANS_HINT = 'Без привязки выход получает только общий пул; тарифы с привязкой ходят только через свои выходы'

function PathBadge({ label, path, enabled = true, stale }) {
  if (stale) return null
  const v = pathView(path, { enabled })
  const live = !v.off
  const title = [`${label}: ${v.label}`, v.hint, live && path?.rtt_ms > 0 && `${path.rtt_ms} мс`, live && path?.error,
    live && path?.checked_at && `проверено ${formatDate(path.checked_at)}`].filter(Boolean).join(' · ')
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap" title={title}>
      <span className="text-[11px] text-tertiary">{label}</span>
      <Badge variant={v.variant}>{v.label}</Badge>
      {live && path?.checked_at && <span className="font-mono tabular-nums text-[11px] text-tertiary">{formatRelativeRu(path.checked_at)}</span>}
    </span>
  )
}

// HostKeyNotice: servers the Hub will not SSH into until the owner compares the fingerprint with the
// provider's console. The confirmation itself lives in the server card.
function HostKeyNotice({ nodes, manage, onOpen }) {
  if (nodes.length === 0) return null
  return (
    <div role="status" className="mb-4 rounded border border-warning/30 bg-warning/10 px-3 py-2">
      <div className="flex items-start gap-2">
        <KeyRound className="w-4 h-4 text-warning mt-0.5 shrink-0" aria-hidden="true" />
        <div className="flex-1 min-w-0">
          <p className="text-sm text-primary">Не подтверждён отпечаток ключа SSH</p>
          <p className="text-xs text-secondary mt-0.5">
            Пока отпечаток не сверен с консолью хостера, Hub не подключается к серверу: настройка, проверка, нагрузка и отключение недоступны. Трафик пользователей это не останавливает.
          </p>
          <ul className="mt-2 space-y-2">
            {nodes.map((n) => {
              const type = n.host_key?.key_type
              const file = hostKeyFile(type)
              return (
                <li key={n.id} className="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
                  <div className="flex-1 min-w-0 text-xs">
                    <span className="text-primary">{n.name || `Сервер ${n.id}`}</span>
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

function LoadCell({ c, stale, why }) {
  return <span className={`whitespace-nowrap ${c.warn ? 'text-warning' : stale ? 'text-tertiary' : 'text-primary'}`} title={why || undefined}>{c.text}</span>
}

// ExitsTable: every exit with both tunnel paths and its load.
function ExitsTable({ items, onOpen }) {
  return (
    <>
      <Table>
        <Thead>
          <Tr>
            <Th>Выход</Th>
            <Th>Состояние</Th>
            <Th>Туннель</Th>
            <Th align="right">CPU</Th>
            <Th align="right">RAM</Th>
            <Th align="right">Канал, Мбит/с</Th>
            <Th align="right">Подключения</Th>
            <Th align="right"><span title={PRIORITY_HINT}>Вес</span></Th>
            <Th align="right"><span title={PLANS_HINT}>Тарифов</span></Th>
          </Tr>
        </Thead>
        <Tbody>
          {items.map((n) => {
            const st = nodeState(n)
            const stale = pathsStale(n)
            const v = loadView(n.load, { portMbps: n.owner?.port_mbps, role: 'exit' })
            return (
              <Tr key={n.id} onClick={() => onOpen(n.id)} className="cursor-pointer">
                <Td>
                  <button type="button" onClick={(e) => { e.stopPropagation(); onOpen(n.id) }}
                    className="block text-left text-primary hover:text-accent rounded focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent">
                    {n.name || `Выход ${n.id}`}
                  </button>
                  <div className="font-mono text-xs text-tertiary whitespace-nowrap">
                    <span title={countryName(n.country_code)}>{flag(n.country_code) || n.country_code}</span> · {n.ip_address}
                  </div>
                </Td>
                <Td><span title={st.hint} className="inline-block whitespace-nowrap"><Badge variant={st.variant}>{st.label}</Badge></span></Td>
                <Td>
                  {stale ? <span className="text-xs text-tertiary" title="Выход отключён — пути не проверяются">—</span> : (
                    <div className="flex flex-col gap-1">
                      <PathBadge label="UDP" path={n.paths?.hy} />
                      <PathBadge label="TCP" path={n.paths?.tcp} enabled={n.tunnel_fallback_enabled !== false} />
                    </div>
                  )}
                </Td>
                {v.empty ? (
                  <>
                    <Td align="right"><span className="text-xs text-tertiary" title={v.why}>{n.lifecycle_state === 'online' ? 'нет данных' : '—'}</span></Td>
                    <Td align="right"><span className="text-tertiary">—</span></Td>
                    <Td align="right"><span className="text-tertiary">—</span></Td>
                    <Td align="right"><span className="text-tertiary">—</span></Td>
                  </>
                ) : (
                  <>
                    <Td mono align="right"><LoadCell c={v.cpu} stale={v.stale} why={v.why} /></Td>
                    <Td mono align="right"><LoadCell c={v.ram} stale={v.stale} why={v.why} /></Td>
                    <Td mono align="right" className="whitespace-nowrap"><LoadCell c={v.net} stale={v.stale} why={v.why} /></Td>
                    <Td mono align="right"><LoadCell c={v.conns} stale={v.stale} why={v.why} /></Td>
                  </>
                )}
                <Td mono align="right">{n.load_weight}</Td>
                <Td mono align="right">{n.plan_ids?.length ?? 0}</Td>
              </Tr>
            )
          })}
        </Tbody>
      </Table>
      <p className="text-xs text-tertiary mt-2">
        <span className="text-secondary">Вес.</span> {PRIORITY_HINT}. Нагрузка — последняя минутная проба по SSH; список обновляется каждые {POLL_MS / 1000} с.
        Жёлтым — выше порогов «Мощности».
      </p>
    </>
  )
}

/**
 * ServersPage — «Серверы»: «Выходы», «Входы в РФ» (was «Входы»), «Hub», «Мощность». One list of
 * every server (GET /nodes), the card (?node=<id>) for any role, the wizard (?add=1) with the
 * tab's role, the phone check (?phone=<id>). Old links: /nodes?role=…, /entries, /servers/:id.
 */
export default function ServersPage() {
  const { can } = useAuth()
  const manage = can('nodes.provision')
  const [f, setF] = useUrlState({ tab: '', role: '', node: '', add: '', phone: '' })
  const tab = serverTab(f.tab, f.role)
  const [items, setItems] = useState(null)
  const [error, setError] = useState('')
  const [cap, setCap] = useState({ rep: null, error: '' })
  const [nextRequest] = useState(() => requestSequencer())

  // A failed refresh keeps the last list and says so instead of turning it into «серверов нет».
  const load = useCallback(async () => {
    const isCurrent = nextRequest()
    try {
      const d = await fetchNodes('')
      if (!isCurrent()) return
      setItems(d.items || [])
      setError('')
    } catch (e) {
      if (!isCurrent()) return
      setError(e.message)
    }
  }, [nextRequest])
  useEffect(() => { load() }, [load])
  const loadRef = useRef(load)
  useEffect(() => { loadRef.current = load }, [load])
  useEffect(() => startPolling(loadRef, POLL_MS, () => document.visibilityState === 'visible'), [])

  const loadCapacity = useCallback(async () => {
    try {
      setCap({ rep: await fetchCapacity(), error: '' })
    } catch (e) {
      setCap({ rep: null, error: e?.message || 'ошибка' })
    }
  }, [])
  useEffect(() => { if (tab === 'capacity') loadCapacity() }, [tab, loadCapacity])

  const exits = useMemo(() => (items || []).filter((n) => n.role !== 'relay'), [items])
  const relays = useMemo(() => (items || []).filter((n) => n.role === 'relay'), [items])
  const byId = useMemo(() => Object.fromEntries((items || []).map((n) => [n.id, n])), [items])
  const tabs = SERVER_TABS.map((t) => ({
    ...t,
    count: t.key === 'exit' && items ? exits.length : t.key === 'relay' && items ? relays.length : undefined,
  }))

  const openNode = (id) => setF({ node: String(id) })
  const current = items?.find((n) => String(n.id) === f.node)
  const addRole = tab === 'relay' ? 'relay' : 'exit'
  const wizard = manage && f.add === '1'
  const addButton = manage && (
    <Button icon={<Plus className="w-4 h-4" />} onClick={() => setF({ add: '1' })}>{addRole === 'relay' ? 'Добавить вход в РФ' : 'Добавить выход'}</Button>
  )

  const listState = (list, emptyTitle, emptyText) => {
    if (items === null) {
      return error
        ? <EmptyState icon={<Server className="w-8 h-8" />} title="Не удалось загрузить серверы" description={error}
          action={<Button variant="secondary" onClick={load}>Повторить</Button>} />
        : <TableSkeleton rows={3} cols={6} />
    }
    if (list.length === 0) return <EmptyState icon={<Server className="w-8 h-8" />} title={emptyTitle} description={emptyText} action={addButton} />
    return null
  }

  return (
    <div>
      <PageHeader title="Серверы"
        subtitle="Выходы за рубежом, входы в РФ и Hub: состояние, нагрузка, настройка без SSH и когда докупать"
        actions={(tab === 'exit' || tab === 'relay') && addButton} />
      <Tabs id="servers-tab" label="Раздел серверов" tabs={tabs} value={tab}
        onChange={(t) => setF({ tab: t, role: '', node: '', phone: '' })} className="mb-4" />
      <TabPanel tabsId="servers-tab" tabKey={tab}>
        {items !== null && error && <p role="alert" className="text-sm text-danger mb-4">Не удалось обновить: {error}</p>}

        {tab === 'exit' && (
          <>
            {items !== null && <HostKeyNotice nodes={unconfirmedKeys(exits)} manage={manage} onOpen={openNode} />}
            {listState(exits, 'Выходов пока нет',
              'Купите VPS за рубежом (Ubuntu или Debian, x86_64) и добавьте его — мастер покажет, что вставить у хостера, всё настроит и проверит сам.')
              || <ExitsTable items={exits} onOpen={openNode} />}
          </>
        )}

        {tab === 'relay' && (
          <>
            {items !== null && <HostKeyNotice nodes={unconfirmedKeys(relays)} manage={manage} onOpen={openNode} />}
            <EntriesPanel nodesById={byId} onAdd={() => setF({ add: '1' })} onOpenNode={openNode}
              phoneId={f.phone} onPhone={(id) => setF({ phone: id ? String(id) : '' })} />
          </>
        )}

        {tab === 'hub' && <HubPanel />}

        {tab === 'capacity' && (
          cap.rep ? <CapacityPanel rep={cap.rep} />
            : cap.error ? (
              <div role="alert" className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded border border-danger/30 bg-danger/10 px-3 py-2">
                <span className="text-sm text-danger">Не удалось посчитать мощность: {cap.error}</span>
                <Button variant="secondary" size="sm" icon={<RefreshCw className="w-3.5 h-3.5" />} onClick={loadCapacity}>Повторить</Button>
              </div>
            ) : <div className="space-y-3"><Skeleton className="h-10" /><Skeleton className="h-40" /><Skeleton className="h-40" /></div>
        )}
      </TabPanel>

      {wizard && (
        <AddNodeWizard initialRole={addRole}
          onClose={() => { setF({ add: '' }); load() }}
          onOpenNode={(id) => { setF({ add: '', node: String(id) }); load() }} />
      )}
      {current && (
        <NodeDrawer node={current} lastExit={isLastExit(current, items)} onClose={() => setF({ node: '' })} onChanged={load} />
      )}
    </div>
  )
}
