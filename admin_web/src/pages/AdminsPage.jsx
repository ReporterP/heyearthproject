import { useState, useEffect } from 'react'
import { ShieldCheck } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import {
  fetchAdminUsers, createAdminUser, updateAdminUser,
  fetchAdminRoles, createAdminRole, updateAdminRole,
  fetchAdminPermissions,
} from '../api/admin.js'
import CrudDialog, { FormField, TextInput, Checkbox } from '../components/CrudDialog.jsx'
import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  EmptyState,
  TableSkeleton,
} from '../components/ui/index.js'

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
      // permissions come as objects {Key, Name, Description} or strings — normalize to strings
      const rawPerms = p.permissions || p || []
      setAllPerms(rawPerms.map(x => typeof x === 'object' ? (x.Key || x.key || x.name) : x))
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
    // normalize role permissions to string keys
    setRolePerms((r.permissions || []).map(x => typeof x === 'object' ? (x.Key || x.key) : x))
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

  if (loading) return (
    <div>
      <PageHeader title={t.sectionAdminUsers} subtitle="Администраторы и роли" />
      <TableSkeleton rows={6} cols={5} />
    </div>
  )

  return (
    <div className="space-y-8">
      {/* Admin Users */}
      <section>
        <PageHeader
          title={t.sectionAdminUsers}
          subtitle="Учётные записи с доступом к админ-панели"
          actions={can('admins.write') ? (
            <Button onClick={openCreateUser}>+ {t.createAdmin}</Button>
          ) : null}
        />
        {users.length === 0 ? (
          <EmptyState icon={<ShieldCheck className="w-12 h-12" />} title="Нет администраторов" description="Создайте первую учётную запись администратора." />
        ) : (
          <Table>
            <Thead>
              <Tr>
                <Th>ID</Th>
                <Th>{t.fieldUsername}</Th>
                <Th>{t.fieldDisplayName}</Th>
                <Th>{t.fieldRoles}</Th>
                <Th>{t.fieldAdminIsActive}</Th>
                {can('admins.write') && <Th></Th>}
              </Tr>
            </Thead>
            <Tbody>
              {users.map(u => (
                <Tr key={u.id}>
                  <Td mono>{u.id}</Td>
                  <Td><span className="font-mono text-primary">{u.username}</span></Td>
                  <Td>{u.display_name || '—'}</Td>
                  <Td>
                    <div className="flex flex-wrap gap-1">
                      {(u.roles || []).map((r, i) => (
                        <Badge key={i} variant="accent">{r.name || r.key || r}</Badge>
                      ))}
                    </div>
                  </Td>
                  <Td>
                    {u.is_active !== false
                      ? <Badge variant="success">Активен</Badge>
                      : <Badge variant="default">Отключён</Badge>}
                  </Td>
                  {can('admins.write') && (
                    <Td>
                      <Button variant="ghost" size="sm" onClick={() => openEditUser(u)}>{t.edit}</Button>
                    </Td>
                  )}
                </Tr>
              ))}
            </Tbody>
          </Table>
        )}
      </section>

      {/* Roles */}
      <section>
        <PageHeader
          title={t.sectionRoles}
          subtitle="Роли и наборы разрешений"
          actions={can('admins.write') ? (
            <Button onClick={openCreateRole}>+ {t.createRole}</Button>
          ) : null}
        />
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {roles.map(r => (
            <Card key={r.id}>
              <CardHeader>
                <div className="flex items-center justify-between">
                  <h3 className="font-semibold text-primary">{r.name || r.key}</h3>
                  {r.is_system && <Badge variant="warning">{t.fieldIsSystem}</Badge>}
                </div>
                <p className="text-xs text-tertiary font-mono mt-1">{r.key}</p>
              </CardHeader>
              <CardBody>
                <div className="flex flex-wrap gap-1">
                  {(r.permissions || []).map((p, i) => {
                    const label = typeof p === 'object' ? (p.Key || p.key || p.name) : p
                    return (
                      <Badge key={label || i} variant="default">{label}</Badge>
                    )
                  })}
                </div>
                {can('admins.write') && !r.is_system && (
                  <div className="mt-3">
                    <Button variant="ghost" size="sm" onClick={() => openEditRole(r)}>{t.edit}</Button>
                  </div>
                )}
              </CardBody>
            </Card>
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
              <label key={r.id} className="flex items-center gap-1 text-sm cursor-pointer text-primary">
                <input type="checkbox" checked={userRoles.includes(r.id)} onChange={() => toggleUserRole(r.id)} className="rounded border-default bg-elevated text-accent focus:ring-accent" />
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
              <label key={p} className="flex items-center gap-1 text-xs cursor-pointer text-primary">
                <input type="checkbox" checked={rolePerms.includes(p)} onChange={() => togglePerm(p)} className="rounded border-default bg-elevated text-accent focus:ring-accent" />
                {p}
              </label>
            ))}
          </div>
        </FormField>
      </CrudDialog>
    </div>
  )
}
