-- Cleanup plan D5: raw traffic_stats rows are kept 45 days (cmd/api/retention.go), per-day
-- sums of Moscow days stay in traffic_stats_daily. Every finished day is summed here once; the
-- API keeps the sums current. A BRIN index serves the time-only scans (the purge, the dashboard).
CREATE TABLE IF NOT EXISTS traffic_stats_daily (
    user_id        TEXT   NOT NULL REFERENCES users(user_id) ON DELETE CASCADE,
    day            DATE   NOT NULL,
    upload_bytes   BIGINT NOT NULL DEFAULT 0,
    download_bytes BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);
INSERT INTO traffic_stats_daily (user_id, day, upload_bytes, download_bytes)
SELECT user_id, (recorded_at AT TIME ZONE 'Europe/Moscow')::date, SUM(upload_bytes), SUM(download_bytes)
  FROM traffic_stats
 WHERE recorded_at < (date_trunc('day', NOW() AT TIME ZONE 'Europe/Moscow') AT TIME ZONE 'Europe/Moscow')
 GROUP BY 1, 2
ON CONFLICT (user_id, day) DO UPDATE SET upload_bytes = EXCLUDED.upload_bytes, download_bytes = EXCLUDED.download_bytes;
CREATE INDEX IF NOT EXISTS idx_traffic_stats_recorded_brin ON traffic_stats USING brin (recorded_at);

-- config_test_results belonged to the auto-tuner (removed in D1): a backup copy, then gone.
CREATE TABLE IF NOT EXISTS config_test_results_bak_20261113 AS SELECT * FROM config_test_results;
DROP TABLE IF EXISTS config_test_results;
