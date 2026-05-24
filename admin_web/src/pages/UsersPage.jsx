import { useState, useEffect, useCallback } from 'react'
import { Users } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchUsersPaged, createUser, updateUser, deleteUser, fetchUserDevices } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import { formatDateShort } from '../utils/format.js'
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
  Dialog,
} from '../components/ui/index.js'

const STATUS_FILTERS = [
  { value: '', key: 'filterAll' },
  { value: 'active', key: 'filterActive' },
  { value: 'blocked', key: 'filterBlocked' },
]

export default function UsersPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [f, setF] = useUrlState({ search: '', status: '', sort: 'created_at', dir: 'desc', page: 1 })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 20

  // Local search input bound to URL state on submit
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

  function openCreate() {
    setUsername('')
    setPassword('')
    setRole('user')
    setBlocked(false)
    setDialog({ mode: 'create' })
  }

  function openEdit(item) {
    setUsername(item.username || '')
    setPassword('')
    setRole(item.role || 'user')
    setBlocked(item.is_blocked || false)
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
      load()
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
      load()
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
      load()
    } catch (err) {
      toast.error(err.message)
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title={t.usersTitle + (total > 0 ? ` (${total})` : '')}
        subtitle={t.usersExportHint}
        actions={can('users.write') ? (
          <Button variant="primary" onClick={openCreate}>+ {t.createUser}</Button>
        ) : null}
      />

      {/* Status filter tabs */}
      <div className="flex flex-wrap items-center gap-2">
        {STATUS_FILTERS.map(sf => (
          <Button
            key={sf.value}
            size="sm"
            variant={f.status === sf.value ? 'primary' : 'secondary'}
            onClick={() => setF({ status: sf.value, page: 1 })}
          >
            {t[sf.key]}
          </Button>
        ))}
      </div>

      {/* Search */}
      <form onSubmit={onSearchSubmit} className="flex items-center gap-2 max-w-md">
        <Input
          type="text"
          placeholder={t.search}
          value={searchInput}
          onChange={e => setSearchInput(e.target.value)}
        />
        <Button type="submit" variant="secondary">{t.search}</Button>
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
              <Th sortKey="id" sortState={sortState} onSort={onSort}>ID</Th>
              <Th sortKey="user_id" sortState={sortState} onSort={onSort}>{t.fieldUserId}</Th>
              <Th sortKey="username" sortState={sortState} onSort={onSort}>{t.fieldUsername}</Th>
              <Th sortKey="role" sortState={sortState} onSort={onSort}>{t.fieldRole}</Th>
              <Th>{t.userPlan}</Th>
              <Th sortKey="created_at" sortState={sortState} onSort={onSort}>{t.fieldCreatedAt}</Th>
              <Th>{t.fieldBlocked}</Th>
              <Th>Действия</Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map(u => (
              <Tr key={u.id}>
                <Td mono>{u.id}</Td>
                <Td mono className="text-xs">{u.user_id}</Td>
                <Td>{u.username || '—'}</Td>
                <Td>{u.role}</Td>
                <Td>{u.plan || '—'}</Td>
                <Td className="text-xs text-tertiary whitespace-nowrap">{formatDateShort(u.created_at)}</Td>
                <Td>
                  {u.is_blocked
                    ? <Badge variant="danger">blocked</Badge>
                    : <Badge variant="success">active</Badge>}
                </Td>
                <Td>
                  <div className="flex items-center gap-2 flex-wrap">
                    {can('users.write') && (
                      <button
                        onClick={() => handleQuickToggleBlock(u)}
                        title={u.is_blocked ? t.quickUnblock : t.quickBlock}
                        aria-label={u.is_blocked ? t.quickUnblock : t.quickBlock}
                        className={`relative inline-flex h-5 w-9 items-center rounded-full transition ${u.is_blocked ? 'bg-danger/30' : 'bg-success/30'}`}
                      >
                        <span className={`inline-block h-3.5 w-3.5 rounded-full transition-transform ${u.is_blocked ? 'translate-x-1 bg-danger' : 'translate-x-[18px] bg-success'}`} />
                      </button>
                    )}
                    <Button size="sm" variant="ghost" onClick={() => showDevices(u)}>Devices</Button>
                    {can('users.write') && (
                      <>
                        <Button size="sm" variant="ghost" onClick={() => openEdit(u)}>{t.edit}</Button>
                        <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm(u)}>{t.delete}</Button>
                      </>
                    )}
                  </div>
                </Td>
              </Tr>
            ))}
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

      {/* Devices modal */}
      <Dialog
        open={!!devices}
        onClose={() => setDevices(null)}
        title={devices ? t.registeredDevicesLabel(devices.items.length) : ''}
        maxWidth="max-w-md"
      >
        {devices && (devices.items.length === 0 ? (
          <p className="text-secondary text-sm">{t.usersNoDevices || 'No devices'}</p>
        ) : (
          <div className="space-y-2 max-h-60 overflow-y-auto">
            {devices.items.map((d, i) => (
              <div key={i} className="text-sm bg-elevated rounded p-3 border border-default">
                <p className="font-mono text-xs text-tertiary">{d.device_id}</p>
                <p className="text-primary">{d.platform} {d.app_version}</p>
                {d.last_seen && <p className="text-xs text-tertiary">{t.deviceLastSeen}: {new Date(d.last_seen).toLocaleString()}</p>}
              </div>
            ))}
          </div>
        ))}
        <div className="flex justify-end mt-4">
          <Button variant="ghost" onClick={() => setDevices(null)}>{t.close}</Button>
        </div>
      </Dialog>

      {/* Create / Edit dialog */}
      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createUser : t.editUser}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
        {dialog?.mode === 'edit' && dialog.item?.user_id && (
          <FormField label={t.fieldUserId}>
            <div className="w-full px-3 py-2 rounded bg-elevated/50 border border-default text-tertiary text-sm font-mono select-all">
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
