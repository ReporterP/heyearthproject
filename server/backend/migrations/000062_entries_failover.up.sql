-- 000062_entries_failover.up.sql
-- Subproject 2 «Пул нод», phase 3 «Резервный вход в РФ» (spec 2026-10-12, section 4).
-- Applied as one Exec; every statement is idempotent.
-- entry_state: one row per entry. 0 = the Hub (no servers row), otherwise servers.id of a relay
-- (role 'relay'). Health columns are written only by cmd/api/entry_monitor.go, address_mode and
-- domain by the admin («Входы»). No FK: row 0 has no servers row; a relay's row is removed by its
-- deprovision (biz.DeleteEntryState).
CREATE TABLE IF NOT EXISTS entry_state (
    entry_id       INTEGER     PRIMARY KEY CHECK (entry_id >= 0),
    address_mode   TEXT        NOT NULL DEFAULT 'ip' CHECK (address_mode IN ('ip', 'domain')),
    domain         TEXT        NOT NULL DEFAULT '',
    ext_status     TEXT        NOT NULL DEFAULT 'unknown' CHECK (ext_status IN ('unknown', 'healthy', 'unhealthy')),
    ext_checked_at TIMESTAMPTZ,
    ext_error      TEXT        NOT NULL DEFAULT '',
    udp_status     TEXT        NOT NULL DEFAULT 'unknown' CHECK (udp_status IN ('unknown', 'healthy', 'unhealthy')),
    udp_checked_at TIMESTAMPTZ,
    udp_error      TEXT        NOT NULL DEFAULT '',
    ru_status      TEXT        NOT NULL DEFAULT 'unknown' CHECK (ru_status IN ('unknown', 'reachable', 'unreachable')),
    ru_checked_at  TIMESTAMPTZ,
    ru_link        TEXT        NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO entry_state (entry_id) VALUES (0) ON CONFLICT (entry_id) DO NOTHING;

-- A relay is never an exit: every exit query (xray, Bamboo, MTProto, ghost reconciler) filters
-- is_exit_node = TRUE, so this constraint keeps relays out of exit routing at the DB level.
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_relay_not_exit;
ALTER TABLE servers ADD CONSTRAINT servers_relay_not_exit CHECK (role <> 'relay' OR is_exit_node IS NOT TRUE);

INSERT INTO app_settings (key, value) VALUES
    ('entry_failover_enabled', 'true'),
    ('entry_failover_active', 'false'),
    ('entry_failover_target', '0'),
    ('entry_failover_since', ''),
    ('entry_failover_test_user', ''),
    ('entry_probe_simulate_hub_fail_until', ''),
    ('subscription_update_interval_hours', '4')
ON CONFLICT (key) DO NOTHING;
