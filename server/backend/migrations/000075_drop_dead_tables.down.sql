-- Emergency path: the rows come back from the backup copies; keys, sequences and indexes do not.
-- Brings the tables and columns back from the backup copies (rows included).
CREATE TABLE IF NOT EXISTS ssh_keys AS SELECT * FROM ssh_keys_bak_20261113;
CREATE TABLE IF NOT EXISTS app_releases AS SELECT * FROM app_releases_bak_20261113;
CREATE TABLE IF NOT EXISTS user_devices AS SELECT * FROM user_devices_bak_20261113;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS ssh_key_id INT;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS reputation_score DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS uptime_ratio DOUBLE PRECISION NOT NULL DEFAULT 1;
UPDATE servers s SET ssh_key_id = b.ssh_key_id, reputation_score = b.reputation_score, uptime_ratio = b.uptime_ratio
    FROM servers_dead_cols_bak_20261113 b WHERE b.id = s.id;
INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('releases.read', 'Releases Read', 'View releases'),
    ('releases.write', 'Releases Write', 'Upload and activate releases')
    ON CONFLICT (permission_key) DO NOTHING;
INSERT INTO admin_role_permissions (role_id, permission_id)
    SELECT b.role_id, p.id FROM admin_role_permissions_bak_20261113 b
    JOIN admin_permissions p ON p.permission_key = b.permission_key
    JOIN admin_roles r ON r.id = b.role_id
    WHERE b.permission_key IN ('releases.read', 'releases.write')
    ON CONFLICT DO NOTHING;
