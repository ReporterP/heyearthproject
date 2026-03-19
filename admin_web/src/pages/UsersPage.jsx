import { useState } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import usePagedData from '../hooks/usePagedData.js'
import { fetchUsersPaged, createUser, updateUser, deleteUser, fetchUserDevices } from '../api/admin.js'
import SearchPaginationBar from '../components/SearchPaginationBar.jsx'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import StatusBadge from '../components/StatusBadge.jsx'

export default function UsersPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const paged = usePagedData(fetchUsersPaged)

  const [dialog, setDialog] = useState(null)
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [devices, setDevices] = useState(null)

  // form
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
      setDevices({ user, items: devs.devices || devs || [] })
    } catch (err) {
      toast.error(err.message)
    }
  }

  async function handleSave() {
    setSaving(true)
    try {
      const payload = { username, role, is_blocked: blocked }
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

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <h2 className="text-2xl font-bold">{t.usersTitle}</h2>
        {can('users.write') && (
          <button onClick={openCreate} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
            + {t.createUser}
          </button>
        )}
      </div>

      <SearchPaginationBar
        search={paged.search} onSearch={paged.doSearch}
        page={paged.page} totalPages={paged.totalPages} onPageChange={paged.setPage}
        loading={paged.loading}
      />

      {paged.error && <p className="text-red-400 mb-4">{paged.error}</p>}

      {paged.items.length === 0 && !paged.loading ? (
        <p className="text-text-secondary">{t.noUsers}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.fieldUserId}</th>
                <th className="py-2 px-3">{t.fieldUsername}</th>
                <th className="py-2 px-3">{t.fieldRole}</th>
                <th className="py-2 px-3">{t.userPlan}</th>
                <th className="py-2 px-3">{t.fieldBlocked}</th>
                <th className="py-2 px-3"></th>
              </tr>
            </thead>
            <tbody>
              {paged.items.map(u => (
                <tr key={u.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{u.id}</td>
                  <td className="py-2 px-3 font-mono text-xs">{u.user_id}</td>
                  <td className="py-2 px-3">{u.username || '\u2014'}</td>
                  <td className="py-2 px-3">{u.role}</td>
                  <td className="py-2 px-3">{u.plan || '\u2014'}</td>
                  <td className="py-2 px-3">{u.is_blocked ? <StatusBadge status="blocked" /> : ''}</td>
                  <td className="py-2 px-3 flex gap-2 flex-wrap">
                    <button onClick={() => showDevices(u)} className="text-text-secondary hover:text-text-primary text-xs">Devices</button>
                    {can('users.write') && (
                      <>
                        <button onClick={() => openEdit(u)} className="text-brand hover:underline">{t.edit}</button>
                        <button onClick={() => setConfirm(u)} className="text-red-400 hover:underline">{t.delete}</button>
                      </>
                    )}
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

      <CrudDialog
        open={!!dialog} title={dialog?.mode === 'create' ? t.createUser : t.editUser}
        onClose={() => setDialog(null)} onSubmit={handleSave} loading={saving}
      >
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
