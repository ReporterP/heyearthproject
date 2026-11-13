-- The raw rows already purged do not come back; the sums are dropped with their table.
DROP INDEX IF EXISTS idx_traffic_stats_recorded_brin;
DROP TABLE IF EXISTS traffic_stats_daily;
CREATE TABLE IF NOT EXISTS config_test_results AS SELECT * FROM config_test_results_bak_20261113;
