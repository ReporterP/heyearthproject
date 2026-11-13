INSERT INTO admin_permissions (permission_key, name, description) VALUES ('servers.files', 'Server Files', 'Browse server files') ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
    SELECT b.role_id, p.id FROM admin_role_permissions_bak_20261113 b
    JOIN admin_permissions p ON p.permission_key = b.permission_key
    JOIN admin_roles r ON r.id = b.role_id
    WHERE b.permission_key IN ('servers.files')
    ON CONFLICT DO NOTHING;
