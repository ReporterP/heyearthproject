# Traffic Limits & Visibility — Design Spec

**Beads epic:** `heyearthproject-1hwm`
**Date:** 2026-05-25
**Status:** Draft → for user review

## Problem

Today traffic display and enforcement is broken in three places:

1. **Counting**: `user_subscriptions.traffic_used` accumulates from `started_at` and never resets. The dashboard shows "subscription-lifetime usage", not "this month". Users with long subscriptions see ever-growing numbers that exceed their monthly quota even though they never used the proxy much last month.
2. **Limit enforcement**: `checkTrafficLimits()` only logs when a user exceeds the all-time limit. There is no actual cutoff — the proxy keeps working past the quota.
3. **happ display**: `Subscription-Userinfo` header omits `total` for unlimited plans, so happ has no usage indicator at all. Competitors show "2 GB / ∞" for unlimited users; we show nothing.

User feedback (verbatim):
> «Лимит считается не за период, а вообще за всё время. Хочу динамику за день / неделю / месяц / год, дефолт месяц. И чтобы по истечении лимита пользователь отключался. И чтобы даже на безлимите happ показывал израсходовано.»

## Goal

Replace subscription-lifetime counting with **billing-cycle counting** (cycle = 1 calendar month from `started_at`). Show per-period dynamics (day / week / month / year) in both admin and user portal, default month. Cutoff VPN traffic when current-cycle limit is hit. happ shows usage for both limited and unlimited plans (`total=0` standard convention for "∞").

## Concepts

### Billing cycle

A subscription started at `started_at`. The current cycle is:

```
months_passed = floor( age(NOW, started_at) in months )
cycle_start   = started_at + months_passed * INTERVAL '1 month'
cycle_end     = cycle_start + INTERVAL '1 month'
```

Example: subscription started 2026-04-15 14:00. Today is 2026-05-25 14:00. months_passed = 1. cycle_start = 2026-05-15 14:00. cycle_end = 2026-06-15 14:00.

For a subscription that started today and is younger than 1 month, `cycle_start = started_at`.

If a subscription has no `started_at` (legacy data), treat `cycle_start = NOW() - INTERVAL '30 days'` as a graceful fallback.

### Cycle usage

```sql
SELECT COALESCE(SUM(upload_bytes + download_bytes), 0)
FROM traffic_stats
WHERE user_id = (users.user_id of the subscription owner)
  AND recorded_at >= cycle_start
  AND recorded_at < cycle_end
```

### Display periods

| Period | Window | Source |
|--------|--------|--------|
| `day`   | `recorded_at >= date_trunc('day', NOW())` | calendar today |
| `week`  | `recorded_at >= NOW() - INTERVAL '7 days'` | rolling 7d |
| `month` | `recorded_at >= cycle_start AND < cycle_end` | **billing cycle** (consistent with limit) |
| `year`  | `recorded_at >= NOW() - INTERVAL '365 days'` | rolling 365d |

For each period the response also includes a per-day breakdown (`by_day: [{day, bytes}]`) for the chart. The `year` period uses per-month buckets (`by_month: [{month, bytes}]`) since 365 daily bars are too many for a 600px chart.

### Limit

`plans.traffic_limit_bytes`:
- `NULL` or `0` → **unlimited**
- `> 0` → cycle quota in bytes

The interpretation is fixed: limit is per-cycle (cycle = subscription month). No schema change; only the meaning of the existing column changes.

`user_subscriptions.traffic_limit` (per-row override) is kept for admin overrides. If set on a subscription, it wins over the plan's limit. Same per-cycle interpretation.

### Cutoff

Cutoff = exclusion from Xray inbound clients (VLESS Reality, VLESS XHTTP, Shadowsocks, SOCKS, HTTP). The user is removed from `clients` arrays in the generated Xray config. Their browsing dashboard, payment, promocode redemption, devices, and admin actions on themselves continue to work.

The cutoff is **automatic** and **dynamic** — there's no persistent `quota_exceeded` flag. Each Xray sync run computes per-user `cycle_used >= cycle_limit` from `traffic_stats`. When a new cycle starts (`NOW >= cycle_end`), the next sync picks up cycle_used=0 and re-includes the user.

A new column would let us read state in one query instead of recomputing, but with ~1k users and an existing index on `traffic_stats(user_id, recorded_at)` the live aggregation is <50ms. YAGNI.

## API

### `GET /api/v1/user/traffic` (revised response)

```json
{
  "subscription": {
    "started_at": "2026-04-15T14:00:00Z",
    "valid_until": "2027-04-15T14:00:00Z",
    "plan_name": "Спасти лес",
    "is_unlimited": false
  },
  "cycle": {
    "started_at": "2026-05-15T14:00:00Z",
    "ends_at":    "2026-06-15T14:00:00Z",
    "used_bytes":  8500000000,
    "limit_bytes": 10737418240,
    "percent":     79.2,
    "exceeded":    false
  },
  "periods": {
    "day":   { "used_bytes": 1234567,      "by_day":   [{"day":"2026-05-25","bytes":1234567}] },
    "week":  { "used_bytes": 12000000000,  "by_day":   [{"day":"2026-05-19","bytes":1500000000}, ...] },
    "month": { "used_bytes": 8500000000,   "by_day":   [{"day":"2026-05-15","bytes":300000000}, ...] },
    "year":  { "used_bytes": 100000000000, "by_month": [{"month":"2026-05","bytes":8500000000}, ...] }
  }
}
```

`cycle.limit_bytes` is `0` when the plan is unlimited. The frontend uses this for "X / ∞" display vs progress bar.

### `GET /api/admin/users/{id}/traffic` (revised — same shape as user endpoint)

Admin can call for any user. Permission: `users.read`.

Currently this endpoint returns `{user_db_id, days, limit_bytes, total_bytes, by_day, by_country}` — replace entirely with the same `{subscription, cycle, periods}` shape. The `by_country` field stays empty `[]` (v1 limitation — `traffic_stats` has no `server_id`); kept for future use.

## Backend Changes

### `internal/biz/traffic.go` (new file)

Encapsulates all cycle math and aggregation helpers, so handlers stay thin.

```go
// CycleWindow computes the current billing cycle for a subscription start.
// Returns NOW-based fallback if startedAt is NULL.
func CycleWindow(startedAt sql.NullTime, now time.Time) (cycleStart, cycleEnd time.Time)

// PeriodUsage returns total bytes for `userUUID` within [from, to).
func PeriodUsage(userUUID string, from, to time.Time) (int64, error)

// PeriodBreakdownByDay returns daily totals for a window. Used by `day`, `week`, `month`.
func PeriodBreakdownByDay(userUUID string, from, to time.Time) ([]DayBucket, error)

// PeriodBreakdownByMonth — used by `year`.
func PeriodBreakdownByMonth(userUUID string, from, to time.Time) ([]MonthBucket, error)

// IsCycleExceeded returns true if cycle usage >= effective limit for this user.
// effective_limit = COALESCE(user_subscriptions.traffic_limit, plans.traffic_limit_bytes).
// Returns false for unlimited (0/NULL) plans.
func IsCycleExceeded(userDBID int) (bool, error)
```

### `cmd/api/main.go` (handleUserTraffic)

Replace the existing implementation with one that uses `biz.CycleWindow` + the 4 period helpers. Read `started_at` from the active subscription. Build the JSON shape above.

### `cmd/api/subscription_handler.go` (buildSubscriptionUserinfo)

Replace `traffic_used` (subscription-lifetime) with `cycleUsed` (this billing cycle). For unlimited plans set `total=0` — happ standard for "∞".

```go
func buildSubscriptionUserinfo(userID string) string {
    sub := getActiveSubscription(userID) // started_at, valid_until, effective_limit, user_db_id
    if sub == nil { return "" }
    cycleStart, cycleEnd := biz.CycleWindow(sub.StartedAt, time.Now())
    used, _ := biz.PeriodUsage(userID, cycleStart, cycleEnd)
    total := sub.EffectiveLimit // 0 for unlimited
    return fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d",
        used, total, cycleEnd.Unix())
}
```

Note: `expire` is now **end of current cycle**, not `valid_until` of subscription. This is intentional — happ then shows "Истекает 15 июня" (when the user's quota resets), which is the meaningful date for users on monthly plans. For yearly subscriptions on an unlimited plan, `expire` still rolls every 30 days; that's acceptable UX — it tells happ when to next-fetch the subscription, not when the account ends.

If we want the user to also see the subscription's hard end date, we can keep an optional second header `Subscription-Expire-At: <unix>` but happ may not surface it. Out of scope for v1; the dashboard already shows the subscription end date.

### `cmd/api/xray_sync.go` (quota cutoff)

Inside the function that builds the `clients` arrays for each inbound, add a filter step:

```go
quotaExceeded := biz.ListQuotaExceededUUIDs() // SELECT u.user_id WHERE cycle_used >= effective_limit AND limit > 0
clients = clients.filter(c => !quotaExceeded.has(c.UUID))
```

A new biz function `ListQuotaExceededUUIDs()`:

```sql
SELECT u.user_id
FROM users u
JOIN user_subscriptions us ON us.user_id = u.id AND us.valid_until > NOW()
LEFT JOIN plans p ON p.id = us.plan_id
WHERE COALESCE(us.traffic_limit, p.traffic_limit_bytes, 0) > 0
  AND (
    SELECT COALESCE(SUM(upload_bytes + download_bytes), 0)
    FROM traffic_stats
    WHERE user_id = u.user_id
      AND recorded_at >= GREATEST(
        us.started_at,
        us.started_at + (
          EXTRACT(YEAR FROM age(NOW(), us.started_at)) * 12 +
          EXTRACT(MONTH FROM age(NOW(), us.started_at))
        )::int * INTERVAL '1 month'
      )
  ) >= COALESCE(us.traffic_limit, p.traffic_limit_bytes)
```

This is computed once per Xray sync (every 5 minutes), returns ~0-5 UUIDs in practice. Performance OK with existing indexes.

When the user crosses quota, the next 5-min sync removes them. When the cycle resets, the next sync re-includes them. No restart needed.

### `cmd/api/traffic_stats.go` (checkTrafficLimits — replace)

The existing `checkTrafficLimits()` checks against `us.traffic_used` (subscription-lifetime). Replace with a call to `biz.ListQuotaExceededUUIDs()` + log + trigger `RequestXraySync()` so the cutoff happens within ~2 seconds after the offending traffic_stats row was inserted, not waiting for the next 5-min sync.

### `users.traffic_used` and `users.traffic_limit`

Existing columns are NOT removed. They remain as legacy/audit fields, no longer authoritative for limits or display. Migration is unnecessary — `traffic_used` will just keep growing as historical data. A follow-up cleanup task can decide whether to drop them later.

## Frontend Changes

### Site `DashboardPage.jsx`

Replace the existing single-progress-bar Traffic section with a card containing:

```
┌─ Трафик ─────────────────────────────────────┐
│  [день] [неделя] [месяц✓] [год]              │  ← period switcher
│                                              │
│  8.5 GB / 10 GB              ━━━━━━━━━━ 79%  │  ← cycle progress (only if limit)
│  Цикл: 15.05 → 15.06.2026                    │
│                                              │
│  ▁▃▆█▆▃▁▅▇▆▃▁▂▄█▇▅▂▁▃▅▇▆▄▂▁▃▆█▇             │  ← by_day chart (height = bytes)
│                                              │
│  День: 1.2 GB · Неделя: 12 GB · Год: 100 GB  │  ← all-period summary
└──────────────────────────────────────────────┘
```

For unlimited plans:
- "8.5 GB / ∞" (no percent, no progress bar)
- Bar chart still shows daily breakdown
- The "День / Неделя / Год" summary still works

### Admin `UserDrawer` → Traffic+Payments tab

Same period switcher + chart in the existing tab. The Payments section below stays unchanged. Endpoint changes from the current `/api/admin/users/{id}/traffic?days=30` to the new shape (no `days` query param).

### Site `api/user.js` + Admin `api/admin.js`

Adjust the existing `getTrafficStats(token)` and `fetchUserTraffic(id)` to consume the new response shape. No new endpoints — just response evolves.

## Acceptance Criteria

1. `traffic_used` on the user portal **resets every cycle** (subscription month from `started_at`).
2. Dashboard period switcher works for day / week / month / year, default month.
3. Bar chart renders daily breakdown (year uses monthly buckets).
4. Admin user drawer Traffic+Payments tab shows the same period switcher + chart.
5. When user exceeds cycle limit, within ≤5 minutes the user's UUID is removed from all Xray inbound clients (VLESS Reality, VLESS XHTTP, SOCKS, HTTP, Shadowsocks).
6. happ Subscription-Userinfo shows `download=<cycle_used>; total=<cycle_limit>` for limited plans, and `total=0` for unlimited plans → happ displays "X / ∞".
7. `expire=` in happ header is the **end of current cycle**, not `valid_until`.
8. Period query on `traffic_stats` runs in <100ms with existing index (`user_id, recorded_at`).
9. Backend `go build ./... && go vet ./...` clean.
10. Frontend `npm run build` (site + admin_web) clean.

## Risks

- **Cycle-end interpretation for happ `expire`**: users on yearly plans will see the date roll every 30 days. We surface this in dashboard's separate "Подписка действует до DD.MM.YYYY" indicator (already there). Edge case: monthly billing cycle ≠ purchase anniversary if month lengths differ (Feb→Mar shifts by 2-3 days). PostgreSQL handles this correctly via `INTERVAL '1 month'` arithmetic.
- **Quota cutoff race**: between traffic collector → cycle exceed → next Xray sync there's a ~2-second debounce window. User can burn a few more MB during it. Acceptable.
- **Yearly plan with monthly cycle**: if `annual-premium` (365 days, 3000₽) has `traffic_limit_bytes=0` → unlimited → cycle math still runs (computes 30d window) but limit check skipped. Bar chart works. No issue.
- **Daylight savings / TZ**: all timestamps in DB are TIMESTAMPTZ stored UTC. `started_at + INTERVAL '1 month'` works correctly across DST. `date_trunc('day', NOW())` uses server timezone (UTC) — display in dashboard formats to local on the client side. Acceptable.
- **Legacy subscriptions with NULL `started_at`**: fallback to `NOW - 30 days` means their cycle is the last 30 days. Once they get a new subscription with `started_at`, normal cycle math kicks in.

## Out of Scope

- Configurable cycle period (always 1 month — no admin override for 14-day / 2-month cycles)
- Per-protocol quota (Reality vs SOCKS uses different bytes)
- Quota usage email notifications (happ shows it; no separate channel)
- Historical re-aggregation if `traffic_stats` has gaps (we use what's there)
- Migration to drop `users.traffic_used` / `users.traffic_limit` (future cleanup)

## Self-review

**Placeholder scan**: no TBD / TODO. Every section either points to existing files or specifies the new code shape.

**Internal consistency**:
- `cycle` semantics identical across happ header, admin endpoint, user endpoint, frontend display.
- `expire` in happ = `cycle_end`, consistent with the "limit resets per cycle" promise.
- `total=0` semantics for unlimited consistent with happ's competitor convention (confirmed by screenshot).

**Ambiguity check**:
- "Period = day" → calendar today (00:00 server time, UTC). Documented.
- "Period = week" → last 7 days rolling, NOT ISO week. Documented.
- "Period = month" → current billing cycle, NOT calendar month. Documented.
- "Period = year" → last 365 days rolling, NOT calendar year. Documented.
- "Limit = unlimited" → either `NULL` or `0` in DB. Documented.

**Scope check**: one cohesive feature, one plan, ~5-7 implementation tasks.

## Execution

Next step: invoke `superpowers:writing-plans` to decompose into tasks. Plan likely 5-7 tasks:

1. Backend — `biz/traffic.go` helpers (CycleWindow, PeriodUsage, breakdowns, IsCycleExceeded, ListQuotaExceededUUIDs)
2. Backend — replace `handleUserTraffic` and admin `ListUserTrafficHandler` with new shape
3. Backend — `buildSubscriptionUserinfo` switch to cycle data + total=0 for unlimited
4. Backend — `xray_sync.go` exclude quota-exceeded UUIDs from clients; replace `checkTrafficLimits()` to trigger sync
5. Frontend — site `DashboardPage.jsx` period switcher + bar chart
6. Frontend — admin `UserDrawer` → `TrafficPaymentsTab` period switcher + bar chart (same component if reusable)
7. Deploy + smoke (curl new endpoints, happ subscribe URL shows total=0 for unlimited, quota cutoff manual test)

Total estimate: ~5-6 hours focused work.
