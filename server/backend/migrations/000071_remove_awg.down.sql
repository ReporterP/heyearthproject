ALTER TABLE plans ADD COLUMN IF NOT EXISTS amnezia_wg_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS awg_private_key   TEXT,
  ADD COLUMN IF NOT EXISTS awg_public_key    TEXT,
  ADD COLUMN IF NOT EXISTS awg_preshared_key TEXT,
  ADD COLUMN IF NOT EXISTS awg_cps_params    JSONB,
  ADD COLUMN IF NOT EXISTS awg_ip_address    VARCHAR(16);
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_awg_ip ON users(awg_ip_address) WHERE awg_ip_address IS NOT NULL;
UPDATE users u SET awg_private_key = b.awg_private_key, awg_public_key = b.awg_public_key,
       awg_preshared_key = b.awg_preshared_key, awg_cps_params = b.awg_cps_params, awg_ip_address = b.awg_ip_address
  FROM users_awg_bak_20261113 b WHERE b.id = u.id;
UPDATE plans p SET amnezia_wg_enabled = TRUE FROM plans_awg_bak_20261113 b WHERE b.id = p.id;
INSERT INTO app_settings (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings_bak_20261113 WHERE key LIKE 'awg\_%'
ON CONFLICT (key) DO NOTHING;
