-- Дуб's Host header (the Hub's XHTTP inbound checks it) frozen at today's main_server_host, so a
-- change of the Hub's address no longer changes it (spec 2026-11-20-hub-address-change-design).
-- Same value as before: the generated xray config and every subscription stay byte-identical.
INSERT INTO app_settings (key, value, updated_at)
SELECT 'hub_xhttp_host', value, CURRENT_TIMESTAMP FROM app_settings
 WHERE key = 'main_server_host' AND btrim(value) <> ''
ON CONFLICT (key) DO NOTHING;
