-- 000045_direct_routing_grant_super_admin.down.sql
-- Reverses 000045: removes the two direct-routing permission bindings from super_admin.

DELETE FROM admin_role_permissions
WHERE role_id = (SELECT id FROM admin_roles WHERE role_key = 'super_admin')
  AND permission_id IN (
    SELECT id FROM admin_permissions WHERE permission_key IN ('routing.direct.view', 'routing.direct.manage')
  );
