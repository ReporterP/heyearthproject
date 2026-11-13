-- Cleanup plan D1: the server file browser is gone; its permission leaves the catalog and every role.
DELETE FROM admin_role_permissions WHERE permission_id IN (SELECT id FROM admin_permissions WHERE permission_key = 'servers.files');
DELETE FROM admin_permissions WHERE permission_key = 'servers.files';
