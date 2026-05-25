-- 000049_express_integration.down.sql
DELETE FROM app_settings
 WHERE key IN ('hysteria2_stats_secret', 'hysteria2_tunnel_mode');

ALTER TABLE servers
  DROP COLUMN IF EXISTS last_rtt_at,
  DROP COLUMN IF EXISTS last_rtt_ms;
