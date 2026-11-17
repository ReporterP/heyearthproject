-- Cleanup plan C9: settings no code reads any more. The rows are kept in a backup table
-- (restored by the down migration).
CREATE TABLE IF NOT EXISTS app_settings_bak_20261113 AS SELECT key, value, updated_at FROM app_settings WHERE FALSE;
INSERT INTO app_settings_bak_20261113 (key, value, updated_at)
SELECT key, value, updated_at FROM app_settings
 WHERE (key LIKE 'b2b\_price\_%' OR key IN ('b2b_base_own_servers')
        OR key LIKE 'app\_build\_%'
        OR key IN ('hub_vless_port', 'mtproto_host', 'price_premium', 'price_standard', 'yukassa_test_mode'))
   AND key NOT IN (SELECT key FROM app_settings_bak_20261113);
DELETE FROM app_settings
 WHERE key LIKE 'b2b\_price\_%' OR key IN ('b2b_base_own_servers')
    OR key LIKE 'app\_build\_%'
    OR key IN ('hub_vless_port', 'mtproto_host', 'price_premium', 'price_standard', 'yukassa_test_mode');
-- The B2B section is hidden unless switched on (C10).
INSERT INTO app_settings (key, value) VALUES ('b2b_enabled', 'false') ON CONFLICT (key) DO NOTHING;
