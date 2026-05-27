-- Add cdn_xh_path setting (XHTTP transport instead of deprecated WS).
-- Keep existing cdn_ws_path for backward compat (unused but harmless).

INSERT INTO app_settings (key, value, updated_at)
VALUES ('cdn_xh_path', '/cdn-xh', NOW())
ON CONFLICT (key) DO NOTHING;
