-- Three settings rows controlling the Cloudflare CDN fronting profile.
-- cdn_enabled is the master toggle (default false, off until E2E verified).
-- cdn_host and cdn_ws_path are configurable for future rotation without redeploy.

INSERT INTO app_settings (key, value, updated_at)
VALUES
    ('cdn_enabled',  'false',           NOW()),
    ('cdn_host',     'cdn.hiearth.ru',  NOW()),
    ('cdn_ws_path',  '/cdn-ws',         NOW())
ON CONFLICT (key) DO NOTHING;
