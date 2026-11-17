-- Cleanup plan D7: AmneziaWG («Пальма») is gone. The per-user keys, the plan flag and the
-- AWG settings are kept in backup tables (the down migration restores them).
CREATE TABLE IF NOT EXISTS users_awg_bak_20261113 AS
SELECT id, awg_private_key, awg_public_key, awg_preshared_key, awg_cps_params, awg_ip_address
  FROM users WHERE awg_public_key IS NOT NULL OR awg_ip_address IS NOT NULL;
CREATE TABLE IF NOT EXISTS plans_awg_bak_20261113 AS SELECT id, amnezia_wg_enabled FROM plans WHERE amnezia_wg_enabled;
CREATE TABLE IF NOT EXISTS app_settings_bak_20261113 AS SELECT key, value, updated_at FROM app_settings WHERE FALSE;
INSERT INTO app_settings_bak_20261113 (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings
 WHERE key LIKE 'awg\_%' AND key NOT IN (SELECT key FROM app_settings_bak_20261113);
DELETE FROM app_settings WHERE key LIKE 'awg\_%';
DROP INDEX IF EXISTS idx_users_awg_ip;
ALTER TABLE users
  DROP COLUMN IF EXISTS awg_private_key,
  DROP COLUMN IF EXISTS awg_public_key,
  DROP COLUMN IF EXISTS awg_preshared_key,
  DROP COLUMN IF EXISTS awg_cps_params,
  DROP COLUMN IF EXISTS awg_ip_address;
ALTER TABLE plans DROP COLUMN IF EXISTS amnezia_wg_enabled;
