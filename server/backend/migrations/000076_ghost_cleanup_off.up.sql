-- Cleanup plan D2: the ghost reconciler only reports orphan Hub units by default; it no longer
-- deprovisions them on its own (a wrong lifecycle row must not take a working exit down).
CREATE TABLE IF NOT EXISTS app_settings_bak_20261113 AS SELECT key, value, updated_at FROM app_settings WHERE FALSE;
INSERT INTO app_settings_bak_20261113 (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings
 WHERE key = 'ghost_auto_cleanup' AND key NOT IN (SELECT key FROM app_settings_bak_20261113);
UPDATE app_settings SET value = 'false', updated_at = CURRENT_TIMESTAMP WHERE key = 'ghost_auto_cleanup';
