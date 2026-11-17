import { useCallback, useEffect, useId, useState } from 'react'
import { ShieldCheck, Plus, Lock } from 'lucide-react'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import {
  fetchAdminUsers, createAdminUser, updateAdminUser,
  fetchAdminRoles, createAdminRole, updateAdminRole,
  fetchAdminPermissions,
} from '../api/admin.js'
import {
  Badge, Button, Card, CardBody, CardHeader, Dialog, Input, PageHeader, Switch,
  Table, Thead, Tbody, Tr, Th, Td, EmptyState, TableSkeleton,
} from '../components/ui/index.js'
import {
  PASSWORD_MIN, normalizePermissions, permissionLabel, emptyAdminForm, adminFormFrom, adminPayload,
  emptyRoleForm, roleFormFrom, rolePayload, toggle, roleNames,
} from '../utils/adminForms.js'

function Field({ id, label, hint, children }) {
  return (
    <div>
      <label htmlFor={id} className="block text-xs text-secondary mb-1">{label}</label>
      {children}
      {hint && <p className="text-xs text-tertiary mt-1">{hint}</p>}
    </div>
  )
}

function FormButtons({ saving, error, onCancel, submitLabel }) {
  return (
    <>
      {error && <p role="alert" className="text-sm text-danger">{error}</p>}
      <div className="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 pt-2">
        <Button type="button" variant="ghost" onClick={onCancel}>Отмена</Button>
        <Button type="submit" loading={saving}>{submitLabel}</Button>
      </div>
    </>
  )
}

function AdminDialog({ dialog, roles, onClose, onSaved }) {
  const toast = useToast()
  const uid = useId()
  const [form, setForm] = useState(() => (dialog.mode === 'edit' ? adminFormFrom(dialog.item) : emptyAdminForm()))
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))

  async function submit(e) {
    e.preventDefault()
    const { body, error: err } = adminPayload(form, dialog.mode)
    setError(err)
    if (err) return
    setSaving(true)
    try {
      if (dialog.mode === 'create') await createAdminUser(body)
      else await updateAdminUser(dialog.item.id, body)
      toast.success(dialog.mode === 'create' ? 'Администратор создан' : 'Сохранено — права действуют с первого же запроса')
      onSaved()
    } catch (ex) {
      setError(ex?.message || 'Не удалось сохранить')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onClose={onClose} title={dialog.mode === 'create' ? 'Новый администратор' : `Администратор ${dialog.item.username}`} maxWidth="max-w-lg">
      <form onSubmit={submit} className="space-y-4">
        <Field id={`${uid}-u`} label="Логин">
          <Input id={`${uid}-u`} value={form.username} onChange={(e) => set({ username: e.target.value })} autoComplete="off" className="font-mono" autoFocus />
        </Field>
        <Field id={`${uid}-n`} label="Имя (необязательно)">
          <Input id={`${uid}-n`} value={form.displayName} onChange={(e) => set({ displayName: e.target.value })} />
        </Field>
        <Field id={`${uid}-p`} label={dialog.mode === 'create' ? 'Пароль' : 'Новый пароль (пусто — не менять)'} hint={`Не короче ${PASSWORD_MIN} символов`}>
          <Input id={`${uid}-p`} type="password" value={form.password} onChange={(e) => set({ password: e.target.value })} autoComplete="new-password" />
        </Field>
        <fieldset>
          <legend className="text-xs text-secondary mb-1">Роли</legend>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-3">
            {roles.map((r) => (
              <label key={r.id} className="flex items-center gap-2 min-h-9 text-sm text-primary cursor-pointer">
                <input type="checkbox" className="accent-accent" checked={form.roleKeys.includes(r.role_key)}
                  onChange={() => set({ roleKeys: toggle(form.roleKeys, r.role_key) })} />
                <span className="truncate">{r.name || r.role_key}</span>
                <code className="font-mono text-[11px] text-tertiary">{r.role_key}</code>
              </label>
            ))}
          </div>
        </fieldset>
        <Switch checked={form.isActive} onChange={(v) => set({ isActive: v })} label="Активен"
          description="Отключённый администратор не может войти; его текущая сессия перестаёт работать сразу." />
        <FormButtons saving={saving} error={error} onCancel={onClose} submitLabel={dialog.mode === 'create' ? 'Создать' : 'Сохранить'} />
      </form>
    </Dialog>
  )
}

function RoleDialog({ dialog, perms, onClose, onSaved }) {
  const toast = useToast()
  const uid = useId()
  const [form, setForm] = useState(() => (dialog.mode === 'edit' ? roleFormFrom(dialog.item) : emptyRoleForm()))
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const set = (patch) => setForm((f) => ({ ...f, ...patch }))

  async function submit(e) {
    e.preventDefault()
    const { body, error: err } = rolePayload(form, dialog.mode)
    setError(err)
    if (err) return
    setSaving(true)
    try {
      if (dialog.mode === 'create') await createAdminRole(body)
      else await updateAdminRole(dialog.item.id, body)
      toast.success(dialog.mode === 'create' ? 'Роль создана' : 'Роль сохранена — права действуют сразу у всех с этой ролью')
      onSaved()
    } catch (ex) {
      setError(ex?.message || 'Не удалось сохранить')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open onClose={onClose} title={dialog.mode === 'create' ? 'Новая роль' : `Роль «${dialog.item.name}»`} maxWidth="max-w-2xl">
      <form onSubmit={submit} className="space-y-4">
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <Field id={`${uid}-k`} label="Ключ" hint={dialog.mode === 'create' ? 'Латиница, цифры, «_»; потом не меняется' : 'Не меняется'}>
            <Input id={`${uid}-k`} value={form.roleKey} disabled={dialog.mode === 'edit'} onChange={(e) => set({ roleKey: e.target.value })} className="font-mono" placeholder="support" />
          </Field>
          <Field id={`${uid}-n`} label="Название">
            <Input id={`${uid}-n`} value={form.name} onChange={(e) => set({ name: e.target.value })} placeholder="Поддержка" />
          </Field>
        </div>
        <Field id={`${uid}-d`} label="Описание (необязательно)">
          <Input id={`${uid}-d`} value={form.description} onChange={(e) => set({ description: e.target.value })} />
        </Field>
        <fieldset>
          <legend className="text-xs text-secondary mb-1">Права</legend>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-3 max-h-72 overflow-y-auto rounded border border-default p-2">
            {perms.map((p) => (
              <label key={p.key} className="flex items-start gap-2 py-1 text-sm text-primary cursor-pointer" title={p.hint || undefined}>
                <input type="checkbox" className="accent-accent mt-1" checked={form.permissionKeys.includes(p.key)}
                  onChange={() => set({ permissionKeys: toggle(form.permissionKeys, p.key) })} />
                <span className="min-w-0">
                  <span className="block">{p.label}</span>
                  <code className="block font-mono text-[11px] text-tertiary">{p.key}</code>
                </span>
              </label>
            ))}
          </div>
        </fieldset>
        <FormButtons saving={saving} error={error} onCancel={onClose} submitLabel={dialog.mode === 'create' ? 'Создать' : 'Сохранить'} />
      </form>
    </Dialog>
  )
}

/**
 * AdminsPage — «Администраторы и роли»: who can enter the panel and with which rights. Roles and
 * rights come from the DB on every request, so a change acts at once. The system role is fixed;
 * the last active super admin cannot be switched off (the API refuses).
 */
export default function AdminsPage() {
  const { can } = useAuth()
  const toast = useToast()
  const canEdit = can('admins.write')
  const [data, setData] = useState(null)
  const [adminDialog, setAdminDialog] = useState(null)
  const [roleDialog, setRoleDialog] = useState(null)

  const load = useCallback(async () => {
    try {
      const [u, r, p] = await Promise.all([fetchAdminUsers(), fetchAdminRoles(), fetchAdminPermissions()])
      setData({ users: Array.isArray(u) ? u : u?.admins || [], roles: Array.isArray(r) ? r : r?.roles || [], perms: normalizePermissions(p) })
    } catch (err) {
      toast.error(err.message)
      setData((d) => d || { users: [], roles: [], perms: [], error: err.message })
    }
  }, [toast])
  useEffect(() => { load() }, [load])

  if (!data) {
    return (
      <div>
        <PageHeader title="Администраторы" subtitle="Кто входит в панель и с какими правами" />
        <TableSkeleton rows={4} cols={5} />
      </div>
    )
  }
  const saved = () => { setAdminDialog(null); setRoleDialog(null); load() }
  return (
    <div className="space-y-8">
      <section>
        <PageHeader title="Администраторы" subtitle="Кто входит в панель и с какими правами. Изменения прав действуют сразу."
          actions={canEdit && <Button icon={<Plus className="w-4 h-4" />} onClick={() => setAdminDialog({ mode: 'create' })}>Добавить администратора</Button>} />
        {data.users.length === 0 ? (
          <EmptyState icon={<ShieldCheck className="w-8 h-8" />} title="Администраторов нет" description={data.error || 'Создайте первую учётную запись.'} />
        ) : (
          <Table>
            <Thead>
              <Tr><Th>Логин</Th><Th>Имя</Th><Th>Роли</Th><Th>Статус</Th>{canEdit && <Th />}</Tr>
            </Thead>
            <Tbody>
              {data.users.map((u) => (
                <Tr key={u.id}>
                  <Td mono className="text-primary">{u.username}</Td>
                  <Td>{u.display_name || '—'}</Td>
                  <Td>
                    <div className="flex flex-wrap gap-1">
                      {roleNames(u.roles, data.roles).map((n) => <Badge key={n} variant="accent">{n}</Badge>)}
                    </div>
                  </Td>
                  <Td>{u.is_active !== false ? <Badge variant="success">активен</Badge> : <Badge>отключён</Badge>}</Td>
                  {canEdit && <Td align="right"><Button variant="ghost" size="sm" onClick={() => setAdminDialog({ mode: 'edit', item: u })}>Изменить</Button></Td>}
                </Tr>
              ))}
            </Tbody>
          </Table>
        )}
      </section>

      <section>
        <PageHeader title="Роли" subtitle="Наборы прав. Системная роль super_admin неизменна — для ограниченного доступа создайте свою."
          actions={canEdit && <Button variant="secondary" icon={<Plus className="w-4 h-4" />} onClick={() => setRoleDialog({ mode: 'create' })}>Создать роль</Button>} />
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
          {data.roles.map((r) => (
            <Card key={r.id}>
              <CardHeader className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <h3 className="text-sm font-semibold text-primary">{r.name || r.role_key}</h3>
                  <p className="font-mono text-xs text-tertiary">{r.role_key}</p>
                  {r.description && <p className="text-xs text-secondary mt-1">{r.description}</p>}
                </div>
                {r.is_system
                  ? <Badge variant="warning"><Lock className="w-3 h-3 mr-1 inline" aria-hidden="true" />системная</Badge>
                  : canEdit && <Button variant="ghost" size="sm" onClick={() => setRoleDialog({ mode: 'edit', item: r })}>Изменить</Button>}
              </CardHeader>
              <CardBody>
                {(r.permissions || []).includes('*')
                  ? <p className="text-xs text-secondary">Все права</p>
                  : (
                    <div className="flex flex-wrap gap-1">
                      {(r.permissions || []).map((p) => <Badge key={p} title={p}>{permissionLabel(p)}</Badge>)}
                      {(r.permissions || []).length === 0 && <span className="text-xs text-tertiary">Прав нет</span>}
                    </div>
                  )}
              </CardBody>
            </Card>
          ))}
        </div>
      </section>

      {adminDialog && <AdminDialog dialog={adminDialog} roles={data.roles} onClose={() => setAdminDialog(null)} onSaved={saved} />}
      {roleDialog && <RoleDialog dialog={roleDialog} perms={data.perms} onClose={() => setRoleDialog(null)} onSaved={saved} />}
    </div>
  )
}
