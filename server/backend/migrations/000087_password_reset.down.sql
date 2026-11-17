DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'users.password_reset');
DELETE FROM admin_permissions WHERE permission_key = 'users.password_reset';
ALTER TABLE users DROP COLUMN IF EXISTS password_reset_at;
ALTER TABLE users DROP COLUMN IF EXISTS password_must_change;
