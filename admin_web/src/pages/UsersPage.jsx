import { useState, useEffect, useCallback, useMemo } from 'react'
import { Users, MoreHorizontal } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { fetchUsersPaged } from '../api/admin.js'
import { hasPlan } from '../utils/users.js'
import { useUrlState } from '../hooks/useUrlState.js'
import { useHotkey } from '../hooks/useHotkey.js'
import UserDrawer from '../components/UserDrawer.jsx'
import { formatDateShort, formatBytes, formatRelativeRu } from '../utils/format.js'
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
  Badge,
  TableSkeleton,
  EmptyState,
} from '../components/ui/index.js'

// Status filter tabs. v1 ships without live counts — labels only.
// To add counts later, hit /users/status-counts endpoint (out of scope).
const STATUS_TABS = [
  { value: '',         key: 'filterAll' },
  { value: 'active',   key: 'statusActive' },
  { value: 'expiring', key: 'statusExpiring' },
  { value: 'no_plan',  key: 'statusNoPlan' },
  { value: 'blocked',  key: 'statusBlocked' },
]

// Status dot color resolved from row state.
//   blocked        → danger (red)
//   no plan / expired → tertiary (gray)
//   expiring in 7d → warning (amber)
//   active         → success (green)
function statusDot(user) {
  if (user.is_blocked) return 'bg-danger'
  if (!user.valid_until) return 'bg-tertiary'
  const expires = new Date(user.valid_until).getTime()
  const now = Date.now()
  if (expires < now) return 'bg-tertiary'
  if (expires - now < 7 * 86400000) return 'bg-warning'
  return 'bg-success'
}

export default function UsersPage() {
  const { t } = useT()

  const [f, setF] = useUrlState({
    search: '', status: '', sort: 'last_seen', dir: 'desc', page: 1,
    open: '', tab: 'profile',
  })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(-1)
  const pageSize = 20

  const [searchInput, setSearchInput] = useState(f.search)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchUsersPaged({ page: f.page, pageSize, search: f.search, status: f.status, sort: f.sort, dir: f.dir })
      .then(data => {
        setItems(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [f.page, f.search, f.status, f.sort, f.dir])

  useEffect(() => { load() }, [load])

  // Drawer: derive selected user object from items + URL state.
  // v1 limitation: if `f.open` references a user not in the current page,
  // selectedUser will be null and the drawer will not render. User can
  // re-click after paginating. A future iteration could fetch the single
  // user on cache miss.
  const selectedUser = useMemo(() => {
    if (!f.open) return null
    const id = Number(f.open)
    return items.find(u => u.id === id) || null
  }, [items, f.open])

  function openDrawer(user, tab = 'profile') {
    setF({ open: String(user.id), tab })
  }
  function closeDrawer() {
    setF({ open: '', tab: '' })
  }

  // J/K row navigation + Enter to open. Skip when drawer or create dialog is open
  // so the global hotkey doesn't fight with form inputs / drawer ESC.
  useHotkey({ key: 'j' }, () => {
    if (f.open) return
    setSelectedIndex(i => Math.min(items.length - 1, i + 1))
  })
  useHotkey({ key: 'k' }, () => {
    if (f.open) return
    setSelectedIndex(i => Math.max(0, i - 1))
  })
  useHotkey({ key: 'Enter' }, () => {
    if (f.open) return
    if (selectedIndex >= 0 && items[selectedIndex]) {
      openDrawer(items[selectedIndex])
    }
  })

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-4">
      <PageHeader
        title={t.usersTitle + (total > 0 ? ` (${total})` : '')}
        subtitle="Пользователи регистрируются сами — в приложении или на сайте. Здесь — поиск, карточка, блокировка."
      />

      {/* Status filter tabs — replaces previous button group. Counts deferred to v2. */}
      <div className="flex flex-wrap items-center gap-1 border-b border-default">
        {STATUS_TABS.map(tab => {
          const active = f.status === tab.value
          return (
            <button
              key={tab.value || 'all'}
              onClick={() => setF({ status: tab.value, page: 1 })}
              className={`px-3 py-1.5 text-sm font-medium rounded-t transition relative ${
                active ? 'text-accent bg-accent/10' : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {t[tab.key]}
              {active && <span className="absolute left-0 right-0 -bottom-px h-0.5 bg-accent" />}
            </button>
          )
        })}
      </div>

      {/* Search */}
      <form onSubmit={onSearchSubmit} className="flex flex-col sm:flex-row sm:items-center gap-2 max-w-md">
        <Input
          type="text"
          placeholder={t.search}
          value={searchInput}
          onChange={e => setSearchInput(e.target.value)}
          className="w-full sm:flex-1"
        />
        <Button type="submit" variant="secondary" className="w-full sm:w-auto">{t.search}</Button>
      </form>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={7} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Users className="w-12 h-12" />}
          title={t.noUsers}
          description={t.noUsersDesc}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="username" sortState={sortState} onSort={onSort}>{t.fieldUsername}</Th>
              <Th>{t.userPlan}</Th>
              <Th>{t.fieldStatus}</Th>
              <Th sortKey="valid_until" sortState={sortState} onSort={onSort}>{t.columnValidUntil}</Th>
              <Th sortKey="last_seen" sortState={sortState} onSort={onSort}>{t.columnLastSeen}</Th>
              <Th sortKey="created_at" sortState={sortState} onSort={onSort}>{t.fieldCreatedAt}</Th>
              <Th sortKey="traffic_30d_bytes" sortState={sortState} onSort={onSort}>{t.columnTraffic30d}</Th>
              <Th></Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map((u, idx) => {
              const isSelected = idx === selectedIndex
              const trafficPct = u.traffic_limit_bytes > 0
                ? Math.min(100, Math.round((u.traffic_30d_bytes / u.traffic_limit_bytes) * 100))
                : 0
              return (
                <Tr
                  key={u.id}
                  onClick={() => openDrawer(u)}
                  className={`group cursor-pointer ${isSelected ? 'bg-accent/10 border-l-2 border-l-accent' : ''}`}
                >
                  <Td>
                    <div className="flex flex-col">
                      <span className="text-primary text-sm">{u.username || '—'}</span>
                      <span className="font-mono text-xs text-tertiary">{u.user_id}</span>
                    </div>
                  </Td>
                  <Td>{hasPlan(u) ? <Badge variant="accent">{u.current_plan}</Badge> : <span className="text-tertiary">—</span>}</Td>
                  <Td>
                    <span
                      className={`inline-block w-2 h-2 rounded-full ${statusDot(u)}`}
                      aria-label={u.is_blocked ? 'blocked' : 'active'}
                    />
                  </Td>
                  <Td mono className="text-xs whitespace-nowrap">{u.valid_until ? formatDateShort(u.valid_until) : '—'}</Td>
                  <Td className="text-xs text-tertiary whitespace-nowrap">{formatRelativeRu(u.last_seen)}</Td>
                  <Td mono className="text-xs text-tertiary whitespace-nowrap">{formatDateShort(u.created_at)}</Td>
                  <Td>
                    <div className="flex flex-col gap-1 min-w-[120px]">
                      <div className="h-1 bg-canvas border border-default rounded-full overflow-hidden">
                        <div
                          className={`h-full transition-all ${trafficPct >= 90 ? 'bg-danger' : trafficPct >= 70 ? 'bg-warning' : 'bg-info'}`}
                          style={{ width: `${trafficPct}%` }}
                        />
                      </div>
                      <span className="text-xs text-tertiary font-mono tabular-nums">
                        {formatBytes(u.traffic_30d_bytes || 0)}
                        {u.traffic_limit_bytes > 0 && ` / ${formatBytes(u.traffic_limit_bytes)}`}
                      </span>
                    </div>
                  </Td>
                  <Td align="right">
                    {/* Hover ⋯ overflow button — opens drawer same as row click.
                        v2: per-row dropdown with quick actions. For v1 keep it as
                        a visual affordance only — actions all live in the drawer. */}
                    <button
                      onClick={e => { e.stopPropagation(); openDrawer(u) }}
                      className="opacity-0 group-hover:opacity-100 hover:bg-hover text-tertiary rounded p-1 transition"
                      aria-label={t.actions}
                    >
                      <MoreHorizontal className="w-3.5 h-3.5" />
                    </button>
                  </Td>
                </Tr>
              )
            })}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={f.page <= 1} onClick={() => setF({ page: f.page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{f.page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={f.page >= totalPages} onClick={() => setF({ page: f.page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
        </div>
      )}

      {/* Drawer — render only when selectedUser resolves */}
      <UserDrawer
        user={selectedUser}
        defaultTab={f.tab || 'profile'}
        onClose={closeDrawer}
        onUserMutated={load}
      />

    </div>
  )
}
