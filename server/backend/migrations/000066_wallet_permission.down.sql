DELETE FROM admin_role_permissions
WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'wallet.adjust');
DELETE FROM admin_permissions WHERE permission_key = 'wallet.adjust';
