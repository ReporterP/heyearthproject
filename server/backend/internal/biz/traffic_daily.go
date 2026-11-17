package biz

import (
	"context"
	"fmt"

	"github.com/heyearth/backend/internal/db"
)

// Cleanup plan D5: raw traffic_stats rows live 45 days; per-day sums (Moscow days) in
// traffic_stats_daily stay. Readers of long periods take whole days before rawBoundarySQL from
// the sums and everything after it from the raw rows, so the purge never changes a total. The
// purge keeps one day of slack past the boundary and deletes only days already summed.

// rawBoundarySQL: Moscow midnight 44 days ago (timestamptz).
const rawBoundarySQL = `((date_trunc('day', NOW() AT TIME ZONE 'Europe/Moscow') - INTERVAL '44 days') AT TIME ZONE 'Europe/Moscow')`

// rollupTrafficDailyQuery upserts the sums of every finished Moscow day from two days before the
// last summed one (a late row, a missed run) up to yesterday; an empty table sums everything.
const rollupTrafficDailyQuery = `
WITH b AS (
  SELECT COALESCE((SELECT MAX(day) FROM traffic_stats_daily) - 2, DATE '1970-01-01') AS from_day,
         (NOW() AT TIME ZONE 'Europe/Moscow')::date AS today
)
INSERT INTO traffic_stats_daily (user_id, day, upload_bytes, download_bytes)
SELECT ts.user_id, (ts.recorded_at AT TIME ZONE 'Europe/Moscow')::date, SUM(ts.upload_bytes), SUM(ts.download_bytes)
  FROM traffic_stats ts, b
 WHERE ts.recorded_at >= (b.from_day::timestamp AT TIME ZONE 'Europe/Moscow')
   AND ts.recorded_at < (b.today::timestamp AT TIME ZONE 'Europe/Moscow')
 GROUP BY 1, 2
ON CONFLICT (user_id, day) DO UPDATE
   SET upload_bytes = EXCLUDED.upload_bytes, download_bytes = EXCLUDED.download_bytes`

// purgeOldTrafficQuery deletes one batch of raw rows older than 45 Moscow days whose day is
// already summed (nothing while the sums are empty).
const purgeOldTrafficQuery = `
DELETE FROM traffic_stats WHERE id IN (
  SELECT id FROM traffic_stats
   WHERE recorded_at < ((date_trunc('day', NOW() AT TIME ZONE 'Europe/Moscow') - INTERVAL '45 days') AT TIME ZONE 'Europe/Moscow')
     AND recorded_at < ((((SELECT MAX(day) FROM traffic_stats_daily) + 1)::timestamp) AT TIME ZONE 'Europe/Moscow')
   LIMIT $1)`

const purgeTrafficBatch = 50000

// RollupTrafficDaily writes the per-day sums of the finished days; it returns the rows written.
func RollupTrafficDaily(ctx context.Context) (int64, error) {
	res, err := db.DB.ExecContext(ctx, rollupTrafficDailyQuery)
	if err != nil {
		return 0, fmt.Errorf("traffic daily rollup: %w", err)
	}
	return res.RowsAffected()
}

// PurgeOldTraffic deletes raw rows past the retention in batches; it returns how many went.
func PurgeOldTraffic(ctx context.Context) (int64, error) {
	var total int64
	for {
		res, err := db.DB.ExecContext(ctx, purgeOldTrafficQuery, purgeTrafficBatch)
		if err != nil {
			return total, fmt.Errorf("traffic purge: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < purgeTrafficBatch || ctx.Err() != nil {
			return total, ctx.Err()
		}
	}
}
