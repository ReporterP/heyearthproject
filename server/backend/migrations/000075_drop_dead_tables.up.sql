-- Cleanup plan D1: dead tables and columns. Each goes to a backup copy first.
--   ssh_keys (+ servers.ssh_key_id): node logins use the Hub key or the encrypted password; the
--     table kept private keys in plain text and had no row on production.
--   app_releases: never had a row; CheckAppUpdate offers no version, /user/downloads is empty.
--   user_devices: nothing reads it after this change ("was online" is the last traffic now) and
--     no row was written since April 2026.
--   servers.reputation_score / uptime_ratio: written by the tunnel monitor, read by nothing.
CREATE TABLE IF NOT EXISTS ssh_keys_bak_20261113 AS SELECT * FROM ssh_keys;
CREATE TABLE IF NOT EXISTS app_releases_bak_20261113 AS SELECT * FROM app_releases;
CREATE TABLE IF NOT EXISTS user_devices_bak_20261113 AS SELECT * FROM user_devices;
CREATE TABLE IF NOT EXISTS servers_dead_cols_bak_20261113 AS
    SELECT id, ssh_key_id, reputation_score, uptime_ratio FROM servers;

ALTER TABLE servers DROP COLUMN IF EXISTS ssh_key_id;
ALTER TABLE servers DROP COLUMN IF EXISTS reputation_score;
ALTER TABLE servers DROP COLUMN IF EXISTS uptime_ratio;
DROP TABLE IF EXISTS ssh_keys;
DROP TABLE IF EXISTS app_releases;
DROP TABLE IF EXISTS user_devices;

CREATE TABLE IF NOT EXISTS admin_role_permissions_bak_20261113 (role_id INT NOT NULL, permission_key TEXT NOT NULL);
INSERT INTO admin_role_permissions_bak_20261113 (role_id, permission_key)
    SELECT rp.role_id, p.permission_key FROM admin_role_permissions rp JOIN admin_permissions p ON p.id = rp.permission_id
    WHERE p.permission_key IN ('releases.read', 'releases.write');
DELETE FROM admin_role_permissions WHERE permission_id IN
    (SELECT id FROM admin_permissions WHERE permission_key IN ('releases.read', 'releases.write'));
DELETE FROM admin_permissions WHERE permission_key IN ('releases.read', 'releases.write');
