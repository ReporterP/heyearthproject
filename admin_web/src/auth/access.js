// Which admin pages an admin may open: the same permission keys the backend checks
// (internal/admin/routes.go). One source for the sidebar and the ⌘K palette.

// href → permission; null = every admin (account, theme, logout live on /settings).
export const PAGE_PERMS = {
  '/dashboard': 'dashboard.read',
  '/users': 'users.read',
  '/promocodes': 'promocodes.read',
  '/servers': 'servers.read', // «Серверы»: exits, entries in RU, the Hub, «Мощность»
  '/nodes': 'servers.read', // old address → /servers
  '/entries': 'servers.read', // old address → /servers?tab=relay
  '/direct-routes': 'routing.direct.view',
  '/mtproto': 'settings.read',
  '/alerts': 'settings.read',
  '/support': 'support.manage',
  '/news': 'news.manage',
  '/telegram-bot': 'settings.read',
  '/plans': 'settings.read',
  '/payments': 'payments.read',
  '/payment-ops': 'payments.read', // old address → /payments?tab=emails
  '/referrals': 'users.read',
  '/admins': 'admins.read',
  '/audit-log': 'audit.read',
  '/tenants': 'tenants.read',
  '/b2b-analytics': 'tenants.read',
  '/settings': null,
}

// Pages a tenant (B2B) admin never sees, whatever the permissions.
export const TENANT_HIDDEN = new Set([
  '/servers', '/nodes', '/entries', '/direct-routes', '/mtproto', '/alerts', '/support', '/news', '/telegram-bot', '/admins', '/audit-log', '/tenants', '/b2b-analytics', '/settings',
])

export function canOpen(href, { can, isTenantAdmin }) {
  if (isTenantAdmin && TENANT_HIDDEN.has(href)) return false
  if (!Object.hasOwn(PAGE_PERMS, href)) return false // an unknown page is hidden, not shown by default
  const perm = PAGE_PERMS[href]
  return perm === null || can(perm)
}

// visibleItems keeps the pages an admin may open (by i.href or i.to) and every action.
export function visibleItems(items, ctx) {
  return items.filter((i) => i.kind === 'action' || canOpen(i.href ?? i.to, ctx))
}
