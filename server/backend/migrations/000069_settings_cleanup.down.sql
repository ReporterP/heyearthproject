INSERT INTO app_settings (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings_bak_20261113
ON CONFLICT (key) DO NOTHING;
DELETE FROM app_settings WHERE key = 'b2b_enabled';
