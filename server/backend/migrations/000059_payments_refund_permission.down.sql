-- 000059_payments_refund_permission.down.sql
-- Reverses 000059: the role binding, then the permission row.

DELETE FROM admin_role_permissions
WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'payments.refund');

DELETE FROM admin_permissions WHERE permission_key = 'payments.refund';
