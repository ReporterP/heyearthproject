-- Cleanup plan D1 (left after the auto-tuner went): its four settings, to the backup first.
CREATE TABLE IF NOT EXISTS app_settings_bak_20261113 AS SELECT key, value, updated_at FROM app_settings WHERE FALSE;
INSERT INTO app_settings_bak_20261113 (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings
 WHERE key LIKE 'auto\_tuner\_%' AND key NOT IN (SELECT key FROM app_settings_bak_20261113);
DELETE FROM app_settings WHERE key LIKE 'auto\_tuner\_%';
