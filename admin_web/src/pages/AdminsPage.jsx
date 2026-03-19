import { useState, useEffect } from 'react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import {
  fetchAdminUsers, createAdminUser, updateAdminUser,
  fetchAdminRoles, createAdminRole, updateAdminRole,
  fetchAdminPermissions,
} from '../api/admin.js'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'

export default function AdminsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [users, setUsers] = useState([])
  const [roles, setRoles] = useState([])
  const [allPerms, setAllPerms] = useState([])
  const [loading, setLoading] = useState(true)

  const [userDialog, setUserDialog] = useState(null)
  const [roleDialog, setRoleDialog] = useState(null)
  const [saving, setSaving] = useState(false)

  // user form
  const [username, setUsername] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [password, setPassword] = useState('')
  const [userRoles, setUserRoles] = useState([])
  const [isActive, setIsActive] = useState(true)

  // role form
  const [roleKey, setRoleKey] = useState('')
  const [roleName, setRoleName] = useState('')
  const [rolePerms, setRolePerms] = useState([])

  async function load() {
    setLoading(true)
    try {
      const [u, r, p] = await Promise.all([fetchAdminUsers(), fetchAdminRoles(), fetchAdminPermissions()])
      setUsers(u.admins || u || [])
      setRoles(r.roles || r || [])
      setAllPerms(p.permissions || p || [])
    } catch (err) {
      toast.error(err.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // user CRUD
  function openCreateUser() {
    setUsername(''); setDisplayName(''); setPassword(''); setUserRoles([]); setIsActive(true)
    setUserDialog({ mode: 'create' })
  }

  function openEditUser(u) {
    setUsername(u.username || ''); setDisplayName(u.display_name || ''); setPassword('')
    setUserRoles(u.roles?.map(r => r.id || r) || []); setIsActive(u.is_active !== false)
    setUserDialog({ mode: 'edit', item: u })
  }

  async function handleSaveUser() {
    setSaving(true)
    try {
      const payload = { username, display_name: displayName, role_ids: userRoles, is_active: isActive }
      if (password) payload.password = password
      if (userDialog.mode === 'create') {
        await createAdminUser(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateAdminUser(userDialog.item.id, payload)
        toast.success(t.save + ' OK')
      }
      setUserDialog(null)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  // role CRUD
  function openCreateRole() {
    setRoleKey(''); setRoleName(''); setRolePerms([])
    setRoleDialog({ mode: 'create' })
  }

  function openEditRole(r) {
    setRoleKey(r.key || ''); setRoleName(r.name || '')
    setRolePerms(r.permissions || [])
    setRoleDialog({ mode: 'edit', item: r })
  }

  async function handleSaveRole() {
    setSaving(true)
    try {
      const payload = { key: roleKey, name: roleName, permissions: rolePerms }
      if (roleDialog.mode === 'create') {
        await createAdminRole(payload)
        toast.success(t.create + ' OK')
      } else {
        await updateAdminRole(roleDialog.item.id, payload)
        toast.success(t.save + ' OK')
      }
      setRoleDialog(null)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  function togglePerm(perm) {
    setRolePerms(prev => prev.includes(perm) ? prev.filter(p => p !== perm) : [...prev, perm])
  }

  function toggleUserRole(roleId) {
    setUserRoles(prev => prev.includes(roleId) ? prev.filter(r => r !== roleId) : [...prev, roleId])
  }

  if (loading) return <p className="text-text-secondary">{t.loading}</p>

  return (
    <div className="space-y-8">
      {/* Admin Users */}
      <section>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-2xl font-bold">{t.sectionAdminUsers}</h2>
          {can('admins.write') && (
            <button onClick={openCreateUser} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
              + {t.createAdmin}
            </button>
          )}
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-white/10 text-text-secondary text-left">
                <th className="py-2 px-3">ID</th>
                <th className="py-2 px-3">{t.fieldUsername}</th>
                <th className="py-2 px-3">{t.fieldDisplayName}</th>
                <th className="py-2 px-3">{t.fieldRoles}</th>
                <th className="py-2 px-3">{t.fieldAdminIsActive}</th>
                {can('admins.write') && <th className="py-2 px-3"></th>}
              </tr>
            </thead>
            <tbody>
              {users.map(u => (
                <tr key={u.id} className="border-b border-white/5 hover:bg-white/5 transition">
                  <td className="py-2 px-3">{u.id}</td>
                  <td className="py-2 px-3">{u.username}</td>
                  <td className="py-2 px-3">{u.display_name || '\u2014'}</td>
                  <td className="py-2 px-3">
                    <div className="flex flex-wrap gap-1">
                      {(u.roles || []).map((r, i) => (
                        <span key={i} className="text-xs bg-brand/20 text-brand px-2 py-0.5 rounded-full">{r.name || r.key || r}</span>
                      ))}
                    </div>
                  </td>
                  <td className="py-2 px-3">{u.is_active !== false ? '\u2713' : '\u2717'}</td>
                  {can('admins.write') && (
                    <td className="py-2 px-3">
                      <button onClick={() => openEditUser(u)} className="text-brand hover:underline">{t.edit}</button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* Roles */}
      <section>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-2xl font-bold">{t.sectionRoles}</h2>
          {can('admins.write') && (
            <button onClick={openCreateRole} className="px-4 py-2 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-dark transition">
              + {t.createRole}
            </button>
          )}
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {roles.map(r => (
            <div key={r.id} className="bg-surface-card rounded-xl border border-white/10 p-4">
              <div className="flex items-center justify-between mb-2">
                <h3 className="font-semibold">{r.name || r.key}</h3>
                {r.is_system && <span className="text-xs bg-yellow-500/20 text-yellow-400 px-2 py-0.5 rounded-full">{t.fieldIsSystem}</span>}
              </div>
              <p className="text-xs text-text-secondary font-mono mb-2">{r.key}</p>
              <div className="flex flex-wrap gap-1">
                {(r.permissions || []).map(p => (
                  <span key={p} className="text-xs bg-surface-alt text-text-secondary px-2 py-0.5 rounded">{p}</span>
                ))}
              </div>
              {can('admins.write') && !r.is_system && (
                <button onClick={() => openEditRole(r)} className="mt-3 text-xs text-brand hover:underline">{t.edit}</button>
              )}
            </div>
          ))}
        </div>
      </section>

      {/* User dialog */}
      <CrudDialog
        open={!!userDialog}
        title={userDialog?.mode === 'create' ? t.createAdmin : t.editAdmin}
        onClose={() => setUserDialog(null)} onSubmit={handleSaveUser} loading={saving}
      >
        <FormField label={t.fieldUsername}><TextInput value={username} onChange={setUsername} autoFocus /></FormField>
        <FormField label={t.fieldDisplayName}><TextInput value={displayName} onChange={setDisplayName} /></FormField>
        <FormField label={t.fieldNewPassword}><TextInput value={password} onChange={setPassword} type="password" /></FormField>
        <Checkbox label={t.fieldAdminIsActive} checked={isActive} onChange={setIsActive} />
        <FormField label={t.fieldRoles}>
          <div className="flex flex-wrap gap-2">
            {roles.map(r => (
              <label key={r.id} className="flex items-center gap-1 text-sm cursor-pointer">
                <input type="checkbox" checked={userRoles.includes(r.id)} onChange={() => toggleUserRole(r.id)} className="rounded border-white/20 bg-surface-alt text-brand focus:ring-brand" />
                {r.name || r.key}
              </label>
            ))}
          </div>
        </FormField>
      </CrudDialog>

      {/* Role dialog */}
      <CrudDialog
        open={!!roleDialog}
        title={roleDialog?.mode === 'create' ? t.createRole : t.editRole}
        onClose={() => setRoleDialog(null)} onSubmit={handleSaveRole} loading={saving}
      >
        <FormField label={t.fieldRoleKey}><TextInput value={roleKey} onChange={setRoleKey} disabled={roleDialog?.mode === 'edit'} /></FormField>
        <FormField label={t.fieldDisplayName}><TextInput value={roleName} onChange={setRoleName} /></FormField>
        <FormField label={t.fieldPermissions}>
          <div className="grid grid-cols-2 gap-1 max-h-48 overflow-y-auto">
            {allPerms.map(p => (
              <label key={p} className="flex items-center gap-1 text-xs cursor-pointer">
                <input type="checkbox" checked={rolePerms.includes(p)} onChange={() => togglePerm(p)} className="rounded border-white/20 bg-surface-alt text-brand focus:ring-brand" />
                {p}
              </label>
            ))}
          </div>
        </FormField>
      </CrudDialog>
    </div>
  )
}
