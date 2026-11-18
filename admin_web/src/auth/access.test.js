import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { canOpen, visibleItems, pageGate, PAGE_PERMS, TENANT_HIDDEN } from './access.js'

const ctx = (perms, isTenantAdmin = false) => ({ can: (p) => perms.includes(p), isTenantAdmin })

test('a payments operator sees payments and settings, not servers or tariffs', () => {
  const c = ctx(['payments.read'])
  for (const href of ['/payments', '/payment-ops', '/settings']) assert.equal(canOpen(href, c), true, href)
  for (const href of ['/servers', '/plans', '/alerts', '/admins']) assert.equal(canOpen(href, c), false, href)
})

test('tenant admins never see infrastructure pages, whatever their permissions', () => {
  const all = Object.values(PAGE_PERMS).filter(Boolean)
  assert.ok(TENANT_HIDDEN.size > 0)
  for (const href of TENANT_HIDDEN) {
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

test('every page route in main.jsx has a PAGE_PERMS entry (no page silently vanishes from the nav)', () => {
  const src = readFileSync(new URL('../main.jsx', import.meta.url), 'utf8')
  const paths = [...src.matchAll(/path="([^"]+)"/g)].map((m) => m[1])
  const pages = paths.filter((p) => p !== '/login' && p !== '*' && !p.includes(':'))
  assert.ok(pages.length >= 15, `only ${pages.length} routes found — has the route syntax changed?`)
  for (const p of pages) assert.ok(Object.hasOwn(PAGE_PERMS, p), `${p} is routed but missing from PAGE_PERMS`)
  for (const p of TENANT_HIDDEN) assert.ok(Object.hasOwn(PAGE_PERMS, p), `${p} is tenant-hidden but missing from PAGE_PERMS`)
})

test('object built-ins are not pages', () => {
  for (const href of ['constructor', 'toString', '__proto__', 'hasOwnProperty']) {
    assert.equal(canOpen(href, { can: () => true, isTenantAdmin: false }), false, href)
  }
})

test('a page the admin may not open is refused by the router, not rendered empty (QA 2026-11-18)', () => {
  const support = ctx(['dashboard.read', 'users.read', 'support.manage'])
  assert.equal(pageGate('/plans', support), 'denied')
  assert.equal(pageGate('/audit-log', support), 'denied')
  assert.equal(pageGate('/admins', support), 'denied')
  assert.equal(pageGate('/support', support), 'open')
  assert.equal(pageGate('/settings', support), 'open')
  assert.equal(pageGate('/servers/12', ctx(['servers.read'])), 'open', 'a nested path takes its section\'s permission')
  assert.equal(pageGate('/servers/12', support), 'denied')
  assert.equal(pageGate('/nowhere', support), 'open', 'unknown paths fall through to the catch-all redirect')
})

test('every page route in main.jsx sits under PageGuard', () => {
  const src = readFileSync(new URL('../main.jsx', import.meta.url), 'utf8')
  const guard = src.indexOf('<Route element={<PageGuard />}>')
  assert.ok(guard > 0, 'main.jsx has no <Route element={<PageGuard />}>')
  const first = src.indexOf('path="/dashboard"')
  const close = src.indexOf('</Route>', guard)
  assert.ok(first > guard && first < close, 'the page routes are not inside the PageGuard route')
  assert.ok(src.indexOf('path="/telegram-bot"') < close, 'the last page route is outside the PageGuard route')
})
