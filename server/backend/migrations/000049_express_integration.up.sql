-- 000049_express_integration.up.sql
-- Express integration with Xray architecture: RTT tracking for adaptive
-- tunnel exit selection + Hysteria2 stats-API secret + tunnel mode state.
--
-- Predecessor: 000048_express_hysteria2 (introduced Express plan toggles).
-- See docs/superpowers/specs/2026-05-26-express-integration-design.md A1/A2.

-- Seed uses gen_random_uuid() — built-in in PG 13+, no extension needed.
-- Two UUIDs concatenated and stripped of dashes give 64 hex chars (32 bytes)
-- — enough entropy for a Bearer secret. Avoids pgcrypto dependency since
-- the heyearth DB user lacks CREATE EXTENSION privilege on managed Postgres.

-- RTT tracking for exit selection. tunnel-monitor (cmd/api/xray_sync.go::
-- checkTunnelHealth) measures TCP+TLS handshake latency every 30s and now
-- persists the result here so biz.SelectExpressExit() can pick best-RTT.
ALTER TABLE servers
  ADD COLUMN IF NOT EXISTS last_rtt_ms INTEGER     DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS last_rtt_at TIMESTAMPTZ DEFAULT NULL;

-- Hysteria2 stats API secret — used as Bearer for GET /traffic on the
-- in-process trafficStats listener (127.0.0.1:7653). Generated once on
-- migration apply via pgcrypto's gen_random_bytes; never rotated except by
-- manual UPDATE. Length: 64 hex chars (32 bytes).
--
-- hysteria2_tunnel_mode — "shared" (single exit, reuse hysteria2-client.service
-- SOCKS5 :1090) or "dedicated" (2+ exits, separate client targeting best-RTT
-- exit on SOCKS5 :1091). Defaults to "shared" — matches current single-exit
-- production state.
INSERT INTO app_settings (key, value)
VALUES
  ('hysteria2_stats_secret',
   replace(gen_random_uuid()::text, '-', '') ||
   replace(gen_random_uuid()::text, '-', '')),
  ('hysteria2_tunnel_mode', 'shared')
ON CONFLICT (key) DO NOTHING;
