-- Cleanup plan D1: the server file browser is gone; its permission leaves the catalog and every role.
CREATE TABLE IF NOT EXISTS admin_role_permissions_bak_20261113 (role_id INT NOT NULL, permission_key TEXT NOT NULL);
INSERT INTO admin_role_permissions_bak_20261113 (role_id, permission_key)
    SELECT rp.role_id, p.permission_key FROM admin_role_permissions rp JOIN admin_permissions p ON p.id = rp.permission_id
    WHERE p.permission_key IN ('servers.files');
DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'servers.files');
DELETE FROM admin_permissions WHERE permission_key = 'servers.files';
