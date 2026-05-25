package biz

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// DayBucket aggregates one calendar day of traffic.
type DayBucket struct {
	Day   string `json:"day"` // YYYY-MM-DD (UTC)
	Bytes int64  `json:"bytes"`
}

// MonthBucket aggregates one calendar month of traffic.
type MonthBucket struct {
	Month string `json:"month"` // YYYY-MM (UTC)
	Bytes int64  `json:"bytes"`
}

// CycleWindow returns the [start, end) of the current billing cycle for a
// subscription that started at startedAt. A "cycle" is one calendar month
// from started_at, advancing monthly. If startedAt is NULL, falls back to
// (now - 30d, now + 30d) so legacy data still produces a sensible window.
func CycleWindow(startedAt sql.NullTime, now time.Time) (cycleStart, cycleEnd time.Time) {
	if !startedAt.Valid {
		return now.AddDate(0, 0, -30), now.AddDate(0, 0, 30)
	}
	months := monthsBetween(startedAt.Time, now)
	cycleStart = addMonths(startedAt.Time, months)
	cycleEnd = addMonths(startedAt.Time, months+1)
	return
}

// monthsBetween returns how many whole calendar months elapsed from a to b.
// If b is before a, returns 0 (we never produce a negative cycle index).
func monthsBetween(a, b time.Time) int {
	if b.Before(a) {
		return 0
	}
	years := b.Year() - a.Year()
	months := int(b.Month()) - int(a.Month())
	total := years*12 + months
	// If b's day-of-month is before a's, the last partial month doesn't count.
	if b.Day() < a.Day() {
		total--
	}
	if total < 0 {
		total = 0
	}
	return total
}

// addMonths returns t + n calendar months using PostgreSQL-style clamping:
// 2026-01-31 + 1 month = 2026-02-28 (clamps to last day of target month).
// Go's stdlib time.AddDate(0,1,0) would overflow into March, which is NOT
// what we want for billing cycle math.
func addMonths(t time.Time, n int) time.Time {
	targetYear, targetMonth := t.Year(), int(t.Month())+n
	for targetMonth > 12 {
		targetMonth -= 12
		targetYear++
	}
	for targetMonth < 1 {
		targetMonth += 12
		targetYear--
	}
	// Last day of target month
	firstOfNext := time.Date(targetYear, time.Month(targetMonth)+1, 1, 0, 0, 0, 0, t.Location())
	lastDay := firstOfNext.AddDate(0, 0, -1).Day()
	day := t.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(targetYear, time.Month(targetMonth), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// PeriodUsage returns SUM(upload+download) for userUUID in [from, to).
func PeriodUsage(userUUID string, from, to time.Time) (int64, error) {
	var bytes sql.NullInt64
	err := db.DB.QueryRow(`
		SELECT COALESCE(SUM(upload_bytes + download_bytes), 0)
		FROM traffic_stats
		WHERE user_id = $1
		  AND recorded_at >= $2
		  AND recorded_at <  $3
	`, userUUID, from, to).Scan(&bytes)
	if err != nil {
		return 0, fmt.Errorf("period usage: %w", err)
	}
	return bytes.Int64, nil
}

// PeriodBreakdownByDay returns per-day buckets in [from, to). Days with no
// traffic are omitted (frontend zero-fills if it needs a continuous chart).
func PeriodBreakdownByDay(userUUID string, from, to time.Time) ([]DayBucket, error) {
	rows, err := db.DB.Query(`
		SELECT to_char(date_trunc('day', recorded_at), 'YYYY-MM-DD') AS day,
		       SUM(upload_bytes + download_bytes) AS bytes
		FROM traffic_stats
		WHERE user_id = $1
		  AND recorded_at >= $2
		  AND recorded_at <  $3
		GROUP BY day
		ORDER BY day
	`, userUUID, from, to)
	if err != nil {
		return nil, fmt.Errorf("by-day: %w", err)
	}
	defer rows.Close()
	var out []DayBucket
	for rows.Next() {
		var b DayBucket
		if err := rows.Scan(&b.Day, &b.Bytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []DayBucket{}
	}
	return out, nil
}

// PeriodBreakdownByMonth returns per-month buckets in [from, to). Used by the
// year period (365 daily bars are too many for a 600px chart).
func PeriodBreakdownByMonth(userUUID string, from, to time.Time) ([]MonthBucket, error) {
	rows, err := db.DB.Query(`
		SELECT to_char(date_trunc('month', recorded_at), 'YYYY-MM') AS month,
		       SUM(upload_bytes + download_bytes) AS bytes
		FROM traffic_stats
		WHERE user_id = $1
		  AND recorded_at >= $2
		  AND recorded_at <  $3
		GROUP BY month
		ORDER BY month
	`, userUUID, from, to)
	if err != nil {
		return nil, fmt.Errorf("by-month: %w", err)
	}
	defer rows.Close()
	var out []MonthBucket
	for rows.Next() {
		var b MonthBucket
		if err := rows.Scan(&b.Month, &b.Bytes); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []MonthBucket{}
	}
	return out, nil
}

// EffectiveLimit returns the effective per-cycle quota for the user's active
// subscription. Returns 0 if user has no active sub or the plan is unlimited.
// Priority: user_subscriptions.traffic_limit (per-row override) > plans.traffic_limit_bytes.
func EffectiveLimit(userDBID int) (limit int64, startedAt sql.NullTime, err error) {
	err = db.DB.QueryRow(`
		SELECT COALESCE(us.traffic_limit, p.traffic_limit_bytes, 0),
		       us.started_at
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC
		LIMIT 1
	`, userDBID).Scan(&limit, &startedAt)
	if err == sql.ErrNoRows {
		return 0, sql.NullTime{}, nil
	}
	return
}

// IsCycleExceeded returns true if the user's current-cycle usage >= effective
// limit AND limit > 0. Returns false for unlimited plans, no-active-sub
// users, and users below their quota.
func IsCycleExceeded(userDBID int, userUUID string) (bool, error) {
	limit, startedAt, err := EffectiveLimit(userDBID)
	if err != nil || limit <= 0 {
		return false, err
	}
	cs, ce := CycleWindow(startedAt, time.Now())
	used, err := PeriodUsage(userUUID, cs, ce)
	if err != nil {
		return false, err
	}
	return used >= limit, nil
}

// ListQuotaExceededUUIDs returns the set of user UUIDs that have an active
// subscription AND a positive cycle limit AND cycle usage >= that limit.
// Computed in a single SQL query for performance — called by xray_sync on
// every sync tick and by checkTrafficLimits after each collector iteration.
func ListQuotaExceededUUIDs() (map[string]struct{}, error) {
	rows, err := db.DB.Query(`
		WITH active AS (
			SELECT u.id AS uid, u.user_id AS uuid, us.started_at,
			       COALESCE(us.traffic_limit, p.traffic_limit_bytes, 0) AS lim
			FROM users u
			JOIN user_subscriptions us ON us.user_id = u.id AND us.valid_until > NOW()
			LEFT JOIN plans p ON p.id = us.plan_id
			WHERE COALESCE(us.traffic_limit, p.traffic_limit_bytes, 0) > 0
		),
		cycles AS (
			SELECT uid, uuid, lim,
			       GREATEST(
			           COALESCE(started_at, NOW() - INTERVAL '30 days'),
			           COALESCE(started_at, NOW() - INTERVAL '30 days') + (
			             EXTRACT(YEAR  FROM age(NOW(), COALESCE(started_at, NOW() - INTERVAL '30 days'))) * 12 +
			             EXTRACT(MONTH FROM age(NOW(), COALESCE(started_at, NOW() - INTERVAL '30 days')))
			           )::int * INTERVAL '1 month'
			       ) AS cycle_start
			FROM active
		)
		SELECT uuid
		FROM cycles c
		WHERE (
			SELECT COALESCE(SUM(upload_bytes + download_bytes), 0)
			FROM traffic_stats
			WHERE user_id = c.uuid AND recorded_at >= c.cycle_start
		) >= c.lim
	`)
	if err != nil {
		return nil, fmt.Errorf("quota-exceeded list: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			continue
		}
		out[uuid] = struct{}{}
	}
	return out, nil
}
