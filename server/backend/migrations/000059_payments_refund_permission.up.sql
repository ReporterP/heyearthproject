-- 000059_payments_refund_permission.up.sql
-- Node pool, phase 1: refunds (and the other YooKassa money actions: cancel, check-status,
-- resend) get their own permission instead of riding on AuthMiddleware alone. Granted to
-- super_admin; internal/admin permissionCatalog carries the same key, so ensureSuperAdminRole
-- (which rebuilds super_admin's permissions on every login) keeps it.

INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('payments.refund', 'Payments Refund', 'Refund, cancel, re-check and resend payments (money leaves the account)')
ON CONFLICT (permission_key) DO NOTHING;

INSERT INTO admin_role_permissions (role_id, permission_id)
SELECT r.id, p.id
FROM admin_roles r
CROSS JOIN admin_permissions p
WHERE r.role_key = 'super_admin'
  AND p.permission_key = 'payments.refund'
ON CONFLICT DO NOTHING;
