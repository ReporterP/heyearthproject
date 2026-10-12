-- The quota query (biz.quotaExceededQuery: per limited user, SUM(upload_bytes +
-- download_bytes) of its traffic_stats rows since the cycle start) runs every
-- minute while the cutoff is armed and on every xray sync. With only
-- idx_traffic_stats_user_time it is a bitmap heap scan over the whole cycle
-- (prod 2026-10: 3.7M rows, 869 MB; one user's 30-day SUM took 1.1 s). The
-- covering index turns it into an index-only scan.
--
-- Deliberately not CONCURRENTLY: a failed concurrent build leaves an INVALID
-- index behind that IF NOT EXISTS would accept on the next start, while a plain
-- build either succeeds or rolls back. It blocks writes to traffic_stats (the
-- one-minute collector) while it runs, fine on small databases. On production
-- the operator builds the same index beforehand with
--   CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_traffic_stats_user_time_cov
--     ON traffic_stats (user_id, recorded_at) INCLUDE (upload_bytes, download_bytes);
-- so this statement is a no-op there. Check that the concurrent build left a
-- VALID index (pg_index.indisvalid) before deploying: an INVALID one also
-- satisfies IF NOT EXISTS and must be dropped and rebuilt.
CREATE INDEX IF NOT EXISTS idx_traffic_stats_user_time_cov
    ON traffic_stats (user_id, recorded_at) INCLUDE (upload_bytes, download_bytes);
