INSERT INTO app_settings (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings_bak_20261113 WHERE key LIKE 'auto\_tuner\_%'
ON CONFLICT (key) DO NOTHING;
