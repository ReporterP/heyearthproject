-- Cleanup plan D1: «Облако» (CDN, never switched on in production) and the donation block are
-- gone; their settings go to the backup table (the down migration restores them).
CREATE TABLE IF NOT EXISTS app_settings_bak_20261113 AS SELECT key, value, updated_at FROM app_settings WHERE FALSE;
INSERT INTO app_settings_bak_20261113 (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings
 WHERE (key LIKE 'cdn\_%' OR key LIKE 'donation\_%') AND key NOT IN (SELECT key FROM app_settings_bak_20261113);
DELETE FROM app_settings WHERE key LIKE 'cdn\_%' OR key LIKE 'donation\_%';
