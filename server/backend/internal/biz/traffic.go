package biz

import (
	"context"
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
// quotaExceededQuery computes the same cycle start in SQL (NULL → now - 30d,
// otherwise started_at + whole months of age(now, started_at)); keep them in
// step, the quota cutoff and the cabinet must judge the same window.
func CycleWindow(startedAt sql.NullTime, now time.Time) (cycleStart, cycleEnd time.Time) {
	if !startedAt.Valid {
		return now.AddDate(0, 0, -30), now.AddDate(0, 0, 30)
	}
	months := monthsBetween(startedAt.Time, now)
	cycleStart = addMonths(startedAt.Time, months)
	cycleEnd = addMonths(startedAt.Time, months+1)
	return
}

// monthsBetween returns how many whole calendar months elapsed from a to b,
// like the months part of PostgreSQL's age(b, a): b is taken in a's location,
// and the last month counts only once b reached a's day of month AND its time
// of day (otherwise, for some hours on the anniversary day, the cycle would
// start in the future). If b is before a, returns 0 (we never produce a
// negative cycle index).
func monthsBetween(a, b time.Time) int {
	b = b.In(a.Location())
	if b.Before(a) {
		return 0
	}
	years := b.Year() - a.Year()
	months := int(b.Month()) - int(a.Month())
	total := years*12 + months
	// If b's day-of-month (and time of day) is before a's, the last partial month doesn't count.
	if b.Day() < a.Day() || (b.Day() == a.Day() && clockOf(b) < clockOf(a)) {
		total--
	}
	if total < 0 {
		total = 0
	}
	return total
}

// clockOf is the time of day of t (in t's location).
func clockOf(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute +
		time.Duration(t.Second())*time.Second + time.Duration(t.Nanosecond())
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

// effectiveSubOrderBy returns the ORDER BY list that puts a user's effective
// subscription first among their ACTIVE subscriptions, for the table alias given
// (a trusted SQL identifier, always a literal at the call sites). The effective
// subscription is the one whose plan the user actually runs on: plan-bearing
// subscriptions first (a legacy plan-less one only when no subscription has a
// plan), then the latest valid_until, then the newest row so the choice is
// deterministic. A user may hold several active subscriptions at once (a paid
// plan plus a not-yet-expired free trial); the quota, the cabinet traffic card
// and the plan routing must all be judged against this ONE row, otherwise a paid
// unlimited plan gets cut off by the trial's 10 GB limit.
//
// Queries ordered by it (package-level vars, asserted by TestEffectiveSubQueries):
//   - activeVlessClientsQuery (best_plan_id, alias "sub") and effectivePlanIDQuery
//     (EffectivePlanID): the plan the Hub routes the user by, and the Happ
//     routing deeplink of the subscription endpoint;
//   - quotaExceededQuery, effectiveLimitQuery, trafficSubscriptionQuery: the
//     quota cutoff, IsCycleExceeded and the cabinet traffic card;
//   - hysteria2AuthPlanQuery, hysteria2PlanGateQuery: the Express (Bamboo) plan
//     gate. They join plans (plan_id is never NULL there) and keep their extra
//     filters (status = 'active', the legacy traffic_used counter), which may skip
//     the effective row for an older plan row; there the shared ordering only
//     makes the tie-break deterministic.
//
// NOT every subscription lookup uses it: GetUserPlan, GetUserSubscription, the
// upgrade/proration queries in plans.go, client_identity.go and the payment
// handlers still order by valid_until alone. They can pick a different row
// (notably a later plan-less legacy subscription), and switching them would
// change protocol, AmneziaWG, auto-renew and pricing decisions, not just a
// tie-break, so they are left as they are.
func effectiveSubOrderBy(alias string) string {
	return fmt.Sprintf("(%[1]s.plan_id IS NOT NULL) DESC, %[1]s.valid_until DESC, %[1]s.id DESC", alias)
}

// effectiveLimitQuery: limit and cycle anchor of the effective subscription
// (us.user_id = $1, users.id).
var effectiveLimitQuery = `
		SELECT COALESCE(us.traffic_limit, p.traffic_limit_bytes, 0),
		       us.started_at
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY ` + effectiveSubOrderBy("us") + `
		LIMIT 1
	`

// EffectiveLimit returns the effective per-cycle quota for the user's active
// subscription. Returns 0 if user has no active sub or the plan is unlimited.
// Priority: user_subscriptions.traffic_limit (per-row override) > plans.traffic_limit_bytes.
func EffectiveLimit(userDBID int) (limit int64, startedAt sql.NullTime, err error) {
	err = db.DB.QueryRow(effectiveLimitQuery, userDBID).Scan(&limit, &startedAt)
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

// ListQuotaExceededUserIDsContext is ListQuotaExceededUserIDs bounded by ctx.
func ListQuotaExceededUserIDsContext(ctx context.Context) (map[string]struct{}, error) {
	rows, err := db.DB.QueryContext(ctx, quotaExceededQuery)
	if err != nil {
		return nil, fmt.Errorf("quota-exceeded list: %w", err)
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("quota-exceeded scan: %w", err)
		}
		out[userID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("quota-exceeded rows: %w", err)
	}
	return out, nil
}

// quotaExceededQuery lists the users.user_id of everyone whose current-cycle
// usage reached the limit of their effective subscription (see
// ListQuotaExceededUserIDs). The cycle start is CycleWindow's: a NULL
// started_at means the last 30 days, otherwise started_at plus the whole
// months elapsed. (The former COALESCE(started_at, now - 30 days) + whole
// months of age() moved a NULL start a month forward, to about now, whenever
// age(now, now - 30 days) reached one month — after a month of 30 days or
// less — so those users counted almost no traffic.)
var quotaExceededQuery = `
		WITH active AS (
			SELECT u.user_id, eff.started_at,
			       COALESCE(eff.traffic_limit, p.traffic_limit_bytes, 0) AS lim
			FROM users u
			JOIN LATERAL (
				SELECT us.plan_id, us.started_at, us.traffic_limit
				FROM user_subscriptions us
				WHERE us.user_id = u.id AND us.valid_until > NOW()
				ORDER BY ` + effectiveSubOrderBy("us") + `
				LIMIT 1
			) eff ON TRUE
			LEFT JOIN plans p ON p.id = eff.plan_id
			WHERE COALESCE(eff.traffic_limit, p.traffic_limit_bytes, 0) > 0
		),
		cycles AS (
			SELECT user_id, lim,
			       CASE
			         WHEN started_at IS NULL THEN NOW() - INTERVAL '30 days'
			         ELSE GREATEST(
			                started_at,
			                started_at + (
			                  EXTRACT(YEAR  FROM age(NOW(), started_at)) * 12 +
			                  EXTRACT(MONTH FROM age(NOW(), started_at))
			                )::int * INTERVAL '1 month'
			              )
			       END AS cycle_start
			FROM active
		)
		SELECT c.user_id
		FROM cycles c
		WHERE (
			SELECT COALESCE(SUM(ts.upload_bytes + ts.download_bytes), 0)
			FROM traffic_stats ts
			WHERE ts.user_id = c.user_id AND ts.recorded_at >= c.cycle_start
		) >= c.lim
	`

// TrafficResponse is the unified JSON shape returned by /api/v1/user/traffic
// and /api/admin/users/{id}/traffic.
type TrafficResponse struct {
	Subscription *TrafficSubscriptionInfo `json:"subscription,omitempty"`
	Cycle        *TrafficCycleInfo        `json:"cycle,omitempty"`
	Periods      TrafficPeriods           `json:"periods"`
}

type TrafficSubscriptionInfo struct {
	StartedAt   string `json:"started_at,omitempty"`  // RFC3339
	ValidUntil  string `json:"valid_until,omitempty"` // RFC3339
	PlanName    string `json:"plan_name"`
	IsUnlimited bool   `json:"is_unlimited"`
}

type TrafficCycleInfo struct {
	StartedAt  string  `json:"started_at"` // RFC3339, cycle start
	EndsAt     string  `json:"ends_at"`    // RFC3339, cycle end
	UsedBytes  int64   `json:"used_bytes"`
	LimitBytes int64   `json:"limit_bytes"` // 0 = unlimited
	Percent    float64 `json:"percent"`
	Exceeded   bool    `json:"exceeded"`
}

type TrafficPeriods struct {
	Day   TrafficPeriodDay   `json:"day"`
	Week  TrafficPeriodDay   `json:"week"`
	Month TrafficPeriodDay   `json:"month"`
	Year  TrafficPeriodMonth `json:"year"`
}

type TrafficPeriodDay struct {
	UsedBytes int64       `json:"used_bytes"`
	ByDay     []DayBucket `json:"by_day"`
}

type TrafficPeriodMonth struct {
	UsedBytes int64         `json:"used_bytes"`
	ByMonth   []MonthBucket `json:"by_month"`
}

// trafficSubscriptionQuery: the effective subscription and its plan for the
// cabinet traffic card (us.user_id = $1, users.id).
var trafficSubscriptionQuery = `
		SELECT us.started_at, us.valid_until,
		       COALESCE(p.name, 'standard'),
		       p.traffic_limit_bytes,
		       us.traffic_limit
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY ` + effectiveSubOrderBy("us") + `
		LIMIT 1
	`

// BuildTrafficResponse assembles the unified traffic payload for a user.
// Returns zero-valued Periods if the user has no traffic_stats rows.
// Used by both /api/v1/user/traffic and /api/admin/users/{id}/traffic.
func BuildTrafficResponse(userDBID int, userUUID string) (TrafficResponse, error) {
	now := time.Now()

	// 1. Active subscription + plan info
	var sub TrafficSubscriptionInfo
	var startedAt sql.NullTime
	var validUntil sql.NullTime
	var planName sql.NullString
	var planLimit sql.NullInt64
	var subLimit sql.NullInt64
	err := db.DB.QueryRow(trafficSubscriptionQuery, userDBID).Scan(&startedAt, &validUntil, &planName, &planLimit, &subLimit)

	var hasSub bool
	if err == nil {
		hasSub = true
		sub.PlanName = planName.String
		if startedAt.Valid {
			sub.StartedAt = startedAt.Time.Format(time.RFC3339)
		}
		if validUntil.Valid {
			sub.ValidUntil = validUntil.Time.Format(time.RFC3339)
		}
	} else if err != sql.ErrNoRows {
		return TrafficResponse{}, fmt.Errorf("load subscription: %w", err)
	}

	// 2. Effective limit (sub override > plan limit > 0/unlimited)
	var effLimit int64
	if subLimit.Valid && subLimit.Int64 > 0 {
		effLimit = subLimit.Int64
	} else if planLimit.Valid && planLimit.Int64 > 0 {
		effLimit = planLimit.Int64
	}
	sub.IsUnlimited = effLimit == 0

	// 3. Cycle window
	cs, ce := CycleWindow(startedAt, now)
	cycleUsed, _ := PeriodUsage(userUUID, cs, ce)
	var pct float64
	if effLimit > 0 {
		pct = float64(cycleUsed) / float64(effLimit) * 100.0
		if pct > 100 {
			pct = 100
		}
	}
	cycle := TrafficCycleInfo{
		StartedAt:  cs.Format(time.RFC3339),
		EndsAt:     ce.Format(time.RFC3339),
		UsedBytes:  cycleUsed,
		LimitBytes: effLimit,
		Percent:    pct,
		Exceeded:   effLimit > 0 && cycleUsed >= effLimit,
	}

	// 4. Display periods
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := now.AddDate(0, 0, -7)
	yearStart := now.AddDate(0, 0, -365)

	periods := TrafficPeriods{}

	dayUsed, _ := PeriodUsage(userUUID, dayStart, now)
	dayBuckets, _ := PeriodBreakdownByDay(userUUID, dayStart, now)
	periods.Day = TrafficPeriodDay{UsedBytes: dayUsed, ByDay: dayBuckets}

	weekUsed, _ := PeriodUsage(userUUID, weekStart, now)
	weekBuckets, _ := PeriodBreakdownByDay(userUUID, weekStart, now)
	periods.Week = TrafficPeriodDay{UsedBytes: weekUsed, ByDay: weekBuckets}

	monthUsed, _ := PeriodUsage(userUUID, cs, ce)
	monthBuckets, _ := PeriodBreakdownByDay(userUUID, cs, ce)
	periods.Month = TrafficPeriodDay{UsedBytes: monthUsed, ByDay: monthBuckets}

	yearUsed, _ := PeriodUsage(userUUID, yearStart, now)
	yearBuckets, _ := PeriodBreakdownByMonth(userUUID, yearStart, now)
	periods.Year = TrafficPeriodMonth{UsedBytes: yearUsed, ByMonth: yearBuckets}

	resp := TrafficResponse{Periods: periods, Cycle: &cycle}
	if hasSub {
		resp.Subscription = &sub
	}
	return resp, nil
}
