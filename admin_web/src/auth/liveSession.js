// The stored login answer with the rights the server has now (GET /api/admin/me). Without it the
// panel showed the rights of the moment of login: a right added later (a new permission after a
// deploy, a role change) stayed hidden until the admin signed in again, a removed one stayed
// visible (the API refused it anyway). Returns the same object when nothing changed, so the
// caller can skip a re-render and a localStorage write.
export function withLivePermissions(session, live) {
  if (!session || !live || !Array.isArray(live.permissions)) return session
  const perms = [...live.permissions].sort()
  const roles = Array.isArray(live.roles) ? [...live.roles].sort() : null
  const same = (a, b) => Array.isArray(a) && a.length === b.length && [...a].sort().every((x, i) => x === b[i])
  const permsSame = same(session.permissions, perms) && (!session.admin || same(session.admin.permissions, perms))
  const rolesSame = !roles || same(session.roles, roles)
  if (permsSame && rolesSame) return session
  const next = { ...session, permissions: perms }
  if (roles) next.roles = roles
  if (session.admin) next.admin = { ...session.admin, permissions: perms, ...(roles ? { roles } : {}) }
  return next
}
