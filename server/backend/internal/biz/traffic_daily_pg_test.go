package biz

import (
	"context"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

// D5: the purge removes raw rows older than 45 days only after their days are summed, and no
// total a reader shows changes because of it.
func TestTrafficRetentionKeepsTotals(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	testpg.NewUser(t, conn, "ret-user")
	testpg.NewUser(t, conn, "ret-other")
	add := func(user, ago string, bytes int64) {
		testpg.MustExec(t, conn, `INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes, recorded_at) VALUES ($1, $2, $2, NOW() - $3::interval)`,
			user, bytes/2, ago)
	}
	add("ret-user", "100 days", 1000)
	add("ret-user", "50 days", 2000)
	add("ret-user", "10 days", 3000)
	add("ret-user", "1 minute", 4000)
	add("ret-other", "60 days", 500)
	count := func() (n int) {
		if err := conn.QueryRow(`SELECT COUNT(*) FROM traffic_stats`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return
	}
	now := time.Now()
	year := func() int64 {
		v, err := PeriodUsage("ret-user", now.AddDate(0, 0, -365), now)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	months := func() (sum int64) {
		b, err := PeriodBreakdownByMonth("ret-user", now.AddDate(0, 0, -365), now)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range b {
			sum += m.Bytes
		}
		return
	}

	// The migration summed the (then empty) table; nothing is summed yet, so nothing is purged.
	testpg.MustExec(t, conn, `DELETE FROM traffic_stats_daily`)
	if n, err := PurgeOldTraffic(ctx); err != nil || n != 0 || count() != 5 {
		t.Fatalf("purge before any sum: %d %v, rows %d", n, err, count())
	}

	if _, err := RollupTrafficDaily(ctx); err != nil {
		t.Fatal(err)
	}
	var days int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM traffic_stats_daily`).Scan(&days); err != nil || days != 4 {
		t.Fatalf("summed days: %d %v (today is not summed)", days, err)
	}
	if got := year(); got != 10000 {
		t.Fatalf("year before the purge: %d", got)
	}
	// A second rollup rewrites the same sums.
	if _, err := RollupTrafficDaily(ctx); err != nil {
		t.Fatal(err)
	}
	if got := year(); got != 10000 {
		t.Fatalf("year after a second rollup: %d", got)
	}

	n, err := PurgeOldTraffic(ctx)
	if err != nil || n != 3 || count() != 2 {
		t.Fatalf("purge: %d %v, rows left %d (want 3 removed: 100, 60 and 50 days old)", n, err, count())
	}
	if got := year(); got != 10000 {
		t.Fatalf("year after the purge: %d", got)
	}
	if got := months(); got != 10000 {
		t.Fatalf("months after the purge: %d", got)
	}
	if got, err := PeriodUsage("ret-user", now.AddDate(0, 0, -30), now); err != nil || got != 7000 {
		t.Fatalf("30 days: %d %v", got, err)
	}
	if got, err := PeriodUsage("ret-other", now.AddDate(0, 0, -365), now); err != nil || got != 500 {
		t.Fatalf("other user's year: %d %v", got, err)
	}
	if n, err := PurgeOldTraffic(ctx); err != nil || n != 0 {
		t.Fatalf("second purge: %d %v", n, err)
	}
}
