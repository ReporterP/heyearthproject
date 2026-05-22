DELETE FROM app_settings WHERE key = 'mtproto_auto_install';
DROP INDEX IF EXISTS idx_servers_mtproto_health;
ALTER TABLE servers
  DROP COLUMN IF EXISTS mtproto_installed_at,
  DROP COLUMN IF EXISTS mtproto_last_check,
  DROP COLUMN IF EXISTS mtproto_status;
INSERT INTO app_settings (key, value) VALUES ('mtproto_host', '') ON CONFLICT (key) DO NOTHING;
