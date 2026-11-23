-- 000096_relay_tunnel_flag.up.sql
-- The GRE tunnel of a reserve entry (internal/provision relay_tunnel.go) is the owner's choice per
-- entry, off by default: without it the relay recipe forwards to the Hub's public address and takes
-- down a tunnel an earlier install left. Entry 6 has been on the tunnel since 2026-11-20
-- (docs/incidents/2026-11-20.md, deploy --relay-tunnel 6): it keeps it.
ALTER TABLE servers ADD COLUMN IF NOT EXISTS relay_tunnel BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE servers SET relay_tunnel = TRUE WHERE id = 6 AND role = 'relay';
