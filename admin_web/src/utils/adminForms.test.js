import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  normalizePermissions, adminFormFrom, adminPayload, roleFormFrom, rolePayload, toggle, roleNames, permissionLabel,
} from './adminForms.js'

test('admin form: the fields the API reads (role_keys), the checks before it', () => {
  const f = adminFormFrom({ username: 'helper', display_name: 'Хелпер', roles: ['support'], is_active: true })
  assert.deepEqual(f.roleKeys, ['support'])
  assert.deepEqual(adminPayload(f, 'edit'), { body: { username: 'helper', display_name: 'Хелпер', role_keys: ['support'], is_active: true }, error: '' })
  assert.match(adminPayload({ ...f, password: 'short' }, 'edit').error, /не короче 10/)
  assert.equal(adminPayload({ ...f, password: 'long-enough-1' }, 'edit').body.password, 'long-enough-1')
  assert.match(adminPayload({ ...f, password: '' }, 'create').error, /пароль/)
  assert.match(adminPayload({ ...f, roleKeys: [] }, 'edit').error, /роль/)
  assert.match(adminPayload({ ...f, username: ' ' }, 'edit').error, /логин/)
})

test('role form: role_key once, permission_keys sorted, no is_system', () => {
  const f = roleFormFrom({ role_key: 'support', name: 'Поддержка', permissions: ['users.read'] })
  const edited = { ...f, permissionKeys: toggle(toggle(f.permissionKeys, 'payments.read'), 'users.read') }
  assert.deepEqual(rolePayload(edited, 'edit'), { body: { name: 'Поддержка', description: '', permission_keys: ['payments.read'] }, error: '' })
  const created = rolePayload({ roleKey: 'support', name: 'Поддержка', description: ' ', permissionKeys: ['b', 'a'] }, 'create')
  assert.deepEqual(created.body, { role_key: 'support', name: 'Поддержка', description: '', permission_keys: ['a', 'b'] })
  assert.ok(!('is_system' in created.body))
  assert.match(rolePayload({ roleKey: 'Bad Key', name: 'x', permissionKeys: [] }, 'create').error, /Ключ роли/)
  assert.match(rolePayload({ roleKey: 'ok_key', name: '', permissionKeys: [] }, 'create').error, /название/)
})

test('permissions in words; role names', () => {
  assert.deepEqual(normalizePermissions([{ Key: 'users.read', Name: 'Users Read', Description: 'View users' }, 'x.y']),
    [{ key: 'users.read', label: 'Пользователи: просмотр', hint: 'View users' }, { key: 'x.y', label: 'x.y', hint: '' }])
  assert.equal(permissionLabel('payments.refund'), 'Платежи: возвраты (деньги уходят со счёта)')
  assert.deepEqual(roleNames(['support', 'gone'], [{ role_key: 'support', name: 'Поддержка' }]), ['Поддержка', 'gone'])
})
