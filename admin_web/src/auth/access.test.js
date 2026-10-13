import { test } from 'node:test'
import assert from 'node:assert/strict'
import { canOpen, visibleItems, PAGE_PERMS } from './access.js'

const ctx = (perms, isTenantAdmin = false) => ({ can: (p) => perms.includes(p), isTenantAdmin })

test('a payments operator sees payments and settings, not servers or tariffs', () => {
  const c = ctx(['payments.read'])
  for (const href of ['/payments', '/payment-ops', '/settings']) assert.equal(canOpen(href, c), true, href)
  for (const href of ['/servers', '/plans', '/auto-tuner', '/admins']) assert.equal(canOpen(href, c), false, href)
})

test('tenant admins never see infrastructure pages, whatever their permissions', () => {
  const all = Object.values(PAGE_PERMS).filter(Boolean)
  for (const href of ['/servers', '/auto-tuner', '/alerts', '/admins', '/settings']) {
    assert.equal(canOpen(href, ctx(all, true)), false, href)
  }
  assert.equal(canOpen('/users', ctx(all, true)), true)
})

test('unknown pages are hidden, actions are kept', () => {
  assert.equal(canOpen('/nope', ctx(['dashboard.read'])), false)
  const items = [
    { id: 'go-servers', kind: 'page', href: '/servers' },
    { id: 'go-dashboard', kind: 'page', href: '/dashboard' },
    { id: 'action-logout', kind: 'action', perform: 'logout' },
    { to: '/payments' },
  ]
  assert.deepEqual(visibleItems(items, ctx(['dashboard.read'])).map((i) => i.id ?? i.to), ['go-dashboard', 'action-logout'])
})
