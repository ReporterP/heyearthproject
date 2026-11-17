import { test } from 'node:test'
import assert from 'node:assert/strict'
import { withLivePermissions } from './liveSession.js'

test('live rights replace the ones stored at login', () => {
  const stored = { token: 't', permissions: ['users.read'], roles: ['super_admin'], admin: { id: 1, username: 'admin', permissions: ['users.read'], roles: ['super_admin'] } }
  const next = withLivePermissions(stored, { permissions: ['users.read', 'users.password_reset'], roles: ['super_admin'] })
  assert.notEqual(next, stored)
  assert.deepEqual(next.permissions, ['users.password_reset', 'users.read'])
  assert.deepEqual(next.admin.permissions, ['users.password_reset', 'users.read'])
  assert.equal(next.token, 't')
  assert.equal(next.admin.username, 'admin')
  // a removed right disappears too
  assert.deepEqual(withLivePermissions(next, { permissions: ['users.read'] }).permissions, ['users.read'])
})

test('nothing changed, nothing to save; bad answers keep the session', () => {
  const stored = { token: 't', permissions: ['b', 'a'], roles: ['r'], admin: { permissions: ['a', 'b'], roles: ['r'] } }
  assert.equal(withLivePermissions(stored, { permissions: ['a', 'b'], roles: ['r'] }), stored)
  assert.equal(withLivePermissions(stored, { error: 'x' }), stored)
  assert.equal(withLivePermissions(stored, null), stored)
  assert.equal(withLivePermissions(null, { permissions: ['a'] }), null)
})
