import { useState, useMemo, useCallback } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchUsersPaged, createUser, updateUser, deleteUser, fetchUserDevices } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

const STATUS_FILTERS = [
  { value: '', key: 'filterAll' },
  { value: 'active', key: 'filterActive' },
  { value: 'blocked', key: 'filterBlocked' },
]

const PLATFORM_OPTIONS = [
  { value: '', label: '\u2014' },
  { value: 'windows', label: 'Windows' },
  { value: 'android', label: 'Android' },
  { value: 'ios', label: 'iOS' },
  { value: 'linux', label: 'Linux' },
  { value: 'macos', label: 'macOS' },
]

// Columns that support sorting (key must match item field)
const SORTABLE_COLUMNS = ['id', 'user_id', 'username', 'role', 'plan', 'created_at', 'preferred_platform', 'telegram_id']

function SortIcon({ column, sortKey, sortDir }) {
  if (column !== sortKey) {
    return (
      <svg className="w-3 h-3 ml-1 inline opacity-30" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
        <path strokeLinecap="round" strokeLinejoin="round" d="M7 16V4m0 0L3 8m4-4l4 4m6 0v12m0 0l4-4m-4 4l-4-4" />
      </svg>
    )
  }
  return sortDir === 'asc' ? (
    <svg className="w-3 h-3 ml-1 inline text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M5 15l7-7 7 7" />
    </svg>
  ) : (
    <svg className="w-3 h-3 ml-1 inline text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
      <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
    </svg>
  )
}

function formatDate(val) {
  if (!val) return '\u2014'
  try {
    return new Date(val).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
  } catch {
    return val
  }
}

export default function UsersPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  // Status filter — passed to backend via pagedFetch
  const [statusFilter, setStatusFilter] = useState('')

  // Custom fetch wrapper that passes status filter
  const fetchWithStatus = useCallback((params) => {
    return fetchUsersPaged({ ...params, status: statusFilter })
  }, [statusFilter])

  const paged = usePagedData(fetchWithStatus)

  // Client-side sorting
  const [sortKey, setSortKey] = useState('')
  const [sortDir, setSortDir] = useState('asc') // 'asc' | 'desc'

  const sortedItems = useMemo(() => {
    if (!sortKey) return paged.items
    const copy = [...paged.items]
    copy.sort((a, b) => {
      let va = a[sortKey] ?? ''
      let vb = b[sortKey] ?? ''
      if (typeof va === 'string') va = va.toLowerCase()
      if (typeof vb === 'string') vb = vb.toLowerCase()
      if (va < vb) return sortDir === 'asc' ? -1 : 1
      if (va > vb) return sortDir === 'asc' ? 1 : -1
      return 0
    })
    return copy
  }, [paged.items, sortKey, sortDir])

  function toggleSort(key) {
    if (sortKey === key) {
      setSortDir(d => d === 'asc' ? 'desc' : 'asc')
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  // Dialogs
  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [devices, setDevices] = useState(null)

  // Form state
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState('user')
  const [blocked, setBlocked] = useState(false)
  const [telegramId, setTelegramId] = useState('')
  const [preferredPlatform, setPreferredPlatform] = useState('')

  function openCreate() {
    setUsername('')
    setPassword('')
    setRole('user')
    setBlocked(false)
    setTelegramId('')
    setPreferredPlatform('')
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setUsername(item.username || '')
    setPassword('')
    setRole(item.role || 'user')
    setBlocked(item.is_blocked || false)
    setTelegramId(item.telegram_id || '')
    setPreferredPlatform(item.preferred_platform || '')
    setDialog({ mode: 'edit', item })
  }

  async function showDevices(user) {
    try {
      const devs = await fetchUserDevices(user.id)
      const items = Array.isArray(devs) ? devs : (Array.isArray(devs?.devices) ? devs.devices : [])
      setDevices({ user, items })
    } catch (err) {
      toast.error(err.message)
    }
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = {
        username,
        role,
        is_blocked: blocked,
        telegram_id: telegramId || undefined,
        preferred_platform: preferredPlatform || undefined,
      }
      if (password) payload.password = password
      if (dialog.mode === 'create') {
        await createUser(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateUser(dialog.item.id, payload)
        toast.success(t.save + ' OK')
      }
      setDialog(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteUser(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  async function handleQuickToggleBlock(user) {
    try {
      await updateUser(user.id, { is_blocked: !user.is_blocked })
      toast.success(!user.is_blocked ? t.quickBlock + ' OK' : t.quickUnblock + ' OK')
      paged.reload()
    } catch (err) {
      toast.error(err.message)
    }
  }

  function handleStatusFilter(val) {
    setStatusFilter(val)
    // Reset to page 1 when filter changes — paged hook will re-fetch
    paged.setPage(1)
  }

  // Column definitions
  const columns = [
    { key: 'id', label: 'ID' },
    { key: 'user_id', label: t.fieldUserId },
    { key: 'username', label: t.fieldUsername },
    { key: 'role', label: t.fieldRole },
    { key: 'plan', label: t.userPlan },
    { key: 'preferred_platform', label: t.fieldPreferredPlatform },
    { key: 'telegram_id', label: t.fieldTelegramId },
    { key: 'created_at', label: t.fieldCreatedAt },
    { key: 'is_blocked', label: t.fieldBlocked, sortable: false },
  ]

  return (
    <div>
      {/* Header: title with count + create button */}
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">
          {t.usersTitle}
          {paged.total > 0 && (
            <span className="ml-2 text-base font-normal text-text-secondary">({paged.total})</span>
          )}
        </h2>
        {can('users.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createUser}
          </button>
        )}
      </div>

      {/* Export hint */}
      <div className="mb-3 px-3 py-2 rounded-lg bg-surface-alt border border-white/5 text-xs text-text-secondary flex items-center gap-2">
        <svg className="w-4 h-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M11.25 11.25l.041-.02a.75.75 0 011.063.852l-.708 2.836a.75.75 0 001.063.853l.041-.021M21 12a9 9 0 11-18 0 9 9 0 0118 0zm-9-3.75h.008v.008H12V8.25z" />
        </svg>
        <span>{t.usersExportHint}</span>
      </div>

      {/* Status filter tabs + search/pagination bar */}
      <div className="flex flex-wrap items-center gap-2 mb-3">
        {STATUS_FILTERS.map(f => (
          <button
            key={f.value}
            onClick={() => handleStatusFilter(f.value)}
            className={`px-3 py-1 rounded-full text-xs font-medium transition ${
              statusFilter === f.value
                ? 'bg-brand text-white'
                : 'bg-surface-alt text-text-secondary hover:text-text-primary border border-white/10'
            }`}
          >
            {t[f.key]}
          </button>
        ))}
      </div>

      <SearchPaginationBar
        search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {/* Empty state */}
      {paged.items.length === 0 && !paged.loading ? (
        <div className="flex flex-col items-center justify-center py-16 text-text-secondary">
          <svg className="w-16 h-16 mb-4 opacity-30" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M15 19.128a9.38 9.38 0 002.625.372 9.337 9.337 0 004.121-.952 4.125 4.125 0 00-7.533-2.493M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M15 19.128H5.228A2.25 2.25 0 013 16.878c0-2.292 1.834-4.21 4.276-4.576A5.25 5.25 0 0112 7.5a5.25 5.25 0 014.724 4.802c.094.008.187.018.28.028M15 19.128v-.003c0-1.113-.285-2.16-.786-3.07M12 12.75a3.75 3.75 0 100-7.5 3.75 3.75 0 000 7.5z" />
          </svg>
          <p className="text-lg font-medium mb-1">{t.noUsers}</p>
          <p className="text-sm">{t.noUsersDesc}</p>
        </div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                {columns.map(col => {
                  const sortable = col.sortable !== false && SORTABLE_COLUMNS.includes(col.key)
                  return (
                    <th
                      key={col.key}
                      className={`py-2 px-3 whitespace-nowrap ${sortable ? 'cursor-pointer select-none hover:text-text-primary transition' : ''}`}
                      onClick={sortable ? () => toggleSort(col.key) : undefined}
                    >
                      {col.label}
                      {sortable && <SortIcon column={col.key} sortKey={sortKey} sortDir={sortDir} />}
                    </th>
                  )
                })}
                <th className="py-2 px-3"></th>
              </tr>
            </thead>
            <tbody>
              {sortedItems.map(u => (
                <tr key={u.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{u.id}</td>
                  <td className="py-2 px-3 font-mono text-xs">{u.user_id}</td>
                  <td className="py-2 px-3">{u.username || '\u2014'}</td>
                  <td className="py-2 px-3">{u.role}</td>
                  <td className="py-2 px-3">{u.plan || '\u2014'}</td>
                  <td className="py-2 px-3">{u.preferred_platform || '\u2014'}</td>
                  <td className="py-2 px-3 font-mono text-xs">{u.telegram_id || '\u2014'}</td>
                  <td className="py-2 px-3 whitespace-nowrap text-xs text-text-secondary">{formatDate(u.created_at)}</td>
                  <td className="py-2 px-3">
                    {u.is_blocked ? (
                      <StatusBadge status="blocked" />
                    ) : (
                      <StatusBadge status="active" />
                    )}
                  </td>
                  <td className="py-2 px-3">
                    <div className="flex items-center gap-2 flex-wrap">
                      {/* Quick block/unblock toggle */}
                      {can('users.write') && (
                        <button
                          onClick={() => handleQuickToggleBlock(u)}
                          title={u.is_blocked ? t.quickUnblock : t.quickBlock}
                          className={`relative inline-flex h-5 w-9 items-center rounded-full transition ${
                            u.is_blocked ? 'bg-red-500/30' : 'bg-green-500/30'
                          }`}
                        >
                          <span
                            className={`inline-block h-3.5 w-3.5 rounded-full transition-transform ${
                              u.is_blocked ? 'translate-x-1 bg-red-400' : 'translate-x-[18px] bg-green-400'
                            }`}
                          />
                        </button>
                      )}
                      <button onClick={() => showDevices(u)} className="text-text-secondary hover:text-text-primary text-xs">Devices</button>
                      {can('users.write') && (
                        <>
                          <button onClick={() => openEdit(u)} className="text-brand hover:underline">{t.edit}</button>
                          <button onClick={() => setConfirm(u)} className="text-red-400 hover:underline">{t.delete}</button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Devices modal */}
      {devices && (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/50" onClick={() => setDevices(null)}>
          <div className="bg-surface-card rounded-xl border border-white/10 p-5 w-full max-w-md m-4" onClick={e => e.stopPropagation()}>
            <h3 className="text-lg font-semibold mb-3">{t.registeredDevicesLabel(devices.items.length)}</h3>
            {devices.items.length === 0 ? (
              <p className="text-text-secondary text-sm">No devices</p>
            ) : (
              <div className="space-y-2 max-h-60 overflow-y-auto">
                {devices.items.map((d, i) => (
                  <div key={i} className="text-sm bg-surface-alt rounded-lg p-3">
                    <p className="font-mono text-xs text-text-secondary">{d.device_id}</p>
                    <p>{d.platform} {d.app_version}</p>
                    {d.last_seen && <p className="text-xs text-text-secondary">{t.deviceLastSeen}: {new Date(d.last_seen).toLocaleString()}</p>}
                  </div>
                ))}
              </div>
            )}
            <button onClick={() => setDevices(null)} className="mt-4 px-4 py-2 rounded-lg text-sm text-text-secondary hover:text-text-primary transition">{t.close}</button>
          </div>
        </div>
      )}

      {/* Create / Edit dialog */}
      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createUser : t.editUser}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        {/* Read-only user_id in edit mode */}
        {dialog?.mode === 'edit' && dialog.item?.user_id && (
          <FormField label={t.fieldUserId}>
            <div className="w-full px-3 py-2 rounded-lg bg-surface-alt/50 border border-white/5 text-text-secondary text-sm font-mono select-all">
              {dialog.item.user_id}
            </div>
          </FormField>
        )}
        <FormField label={t.fieldUsername}>
          <TextInput value={username} onChange={setUsername} autoFocus />
        </FormField>
        <FormField label={t.password}>
          <TextInput value={password} onChange={setPassword} type="password" placeholder={dialog?.mode === 'edit' ? 'Leave blank to keep' : ''} />
        </FormField>
        <FormField label={t.fieldRole}>
          <SelectInput value={role} onChange={setRole} options={[{ value: 'user', label: 'User' }, { value: 'premium', label: 'Premium' }]} />
        </FormField>
        <FormField label={t.fieldTelegramId}>
          <TextInput value={telegramId} onChange={setTelegramId} placeholder="123456789" />
        </FormField>
        <FormField label={t.fieldPreferredPlatform}>
          <SelectInput
            value={preferredPlatform}
            onChange={setPreferredPlatform}
            options={PLATFORM_OPTIONS}
            placeholder={t.fieldPlatformNotSet}
          />
        </FormField>
        <Checkbox label={t.fieldBlocked} checked={blocked} onChange={setBlocked} />
      </CrudDialog>

      <ConfirmDialog
        open={!!confirm}
        message={t.deleteUserConfirm(confirm?.user_id || confirm?.id)}
        onConfirm={handleDelete} onCancel={() => setConfirm(null)} loading={saving}
      />
    </div>
  )
}
