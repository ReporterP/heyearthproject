import { useEffect, useState } from 'react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import DirectRouteEditDialog from '../components/DirectRouteEditDialog.jsx'
import DirectRoutesImportDialog from '../components/DirectRoutesImportDialog.jsx'
import DiscoveryDialog from '../components/DiscoveryDialog'

export default function DirectRoutesPage() {
  const { token } = useAuth()
  const toast = useToast()

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [stats, setStats] = useState(null)
  const [groups, setGroups] = useState([])
  const [loading, setLoading] = useState(true)

  // filters
  const [search, setSearch] = useState('')
  const [debouncedSearch, setDebouncedSearch] = useState('')
  const [group, setGroup] = useState('')
  const [activeFilter, setActiveFilter] = useState('all') // all | active | inactive

  // selection
  const [selected, setSelected] = useState(new Set())

  // dialogs
  const [editOpen, setEditOpen] = useState(false)
  const [editService, setEditService] = useState(null)
  const [importOpen, setImportOpen] = useState(false)
  const [discoveryOpen, setDiscoveryOpen] = useState(false)

  // Debounce search.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search), 300)
    return () => clearTimeout(t)
  }, [search])

  async function fetchAll() {
    setLoading(true)
    try {
      const params = new URLSearchParams()
      if (group) params.set('group', group)
      if (debouncedSearch) params.set('search', debouncedSearch)
      if (activeFilter === 'active') params.set('is_active', 'true')
      if (activeFilter === 'inactive') params.set('is_active', 'false')

      const [respItems, respStats, respGroups] = await Promise.all([
        fetch(`/api/admin/direct-routes?${params.toString()}`, { headers: { Authorization: `Bearer ${token}` } }),
        fetch('/api/admin/direct-routes/stats', { headers: { Authorization: `Bearer ${token}` } }),
        fetch('/api/admin/direct-routes/groups', { headers: { Authorization: `Bearer ${token}` } }),
      ])
      const itemsData = await respItems.json()
      const statsData = await respStats.json()
      const groupsData = await respGroups.json()
      setItems(itemsData.items || [])
      setTotal(itemsData.total || 0)
      setStats(statsData)
      setGroups(groupsData.groups || [])
    } catch (e) {
      toast.error('Ошибка загрузки: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchAll()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [debouncedSearch, group, activeFilter])

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

  async function bulkAction(action) {
    if (selected.size === 0) return
    if (action === 'delete' && !window.confirm(`Удалить ${selected.size} сервисов?`)) return
    try {
      const resp = await fetch('/api/admin/direct-routes/bulk', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
        body: JSON.stringify({ action, ids: Array.from(selected) }),
      })
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: resp.statusText }))
        throw new Error(err.error || resp.statusText)
      }
      toast.success(`Готово: ${selected.size}`)
      setSelected(new Set())
      fetchAll()
    } catch (e) {
      toast.error(e.message)
    }
  }

  async function deleteOne(id, name) {
    if (!window.confirm(`Удалить «${name}»?`)) return
    try {
      const resp = await fetch(`/api/admin/direct-routes/${id}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: resp.statusText }))
        throw new Error(err.error || resp.statusText)
      }
      toast.success('Удалено')
      fetchAll()
    } catch (e) {
      toast.error(e.message)
    }
  }

  const allSelected = selected.size > 0 && selected.size === items.length

  return (
    <div className="p-6 space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold text-slate-100">Прямая маршрутизация</h1>
        <div className="flex gap-2">
          <button onClick={() => { setEditService(null); setEditOpen(true) }}
            className="px-3 py-1.5 rounded bg-emerald-600 text-white hover:bg-emerald-500">
            + Добавить сервис
          </button>
          <button onClick={() => setDiscoveryOpen(true)}
            className="px-3 py-1.5 rounded bg-purple-600 text-white hover:bg-purple-500">
            🔍 Найти по домену
          </button>
          <button onClick={() => setImportOpen(true)}
            className="px-3 py-1.5 rounded bg-sky-600 text-white hover:bg-sky-500">
            Импорт JSON
          </button>
        </div>
      </div>

      <div className="rounded border border-sky-700/40 bg-sky-900/20 p-3 text-sm text-slate-300">
        Сервисы видны пользователям тарифа, у которого включена опция
        «Доступ к РФ-сервисам» в настройках тарифа.
        <a href="/plans" className="ml-1 text-sky-400 hover:underline">Настроить тарифы →</a>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-4 gap-3">
        <Card label="Всего сервисов" value={stats?.total_services ?? 0} />
        <Card label="Активных" value={stats?.active_services ?? 0} tone="emerald" />
        <Card label="Доменов" value={(stats?.total_domains ?? 0).toLocaleString('ru-RU')} tone="sky" />
        <Card label="IP-адресов" value={(stats?.total_ips ?? 0).toLocaleString('ru-RU')} tone="sky" />
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-2 bg-slate-900/40 border border-slate-800 rounded p-2">
        <input
          type="search" value={search} onChange={(e) => setSearch(e.target.value)}
          placeholder="Поиск по slug или названию"
          className="flex-1 min-w-[200px] px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100"
        />
        <select value={group} onChange={(e) => setGroup(e.target.value)}
          className="px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100">
          <option value="">Все группы</option>
          {groups.map((g) => <option key={g} value={g}>{g}</option>)}
        </select>
        <select value={activeFilter} onChange={(e) => setActiveFilter(e.target.value)}
          className="px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-slate-100">
          <option value="all">Все статусы</option>
          <option value="active">Только активные</option>
          <option value="inactive">Только неактивные</option>
        </select>
      </div>

      {/* Bulk bar */}
      {selected.size > 0 && (
        <div className="flex items-center gap-2 bg-slate-800 border border-slate-700 rounded p-2">
          <span className="text-sm text-slate-200">Выбрано: {selected.size}</span>
          <button onClick={() => bulkAction('activate')} className="px-2 py-1 text-xs rounded bg-emerald-700 hover:bg-emerald-600 text-white">Активировать</button>
          <button onClick={() => bulkAction('deactivate')} className="px-2 py-1 text-xs rounded bg-amber-700 hover:bg-amber-600 text-white">Деактивировать</button>
          <button onClick={() => bulkAction('delete')} className="px-2 py-1 text-xs rounded bg-red-700 hover:bg-red-600 text-white">Удалить</button>
          <button onClick={() => setSelected(new Set())} className="ml-auto text-xs text-slate-400 hover:text-slate-200">Очистить</button>
        </div>
      )}

      {/* Table */}
      <div className="overflow-x-auto rounded border border-slate-800">
        <table className="w-full text-sm">
          <thead className="bg-slate-900/60 text-slate-400 text-left">
            <tr>
              <th className="px-2 py-2 w-8">
                <input type="checkbox" checked={allSelected} onChange={toggleSelectAll} />
              </th>
              <th className="px-2 py-2">Slug</th>
              <th className="px-2 py-2">Название</th>
              <th className="px-2 py-2">Группа</th>
              <th className="px-2 py-2 text-right">Доменов</th>
              <th className="px-2 py-2 text-right">IP</th>
              <th className="px-2 py-2">Активен</th>
              <th className="px-2 py-2">Обновлён</th>
              <th className="px-2 py-2"></th>
            </tr>
          </thead>
          <tbody className="text-slate-200">
            {loading && (
              <tr><td colSpan={9} className="px-2 py-6 text-center text-slate-500">Загрузка…</td></tr>
            )}
            {!loading && items.length === 0 && (
              <tr><td colSpan={9} className="px-2 py-6 text-center text-slate-500">Нет сервисов</td></tr>
            )}
            {!loading && items.map((s) => (
              <tr key={s.id} className="border-t border-slate-800 hover:bg-slate-900/40">
                <td className="px-2 py-2">
                  <input type="checkbox" checked={selected.has(s.id)} onChange={() => toggleSelect(s.id)} />
                </td>
                <td className="px-2 py-2 font-mono text-xs">{s.slug}</td>
                <td className="px-2 py-2">{s.name}</td>
                <td className="px-2 py-2"><span className="text-xs px-2 py-0.5 rounded bg-slate-700 text-slate-200">{s.group_name}</span></td>
                <td className="px-2 py-2 text-right text-xs text-slate-400">{(s.domains || []).length}</td>
                <td className="px-2 py-2 text-right text-xs text-slate-400">{((s.ip4 || []).length + (s.ip6 || []).length + (s.cidr4 || []).length + (s.cidr6 || []).length)}</td>
                <td className="px-2 py-2">
                  {s.is_active
                    ? <span className="text-xs px-2 py-0.5 rounded bg-emerald-800/60 text-emerald-200">да</span>
                    : <span className="text-xs px-2 py-0.5 rounded bg-slate-700 text-slate-400">нет</span>}
                </td>
                <td className="px-2 py-2 text-xs text-slate-500">{(s.updated_at || '').replace('T', ' ').slice(0, 16)}</td>
                <td className="px-2 py-2 text-right">
                  <button onClick={() => { setEditService(s); setEditOpen(true) }}
                    className="px-2 py-1 text-xs rounded bg-slate-700 hover:bg-slate-600 text-slate-100">Edit</button>
                  <button onClick={() => deleteOne(s.id, s.name)}
                    className="ml-1 px-2 py-1 text-xs rounded bg-red-800 hover:bg-red-700 text-red-100">Del</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!loading && <div className="text-xs text-slate-500">Итого записей: {total}</div>}

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
    emerald: 'text-emerald-300',
    sky: 'text-sky-300',
    slate: 'text-slate-200',
  }[tone] || 'text-slate-200'
  return (
    <div className="rounded border border-slate-800 bg-slate-900/50 p-3">
      <div className={`text-xl font-semibold ${toneClass}`}>{value}</div>
      <div className="text-xs text-slate-500">{label}</div>
    </div>
  )
}
