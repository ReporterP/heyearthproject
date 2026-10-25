-- 000062_entries_failover.down.sql
-- Reverses 000062: the relay/exit constraint, the entry state table, the entry settings.
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_relay_not_exit;
DROP TABLE IF EXISTS entry_state;
DELETE FROM app_settings WHERE key IN ('entry_failover_enabled', 'entry_failover_active', 'entry_failover_target',
    'entry_failover_since', 'entry_failover_test_user', 'entry_probe_simulate_hub_fail_until',
    'subscription_update_interval_hours');
