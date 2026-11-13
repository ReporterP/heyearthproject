INSERT INTO admin_permissions (permission_key, name, description) VALUES ('servers.files', 'Server Files', 'Browse server files') ON CONFLICT (permission_key) DO NOTHING;
