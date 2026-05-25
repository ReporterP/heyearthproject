# Traffic Limits & Visibility — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace subscription-lifetime traffic counting with billing-cycle counting (cycle = 1 calendar month from `user_subscriptions.started_at`). Show per-period dynamics (day / week / month / year) in admin user drawer and site dashboard, default month. Cutoff VPN traffic when current-cycle limit is hit — user is excluded from Xray inbound clients until next cycle. happ Subscription-Userinfo header switches to cycle data and uses `total=0` for unlimited plans so happ displays "X / ∞".

**Architecture:**
- A new `internal/biz/traffic.go` encapsulates all cycle math and aggregation helpers (CycleWindow, PeriodUsage, breakdowns, IsCycleExceeded, ListQuotaExceededUUIDs).
- `/api/v1/user/traffic` and `/api/admin/users/{id}/traffic` return the same `{subscription, cycle, periods}` JSON shape with 4 period buckets.
- `buildSubscriptionUserinfo` in `subscription_handler.go` switches from `us.traffic_used` (subscription-lifetime) to `cycle_used`, with `total=0` for unlimited.
- `xray_sync.go` filters quota-exceeded UUIDs out of every inbound's `clients` array — dynamic check, no persistent flag, automatic re-include when cycle resets.
- Frontend: site `DashboardPage.jsx` and admin `TrafficPaymentsTab.jsx` get a period switcher + bar chart consuming the new response shape.

**Tech Stack:** Go 1.21 backend (raw `database/sql` + lib/pq), PostgreSQL 15 (no schema migration — existing `traffic_stats` table is sufficient), React 19 + Vite + Tailwind v4 (admin_web + site, JSX no TypeScript), lucide-react icons.

**Epic:** `heyearthproject-1hwm`
**Sub-tasks:**
- T1 — `heyearthproject-ue5l` (biz/traffic.go helpers)
- T2 — `heyearthproject-hu5g` (handleUserTraffic + admin endpoint)
- T3 — `heyearthproject-urn7` (buildSubscriptionUserinfo cycle data)
- T4 — `heyearthproject-vilj` (xray_sync.go quota cutoff)
- T5 — `heyearthproject-1evg` (site DashboardPage period switcher)
- T6 — `heyearthproject-nedr` (admin TrafficPaymentsTab period switcher)
- T7 — `heyearthproject-2tv6` (deploy + smoke)

**Spec:** [docs/superpowers/specs/2026-05-25-traffic-limits-design.md](../specs/2026-05-25-traffic-limits-design.md)

**Dependency graph:**
```
T1 (biz helpers)
 ├─ blocks T2 (endpoints)
 ├─ blocks T3 (happ header)
 └─ blocks T4 (xray sync cutoff)

T2 ─ blocks T5 (site dashboard)
T2 ─ blocks T6 (admin drawer)

All of T3, T4, T5, T6 block T7 (deploy)

Parallelization:
  Wave 1: T1
  Wave 2: T2 || T3 || T4   (all depend only on T1)
  Wave 3: T5 || T6         (depend on T2)
  Wave 4: T7
```

---

## File Structure

### Backend — Create

- `server/backend/internal/biz/traffic.go` — new file. All cycle math + aggregation queries. Pure functions taking `db.DB` as a singleton, returning structs/errors. ~180 lines.

### Backend — Modify

- `server/backend/cmd/api/main.go` — replace `handleUserTraffic` (line ~795) body with new shape via `biz/traffic.go` helpers.
- `server/backend/internal/admin/users_handlers.go` — replace `ListUserTrafficHandler` (line ~320) with new shape, same helpers.
- `server/backend/cmd/api/subscription_handler.go` — rewrite `buildSubscriptionUserinfo` (line ~279) to use cycle data; `total=0` when unlimited.
- `server/backend/cmd/api/xray_sync.go` — call `biz.ListQuotaExceededUUIDs()` once at top of sync; filter UUIDs out of every inbound's `clients` array build path.
- `server/backend/cmd/api/traffic_stats.go` — replace `checkTrafficLimits()` body to delegate to `biz.ListQuotaExceededUUIDs()` and trigger `RequestXraySync()` when the set is non-empty.

### Frontend — Modify

- `site/src/pages/DashboardPage.jsx` — replace existing single Traffic card with period switcher + cycle progress + by_day chart card.
- `site/src/api/user.js` — `getTrafficStats(token)` consumes new shape (no signature change, but result shape evolves).
- `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx` — replace existing chart section with period switcher; same response shape.
- `admin_web/src/api/admin.js` — `fetchUserTraffic(userDbId)` drops the `days` param (endpoint no longer accepts it).

### No new migrations

The existing `traffic_stats(user_id TEXT, upload_bytes, download_bytes, recorded_at TIMESTAMPTZ)` table is sufficient. Existing columns `user_subscriptions.traffic_used` and `traffic_limit` are kept as legacy (not removed in this plan).

---
## Task 1: Backend — `biz/traffic.go` helpers (~45 min)

**Beads:** `heyearthproject-ue5l`

**Files:**
- Create: `server/backend/internal/biz/traffic.go`

Pure aggregation helpers used by every downstream handler. No HTTP, no JSON marshaling — just SQL → structs. Tasks 2-4 (admin/user endpoints, happ header, xray sync filter) all consume these.

### Step 1.1 — [ ] Create `internal/biz/traffic.go`

Use the project's existing biz convention (raw `db.DB.QueryRow/Query`, error wrapping with `fmt.Errorf("...: %w", err)`, no ORM). The file is fully self-contained; nothing else needs to change in Task 1.

```go
package biz

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// DayBucket aggregates one calendar day of traffic.
type DayBucket struct {
	Day   string `json:"day"`   // YYYY-MM-DD (UTC)
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
	// months_passed = floor(months between startedAt and now)
	months := monthsBetween(startedAt.Time, now)
	cycleStart = addMonths(startedAt.Time, months)
	cycleEnd = addMonths(startedAt.Time, months+1)
	return
}

// monthsBetween is a helper: how many whole calendar months from a to b.
// Negative if b < a.
func monthsBetween(a, b time.Time) int {
	years := b.Year() - a.Year()
	months := int(b.Month()) - int(a.Month())
	total := years*12 + months
	// If b's day-of-month is before a's, the last partial month doesn't count.
	if b.Day() < a.Day() || (b.Day() == a.Day() && b.Before(addMonths(a, total))) {
		total--
	}
	if total < 0 {
		total = 0
	}
	return total
}

// addMonths returns t + n calendar months using PG-style behaviour:
// 2026-01-31 + 1 month = 2026-02-28 (clamps to end of month).
func addMonths(t time.Time, n int) time.Time {
	// time.AddDate handles this differently in Go: 2026-01-31.AddDate(0,1,0) = 2026-03-03.
	// That's NOT what PG does. So we do it manually: clamp day to last day of target month.
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
// every sync tick (every 5 minutes) and by checkTrafficLimits after each
// collector iteration.
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
```

### Step 1.2 — [ ] Evidence: backend compiles

Run:
```bash
cd server/backend && go build ./... && go vet ./internal/biz/...
```
Expected: exit code 0, no output. If `go vet` complains about unused imports or shadowed variables, fix and re-run.

### Step 1.3 — [ ] Evidence: cycle math sanity check via psql

Verify the PostgreSQL `EXTRACT + age` arithmetic used in `ListQuotaExceededUUIDs` matches the Go `monthsBetween` helper for a known input (started_at = 2026-04-15, NOW = 2026-05-25 → 1 month passed).

Run:
```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT (EXTRACT(YEAR FROM age('2026-05-25'::timestamptz, '2026-04-15'::timestamptz)) * 12 + EXTRACT(MONTH FROM age('2026-05-25'::timestamptz, '2026-04-15'::timestamptz)))::int;\\\"\"
```
Expected: `1` (one full calendar month between April 15 and May 25).

### Step 1.4 — [ ] Commit

```bash
git add server/backend/internal/biz/traffic.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(biz): traffic.go — cycle math + period aggregation helpers

New helpers:
- CycleWindow: per-subscription billing cycle [start, end)
- PeriodUsage / PeriodBreakdownByDay / PeriodBreakdownByMonth: SQL aggregates
- EffectiveLimit: per-cycle quota with sub-override > plan precedence
- IsCycleExceeded: single-user check
- ListQuotaExceededUUIDs: batch query for xray_sync filter

Pure biz layer, no HTTP. Consumed by handleUserTraffic,
ListUserTrafficHandler, buildSubscriptionUserinfo, and xray_sync clients
filter.

Beads: heyearthproject-ue5l
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Backend — replace `handleUserTraffic` + admin `ListUserTrafficHandler` (~45 min)

**Beads:** `heyearthproject-hu5g`

**Files:**
- Modify: `server/backend/internal/biz/traffic.go` (append `TrafficResponse` types + `BuildTrafficResponse`)
- Modify: `server/backend/cmd/api/main.go` (replace `handleUserTraffic` body, ~line 780-851)
- Modify: `server/backend/internal/admin/users_handlers.go` (replace `ListUserTrafficHandler` body, ~line 320-410)

Both endpoints return the same `{subscription, cycle, periods}` shape. Admin handlers live in `internal/admin/` and cannot import from `cmd/api/`, so the shared response builder lives in `internal/biz/` (already imported by both layers).

### Step 2.1 — [ ] Append response types + `BuildTrafficResponse` to `internal/biz/traffic.go`

Append at the end of the file created in Task 1:

```go
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
	StartedAt  string  `json:"started_at"`  // RFC3339, cycle start
	EndsAt     string  `json:"ends_at"`     // RFC3339, cycle end
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
	err := db.DB.QueryRow(`
		SELECT us.started_at, us.valid_until,
		       COALESCE(p.name, 'standard'),
		       p.traffic_limit_bytes,
		       us.traffic_limit
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC
		LIMIT 1
	`, userDBID).Scan(&startedAt, &validUntil, &planName, &planLimit, &subLimit)

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
```

### Step 2.2 — [ ] Replace `handleUserTraffic` in `cmd/api/main.go`

First locate the current function with Grep (`func handleUserTraffic\(`) — the file is large, so confirm line range before editing. Replace the entire function body (from `func handleUserTraffic(...)` through its closing `}`) with:

```go
func handleUserTraffic(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	var userDBID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&userDBID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	resp, err := biz.BuildTrafficResponse(userDBID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
```

If `biz` is not yet imported in `main.go`, add `"github.com/heyearth/backend/internal/biz"` to the import block. (Search first — most likely already imported.)

### Step 2.3 — [ ] Replace `ListUserTrafficHandler` in `internal/admin/users_handlers.go`

Locate with Grep (`func ListUserTrafficHandler\(`) and confirm line range. The current implementation parses the user id from the path, joins `traffic_stats` directly, and returns `{user_db_id, days, limit_bytes, total_bytes, by_day, by_country}`. Replace the entire function with:

```go
func ListUserTrafficHandler(w http.ResponseWriter, r *http.Request) {
	// Path: /api/admin/users/{id}/traffic
	userDBID, err := parseLastPathSegmentBeforeFinal(r.URL.Path, "/traffic")
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	var userUUID string
	if err := db.DB.QueryRow(`SELECT user_id FROM users WHERE id = $1`, userDBID).Scan(&userUUID); err != nil {
		writeAdminError(w, http.StatusNotFound, "User not found")
		return
	}
	resp, err := biz.BuildTrafficResponse(userDBID, userUUID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, resp)
}
```

Helper assumption: `parseLastPathSegmentBeforeFinal` already exists in this package (it was added in Sub-project B for the same handler). If it doesn't (Grep first), reuse whatever extraction pattern the surrounding handlers use — for example `strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/traffic")` parsed via `strconv.Atoi`.

If `biz` is not imported in `users_handlers.go` (it likely is — `ListUsersHandler` uses it for status filtering), add `"github.com/heyearth/backend/internal/biz"` to the import block. Also confirm `db` is imported (it is — already used several times in this file).

### Step 2.4 — [ ] Evidence: both endpoints compile + handler signatures match

Run:
```bash
cd server/backend && go build ./... && go vet ./...
```
Expected: exit code 0, no output. Any "declared and not used" errors mean a leftover local variable from the old handler — remove it.

Then confirm route registration is untouched (the handler signatures didn't change, but the dispatcher should still match the path):
```bash
cd server/backend && grep -n "ListUserTrafficHandler\|handleUserTraffic" cmd/api/main.go internal/admin/routes.go internal/admin/users_handlers.go
```
Expected: handler is still referenced from `routes.go` (admin dispatcher) and from the HTTP mux setup in `cmd/api/main.go` (user endpoint mapped to `/api/v1/user/traffic`).

### Step 2.5 — [ ] Commit

```bash
git add server/backend/internal/biz/traffic.go server/backend/cmd/api/main.go server/backend/internal/admin/users_handlers.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(traffic): unified {subscription, cycle, periods} response for user + admin endpoints

Both /api/v1/user/traffic and /api/admin/users/{id}/traffic now return the
same shape: active subscription summary, current billing cycle (with
used/limit/percent/exceeded), and four display periods (day, week, month,
year) each with a per-day/per-month bucket breakdown for charting.

Shared assembly lives in biz.BuildTrafficResponse so admin and user
handlers stay thin and stay consistent.

Cycle is the subscription month from started_at — no more
subscription-lifetime accumulation. limit_bytes=0 signals unlimited
(plan.traffic_limit_bytes NULL or 0), which the frontend renders as
"X / ∞".

Beads: heyearthproject-hu5g
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Task 3: buildSubscriptionUserinfo uses cycle data + total=0 for unlimited (~30 min)

**Beads:** `heyearthproject-urn7`

**Files:**
- Modify: `server/backend/cmd/api/subscription_handler.go:273-300` (function `buildSubscriptionUserinfo`)
- Modify: `server/backend/cmd/api/subscription_handler.go:1-14` (import block — verify `database/sql` and `biz` are imported)

**Dependencies:** Task 1 (`biz.CycleWindow`, `biz.PeriodUsage`) must be merged.

### Step 3.1 — [ ] Read current state

Confirm current implementation and import block:

```bash
# Verify line numbers haven't drifted
grep -n "func buildSubscriptionUserinfo" server/backend/cmd/api/subscription_handler.go
# Expected: matches around line 279

grep -n "^import\|^\t\"" server/backend/cmd/api/subscription_handler.go | head -20
# Expected: shows current imports
```

The current import block (lines 1-14) already includes:
- `database/sql`
- `fmt`
- `time`
- `github.com/heyearth/backend/internal/biz`
- `github.com/heyearth/backend/internal/db`

No new imports required.

### Step 3.2 — [ ] Replace function body

The header (lines 273-278) and comments stay — only the function body changes. The new implementation:
- Resolves active subscription with `started_at`, `valid_until`, and effective limit (subscription override > plan default > 0).
- Computes cycle window via `biz.CycleWindow`, cycle usage via `biz.PeriodUsage`.
- Sets `expire=` to `cycle_end` (preferring `valid_until` if it ends sooner — handles users whose subscription ends mid-cycle).
- Always emits `total=` (0 = unlimited, happ standard for "∞").

Replace lines 279-300 (the function body) with:

```go
func buildSubscriptionUserinfo(userID string) string {
	// Resolve active subscription: started_at (for cycle math), valid_until,
	// and the effective per-cycle limit (user override > plan default > 0/unlimited).
	var startedAt sql.NullTime
	var validUntil time.Time
	var subLimit sql.NullInt64
	var planLimit sql.NullInt64
	var userDBID int
	err := db.DB.QueryRow(`
		SELECT u.id, us.started_at, us.valid_until,
		       us.traffic_limit, p.traffic_limit_bytes
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC
		LIMIT 1
	`, userID).Scan(&userDBID, &startedAt, &validUntil, &subLimit, &planLimit)
	if err != nil {
		return ""
	}
	_ = userDBID // reserved for future per-user override path

	// Effective limit (0 = unlimited; total=0 is the happ standard for ∞)
	var effLimit int64
	if subLimit.Valid && subLimit.Int64 > 0 {
		effLimit = subLimit.Int64
	} else if planLimit.Valid && planLimit.Int64 > 0 {
		effLimit = planLimit.Int64
	}

	// Cycle window — usage we report is *this cycle*, not subscription lifetime.
	cycleStart, cycleEnd := biz.CycleWindow(startedAt, time.Now())
	cycleUsed, _ := biz.PeriodUsage(userID, cycleStart, cycleEnd)

	// expire = end of current cycle so happ shows the meaningful date (when
	// quota resets) for monthly-cycle users. If the subscription ends before
	// cycle end (e.g. user's annual plan ends mid-cycle), prefer valid_until.
	expireAt := cycleEnd
	if validUntil.Before(expireAt) {
		expireAt = validUntil
	}

	return fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d",
		cycleUsed, effLimit, expireAt.Unix())
}
```

Note: the duplicated doc-comment on lines 273-274 in the source file ("buildSubscriptionUserinfo формирует заголовок Subscription-Userinfo." appears twice) — leave both lines alone; not in scope for this change.

### Step 3.3 — [ ] Build evidence

```bash
cd server/backend && go build ./... && go vet ./cmd/api/...
```
Expected: exit 0, no warnings about `sql.NullTime`, `sql.NullInt64`, or `biz.CycleWindow` / `biz.PeriodUsage`.

### Step 3.4 — [ ] Curl smoke (post-deploy, runs in Task 7)

This evidence runs after T7 deploys the bundled change. Documented here so the user can verify Task 3's behaviour.

```bash
# Pick any user with an active subscription. Find their subscribe_token:
go run ./cmd/deploy --ssh "psql -U heyearth -d heyearth -t -c \"SELECT u.user_id, u.subscribe_token, p.slug, p.traffic_limit_bytes FROM users u JOIN user_subscriptions us ON us.user_id=u.id LEFT JOIN plans p ON p.id=us.plan_id WHERE us.valid_until > NOW() ORDER BY us.created_at DESC LIMIT 3;\""

# Then for the chosen token, inspect the header:
TOKEN=<paste-token-here>
curl -sI "https://hiearth.ru/api/v1/user/subscribe/$TOKEN" | grep -i 'Subscription-Userinfo'
```

Expected output shape:
- For unlimited plan (e.g. `all-inclusive`): `Subscription-Userinfo: upload=0; download=<bytes>; total=0; expire=<unix>`
- For limited plan: `Subscription-Userinfo: upload=0; download=<bytes>; total=<plan-bytes>; expire=<unix>`
- `download=` must be cycle usage, NOT subscription-lifetime — for a freshly-started subscription it should be a small number (or 0 if no traffic yet).
- `expire=` must be within 30 days of NOW for monthly-cycle users.

### Step 3.5 — [ ] Commit

```bash
git add server/backend/cmd/api/subscription_handler.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(happ): Subscription-Userinfo reports cycle usage; total=0 for unlimited

buildSubscriptionUserinfo now reflects the current billing cycle instead of
subscription-lifetime. Effective limit resolves user_subscriptions.traffic_limit
override over plans.traffic_limit_bytes; 0/NULL means unlimited and emits
total=0 (happ convention for "∞" — see screenshot reference in spec).
expire= is now the end of the current cycle (cycle_end), preferring valid_until
if it ends sooner so users whose subscription expires mid-cycle still see the
real account end date.

Beads: heyearthproject-urn7
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: xray_sync.go excludes quota-exceeded UUIDs + checkTrafficLimits triggers sync (~45 min)

**Beads:** `heyearthproject-vilj`

**Files:**
- Modify: `server/backend/cmd/api/xray_sync.go:96-167` (function `SyncXrayConfig`, sites where active clients are iterated)
- Modify: `server/backend/cmd/api/traffic_stats.go:193-224` (function `checkTrafficLimits`)

**Dependencies:** Task 1 (`biz.ListQuotaExceededUUIDs`) must be merged.

### Step 4.1 — [ ] Read xray_sync.go end-to-end and locate iteration sites

```bash
wc -l server/backend/cmd/api/xray_sync.go  # ~1174
grep -n "range activeClients\|range vlessClients" server/backend/cmd/api/xray_sync.go
```

Document line numbers here in the plan after reading. From the planning-time read:

1. **Line ~120**: scan loop checking if `serverUUID` is already in `activeClients`. **Do NOT add quota check here** — this loop only inspects, never builds clients.
2. **Line ~133**: builds `uuidToUserID` mapping for Xray Stats API. **Do NOT add quota check here** — the stats map should still cover the user's email so that any pre-existing connection stats are attributable; cutoff means new connections refused, not stats deletion.
3. **Line ~142-158**: **PRIMARY SITE.** Builds the `vlessClients` array. This is the array fed into VLESS Reality (`vless-reality`, port 10443) and VLESS XHTTP (`vless-xhttp`, port 2443) inbounds. **Add the quota check here** — filtered users won't appear in either VLESS inbound.
4. **Line ~404**: `for _, client := range activeClients` building `planBalancers`. **Skip filtering here** — a balancer is created per plan, not per user; if some users in the plan are over quota, others still need the balancer.
5. **Line ~437**: `for _, client := range activeClients` building per-user routing rules (`user: [client.UserID]` → `balancerTag`). **Add filter here** — a routing rule for a user whose UUID isn't in clients array would never match anyway, but skipping is cheaper and cleaner. Also reduces routing-table size.

Shadowsocks (port 8388, line ~217) uses a single shared `keys.SSPassword` — there is no per-user clients array. Quota cutoff for SS would require a different approach (server-side per-account auth) and is out of scope for v1; the spec mandates cutoff via VLESS clients filter, which is what 80%+ of users actually use.

SOCKS/HTTP per-user inbounds are NOT in xray_sync.go's main inbound list — those are handled separately (different listener), out of scope for this task per spec.

### Step 4.2 — [ ] Fetch quota-exceeded set at top of SyncXrayConfig

Insert immediately after `activeClients, err := biz.ListActiveVlessClients()` at line 111 (before the serverUUID-injection block):

```go
	// Per-user quota cutoff: load the set ONCE per sync. Users over their
	// current billing cycle quota are excluded from VLESS clients arrays and
	// per-user routing rules. The set re-computes on every sync (debounced
	// 2s), so cycle-resets pick up automatically without a flag.
	quotaExceeded, qerr := biz.ListQuotaExceededUUIDs()
	if qerr != nil {
		log.Printf("[xray-sync] WARN failed to load quota-exceeded set: %v — continuing without cutoff", qerr)
		quotaExceeded = map[string]struct{}{}
	}
```

### Step 4.3 — [ ] Filter vlessClients build loop (PRIMARY SITE)

In the loop at lines 142-158 (`for _, c := range activeClients`), insert the quota-skip at the top of the loop body, before the `email := c.UserID` line:

```go
	vlessClients := make([]map[string]interface{}, 0, len(activeClients))
	skipped := 0
	for _, c := range activeClients {
		if _, blocked := quotaExceeded[c.UUID]; blocked {
			skipped++
			continue // over cycle quota — re-included when cycle resets
		}
		email := c.UserID
		if email == "" {
			email = "server@heyearth"
		}
		level := 0
		if c.IsPremium {
			level = 1
		}
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":    c.UUID,
			"flow":  "xtls-rprx-vision",
			"email": email,
			"level": level,
		})
	}
	if skipped > 0 {
		log.Printf("[xray-sync] quota cutoff: %d user(s) excluded from VLESS clients this sync", skipped)
	}
```

The `serverUUID` check immediately after (line ~161 — re-injects server admin UUID if `len(vlessClients) == 0`) still works correctly: if all real users were skipped due to quota, the server UUID is preserved for admin access.

### Step 4.4 — [ ] Filter per-user routing rules

In the loop at line ~437 (`for _, client := range activeClients` building per-user routing rules), add the same skip:

```go
		// Per-plan routing rules: match user email → plan-specific balancer
		for _, client := range activeClients {
			if _, blocked := quotaExceeded[client.UUID]; blocked {
				continue // quota cutoff — user already missing from clients arrays
			}
			if balancerTag, ok := planBalancers[client.PlanID]; ok {
				rules = append(rules, map[string]interface{}{
					"type":        "field",
					"user":        []string{client.UserID},
					"balancerTag": balancerTag,
				})
			}
		}
```

(The `planBalancers` build loop at line ~404 is NOT filtered — see analysis in Step 4.1.)

### Step 4.5 — [ ] Replace checkTrafficLimits in traffic_stats.go

Replace the entire function body at lines 193-224 with:

```go
// checkTrafficLimits — delegates to biz, logs offenders, triggers an immediate
// debounced xray sync so the cutoff lands within ~2 seconds of the offending
// traffic_stats insert (vs waiting for the next 5-minute periodic sync).
func checkTrafficLimits() {
	over, err := biz.ListQuotaExceededUUIDs()
	if err != nil {
		log.Printf("[traffic-stats] checkTrafficLimits: %v", err)
		return
	}
	if len(over) == 0 {
		return
	}
	for uuid := range over {
		log.Printf("[traffic-stats] ⚠ Пользователь %s превысил cycle-лимит трафика", uuid)
	}
	RequestXraySync()
}
```

Verify imports at the top of `traffic_stats.go` already include `biz` and `log` (they do — both used by neighboring functions).

The old SQL query (referencing `us.traffic_used >= us.traffic_limit`) is removed entirely. `db.DB.Query` is no longer used by this function — but keep the `db` import if other functions in the file still need it (they do — `incrementUserTraffic`, etc.).

### Step 4.6 — [ ] Build evidence

```bash
cd server/backend && go build ./... && go vet ./...
```
Expected: exit 0. No "imported and not used" warning on `db` (other functions still use it).

### Step 4.7 — [ ] Functional evidence (post-deploy, runs in Task 7)

After T7 deploys, set up a controlled test:

```bash
# 1. Find a test user (one you don't mind cutting off temporarily)
go run ./cmd/deploy --ssh "psql -U heyearth -d heyearth -c \"SELECT u.id, u.user_id, p.slug, p.traffic_limit_bytes FROM users u JOIN user_subscriptions us ON us.user_id=u.id LEFT JOIN plans p ON p.id=us.plan_id WHERE us.valid_until>NOW() AND p.traffic_limit_bytes IS NOT NULL AND p.traffic_limit_bytes>0 LIMIT 3;\""

# 2. Pick TEST_USER_DB_ID and TEST_USER_UUID. Override their subscription limit to 1 MB:
go run ./cmd/deploy --ssh "psql -U heyearth -d heyearth -c \"UPDATE user_subscriptions SET traffic_limit=1000000 WHERE user_id=$TEST_USER_DB_ID AND valid_until>NOW();\""

# 3. Insert a traffic_stats row exceeding 1 MB this cycle:
go run ./cmd/deploy --ssh "psql -U heyearth -d heyearth -c \"INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes, recorded_at) VALUES ('$TEST_USER_UUID', 500000, 600000, NOW());\""

# 4. Wait ~10 seconds (traffic collector tick + debounce). Then verify cutoff applied:
sleep 10
go run ./cmd/deploy --ssh "grep 'quota cutoff' /var/log/heyearth-api.log | tail -5"
# Expected: a line like "[xray-sync] quota cutoff: 1 user(s) excluded from VLESS clients this sync"

# 5. Inspect xray config — TEST_USER_UUID should NOT appear in any VLESS clients array:
go run ./cmd/deploy --ssh "cat /opt/heyearth/state/xray/config.json | python3 -c \"import sys,json; d=json.load(sys.stdin); ids=[c.get('id') for ib in d.get('inbounds',[]) if ib.get('protocol')=='vless' for c in (ib.get('settings',{}).get('clients') or [])]; print('total VLESS clients:', len(set(ids))); print('TARGET present:', '$TEST_USER_UUID' in ids)\""
# Expected: "TARGET present: False"

# 6. CLEANUP — restore the user's quota and remove test traffic:
go run ./cmd/deploy --ssh "psql -U heyearth -d heyearth -c \"UPDATE user_subscriptions SET traffic_limit=NULL WHERE user_id=$TEST_USER_DB_ID; DELETE FROM traffic_stats WHERE user_id='$TEST_USER_UUID' AND upload_bytes=500000 AND download_bytes=600000;\""
```

Acceptance: step 5 prints `TARGET present: False` AND step 4 shows the cutoff log line.

### Step 4.8 — [ ] Commit

```bash
git add server/backend/cmd/api/xray_sync.go server/backend/cmd/api/traffic_stats.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(traffic): xray-sync cutoff for quota-exceeded users + checkTrafficLimits triggers sync

SyncXrayConfig now loads biz.ListQuotaExceededUUIDs() once per sync and
filters two sites: (1) the VLESS clients-build loop (covers vless-reality
on :10443 and vless-xhttp on :2443 — both share the vlessClients slice),
and (2) the per-user routing-rule loop. Server admin UUID is preserved.
Logs "[xray-sync] quota cutoff: N user(s) excluded" once per sync when
non-empty. Shadowsocks (shared password) and SOCKS/HTTP (separate listener)
are out of scope per spec; VLESS covers 80%+ of users.

checkTrafficLimits now delegates to biz.ListQuotaExceededUUIDs and calls
RequestXraySync() so the cutoff lands within ~2s of the offending
traffic_stats insert instead of waiting for the next 5-min sync tick.
The old us.traffic_used>=us.traffic_limit query (which counted
subscription-lifetime, not cycle) is removed.

Beads: heyearthproject-vilj
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Task 5: site DashboardPage period switcher + bar chart (~45 min)

**Beads:** `heyearthproject-1evg`

**Files:**
- Create: `site/src/components/TrafficCard.jsx`
- Modify: `site/src/pages/DashboardPage.jsx`
- Verify (no change): `site/src/api/user.js`

### Step 5.1 — [ ] Create `site/src/components/TrafficCard.jsx`

New reusable component encapsulating period switcher + cycle progress + bar chart + summary row. Keeps DashboardPage diff small and lets us reuse if a second user-portal page ever needs the same chart.

```jsx
import { useState, useMemo } from 'react'

const PERIODS = [
  { id: 'day',   label: 'День' },
  { id: 'week',  label: 'Неделя' },
  { id: 'month', label: 'Месяц' },
  { id: 'year',  label: 'Год' },
]

function formatBytes(n) {
  if (!n) return '0 B'
  const k = 1024, units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(n) / Math.log(k))
  return parseFloat((n / Math.pow(k, i)).toFixed(1)) + ' ' + units[i]
}

function formatDateRu(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export default function TrafficCard({ data }) {
  const [period, setPeriod] = useState('month')
  const p = data?.periods?.[period]
  const cycle = data?.cycle
  const isUnlimited = !cycle || cycle.limit_bytes === 0
  const buckets = period === 'year' ? (p?.by_month || []) : (p?.by_day || [])

  const maxBytes = useMemo(
    () => Math.max(1, ...buckets.map(b => b.bytes || 0)),
    [buckets]
  )

  if (!data) return null

  return (
    <div className="rounded-lg border border-default bg-elevated p-4 space-y-3">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <h2 className="text-base font-semibold text-primary">Трафик</h2>
        <div className="flex items-center gap-1">
          {PERIODS.map(po => (
            <button
              key={po.id}
              onClick={() => setPeriod(po.id)}
              className={`text-xs px-2.5 py-1 rounded-md transition ${
                period === po.id
                  ? 'bg-accent text-canvas font-medium'
                  : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {po.label}
            </button>
          ))}
        </div>
      </div>

      {cycle && (
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-3 flex-wrap">
            <span className="text-2xl font-semibold text-primary tabular-nums">
              {formatBytes(cycle.used_bytes)}
            </span>
            <span className="text-sm text-tertiary tabular-nums">
              / {isUnlimited ? '∞' : formatBytes(cycle.limit_bytes)}
              {!isUnlimited && ` · ${cycle.percent.toFixed(0)}%`}
            </span>
          </div>
          {!isUnlimited && (
            <div className="h-1.5 bg-canvas rounded-full overflow-hidden">
              <div
                className={`h-full transition-all ${cycle.exceeded ? 'bg-danger' : 'bg-info'}`}
                style={{ width: `${Math.min(100, cycle.percent)}%` }}
              />
            </div>
          )}
          <p className="text-xs text-tertiary">
            Цикл: {formatDateRu(cycle.started_at)} → {formatDateRu(cycle.ends_at)}
          </p>
        </div>
      )}

      <div className="h-24 flex items-end gap-0.5">
        {buckets.length === 0 ? (
          <p className="text-xs text-tertiary self-center">Нет данных за период</p>
        ) : (
          buckets.map((b, i) => {
            const h = Math.max(2, Math.round((b.bytes / maxBytes) * 96))
            const label = period === 'year' ? b.month : b.day
            return (
              <div
                key={i}
                className="flex-1 bg-info/30 hover:bg-info/60 transition rounded-sm relative group"
                style={{ height: `${h}px` }}
                title={`${label}: ${formatBytes(b.bytes)}`}
              />
            )
          })
        )}
      </div>

      <div className="grid grid-cols-3 gap-3 text-xs text-secondary pt-2 border-t border-default">
        <div>День: <span className="text-primary font-medium tabular-nums">{formatBytes(data.periods?.day?.used_bytes || 0)}</span></div>
        <div>Неделя: <span className="text-primary font-medium tabular-nums">{formatBytes(data.periods?.week?.used_bytes || 0)}</span></div>
        <div>Год: <span className="text-primary font-medium tabular-nums">{formatBytes(data.periods?.year?.used_bytes || 0)}</span></div>
      </div>
    </div>
  )
}
```

### Step 5.2 — [ ] Modify `site/src/pages/DashboardPage.jsx` — add import

Add the import alongside the existing component imports near the top of the file:

```jsx
import TrafficCard from '../components/TrafficCard.jsx'
```

### Step 5.3 — [ ] Modify `site/src/pages/DashboardPage.jsx` — replace Traffic Stats section

Locate the section starting at `{/* ─── Traffic Stats ─── */}` (~ line 630). The block is an IIFE that closes at `})()}` (~ line 714). Replace the **entire** block (including the IIFE wrapper, the colored-threshold logic, exhausted/warning banners, and the upload/download summary) with:

```jsx
        {/* ─── Traffic Stats ─── */}
        {trafficStats && (
          <div className="mb-6 animate-fade-in-up delay-250">
            <TrafficCard data={trafficStats} />
          </div>
        )}
```

Notes:
- The old code referenced `trafficStats.traffic_used`, `trafficStats.traffic_limit`, `trafficStats.upload_bytes`, `trafficStats.download_bytes` — none of those fields exist in the new response. Removing the entire IIFE block is required, not just a partial edit.
- The exhausted/warning banner UX is now handled inside `TrafficCard` via the cycle progress bar color (`bg-danger` vs `bg-info`). If the user later requests an explicit banner, add it to `TrafficCard`.
- The `mb-6 animate-fade-in-up delay-250` wrapper preserves the page's spacing rhythm.

### Step 5.4 — [ ] Verify `site/src/api/user.js::getTrafficStats(token)` needs no changes

Confirm by reading the function (line ~124). The signature is `getTrafficStats(token)` and the URL is `/api/v1/user/traffic`. The backend endpoint shape changed in Phase B Task 2, but the JS function does no parsing — it just returns `jsonOrThrow(res, ...)`. Consumers (only `TrafficCard` now) read the new shape directly.

No code change. Just confirm via:

```bash
cd C:/Users/alex/Documents/heyearthproject && rg -n "export async function getTrafficStats" site/src/api/user.js
```

Expected: prints the function declaration on one line, confirming the signature is unchanged.

### Step 5.5 — [ ] Evidence

Run:

```bash
cd C:/Users/alex/Documents/heyearthproject/site && npm run build
```

Expected: exit code 0, last lines include `✓ built in <N>s` and a fresh `dist/assets/index-<hash>.js`. No TypeScript errors (project is plain JSX). No "Module not found" for `../components/TrafficCard.jsx`. No console warnings about undefined props on TrafficCard.

### Step 5.6 — [ ] Commit

```bash
git add site/src/components/TrafficCard.jsx site/src/pages/DashboardPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(site): TrafficCard with period switcher + cycle progress + bar chart

Replace the lifetime-counted Traffic Stats block in DashboardPage with a
new TrafficCard component that consumes the {subscription, cycle, periods}
shape from /api/v1/user/traffic. Switcher offers день/неделя/месяц/год
(default месяц). Cycle progress bar only renders when a quota is set;
unlimited plans show "X / ∞" with no bar. Bar chart uses by_day for
day/week/month and by_month for year. Summary row at the bottom shows
all-period totals at a glance.

Beads: heyearthproject-1evg
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: admin UserDrawer TrafficPaymentsTab period switcher + bar chart (~45 min)

**Beads:** `heyearthproject-nedr`

**Files:**
- Create: `admin_web/src/components/user-drawer/TrafficCard.jsx`
- Modify: `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx`
- Modify: `admin_web/src/api/admin.js`

### Step 6.1 — [ ] Create `admin_web/src/components/user-drawer/TrafficCard.jsx`

Admin variant of the site's TrafficCard. Identical UX, but:
- No outer card wrapper (`border border-default bg-elevated p-4 rounded-lg`) because the drawer body already provides padding/elevation.
- Uses admin's `formatBytes` from `utils/format.js` (already exists) instead of an inline helper, to match conventions.
- Uses admin's i18n `useT()` if labels need to be translated later — but for v1 we hard-code Russian to mirror the site UX. (Admin translations can land in a follow-up.)

```jsx
import { useState, useMemo } from 'react'
import { formatBytes } from '../../utils/format.js'

const PERIODS = [
  { id: 'day',   label: 'День' },
  { id: 'week',  label: 'Неделя' },
  { id: 'month', label: 'Месяц' },
  { id: 'year',  label: 'Год' },
]

function formatDateRu(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric' })
}

export default function TrafficCard({ data }) {
  const [period, setPeriod] = useState('month')
  const p = data?.periods?.[period]
  const cycle = data?.cycle
  const isUnlimited = !cycle || cycle.limit_bytes === 0
  const buckets = period === 'year' ? (p?.by_month || []) : (p?.by_day || [])

  const maxBytes = useMemo(
    () => Math.max(1, ...buckets.map(b => b.bytes || 0)),
    [buckets]
  )

  if (!data) return null

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <h3 className="text-sm font-semibold text-primary">Трафик</h3>
        <div className="flex items-center gap-1">
          {PERIODS.map(po => (
            <button
              key={po.id}
              onClick={() => setPeriod(po.id)}
              className={`text-xs px-2.5 py-1 rounded-md transition ${
                period === po.id
                  ? 'bg-accent text-canvas font-medium'
                  : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {po.label}
            </button>
          ))}
        </div>
      </div>

      {cycle && (
        <div className="space-y-1.5">
          <div className="flex items-baseline justify-between gap-3 flex-wrap">
            <span className="text-2xl font-semibold text-primary tabular-nums font-mono">
              {formatBytes(cycle.used_bytes)}
            </span>
            <span className="text-sm text-tertiary tabular-nums font-mono">
              / {isUnlimited ? '∞' : formatBytes(cycle.limit_bytes)}
              {!isUnlimited && ` · ${cycle.percent.toFixed(0)}%`}
            </span>
          </div>
          {!isUnlimited && (
            <div className="h-1.5 bg-canvas border border-default rounded-full overflow-hidden">
              <div
                className={`h-full transition-all ${cycle.exceeded ? 'bg-danger' : 'bg-info'}`}
                style={{ width: `${Math.min(100, cycle.percent)}%` }}
              />
            </div>
          )}
          <p className="text-xs text-tertiary">
            Цикл: {formatDateRu(cycle.started_at)} → {formatDateRu(cycle.ends_at)}
          </p>
        </div>
      )}

      <div className="h-24 flex items-end gap-0.5 bg-canvas border border-default rounded p-2">
        {buckets.length === 0 ? (
          <p className="text-xs text-tertiary self-center mx-auto">Нет данных за период</p>
        ) : (
          buckets.map((b, i) => {
            const h = Math.max(2, Math.round((b.bytes / maxBytes) * 80))
            const label = period === 'year' ? b.month : b.day
            return (
              <div
                key={i}
                className="flex-1 bg-info/60 hover:bg-info transition rounded-sm min-w-0"
                style={{ height: `${h}px` }}
                title={`${label}: ${formatBytes(b.bytes)}`}
              />
            )
          })
        )}
      </div>

      <div className="grid grid-cols-3 gap-3 text-xs text-secondary pt-2 border-t border-default">
        <div>День: <span className="text-primary font-medium tabular-nums font-mono">{formatBytes(data.periods?.day?.used_bytes || 0)}</span></div>
        <div>Неделя: <span className="text-primary font-medium tabular-nums font-mono">{formatBytes(data.periods?.week?.used_bytes || 0)}</span></div>
        <div>Год: <span className="text-primary font-medium tabular-nums font-mono">{formatBytes(data.periods?.year?.used_bytes || 0)}</span></div>
      </div>
    </div>
  )
}
```

Theme-token compliance check:
- Backgrounds: `bg-canvas`, `bg-elevated`, `bg-hover`, `bg-info`, `bg-accent`, `bg-danger` — all in admin's allowed token list.
- Text: `text-primary`, `text-secondary`, `text-tertiary`, `text-canvas` — allowed.
- Borders: `border-default` — allowed.
- No `slate-X`/`gray-X`/`zinc-X`. No `shadow-*`. No `rounded-2xl`. Geist Mono via `font-mono` on numbers.

### Step 6.2 — [ ] Modify `admin_web/src/api/admin.js` — drop `days` param

Locate (around line 35):

```js
export function fetchUserTraffic(userDbId, days = 30) {
  return apiFetch(`/users/${userDbId}/traffic?days=${days}`)
}
```

Replace with:

```js
export function fetchUserTraffic(userDbId) {
  return apiFetch(`/users/${userDbId}/traffic`)
}
```

The backend endpoint from Phase B Task 3 no longer accepts a `days` query param; it always returns the unified `{subscription, cycle, periods}` shape.

### Step 6.3 — [ ] Modify `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx`

Full rewrite of the Traffic section. Keep the Payments section verbatim. Show full replacement file:

```jsx
import { useState, useEffect, useCallback } from 'react'
import { useT } from '../../i18n/index.jsx'
import { fetchUserTraffic, fetchUserPayments } from '../../api/admin.js'
import { formatDate } from '../../utils/format.js'
import { Badge } from '../ui/index.js'
import TrafficCard from './TrafficCard.jsx'

export default function TrafficPaymentsTab({ user }) {
  const { t } = useT()
  const [traffic, setTraffic] = useState(null)
  const [payments, setPayments] = useState([])
  const [loadingT, setLoadingT] = useState(true)
  const [loadingP, setLoadingP] = useState(true)
  const [error, setError] = useState('')

  const loadTraffic = useCallback(() => {
    setLoadingT(true)
    fetchUserTraffic(user.id)
      .then(setTraffic)
      .catch(err => setError(err.message))
      .finally(() => setLoadingT(false))
  }, [user.id])

  const loadPayments = useCallback(() => {
    setLoadingP(true)
    fetchUserPayments(user.id)
      .then(data => setPayments(data.items || []))
      .catch(err => setError(err.message))
      .finally(() => setLoadingP(false))
  }, [user.id])

  useEffect(() => { loadTraffic(); loadPayments() }, [loadTraffic, loadPayments])

  return (
    <div className="space-y-6">
      {/* ── Traffic section ─────────────────────────────────────── */}
      <section>
        {loadingT ? (
          <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
        ) : !traffic ? (
          <p className="text-sm text-tertiary">{t.drawerNoTraffic || 'Нет данных о трафике.'}</p>
        ) : (
          <TrafficCard data={traffic} />
        )}
      </section>

      {/* ── Payments section ────────────────────────────────────── */}
      <section>
        <h3 className="text-sm font-semibold text-primary mb-2">{t.drawerPaymentsTitle || 'Платежи'}</h3>
        {loadingP ? (
          <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
        ) : payments.length === 0 ? (
          <p className="text-sm text-tertiary">{t.drawerNoPayments || 'Платежей нет.'}</p>
        ) : (
          <ul className="space-y-1">
            {payments.map(p => {
              const amount = Number(p.amount_rub ?? p.amount ?? 0)
              const positive = amount >= 0
              const variant = p.status === 'succeeded' ? 'success' : p.status === 'failed' ? 'danger' : p.status === 'pending' ? 'warning' : 'default'
              return (
                <li key={p.id} className="flex items-center gap-2 py-1.5 border-b border-default last:border-b-0 text-sm">
                  <span className="text-xs text-tertiary font-mono w-32 shrink-0">{formatDate(p.created_at)}</span>
                  <span className={`font-mono tabular-nums font-semibold w-16 shrink-0 ${positive ? 'text-success' : 'text-warning'}`}>
                    {positive ? '+' : ''}{amount}₽
                  </span>
                  <span className="text-secondary flex-1 truncate">
                    {p.provider || 'YooKassa'} · {p.description || p.purpose || p.plan_name || '—'}
                  </span>
                  <Badge variant={variant}>{p.status}</Badge>
                </li>
              )
            })}
          </ul>
        )}
      </section>

      {error && <p className="text-danger text-sm">{error}</p>}
    </div>
  )
}
```

Diff summary vs current file:
- Drop `formatBytes` import (now lives inside TrafficCard).
- Drop `useMemo` import (no longer needed at this level).
- Drop `maxDayBytes`, `usedPct` derived state (moved into TrafficCard).
- Drop the inline CSS bar chart, the limit progress bar, the by-country breakdown (all replaced by TrafficCard).
- Add `import TrafficCard from './TrafficCard.jsx'`.
- `fetchUserTraffic(user.id)` instead of `fetchUserTraffic(user.id, 30)`.
- Payments section is byte-for-byte identical.

### Step 6.4 — [ ] Evidence

Run:

```bash
cd C:/Users/alex/Documents/heyearthproject/admin_web && npm run build
```

Expected: exit code 0, last lines include `✓ built in <N>s` and a fresh `dist/assets/index-<hash>.js`. No "Module not found" for `./TrafficCard.jsx`. No theme-token warnings.

Additionally, theme-lock self-scan:

```bash
cd C:/Users/alex/Documents/heyearthproject && rg -n "slate-|gray-|zinc-|shadow-|rounded-2xl|Inter|Roboto" admin_web/src/components/user-drawer/TrafficCard.jsx admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx
```

Expected: zero matches.

### Step 6.5 — [ ] Commit

```bash
git add admin_web/src/components/user-drawer/TrafficCard.jsx admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx admin_web/src/api/admin.js
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin): TrafficCard period switcher in user drawer (Трафик+Платежи tab)

Replace the 30-day CSS bar chart in UserDrawer's TrafficPaymentsTab with a
new TrafficCard sibling component that consumes the unified
{subscription, cycle, periods} shape from
/api/admin/users/{id}/traffic. Same UX as the user-portal TrafficCard:
period switcher (день/неделя/месяц/год, default месяц), cycle progress
bar (hidden for unlimited plans), per-day or per-month bar chart, and an
all-period summary row. The Payments section below is untouched.

api/admin.js: drop the days param from fetchUserTraffic — backend no
longer accepts it.

Beads: heyearthproject-nedr
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: deploy + smoke (~60 min)

**Beads:** `heyearthproject-2tv6`

**Files:** none (deploy + verification only)

This task ends with `git push origin main`. No new commit.

### Step 7.1 — [ ] Pre-deploy sanity check

Confirm all six prior commits (Tasks 1-6) are local and the working tree is clean.

```bash
cd C:/Users/alex/Documents/heyearthproject && git status
cd C:/Users/alex/Documents/heyearthproject && git log --oneline -10
```

Expected: `git status` reports "nothing to commit, working tree clean". `git log` shows the 6 new commits on top of `5164ca9` or its successor, in this order (oldest first):
1. `feat(backend): biz/traffic.go — cycle math + period helpers` (Task 1)
2. `feat(backend): unify /user/traffic + admin /users/{id}/traffic ...` (Task 2)
3. `feat(backend): buildSubscriptionUserinfo uses cycle data + ...` (Task 3)
4. `feat(backend): xray_sync filters quota-exceeded UUIDs, ...` (Task 4)
5. `feat(site): TrafficCard with period switcher + ...` (Task 5)
6. `feat(admin): TrafficCard period switcher in user drawer ...` (Task 6)

### Step 7.2 — [ ] Backup production state

Per project safety protocol, take a Hub backup before deploying.

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --backup
```

Expected stdout includes `=== Backup complete: /opt/heyearth/backups/backup-YYYYMMDD-HHMMSS.tar.gz ===`. Note the path — it's the rollback handle if anything goes wrong.

### Step 7.3 — [ ] Backend binary deploy

Stop the service before pushing the binary (SFTP can hang otherwise), then use the fast quick_deploy path.

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run quick_deploy.go
```

Expected from `quick_deploy.go`:
- `Compiling…` → `Done (N MB)`
- `Uploading…` → `Done`
- `Starting service…` → `heyearth-api: ACTIVE ✓`
- Final `Health: {"status":"ok"}` or equivalent 200 response from `/api/v1/health`.

If `systemctl status heyearth-api` shows `failed` or `activating (auto-restart)`, run:

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 80 --no-pager'
```

…and abort. Restore from the Step 7.2 backup if needed.

### Step 7.4 — [ ] Curl smoke — user /traffic shape

Pick a real user token. If you don't already have one from a browser session, grab one via:

```bash
# Option A — extract from your own browser localStorage (incognito on hiearth.ru)
# Option B — DB read: get an active token from user_sessions
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT token FROM user_sessions WHERE expires_at > NOW() ORDER BY created_at DESC LIMIT 1;\\\"\""
```

Then:

```bash
USER_TOKEN=<paste>
curl -s "https://hiearth.ru/api/v1/user/traffic" -H "Authorization: Bearer $USER_TOKEN" | python -m json.tool
```

Expected top-level keys: `subscription`, `cycle`, `periods`. Inside `periods`: `day`, `week`, `month`, `year`. Inside each period: `used_bytes` plus `by_day` (or `by_month` for year). Inside `cycle`: `started_at`, `ends_at`, `used_bytes`, `limit_bytes`, `percent`, `exceeded`.

Cross-check `cycle.limit_bytes`:
- For an unlimited plan user → `0`.
- For a limited plan user → `> 0` equal to the user_subscriptions.traffic_limit override (if set) or plans.traffic_limit_bytes.

If the response is missing fields or returns the legacy shape (`traffic_used`/`traffic_limit`/`by_day` at top level), STOP — the binary on Hub is from before Task 2. Re-run Step 7.3.

### Step 7.5 — [ ] Curl smoke — admin /users/{id}/traffic shape

```bash
ADM_TOKEN=$(curl -s -X POST https://admin.hiearth.ru/api/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"HeYearth2024!"}' \
  | python -c "import sys,json; print(json.load(sys.stdin)['token'])")
USER_ID=$(curl -s "https://admin.hiearth.ru/api/admin/users?limit=1&sort=created_at&dir=desc" \
  -H "Authorization: Bearer $ADM_TOKEN" \
  | python -c "import sys,json; print(json.load(sys.stdin)['items'][0]['id'])")
curl -s "https://admin.hiearth.ru/api/admin/users/$USER_ID/traffic" \
  -H "Authorization: Bearer $ADM_TOKEN" | python -m json.tool
```

Expected: same `{subscription, cycle, periods}` shape as Step 7.4. Verify both endpoints emit byte-identical structure (e.g. via `diff <(...user...) <(...admin same user...)` — modulo ordering, the bag of keys should match).

### Step 7.6 — [ ] Curl smoke — happ Subscription-Userinfo header

The user-facing subscribe URL is `/api/v1/user/subscribe/{subscribe_token}` (token is the user-scoped subscribe identifier, NOT the auth token).

```bash
SUB_TOKEN=$(curl -s "https://admin.hiearth.ru/api/admin/users/$USER_ID" \
  -H "Authorization: Bearer $ADM_TOKEN" \
  | python -c "import sys,json; print(json.load(sys.stdin).get('subscribe_token',''))")
# If the admin endpoint doesn't expose subscribe_token, fall back to DB:
# go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT subscribe_token::text FROM users WHERE id = $USER_ID;\\\"\""

curl -sI "https://hiearth.ru/api/v1/user/subscribe/$SUB_TOKEN" | grep -i 'subscription-userinfo'
```

Expected: a line shaped like
`Subscription-Userinfo: upload=0; download=<N>; total=<L>; expire=<UNIX>`.

Verify by case:
- **Unlimited user**: `total=0`. happ will display `<N> / ∞`.
- **Limited user**: `total=<plan_limit>` (or override). happ will display `<N> / <L>`.
- **`expire`**: must equal `cycle.ends_at` from Step 7.4 converted to unix seconds, NOT `valid_until`. Verify:

```bash
python -c "import datetime; print(int(datetime.datetime.fromisoformat('<cycle.ends_at without Z>').replace(tzinfo=datetime.timezone.utc).timestamp()))"
```

…and compare with the `expire=` value. They must match.

### Step 7.7 — [ ] Deploy frontends

```bash
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --admin-web
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --vue-site
```

Expected for each: `Building...` → `Uploading dist/...` → `Done`. Confirm fresh bundle hashes are served:

```bash
curl -s https://hiearth.ru/         | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1
curl -s https://admin.hiearth.ru/   | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1
```

Expected: two distinct hash strings, neither matching the bundle that was served before this deploy (record the previous hashes earlier if you want a strict diff).

### Step 7.8 — [ ] Functional cutoff test (manual but recommended)

Verifies the Phase B Task 4 quota-cutoff path end-to-end on production: take a real user, give them a 1-byte limit, insert one row > 1 byte, confirm the user's UUID drops from the Xray config within ~10s.

```bash
# Step A — pick a test user and capture their UUID
TEST_UID=$(cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT user_id FROM users WHERE id = $USER_ID;\\\"\"" | head -1)
echo "Test UUID: $TEST_UID"

# Step B — override their active subscription's traffic_limit to 1 byte
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"UPDATE user_subscriptions SET traffic_limit = 1 WHERE user_id = $USER_ID AND valid_until > NOW();\\\"\""

# Step C — insert a traffic_stats row exceeding 1 byte for this user
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"INSERT INTO traffic_stats(user_id, upload_bytes, download_bytes, recorded_at) VALUES ('$TEST_UID', 1024, 0, NOW());\\\"\""

# Step D — wait for the debounced sync (~2s) plus a safety margin, then read the live xray config
sleep 8
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "grep -c '$TEST_UID' /opt/heyearth/state/xray/config.json || echo 'UUID not present in xray config'"

# Step E — cleanup: restore the user's limit and remove the synthetic stat row
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"UPDATE user_subscriptions SET traffic_limit = NULL WHERE user_id = $USER_ID;\\\"\""
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"DELETE FROM traffic_stats WHERE user_id = '$TEST_UID' AND upload_bytes = 1024 AND recorded_at > NOW() - INTERVAL '5 minutes';\\\"\""
```

Expected during Step D: either the literal output `UUID not present in xray config` or `grep -c` returns `0`. If it returns a positive count, the cutoff isn't wired correctly — re-check `xray_sync.go` and `checkTrafficLimits` from Phase B Task 4.

After Step E, on the next sync (≤5min from collector, or trigger one via touching another row) the user is re-included. Sanity check:

```bash
sleep 10
cd C:/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --ssh "grep -c '$TEST_UID' /opt/heyearth/state/xray/config.json"
```

Expected: `> 0`.

### Step 7.9 — [ ] Close Beads tasks + epic + push

Close all 8 issues with rationale, store the cross-session knowledge nugget, and push to remote.

```bash
bd close heyearthproject-ue5l --reason "biz/traffic.go helpers added (CycleWindow, PeriodUsage, PeriodBreakdownByDay/Month, EffectiveLimit, IsCycleExceeded, ListQuotaExceededUUIDs, BuildTrafficResponse); go build+go vet clean"
bd close heyearthproject-hu5g --reason "User /api/v1/user/traffic + admin /api/admin/users/{id}/traffic now return unified {subscription, cycle, periods} shape; curl-verified on prod (Step 7.4 + 7.5)"
bd close heyearthproject-urn7 --reason "buildSubscriptionUserinfo emits cycle download + total=0 for unlimited + expire=cycle_end; header inspected via curl (Step 7.6)"
bd close heyearthproject-vilj --reason "xray_sync.go filters via biz.ListQuotaExceededUUIDs; checkTrafficLimits triggers RequestXraySync; manual quota cutoff verified UUID drop in /opt/heyearth/state/xray/config.json (Step 7.8)"
bd close heyearthproject-1evg --reason "site components/TrafficCard.jsx live in DashboardPage; bundle hash captured Step 7.7"
bd close heyearthproject-nedr --reason "admin_web components/user-drawer/TrafficCard.jsx live in UserDrawer Traffic+Payments tab; bundle hash captured Step 7.7"
bd close heyearthproject-2tv6 --reason "Deploy + smoke complete (Steps 7.1-7.8); all 6 sub-tasks verified end-to-end on hiearth.ru"
bd close heyearthproject-1hwm --reason "Traffic limits & visibility feature shipped 2026-05-25. Spec docs/superpowers/specs/2026-05-25-traffic-limits-design.md. Plan docs/superpowers/plans/2026-05-25-traffic-limits.md."

bd remember --key traffic-limits-cycle "Traffic counting & limits shipped 2026-05-25 (epic heyearthproject-1hwm). Cycle = 1 calendar month from user_subscriptions.started_at. Helpers in internal/biz/traffic.go: CycleWindow, PeriodUsage, PeriodBreakdownByDay/Month, EffectiveLimit (sub.traffic_limit override > plans.traffic_limit_bytes > 0/unlimited), IsCycleExceeded, ListQuotaExceededUUIDs (single CTE), BuildTrafficResponse (shared by user+admin endpoints). API: GET /api/v1/user/traffic and GET /api/admin/users/{id}/traffic return same {subscription, cycle, periods} shape with 4 period buckets (day=calendar-today, week=rolling-7d, month=current-cycle, year=rolling-365d). buildSubscriptionUserinfo in subscription_handler.go reports cycle usage + total=0 for unlimited (happ shows X/∞) + expire=cycle_end. xray_sync.go filters quota-exceeded UUIDs from inbound clients arrays via biz.ListQuotaExceededUUIDs(); dynamic check, no persistent flag, auto-restored on cycle reset. checkTrafficLimits triggers RequestXraySync() for ~2s debounced cutoff. Frontend: site components/TrafficCard.jsx + admin_web components/user-drawer/TrafficCard.jsx — same UX, period switcher (день/неделя/месяц/год default месяц), cycle progress bar (hidden for unlimited), bar chart by_day or by_month, summary row. Schema unchanged — existing traffic_stats(user_id, upload/download, recorded_at) used for all aggregation. Legacy user_subscriptions.traffic_used kept for audit only."

git push origin main
```

Expected: each `bd close` prints a confirmation. `bd remember` returns the stored key. `git push` reports `<old>..<new>  main -> main` with 6 commits pushed.

### Step 7.10 — [ ] Done

Feature is live. The user can now:
- Open `hiearth.ru/dashboard` and switch periods on the Traffic card.
- Admin can open a user drawer → Трафик+Платежи tab and see the same UX.
- happ subscribers see `X / ∞` for unlimited plans and `X / Y · NN%` for limited ones; the `expire` indicator counts down to the next cycle reset.
- A user who exhausts their cycle limit drops from Xray inbound clients within ≤2s of the offending traffic_stats row; they automatically rejoin when their next billing cycle starts.

## Self-review

**Spec coverage** (`docs/superpowers/specs/2026-05-25-traffic-limits-design.md`):
- ✅ Subscription-cycle counting (1 calendar month from `started_at`) → T1 helpers `CycleWindow` + `monthsBetween` + `addMonths`.
- ✅ Display periods day/week/month/year → T2 `BuildTrafficResponse` returns all 4.
- ✅ Default month → T5 + T6 `useState('month')` initial.
- ✅ Cycle progress bar only when limited → T5 + T6 conditional render `{!isUnlimited && <progress>}`.
- ✅ happ `total=0` for unlimited → T3 always emits `total=%d` with effLimit=0 when unlimited.
- ✅ happ `expire=cycle_end` → T3 sets `expireAt = cycleEnd` (preferring `valid_until` if earlier).
- ✅ Cutoff via Xray sync → T4 filter quotaExceeded map from inbound clients arrays.
- ✅ Dynamic check, no persistent flag, auto-restore → T1 `ListQuotaExceededUUIDs` is a single CTE; sync runs every 5min + immediate trigger from collector.
- ✅ Admin endpoint same shape → T2 `ListUserTrafficHandler` delegates to same biz helper.
- ✅ Deploy + curl smoke + manual quota test → T7.

**Placeholder scan**: no «TBD» / «TODO» / «implement later». Every step has either a complete code block or an exact command. Where line numbers may shift, grep commands precede the edit so the engineer locates the line first.

**Type / API consistency**:
- `TrafficResponse` struct identical between user endpoint (`cmd/api`) and admin endpoint (`internal/admin`) — both delegate to `biz.BuildTrafficResponse(userDBID, userUUID)`.
- `cycle.limit_bytes == 0` consistently means "unlimited" in backend response, happ header (`total=0`), and frontend conditional render.
- `started_at` field present in `subscription` object but only used for display; cycle math uses it via `biz.CycleWindow` which is also called internally during `BuildTrafficResponse`.
- `userUUID` (TEXT users.user_id) used everywhere `traffic_stats` is queried (since `traffic_stats.user_id` is the UUID). `userDBID` (INT users.id) used everywhere `user_subscriptions` is queried.
- Beads IDs `heyearthproject-ue5l..2tv6` consistent between header, commit messages, and close commands.

**Risk acknowledgements** (re-confirmed from spec):
- **Cycle math correctness**: PostgreSQL `EXTRACT(MONTH FROM age(NOW(), started_at))` handles month-edge cases (Jan 31 → Feb 28) the same way as Go's `addMonths` helper (which clamps to last-day-of-target-month). T1 includes a psql evidence step verifying the canonical case.
- **xray_sync.go cutoff scope**: Phase B subagent reading the file noted that practical cutoff is achievable for VLESS Reality + VLESS XHTTP (shared `vlessClients` slice + per-user routing rules). Shadowsocks uses a shared password (not per-UUID, can't cut off individual users without major refactor). SOCKS/HTTP have separate per-user account mechanism — should be checked during T4 execution and either covered or documented as a follow-up. **Documented limitation; spec acceptance criterion 5 may need scope adjustment after T4 execution.**
- **happ `expire` field**: now reflects cycle_end (or valid_until if earlier). For yearly plans on an unlimited tariff this means expire rolls every 30 days. happ uses this for re-fetch timing only; dashboard separately shows the subscription's hard end date. Documented in spec.
- **Performance**: `ListQuotaExceededUUIDs()` is a single CTE running on every 5-min sync. With ~1k users and existing `traffic_stats(user_id, recorded_at)` index, expected <100ms. Monitor after deploy.

**Scope check**: 7 tasks fit in one implementation plan. Wave parallelization keeps wall time low. No need to decompose into sub-plans.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-25-traffic-limits.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — 7 tasks with explicit wave dependencies. T1 first (everyone depends on it). Then T2 || T3 || T4 in parallel. Then T5 || T6 in parallel. Finally T7. Fast wall time with subagent dispatches.

**2. Inline Execution** — execute in this session sequentially with checkpoints. Same dependency order: T1 → (T2,T3,T4) → (T5,T6) → T7. Recommended only when you want to stay involved at every step.

**Which approach?**
