import { useEffect, useState, useCallback } from 'react'
import { Route, Search } from 'lucide-react'
import { useToast } from '../components/Toast.jsx'
import { fetchDirectRoutes, fetchDirectRouteStats, fetchDirectRouteGroups, bulkDirectRoutes, deleteDirectRoute } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import DirectRouteEditDialog from '../components/DirectRouteEditDialog.jsx'
import DirectRoutesImportDialog from '../components/DirectRoutesImportDialog.jsx'
import DiscoveryDialog from '../components/DiscoveryDialog'
import {
  Button,
  Input,
  Select,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  Badge,
  TableSkeleton,
  EmptyState, ConfirmDanger,
} from '../components/ui/index.js'

export default function DirectRoutesPage() {
  const [danger, setDanger] = useState(null) // { kind: 'bulk' } | { kind: 'one', id, name }
  const toast = useToast()

  const [f, setF] = useUrlState({ search: '', group: '', is_active: 'all', sort: '', dir: 'asc', page: 1 })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [stats, setStats] = useState(null)
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)

  // Local search input for debouncing
  const [searchInput, setSearchInput] = useState(f.search)
  useEffect(() => {
    const tm = setTimeout(() => {
      if (searchInput !== f.search) setF({ search: searchInput, page: 1 })
    }, 300)
    return () => clearTimeout(tm)
  }, [searchInput]) // eslint-disable-line react-hooks/exhaustive-deps

  // selection
  const [selected, setSelected] = useState(new Set())

  // dialogs
  const [editOpen, setEditOpen] = useState(false)
  const [editService, setEditService] = useState(null)
  const [importOpen, setImportOpen] = useState(false)
  const [discoveryOpen, setDiscoveryOpen] = useState(false)

  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const params = new URLSearchParams()
      if (f.group) params.set('group', f.group)
      if (f.search) params.set('search', f.search)
      if (f.is_active === 'active') params.set('is_active', 'true')
      if (f.is_active === 'inactive') params.set('is_active', 'false')
      if (f.sort) params.set('sort', f.sort)
      if (f.dir) params.set('dir', f.dir)

      const [itemsData, statsData, groupsData] = await Promise.all([
        fetchDirectRoutes(params.toString()), fetchDirectRouteStats(), fetchDirectRouteGroups(),
      ])
      setItems(itemsData.items || [])
      setTotal(itemsData.total || 0)
      setStats(statsData)
      setGroups(groupsData.groups || [])
    } catch (e) {
      toast.error('Ошибка загрузки: ' + e.message)
    } finally {
      setLoading(false)
    }
  }, [f.search, f.group, f.is_active, f.sort, f.dir]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { fetchAll() }, [fetchAll])

  function toggleSelect(id) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function toggleSelectAll() {
    if (selected.size === items.length) setSelected(new Set())
    else setSelected(new Set(items.map((i) => i.id)))
  }

  // bulkAction: delete asks first (ConfirmDanger), the rest run at once.
  async function runBulk(action) {
    await bulkDirectRoutes(action, Array.from(selected))
    toast.success(`Готово: ${selected.size}`)
    setSelected(new Set())
    fetchAll()
  }
  async function bulkAction(action) {
    if (selected.size === 0) return
    if (action === 'delete') { setDanger({ kind: 'bulk' }); return }
    try { await runBulk(action) } catch (e) { toast.error(e.message) }
  }

  function deleteOne(id, name) {
    setDanger({ kind: 'one', id, name })
  }
  async function confirmDanger() {
    if (danger.kind === 'bulk') await runBulk('delete')
    else {
      await deleteDirectRoute(danger.id)
      toast.success('Удалено')
      fetchAll()
    }
  }

  const allSelected = selected.size > 0 && selected.size === items.length

  return (
    <div className="space-y-4">
      <PageHeader
        title="Прямая маршрутизация"
        subtitle="РФ-сервисы, доступные пользователям тарифа с включённой опцией"
        actions={
          <>
            <Button variant="primary" onClick={() => { setEditService(null); setEditOpen(true) }}>
              + Добавить сервис
            </Button>
            <Button variant="secondary" icon={<Search className="w-4 h-4" />} onClick={() => setDiscoveryOpen(true)}>
              Найти по домену
            </Button>
            <Button variant="secondary" onClick={() => setImportOpen(true)}>
              Импорт JSON
            </Button>
          </>
        }
      />

      <div className="rounded border border-info/40 bg-info/15 p-3 text-sm text-secondary">
        Сервисы видны пользователям тарифа, у которого включена опция «Доступ к РФ-сервисам».
        <a href="/plans" className="ml-1 text-info hover:underline">Настроить тарифы →</a>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-4 gap-3">
        <Card label="Всего сервисов" value={stats?.total_services ?? 0} />
        <Card label="Активных" value={stats?.active_services ?? 0} tone="success" />
        <Card label="Доменов" value={(stats?.total_domains ?? 0).toLocaleString('ru-RU')} tone="info" />
        <Card label="IP-адресов" value={(stats?.total_ips ?? 0).toLocaleString('ru-RU')} tone="info" />
      </div>

      {/* Filters */}
      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2 bg-elevated/40 border border-default rounded p-2">
        <Input
          type="search" value={searchInput} onChange={(e) => setSearchInput(e.target.value)}
          placeholder="Поиск по slug или названию"
          className="w-full sm:flex-1 sm:min-w-[200px]"
        />
        <Select value={f.group} onChange={(e) => setF({ group: e.target.value, page: 1 })} className="w-full sm:w-48">
          <option value="">Все группы</option>
          {groups.map((g) => <option key={g} value={g}>{g}</option>)}
        </Select>
        <Select value={f.is_active} onChange={(e) => setF({ is_active: e.target.value, page: 1 })} className="w-full sm:w-48">
          <option value="all">Все статусы</option>
          <option value="active">Только активные</option>
          <option value="inactive">Только неактивные</option>
        </Select>
      </div>

      {/* Bulk bar */}
      {selected.size > 0 && (
        <div className="flex items-center gap-2 bg-elevated border border-default rounded p-2">
          <span className="text-sm text-primary">Выбрано: {selected.size}</span>
          <Button size="sm" variant="primary" onClick={() => bulkAction('activate')}>Активировать</Button>
          <Button size="sm" variant="secondary" onClick={() => bulkAction('deactivate')}>Деактивировать</Button>
          <Button size="sm" variant="danger" onClick={() => bulkAction('delete')}>Удалить</Button>
          <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setSelected(new Set())}>Очистить</Button>
        </div>
      )}

      {/* Table */}
      {loading ? (
        <TableSkeleton rows={10} cols={9} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Route className="w-12 h-12" />}
          title="Нет сервисов"
          description={f.search || f.group || f.is_active !== 'all' ? 'Попробуйте изменить фильтры' : 'Добавьте сервис или импортируйте JSON'}
          action={<Button variant="primary" onClick={() => { setEditService(null); setEditOpen(true) }}>+ Добавить</Button>}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th className="w-8">
                <input type="checkbox" checked={allSelected} onChange={toggleSelectAll} aria-label="Выбрать все" />
              </Th>
              <Th sortKey="slug" sortState={sortState} onSort={onSort}>Slug</Th>
              <Th sortKey="name" sortState={sortState} onSort={onSort}>Название</Th>
              <Th sortKey="group_name" sortState={sortState} onSort={onSort}>Группа</Th>
              <Th align="right">Доменов</Th>
              <Th align="right">IP</Th>
              <Th>Активен</Th>
              <Th sortKey="updated_at" sortState={sortState} onSort={onSort}>Обновлён</Th>
              <Th>Действия</Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map((s) => (
              <Tr key={s.id}>
                <Td>
                  <input type="checkbox" checked={selected.has(s.id)} onChange={() => toggleSelect(s.id)} aria-label={`Выбрать ${s.name}`} />
                </Td>
                <Td mono className="text-xs">{s.slug}</Td>
                <Td>{s.name}</Td>
                <Td>
                  {s.group_name ? <Badge variant="default">{s.group_name}</Badge> : <span className="text-tertiary">—</span>}
                </Td>
                <Td align="right" mono className="text-xs text-tertiary">{(s.domains || []).length}</Td>
                <Td align="right" mono className="text-xs text-tertiary">
                  {((s.ip4 || []).length + (s.ip6 || []).length + (s.cidr4 || []).length + (s.cidr6 || []).length)}
                </Td>
                <Td>
                  {s.is_active
                    ? <Badge variant="success">да</Badge>
                    : <Badge variant="default">нет</Badge>}
                </Td>
                <Td className="text-xs text-disabled whitespace-nowrap">{(s.updated_at || '').replace('T', ' ').slice(0, 16)}</Td>
                <Td>
                  <div className="flex gap-1">
                    <Button size="sm" variant="ghost" onClick={() => { setEditService(s); setEditOpen(true) }}>Изменить</Button>
                    <Button size="sm" variant="ghost" className="text-danger" onClick={() => deleteOne(s.id, s.name)}>Del</Button>
                  </div>
                </Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}
      {!loading && <div className="text-xs text-disabled">Итого записей: {total}</div>}

      <DirectRouteEditDialog
        open={editOpen}
        service={editService}
        onClose={() => setEditOpen(false)}
        onSaved={fetchAll}
      />
      <DirectRoutesImportDialog
        open={importOpen}
        onClose={() => setImportOpen(false)}
        onImported={fetchAll}
      />
      <DiscoveryDialog
        open={discoveryOpen}
        onClose={() => setDiscoveryOpen(false)}
        onCreated={fetchAll}
      />
    </div>
  )
}

function Card({ label, value, tone }) {
  const toneClass = {
    success: 'text-success',
    info: 'text-info',
    default: 'text-primary',
  }[tone] || 'text-primary'
  return (
    <div className="rounded border border-default bg-elevated/40 p-3">
      <div className={`text-xl font-semibold tabular-nums ${toneClass}`}>{value}</div>
      <div className="text-xs text-disabled">{label}</div>
      <ConfirmDanger open={!!danger} onClose={() => setDanger(null)}
        title={danger?.kind === 'bulk' ? `Удалить ${selected.size} сервисов` : `Удалить «${danger?.name || ''}»`}
        confirmText={danger?.kind === 'bulk' ? 'УДАЛИТЬ' : danger?.name || ''} actionLabel="Удалить"
        consequences={['Домены и адреса сервиса перестанут идти напрямую у всех тарифов, где он подключён.', 'Подписки пользователей обновятся автоматически.']}
        onConfirm={confirmDanger} />
    </div>
  )
}
