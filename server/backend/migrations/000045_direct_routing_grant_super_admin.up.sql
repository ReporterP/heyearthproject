-- 000045_direct_routing_grant_super_admin.up.sql
-- Grants the two new direct-routing permissions to the super_admin role.
-- Migration 000044 created the permission rows but did not bind them to any role,
-- which means the admin UI's client-side sidebar filter (can(perm) =
-- permissions.includes(perm)) hides the "Прямая маршрутизация" entry from
-- super_admin users until they explicitly receive these keys.

INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM admin_roles r
CROSS JOIN admin_permissions p
WHERE r.role_key = 'super_admin'
  AND p.permission_key IN ('routing.direct.view', 'routing.direct.manage')
ON CONFLICT DO NOTHING;
