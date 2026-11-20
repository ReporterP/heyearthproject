-- 000098_entry_probe_samples.up.sql
-- The stability of the entries (docs/prd/entries-management.prd.md): every minute the entries
-- monitor (cmd/api/entry_monitor.go) opens several flows of each kind through an exit abroad —
-- each a new connection, its own 5-tuple on every leg — and stores how many succeeded. «Входы»
-- shows the share over 1 h and 24 h; the alert relay-<id>-unstable fires below 95 % over an hour.
-- Kept 3 days (cmd/api/retention.go). entry_id 0 is the Hub.
CREATE TABLE IF NOT EXISTS entry_probe_samples (
    entry_id INTEGER     NOT NULL CHECK (entry_id >= 0),
    at       TIMESTAMPTZ NOT NULL,
    kind     TEXT        NOT NULL CHECK (kind IN ('tcp443', 'tcp2443', 'udp')),
    ok       SMALLINT    NOT NULL CHECK (ok >= 0),
    total    SMALLINT    NOT NULL CHECK (total > 0 AND ok <= total)
);
CREATE INDEX IF NOT EXISTS idx_entry_probe_samples_entry_at ON entry_probe_samples (entry_id, at DESC);
CREATE INDEX IF NOT EXISTS idx_entry_probe_samples_at ON entry_probe_samples (at);
