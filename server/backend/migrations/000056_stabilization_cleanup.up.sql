-- Subproject 1, Task 8 (spec 2026-10-11, part 4).
-- 0) Snapshot of every app_settings row this migration changes or deletes, so the down
--    migration restores the PREVIOUS values instead of guessed defaults. IF NOT EXISTS keeps
--    the first (original) snapshot if the file is ever run again.
CREATE TABLE IF NOT EXISTS app_settings_backup_000056 AS
  SELECT key, value FROM app_settings
   WHERE key IN ('auto_tuner_enabled', 'socks5_port', 'http_proxy_port', 'ss_port', 'ss_method',
                 'hub_socks5_port', 'hub_http_proxy_port');

-- 1) Auto-tuner off: it silently rotated Reality SNI / Hysteria2 obfs (research C3, H2).
UPDATE app_settings SET value = 'false' WHERE key = 'auto_tuner_enabled';
INSERT INTO app_settings (key, value) VALUES ('auto_tuner_enabled', 'false') ON CONFLICT (key) DO NOTHING;

-- 2) SOCKS/HTTP user inbounds are gone (000041). Snapshot for an exact down migration.
CREATE TABLE IF NOT EXISTS plans_protocols_backup_000056 AS
  SELECT id, allowed_protocols FROM plans WHERE allowed_protocols && ARRAY['socks','http']::text[];
-- A plan that loses socks/http must still end up with a usable user protocol: if neither
-- 'vless' nor 'all' remains (e.g. {socks}, {socks,http}, {socks,hysteria2}), 'vless' is added.
UPDATE plans p
   SET allowed_protocols = CASE
         WHEN c.cleaned && ARRAY['vless','all']::text[] THEN c.cleaned
         ELSE array_append(c.cleaned, 'vless') END,
       updated_at = NOW()
  FROM (SELECT id, array_remove(array_remove(allowed_protocols, 'socks'), 'http') AS cleaned
          FROM plans
         WHERE allowed_protocols && ARRAY['socks','http']::text[]) c
 WHERE p.id = c.id;

-- 3) Settings that described the removed inbounds (1080 / 8080 / 8388) and the admin-UI
--    cards for the SOCKS/HTTP ports (hub_socks5_port, hub_http_proxy_port).
DELETE FROM app_settings WHERE key IN ('socks5_port', 'http_proxy_port', 'ss_port', 'ss_method',
                                       'hub_socks5_port', 'hub_http_proxy_port');
