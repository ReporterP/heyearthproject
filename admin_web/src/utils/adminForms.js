// «Администраторы и роли» as pure rules (node --test): what the forms send to the API
// (internal/admin/auth.go adminUserPayload / adminRolePayload) and what they refuse before it.

export const PASSWORD_MIN = 10
export const ROLE_KEY_RE = /^[a-z][a-z0-9_]{1,39}$/

// Russian names of the permission keys (the catalog's names are English).
export const PERMISSION_LABELS = {
  'dashboard.read': 'Главная и метрики',
  'users.read': 'Пользователи: просмотр',
  'users.write': 'Пользователи: изменение, блокировка',
  'users.password_reset': 'Пользователи: сброс пароля (временный пароль виден администратору)',
  'subscriptions.read': 'Подписки: просмотр',
  'subscriptions.write': 'Подписки: изменение',
  'promocodes.read': 'Промокоды: просмотр',
  'promocodes.write': 'Промокоды: изменение',
  'payments.read': 'Платежи: просмотр',
  'payments.write': 'Платежи: ручные платежи',
  'payments.refund': 'Платежи: возвраты (деньги уходят со счёта)',
  'wallet.adjust': 'Кошелёк: ручное зачисление и списание',
  'servers.read': 'Серверы: просмотр',
  'servers.write': 'Серверы: цены, лимиты, параметры',
  'server_ops.execute': 'Серверы: перезапуск агента',
  'nodes.provision': 'Серверы: добавить, настроить, отключить',
  'servers.logs': 'Серверы: журналы',
  'admins.read': 'Администраторы: просмотр',
  'admins.write': 'Администраторы и роли: изменение',
  'settings.read': 'Настройки: просмотр',
  'settings.write': 'Настройки: изменение',
  'tenants.read': 'B2B: просмотр',
  'tenants.write': 'B2B: изменение',
  'routing.direct.view': 'Прямая маршрутизация: просмотр',
  'routing.direct.manage': 'Прямая маршрутизация: изменение',
  'audit.read': 'Журнал аудита',
}
export const permissionLabel = (key) => PERMISSION_LABELS[key] || key

// normalizePermissions: GET /admin-permissions items ({Key, Name, Description}) → [{key, label, hint}].
export function normalizePermissions(raw) {
  return (Array.isArray(raw) ? raw : raw?.permissions || []).map((x) => {
    const key = typeof x === 'object' ? (x.Key || x.key) : x
    return { key, label: permissionLabel(key), hint: typeof x === 'object' ? (x.Description || x.description || '') : '' }
  }).filter((p) => p.key)
}

export const emptyAdminForm = () => ({ username: '', displayName: '', password: '', roleKeys: [], isActive: true })

export function adminFormFrom(u) {
  return { username: u?.username || '', displayName: u?.display_name || '', password: '', roleKeys: [...(u?.roles || [])], isActive: u?.is_active !== false }
}

// adminPayload: the body of POST/PUT /admin-users, or the first error in words.
export function adminPayload(form, mode) {
  const username = String(form.username || '').trim()
  if (!username) return { error: 'Укажите логин' }
  if (mode === 'create' && !form.password) return { error: 'Укажите пароль' }
  if (form.password && form.password.length < PASSWORD_MIN) return { error: `Пароль — не короче ${PASSWORD_MIN} символов` }
  if (!form.roleKeys?.length) return { error: 'Выберите хотя бы одну роль' }
  const body = { username, display_name: String(form.displayName || '').trim(), role_keys: [...form.roleKeys], is_active: !!form.isActive }
  if (form.password) body.password = form.password
  return { body, error: '' }
}

export const emptyRoleForm = () => ({ roleKey: '', name: '', description: '', permissionKeys: [] })

export function roleFormFrom(r) {
  return { roleKey: r?.role_key || '', name: r?.name || '', description: r?.description || '', permissionKeys: [...(r?.permissions || [])] }
}

// rolePayload: the body of POST/PUT /admin-roles (the key is set once, at creation).
export function rolePayload(form, mode) {
  const name = String(form.name || '').trim()
  const key = String(form.roleKey || '').trim()
  if (mode === 'create' && !ROLE_KEY_RE.test(key)) return { error: 'Ключ роли: латиница в нижнем регистре, цифры и «_», например support' }
  if (!name) return { error: 'Укажите название роли' }
  const body = { name, description: String(form.description || '').trim(), permission_keys: [...form.permissionKeys].sort() }
  if (mode === 'create') body.role_key = key
  return { body, error: '' }
}

export const toggle = (list, v) => (list.includes(v) ? list.filter((x) => x !== v) : [...list, v])

// roleNames: an admin's role keys in words.
export function roleNames(keys, roles) {
  return (keys || []).map((k) => roles.find((r) => r.role_key === k)?.name || k)
}
