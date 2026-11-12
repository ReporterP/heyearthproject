// Which admin pages an admin may open: the same permission keys the backend checks
// (internal/admin/routes.go). One source for the sidebar and the ⌘K palette.

// href → permission; null = every admin (account, theme, logout live on /settings).
export const PAGE_PERMS = {
  '/dashboard': 'dashboard.read',
  '/traffic': 'dashboard.read',
  '/users': 'users.read',
  '/subscriptions': 'subscriptions.read',
  '/promocodes': 'promocodes.read',
  '/servers': 'servers.read', // redirects to /nodes
  '/nodes': 'servers.read',
  '/entries': 'servers.read',
  '/ssh-keys': 'servers.read',
  '/releases': 'releases.read',
  '/traffic-masking': 'traffic_masking.read',
  '/direct-routes': 'routing.direct.view',
  '/mtproto': 'settings.read',
  '/auto-tuner': 'settings.read',
  '/alerts': 'settings.read',
  '/plans': 'settings.read',
  '/payments': 'payments.read',
  '/payment-ops': 'payments.read',
  '/admins': 'admins.read',
  '/audit-log': 'audit.read',
  '/tenants': 'tenants.read',
  '/b2b-analytics': 'tenants.read',
  '/settings': null,
}

// Pages a tenant (B2B) admin never sees, whatever the permissions.
export const TENANT_HIDDEN = new Set([
  '/servers', '/nodes', '/entries', '/ssh-keys', '/releases', '/traffic-masking', '/direct-routes', '/mtproto',
  '/auto-tuner', '/alerts', '/admins', '/audit-log', '/tenants', '/b2b-analytics', '/settings',
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
