# Users + Subscriptions UX Merge — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Unify the Users + Subscriptions admin workflow into a single Master + Drawer pattern (Linear/Vercel style). Master list shows 7 columns per user including an inline 30-day traffic bar; clicking a row opens a right-side drawer with three tabs (Профиль / Подписки / Трафик+Платежи). On mobile (<768px) the drawer becomes a fullscreen modal. The `/subscriptions` page is renamed to «История подписок» and becomes a global audit view that deep-links into the users drawer.

**Architecture:** Master list at `/users` with status filter tabs and rich row content. Click → URL state (`?open=<id>&tab=<name>`) + drawer panel slides in. Drawer is a new `<Drawer>` primitive in `components/ui/`, mobile-fullscreen via the same pattern as `<Dialog>`. Three tab components live under `components/user-drawer/`. Backend extends `ListUsersHandler` with two new LATERAL joins (last_seen, traffic_30d) and adds one new endpoint `GET /api/admin/users/{id}/traffic`. No migration.

**Tech Stack:** Go 1.21 (backend, raw `database/sql` + lib/pq), PostgreSQL 15 (existing schema, no migration), React 19 + Vite + Tailwind v4 (admin_web, JSX no TypeScript), react-router-dom v6, native fetch, lucide-react icons, cmdk for command palette.

**Epic:** `heyearthproject-unpl` — Unify Users + Subscriptions UX in admin
**Sub-tasks:** `heyearthproject-bcgm` (T1), `heyearthproject-vuqa` (T2), `heyearthproject-fijd` (T3), `heyearthproject-hiav` (T4), `heyearthproject-i76g` (T5), `heyearthproject-2fyl` (T6)
**Spec:** [docs/superpowers/specs/2026-05-25-users-subs-merge-design.md](../specs/2026-05-25-users-subs-merge-design.md)

---

## File Structure

### Backend — Create

None. The new traffic endpoint lives alongside existing handlers in `users_handlers.go`.

### Backend — Modify

- `server/backend/internal/admin/users_handlers.go` — extend `ListUsersHandler` SELECT (2 new LATERAL joins, 3 new payload fields, 2 new sort whitelist entries, 2 new status filter values). Add new `ListUserTrafficHandler` function.
- `server/backend/internal/admin/routes.go` — add `/api/admin/users/{id}/traffic` to the existing `/api/admin/users/` route dispatcher (next to the `/devices` suffix branch).

### Frontend — Create

- `admin_web/src/components/ui/Drawer.jsx` — right slide-in panel primitive, fullscreen on `<md`.
- `admin_web/src/components/UserDrawer.jsx` — drawer composition with tabs router + per-user state.
- `admin_web/src/components/user-drawer/ProfileTab.jsx` — inline-edit user fields.
- `admin_web/src/components/user-drawer/SubscriptionsTab.jsx` — subscription history table + inline create form.
- `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx` — 30d traffic chart + payment list.

### Frontend — Modify

- `admin_web/src/pages/UsersPage.jsx` — complete rewrite: 7-column table with inline traffic bar, status filter tabs (Все/Активные/Истекают/Без подписки/Заблокированные), drawer wiring, URL state (`open` + `tab`), J/K keyboard nav.
- `admin_web/src/pages/SubscriptionsPage.jsx` — refactor to «История подписок»: drop create button, add expiring-in-N-days + with-promocode filters, row click → navigate to /users with drawer params.
- `admin_web/src/components/Sidebar.jsx` — relabel «Подписки» → «История подписок».
- `admin_web/src/components/commands.js` — relabel `go-subscriptions` Cmd+K entry.
- `admin_web/src/i18n/ru.js` + `admin_web/src/i18n/en.js` — new translation keys (drawer titles, tab labels, button labels, history filter labels).
- `admin_web/src/api/admin.js` — add 3 thin wrappers: `fetchUserSubscriptions(userDbId)`, `fetchUserPayments(userDbId)`, `fetchUserTraffic(userDbId, days)`.
- `admin_web/src/components/ui/index.js` — export the new `Drawer` primitive.

### No Deletions

All existing files are reused or refactored. The old Devices modal in `UsersPage.jsx` migrates to a quick-action button in the drawer header — same modal, different invocation point.

---
## Task 1: Backend — ListUsers extension + traffic endpoint (≈60 min)

**Beads:** `heyearthproject-bcgm`

**Files:**
- Modify: `server/backend/internal/admin/types.go` — extend `adminUserRow` struct with `LastSeen`, `Traffic30dBytes`, `TrafficLimitBytes` (≈ lines 65-77)
- Modify: `server/backend/internal/admin/users_handlers.go` — extend `ListUsersHandler` (lines 13-124); append `ListUserTrafficHandler` at end of file
- Modify: `server/backend/internal/admin/routes.go:32-45` — extend the `/api/admin/users/` dispatcher with `/traffic` suffix branch

**Schema notes (read before coding):**
- `traffic_stats` columns are `(user_id TEXT FK → users.user_id, upload_bytes BIGINT, download_bytes BIGINT, recorded_at TIMESTAMPTZ)` — NOT `(day, bytes_used)` as the spec sketch suggested. Use `recorded_at::date AS day` and `SUM(upload_bytes + download_bytes)` for totals. Foreign key is `users.user_id` (TEXT UUID), not `users.id` (INT).
- `traffic_stats` has **no `server_id` / `exit_node_id` column** → by-country breakdown cannot be derived directly from data today. Return empty `by_country: []` in v1 with a `// TODO: per-country breakdown requires schema change` comment. This keeps the response shape stable for the frontend; v2 can populate it once `traffic_stats.server_id` is added.
- `user_devices.last_seen_at` exists (verified via spec line 196). Cast for sort safety: `MAX(ud.last_seen_at) AS last_seen`.
- `plans.traffic_limit_bytes` exists (confirmed in seed data + spec line 191).

---

### Step 1.1 — [ ] Extend `adminUserRow` struct

`server/backend/internal/admin/types.go` — after the existing `ValidUntil` field (line 76), add 3 fields:

```go
type adminUserRow struct {
	ID                int           `json:"id"`
	UserID            string        `json:"user_id"`
	Username          string        `json:"username"`
	TelegramID        sql.NullInt64 `json:"-"`
	Role              string        `json:"role"`
	IsBlocked         bool          `json:"is_blocked"`
	PreferredPlatform string        `json:"preferred_platform"`
	CreatedAt         time.Time     `json:"-"`
	UpdatedAt         time.Time     `json:"-"`
	CurrentPlan       string        `json:"current_plan"`
	ValidUntil        sql.NullTime  `json:"-"`
	LastSeen          sql.NullTime  `json:"-"`
	Traffic30dBytes   int64         `json:"traffic_30d_bytes"`
	TrafficLimitBytes int64         `json:"traffic_limit_bytes"`
}
```

### Step 1.2 — [ ] Extend `sortCol` whitelist

`server/backend/internal/admin/users_handlers.go`, the `allowedSort` map (lines 46-53):

```go
allowedSort := map[string]string{
	"id":                "u.id",
	"user_id":           "u.user_id",
	"username":          "u.username",
	"created_at":        "u.created_at",
	"updated_at":        "u.updated_at",
	"role":              "u.role",
	"last_seen":         "ls.last_seen",
	"traffic_30d_bytes": "t30.total_bytes",
	"valid_until":       "us.valid_until",
}
```

Then in the ORDER BY string at line 83 — when `sortCol` is `ls.last_seen`, append `NULLS LAST` so NULL last_seen rows sink. Replace the existing `ORDER BY %s %s, u.id DESC` template:

```go
nullsClause := ""
if sortCol == "ls.last_seen" || sortCol == "t30.total_bytes" || sortCol == "us.valid_until" {
	nullsClause = " NULLS LAST"
}
query := fmt.Sprintf(`
	... (SELECT body — see step 1.4) ...
	WHERE %s
	ORDER BY %s %s%s, u.id DESC
	LIMIT $%d OFFSET $%d
`, where, sortCol, dir, nullsClause, argN, argN+1)
```

### Step 1.3 — [ ] Extend `status` filter

Same file, the `if p.Status == ...` block (lines 27-33). Add two cases:

```go
if p.Status == "active" {
	where += ` AND COALESCE(u.is_blocked, FALSE) = FALSE
		AND EXISTS (SELECT 1 FROM user_subscriptions us_f
		            WHERE us_f.user_id = u.id AND us_f.valid_until > NOW())`
} else if p.Status == "blocked" {
	where += " AND COALESCE(u.is_blocked, FALSE) = TRUE"
} else if p.Status == "expiring" {
	where += ` AND COALESCE(u.is_blocked, FALSE) = FALSE
		AND EXISTS (SELECT 1 FROM user_subscriptions us_f
		            WHERE us_f.user_id = u.id
		              AND us_f.valid_until BETWEEN NOW() AND NOW() + INTERVAL '7 days')`
} else if p.Status == "no_plan" {
	where += ` AND NOT EXISTS (SELECT 1 FROM user_subscriptions us_f
		            WHERE us_f.user_id = u.id AND us_f.valid_until > NOW())`
}
```

Note: `COUNT(*)` query at line 36 already uses `where`, so the new predicates automatically participate in the count.

### Step 1.4 — [ ] Add LATERAL joins to the SELECT

Same file, replace the `query := fmt.Sprintf(...)` block (lines 67-85) with:

```go
query := fmt.Sprintf(`
	SELECT u.id, u.user_id, COALESCE(u.username, ''), u.telegram_id, u.role, COALESCE(u.is_blocked, FALSE),
	       COALESCE(u.preferred_platform, ''),
	       u.created_at, COALESCE(u.updated_at, u.created_at),
	       COALESCE(p.name, 'Нет подписки') AS current_plan,
	       us.valid_until,
	       ls.last_seen,
	       COALESCE(t30.total_bytes, 0) AS traffic_30d_bytes,
	       COALESCE(p.traffic_limit_bytes, 0) AS traffic_limit_bytes
	FROM users u
	LEFT JOIN LATERAL (
		SELECT us2.plan_id, us2.valid_until
		FROM user_subscriptions us2
		WHERE us2.user_id = u.id AND us2.valid_until > NOW()
		ORDER BY us2.valid_until DESC
		LIMIT 1
	) us ON TRUE
	LEFT JOIN plans p ON p.id = us.plan_id
	LEFT JOIN LATERAL (
		SELECT MAX(ud.last_seen_at) AS last_seen
		FROM user_devices ud
		WHERE ud.user_id = u.id
	) ls ON TRUE
	LEFT JOIN LATERAL (
		SELECT COALESCE(SUM(ts.upload_bytes + ts.download_bytes), 0) AS total_bytes
		FROM traffic_stats ts
		WHERE ts.user_id = u.user_id
		  AND ts.recorded_at >= NOW() - INTERVAL '30 days'
	) t30 ON TRUE
	WHERE %s
	ORDER BY %s %s%s, u.id DESC
	LIMIT $%d OFFSET $%d
`, where, sortCol, dir, nullsClause, argN, argN+1)
```

Note `ts.user_id = u.user_id` — `traffic_stats.user_id` is TEXT UUID joined to `users.user_id`, not `users.id`.

### Step 1.5 — [ ] Update Scan + payload map

Same file, update `rows.Scan(...)` (line 98) to read 3 additional columns, and extend the payload map (lines 102-121):

```go
var row adminUserRow
if err := rows.Scan(
	&row.ID, &row.UserID, &row.Username, &row.TelegramID, &row.Role, &row.IsBlocked, &row.PreferredPlatform,
	&row.CreatedAt, &row.UpdatedAt, &row.CurrentPlan, &row.ValidUntil,
	&row.LastSeen, &row.Traffic30dBytes, &row.TrafficLimitBytes,
); err != nil {
	writeAdminError(w, http.StatusInternalServerError, "Failed to scan user")
	return
}
payload := map[string]any{
	"id":                  row.ID,
	"user_id":             row.UserID,
	"username":            row.Username,
	"role":                row.Role,
	"is_blocked":          row.IsBlocked,
	"preferred_platform":  row.PreferredPlatform,
	"current_plan":        row.CurrentPlan,
	"created_at":          row.CreatedAt.Format(time.RFC3339),
	"updated_at":          row.UpdatedAt.Format(time.RFC3339),
	"traffic_30d_bytes":   row.Traffic30dBytes,
	"traffic_limit_bytes": row.TrafficLimitBytes,
}
if row.TelegramID.Valid {
	payload["telegram_id"] = row.TelegramID.Int64
}
if row.ValidUntil.Valid {
	payload["valid_until"] = row.ValidUntil.Time.Format(time.RFC3339)
} else {
	payload["valid_until"] = ""
}
if row.LastSeen.Valid {
	payload["last_seen"] = row.LastSeen.Time.Format(time.RFC3339)
} else {
	payload["last_seen"] = nil
}
users = append(users, payload)
```

### Step 1.6 — [ ] Append `ListUserTrafficHandler`

Append to the end of `server/backend/internal/admin/users_handlers.go`:

```go
// ListUserTrafficHandler — GET /api/admin/users/{id}/traffic?days=30
// Returns daily upload+download breakdown for the user (joined via users.user_id),
// total bytes over the window, the user's current plan limit, and a per-country
// breakdown (empty in v1 — traffic_stats lacks a server_id column).
func ListUserTrafficHandler(w http.ResponseWriter, r *http.Request) {
	userDbID, ok := parseUsersTrafficID(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}

	// Look up user_id (TEXT UUID) and current plan limit.
	var userTextID string
	var limitBytes int64
	err := db.DB.QueryRow(`
		SELECT u.user_id, COALESCE(p.traffic_limit_bytes, 0)
		FROM users u
		LEFT JOIN LATERAL (
			SELECT us2.plan_id
			FROM user_subscriptions us2
			WHERE us2.user_id = u.id AND us2.valid_until > NOW()
			ORDER BY us2.valid_until DESC
			LIMIT 1
		) us ON TRUE
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE u.id = $1
	`, userDbID).Scan(&userTextID, &limitBytes)
	if err != nil {
		writeAdminError(w, http.StatusNotFound, "user not found")
		return
	}

	// Daily breakdown.
	rows, err := db.DB.Query(`
		SELECT recorded_at::date AS day,
		       COALESCE(SUM(upload_bytes + download_bytes), 0) AS bytes
		FROM traffic_stats
		WHERE user_id = $1
		  AND recorded_at >= NOW() - ($2 || ' days')::interval
		GROUP BY day
		ORDER BY day
	`, userTextID, fmt.Sprintf("%d", days))
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "traffic query error: "+err.Error())
		return
	}
	defer rows.Close()

	type dayRow struct {
		Day   string `json:"day"`
		Bytes int64  `json:"bytes"`
	}
	byDay := []dayRow{}
	var total int64
	for rows.Next() {
		var d time.Time
		var b int64
		if err := rows.Scan(&d, &b); err != nil {
			continue
		}
		byDay = append(byDay, dayRow{Day: d.Format("2006-01-02"), Bytes: b})
		total += b
	}

	// Per-country breakdown — placeholder. traffic_stats has no server_id /
	// exit_node_id column, so we cannot attribute bytes to an exit country.
	// TODO: when traffic_stats grows a server_id column, join servers.country_code
	// and SUM(upload+download) GROUP BY country_code ORDER BY bytes DESC.
	byCountry := []map[string]any{}

	writeAdminJSON(w, http.StatusOK, map[string]any{
		"user_db_id":  userDbID,
		"days":        days,
		"limit_bytes": limitBytes,
		"total_bytes": total,
		"by_day":      byDay,
		"by_country":  byCountry,
	})
}

// parseUsersTrafficID extracts {id} from "/api/admin/users/{id}/traffic".
func parseUsersTrafficID(path string) (int, bool) {
	trimmed := strings.TrimSuffix(path, "/")
	if !strings.HasSuffix(trimmed, "/traffic") {
		return 0, false
	}
	rest := strings.TrimSuffix(trimmed, "/traffic")
	idx := strings.LastIndex(rest, "/")
	if idx < 0 {
		return 0, false
	}
	id, err := strconv.Atoi(rest[idx+1:])
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
```

Add `"strconv"` to the imports if not already present (currently `users_handlers.go` imports only encoding/json, fmt, net/http, strings, time, plus `internal/db` — `strconv` is new).

### Step 1.7 — [ ] Register route in `routes.go`

`server/backend/internal/admin/routes.go` lines 32-45 — extend the `/api/admin/users/` dispatcher. Insert a `/traffic` suffix branch before the existing `/devices` branch:

```go
http.HandleFunc("/api/admin/users/", AuthMiddleware(func(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/traffic") {
		RequirePermission("users.read", ListUserTrafficHandler).ServeHTTP(w, r)
		return
	}
	if strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/devices") {
		RequirePermission("users.read", ListUserDevicesHandler).ServeHTTP(w, r)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		RequirePermission("users.write", UpdateUserHandler).ServeHTTP(w, r)
	case http.MethodDelete:
		RequirePermission("users.write", DeleteUserHandler).ServeHTTP(w, r)
	default:
		writeAdminError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}))
```

### Step 1.8 — [ ] Evidence

Run:

```bash
cd server/backend && go build ./... && go vet ./internal/admin/...
```

Expected: exit code 0, no output.

Then sanity-check the new SQL parses (no need to start API; the build covers compilation). If a dev backend is conveniently running:

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://localhost:8000/api/admin/users?page=1&page_size=5&sort=last_seen&dir=desc" \
  | jq '.items[0] | {id, last_seen, traffic_30d_bytes, traffic_limit_bytes}'
```

Expected: each item exposes `last_seen` (RFC3339 or null), `traffic_30d_bytes` (int), `traffic_limit_bytes` (int).

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://localhost:8000/api/admin/users/1/traffic?days=7" | jq .
```

Expected JSON keys: `user_db_id`, `days`, `limit_bytes`, `total_bytes`, `by_day` (array), `by_country` (empty array).

```bash
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://localhost:8000/api/admin/users?status=expiring&page_size=5" | jq '.total'
```

Expected: integer count (may be 0 in dev).

If no dev backend is available locally, the `go build` + `go vet` clean is sufficient evidence at the unit level; deferred deploy verification to Task 6 (deploy + smoke).

### Step 1.9 — [ ] Commit

```bash
git add server/backend/internal/admin/users_handlers.go \
        server/backend/internal/admin/types.go \
        server/backend/internal/admin/routes.go

git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(backend): extend ListUsers with last_seen/traffic_30d + add /users/{id}/traffic endpoint

ListUsersHandler now returns three extra fields per row — last_seen
(MAX over user_devices.last_seen_at), traffic_30d_bytes (SUM over
traffic_stats upload+download for the last 30 days), and
traffic_limit_bytes (current plan's limit). Two new sort columns
(last_seen, traffic_30d_bytes) and two new status filters (expiring,
no_plan) wired through the same whitelist + WHERE.

New ListUserTrafficHandler at GET /api/admin/users/{id}/traffic?days=N
returns daily totals + current plan limit. by_country is currently an
empty array because traffic_stats has no server_id column — populated
in a follow-up once the schema grows that link.

Powers the master-list traffic bar and the drawer Трафик tab in the
Users + Subscriptions merge.

Beads: heyearthproject-bcgm
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Frontend — Drawer ui primitive (≈45 min)

**Beads:** `heyearthproject-vuqa`

**Files:**
- Create: `admin_web/src/components/ui/Drawer.jsx`
- Modify: `admin_web/src/components/ui/index.js` — add `Drawer` export

### Step 2.1 — [ ] Create `Drawer.jsx`

Right-side slide-in panel above md, fullscreen on <md. Models after `<Dialog>` (which is already mobile-fullscreen via `rounded-none + h-full + sm:rounded-...`). Theme tokens only — no slate/gray/zinc, no `shadow-*` (mobile-fullscreen drawer has no shadow; desktop drawer relies on hairline border + backdrop dimmer instead of a drop shadow). Geist inherited from root.

```jsx
import { useEffect } from 'react'
import { X } from 'lucide-react'

const WIDTH_CLASSES = {
  md:   'sm:max-w-md',
  lg:   'sm:max-w-lg',
  xl:   'sm:max-w-xl',
  '2xl':'sm:max-w-2xl',
}

/**
 * Drawer — right-side slide-in panel above md, fullscreen modal on <md.
 *
 * Props:
 *   open       boolean
 *   onClose    () => void   — called on backdrop click, ESC, ✕
 *   title      string | ReactNode
 *   subtitle   string (optional)
 *   actions    ReactNode (optional) — header right-side, e.g. quick buttons
 *   width      'md' | 'lg' | 'xl' | '2xl'   default 'xl' (≈576px)
 *   children   ReactNode — body content (scrollable)
 *
 * Behavior:
 *   - ESC closes via window keydown listener.
 *   - Backdrop click closes (desktop only — on mobile the drawer is the screen).
 *   - Body scroll locked while open; original overflow restored on close/unmount.
 *   - Hairline border-l on the left edge above md.
 *   - aria-modal="true", role="dialog", aria-label from title (when string).
 */
export function Drawer({ open, onClose, title, subtitle, actions, width = 'xl', children }) {
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose?.() }
    window.addEventListener('keydown', onKey)
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      window.removeEventListener('keydown', onKey)
      document.body.style.overflow = prev
    }
  }, [open, onClose])

  if (!open) return null

  const widthClass = WIDTH_CLASSES[width] || WIDTH_CLASSES.xl

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      {/* Backdrop — desktop only. On mobile the drawer is fullscreen so no dim layer is needed. */}
      <div
        className="absolute inset-0 bg-canvas/80 backdrop-blur-sm hidden sm:block"
        onClick={onClose}
        aria-hidden="true"
      />
      <aside
        role="dialog"
        aria-modal="true"
        aria-label={typeof title === 'string' ? title : 'Drawer'}
        className={`relative bg-elevated flex flex-col
          w-full h-full
          sm:h-screen sm:border-l sm:border-default
          ${widthClass}`}
        onClick={(e) => e.stopPropagation()}
      >
        <header className="flex items-start gap-3 p-4 border-b border-default flex-shrink-0">
          <div className="flex-1 min-w-0">
            {typeof title === 'string'
              ? <h2 className="text-base font-semibold text-primary tracking-tight truncate">{title}</h2>
              : title}
            {subtitle && (
              <p className="text-xs text-tertiary mt-0.5 truncate">{subtitle}</p>
            )}
          </div>
          {actions && (
            <div className="flex items-center gap-1 flex-shrink-0">
              {actions}
            </div>
          )}
          <button
            type="button"
            onClick={onClose}
            aria-label="Закрыть"
            className="p-1 rounded hover:bg-hover text-secondary hover:text-primary flex-shrink-0"
          >
            <X className="w-4 h-4" />
          </button>
        </header>
        <div className="flex-1 overflow-y-auto">{children}</div>
      </aside>
    </div>
  )
}
```

Verify against the theme-lock STOP catches before saving:
- No `slate-*` / `gray-*` / `zinc-*` — only `bg-elevated`, `border-default`, `text-primary`, `text-secondary`, `text-tertiary`, `bg-canvas`, `bg-hover`. ✓
- No `Inter` / `Roboto` / `Arial` font references — Geist inherited. ✓
- No `shadow-*` / `drop-shadow-*` — hairline `border-l border-default` only. ✓
- Corners: `rounded` (4px) on the close hover bg only; the panel itself has no rounded corners (it goes screen-edge to screen-edge vertically). ✓
- `aria-label="Закрыть"` on the ✕ icon-only button. ✓

### Step 2.2 — [ ] Export from `index.js`

`admin_web/src/components/ui/index.js` — append the new export so consumers can `import { Drawer } from '../components/ui'`:

```js
export { Button } from './Button'
export { Input, Textarea, Select } from './Input'
export { Card, CardHeader, CardBody } from './Card'
export { Dialog } from './Dialog'
export { Drawer } from './Drawer.jsx'
export { Badge } from './Badge'
export { Table, Thead, Tbody, Tr, Th, Td } from './Table'
export { Skeleton, TableSkeleton } from './Skeleton'
export { EmptyState } from './EmptyState'
export { Spinner } from './Spinner'
export { Kbd } from './Kbd'
export { PageHeader } from './PageHeader'
```

The other entries are extension-less because their modules resolve via Vite default-resolution; we keep `.jsx` on the new line because it matches the file we just created and the project CLAUDE.md says ".jsx extension явная в imports". Both styles coexist in the file already (matter of taste); the explicit form is the rule for new code.

### Step 2.3 — [ ] Evidence

The Drawer is not yet consumed by any page, so visual verification is deferred to Task 3 (UserDrawer composition). Build-time evidence is sufficient here:

```bash
cd admin_web && npm run build
```

Expected: build succeeds (`vite build` exits 0), `dist/` regenerated, no `slate-`/`gray-`/`zinc-` warnings, no missing-import errors for `Drawer`.

Sanity-check the export wired in:

```bash
node -e "import('./admin_web/src/components/ui/index.js').then(m => console.log(typeof m.Drawer))"
```

Expected: `function` (the named export resolves). If this fails because of bare Node ESM resolution rules, skip — the `npm run build` already exercises Vite's resolver and is the authoritative check.

### Step 2.4 — [ ] Commit

```bash
git add admin_web/src/components/ui/Drawer.jsx \
        admin_web/src/components/ui/index.js

git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): Drawer ui primitive — right slide-in panel, fullscreen on mobile

New <Drawer> primitive for the Users + Subscriptions merge. Above md
it's a right-aligned panel (max-w-md/lg/xl/2xl) with a hairline left
border and a dimmed backdrop. Below md it occupies the full viewport
the same way <Dialog> does, so the underlying master list is hidden
and the drawer becomes the screen.

ESC and backdrop-click both close. Body scroll is locked while open
and restored on unmount. Icon-only ✕ button carries aria-label.

Theme tokens only — no slate/gray/zinc, no shadow utilities — matches
the existing admin theme lock.

Consumed in the next task by UserDrawer; not yet wired into any page.

Beads: heyearthproject-vuqa
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Task 3: UserDrawer + Profile/Subscriptions/TrafficPayments tabs (~90 min)

**Beads:** `heyearthproject-fijd`

**Files:**
- Modify: `admin_web/src/api/admin.js` (add 3 wrappers)
- Create: `admin_web/src/components/UserDrawer.jsx`
- Create: `admin_web/src/components/user-drawer/ProfileTab.jsx`
- Create: `admin_web/src/components/user-drawer/SubscriptionsTab.jsx`
- Create: `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx`

### - [ ] Step 3.1 — Add API wrappers to `admin_web/src/api/admin.js`

The existing file uses `apiFetch` (returns parsed JSON, authorization handled inside `client.js`) and `pagedFetch` (for `?page=&pageSize=` endpoints). Match that style — do NOT hand-roll `fetch()` with `authHeaders()`. Insert the new wrappers right after the existing `fetchUserDevices` helper (around line 28) so all user-scoped helpers stay grouped.

Open `admin_web/src/api/admin.js` and edit the `// ── Users ──` section. After `fetchUserDevices` add:

```js
export function fetchUserSubscriptions(userDbId) {
  return pagedFetch('/subscriptions', { user_db_id: userDbId, page: 1, pageSize: 100, sort: 'valid_until', dir: 'desc' })
}
export function fetchUserPayments(userDbId) {
  return pagedFetch('/payments', { user_db_id: userDbId, page: 1, pageSize: 50, sort: 'created_at', dir: 'desc' })
}
export function fetchUserTraffic(userDbId, days = 30) {
  return apiFetch(`/users/${userDbId}/traffic?days=${days}`)
}
```

Notes:
- Backend `ListSubscriptionsHandler` and `ListPaymentsHandler` must accept `user_db_id` as a query filter — this was added in Task 1 (Phase A). If a wrapper fails with 400 the backend filter is missing, not the wrapper.
- `fetchUserTraffic` returns `{ user_db_id, days, limit_bytes, total_bytes, by_day, by_country }`. `by_country` is always `[]` in v1 because `traffic_stats` has no `server_id` column.

### - [ ] Step 3.2 — Create `admin_web/src/components/UserDrawer.jsx`

This is the composition layer: it owns the active tab state, header actions (Devices / Block / Delete), and routes between the 3 tab components. The user object is passed in as a prop — UsersPage already has it in the list, no re-fetch needed.

```jsx
import { useState, useEffect } from 'react'
import { Trash2, Smartphone } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { updateUser, deleteUser, fetchUserDevices } from '../api/admin.js'
import { Drawer } from './ui/Drawer.jsx'
import { Button, Badge, Dialog } from './ui/index.js'
import ConfirmDialog from './ConfirmDialog.jsx'
import ProfileTab from './user-drawer/ProfileTab.jsx'
import SubscriptionsTab from './user-drawer/SubscriptionsTab.jsx'
import TrafficPaymentsTab from './user-drawer/TrafficPaymentsTab.jsx'

const TABS = [
  { id: 'profile',       key: 'drawerTabProfile' },
  { id: 'subscriptions', key: 'drawerTabSubs' },
  { id: 'traffic',       key: 'drawerTabTraffic' },
]

export default function UserDrawer({ user, defaultTab = 'profile', onClose, onUserMutated }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [activeTab, setActiveTab] = useState(defaultTab)
  const [devicesOpen, setDevicesOpen] = useState(false)
  const [devicesItems, setDevicesItems] = useState([])
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [busy, setBusy] = useState(false)

  // Sync activeTab when defaultTab changes (e.g. URL state ?tab=subscriptions)
  useEffect(() => { setActiveTab(defaultTab) }, [defaultTab, user?.id])

  if (!user) return null

  async function openDevices() {
    try {
      const devs = await fetchUserDevices(user.id)
      const items = Array.isArray(devs) ? devs : (Array.isArray(devs?.devices) ? devs.devices : [])
      setDevicesItems(items)
      setDevicesOpen(true)
    } catch (err) {
      toast.error(err.message)
    }
  }

  async function toggleBlock() {
    setBusy(true)
    try {
      await updateUser(user.id, { is_blocked: !user.is_blocked })
      toast.success(!user.is_blocked ? (t.quickBlock + ' OK') : (t.quickUnblock + ' OK'))
      onUserMutated?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    setBusy(true)
    try {
      await deleteUser(user.id)
      toast.success(t.delete + ' OK')
      setConfirmDelete(false)
      onUserMutated?.()
      onClose?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  const headerActions = (
    <div className="flex items-center gap-2 flex-wrap">
      <Button size="sm" variant="ghost" onClick={openDevices} icon={<Smartphone className="w-3.5 h-3.5" />}>
        Devices
      </Button>
      {can('users.write') && (
        <button
          onClick={toggleBlock}
          disabled={busy}
          title={user.is_blocked ? t.quickUnblock : t.quickBlock}
          aria-label={user.is_blocked ? t.quickUnblock : t.quickBlock}
          className={`relative inline-flex h-5 w-9 items-center rounded-full transition ${user.is_blocked ? 'bg-danger/30' : 'bg-success/30'}`}
        >
          <span className={`inline-block h-3.5 w-3.5 rounded-full transition-transform ${user.is_blocked ? 'translate-x-1 bg-danger' : 'translate-x-[18px] bg-success'}`} />
        </button>
      )}
      {can('users.write') && (
        <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirmDelete(true)} icon={<Trash2 className="w-3.5 h-3.5" />}>
          {t.delete}
        </Button>
      )}
    </div>
  )

  const subtitle = (
    <span className="flex items-center gap-2 text-xs">
      <span className="font-mono text-tertiary">{user.user_id}</span>
      <span className="text-tertiary">·</span>
      <Badge variant={user.is_blocked ? 'danger' : (user.plan ? 'accent' : 'default')}>
        {user.is_blocked ? 'blocked' : (user.plan || 'no plan')}
      </Badge>
    </span>
  )

  return (
    <>
      <Drawer
        open={!!user}
        onClose={onClose}
        title={user.username || user.user_id}
        subtitle={subtitle}
        actions={headerActions}
      >
        {/* Tabs strip */}
        <nav className="flex items-center gap-1 border-b border-default px-1 -mx-4 mb-4 sticky top-0 bg-canvas z-10">
          {TABS.map(tab => (
            <button
              key={tab.id}
              data-tab={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={`px-3 py-2 text-sm font-medium rounded-t transition relative ${
                activeTab === tab.id
                  ? 'text-accent bg-accent/10'
                  : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {t[tab.key]}
              {activeTab === tab.id && (
                <span className="absolute left-0 right-0 -bottom-px h-0.5 bg-accent" />
              )}
            </button>
          ))}
        </nav>

        {/* Active tab body */}
        <div className="space-y-4">
          {activeTab === 'profile'       && <ProfileTab user={user} onSaved={onUserMutated} />}
          {activeTab === 'subscriptions' && <SubscriptionsTab user={user} onChanged={onUserMutated} />}
          {activeTab === 'traffic'       && <TrafficPaymentsTab user={user} />}
        </div>
      </Drawer>

      {/* Devices modal — reuses existing Dialog primitive, not nested in Drawer */}
      <Dialog
        open={devicesOpen}
        onClose={() => setDevicesOpen(false)}
        title={t.registeredDevicesLabel(devicesItems.length)}
        maxWidth="max-w-md"
      >
        {devicesItems.length === 0 ? (
          <p className="text-secondary text-sm">{t.usersNoDevices || 'No devices'}</p>
        ) : (
          <div className="space-y-2 max-h-60 overflow-y-auto">
            {devicesItems.map((d, i) => (
              <div key={i} className="text-sm bg-elevated rounded p-3 border border-default">
                <p className="font-mono text-xs text-tertiary">{d.device_id}</p>
                <p className="text-primary">{d.platform} {d.app_version}</p>
                {d.last_seen && <p className="text-xs text-tertiary">{t.deviceLastSeen}: {new Date(d.last_seen).toLocaleString()}</p>}
              </div>
            ))}
          </div>
        )}
        <div className="flex justify-end mt-4">
          <Button variant="ghost" onClick={() => setDevicesOpen(false)}>{t.close}</Button>
        </div>
      </Dialog>

      <ConfirmDialog
        open={confirmDelete}
        message={t.deleteUserConfirm(user.user_id || user.id)}
        onConfirm={handleDelete}
        onCancel={() => setConfirmDelete(false)}
        loading={busy}
      />
    </>
  )
}
```

### - [ ] Step 3.3 — Create `admin_web/src/components/user-drawer/ProfileTab.jsx`

Inline-edit pattern: each row has a value + pencil icon. Clicking the pencil swaps the value cell for an Input with Save/Cancel. Save commits via `updateUser(user.id, { [field]: value })` and propagates via `onSaved`.

```jsx
import { useState } from 'react'
import { Pencil, Check, X } from 'lucide-react'
import { useT } from '../../i18n/index.jsx'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { updateUser } from '../../api/admin.js'
import { Input, Select, Button } from '../ui/index.js'
import { formatDate } from '../../utils/format.js'

function EditableField({ label, value, mono, type = 'text', options, canEdit, onSave }) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(value ?? '')
  const [busy, setBusy] = useState(false)

  async function commit() {
    setBusy(true)
    try {
      await onSave(draft)
      setEditing(false)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="grid grid-cols-[140px_1fr] gap-3 items-start py-2 border-b border-default last:border-b-0">
      <div className="text-sm text-tertiary pt-1.5">{label}</div>
      {!editing ? (
        <div className="flex items-center gap-2 min-h-[32px]">
          <span className={`text-sm text-primary ${mono ? 'font-mono' : ''}`}>
            {value || <span className="text-tertiary">—</span>}
          </span>
          {canEdit && (
            <button
              onClick={() => { setDraft(value ?? ''); setEditing(true) }}
              aria-label="Редактировать"
              className="opacity-0 group-hover:opacity-100 hover:bg-hover rounded p-1 transition"
            >
              <Pencil className="w-3 h-3 text-tertiary" />
            </button>
          )}
        </div>
      ) : (
        <div className="flex items-center gap-1">
          {options ? (
            <Select value={draft} onChange={e => setDraft(e.target.value)} className="flex-1">
              {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </Select>
          ) : (
            <Input
              type={type}
              value={draft}
              onChange={e => setDraft(e.target.value)}
              autoFocus
              className="flex-1"
            />
          )}
          <Button size="sm" variant="primary" loading={busy} onClick={commit} aria-label="Сохранить" icon={<Check className="w-3.5 h-3.5" />} />
          <Button size="sm" variant="ghost" onClick={() => setEditing(false)} aria-label="Отмена" icon={<X className="w-3.5 h-3.5" />} />
        </div>
      )}
    </div>
  )
}

export default function ProfileTab({ user, onSaved }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const canEdit = can('users.write')

  async function patch(field, value) {
    try {
      await updateUser(user.id, { [field]: value })
      toast.success(t.save + ' OK')
      onSaved?.()
    } catch (err) {
      toast.error(err.message)
      throw err
    }
  }

  return (
    <div className="group">
      <EditableField label={t.fieldUsername}  value={user.username}    canEdit={canEdit} onSave={v => patch('username', v)} />
      <EditableField label={t.password}        value=""                  canEdit={canEdit} type="password" onSave={v => patch('password', v)} />
      <EditableField label={t.fieldRole}       value={user.role}         canEdit={canEdit} options={[{ value: 'user', label: 'User' }, { value: 'premium', label: 'Premium' }]} onSave={v => patch('role', v)} />
      <EditableField label="Telegram ID"       value={user.telegram_id}  mono canEdit={canEdit} onSave={v => patch('telegram_id', v)} />
      <EditableField label={t.fieldUserId}     value={user.user_id}      mono canEdit={false} />
      <EditableField label={t.fieldCreatedAt}  value={formatDate(user.created_at)} canEdit={false} />
      {user.updated_at && (
        <EditableField label="Обновлён" value={formatDate(user.updated_at)} canEdit={false} />
      )}
    </div>
  )
}
```

### - [ ] Step 3.4 — Create `admin_web/src/components/user-drawer/SubscriptionsTab.jsx`

Inline create form (toggled by [+ Подписка] button — NOT a separate Dialog) + history table.

```jsx
import { useState, useEffect, useCallback } from 'react'
import { Plus, X } from 'lucide-react'
import { useT } from '../../i18n/index.jsx'
import { useAuth } from '../../auth/AuthContext.jsx'
import { useToast } from '../Toast.jsx'
import { fetchUserSubscriptions, saveSubscription, deleteSubscription, fetchPlans } from '../../api/admin.js'
import { Button, Input, Select, Badge, Table, Thead, Tbody, Tr, Th, Td, EmptyState } from '../ui/index.js'
import ConfirmDialog from '../ConfirmDialog.jsx'
import { formatDateShort } from '../../utils/format.js'

const STATUS_VARIANT = { active: 'success', expired: 'danger', cancelled: 'default', pending_renewal: 'warning' }
const STATUS_OPTIONS = [
  { value: 'active', label: 'active' },
  { value: 'expired', label: 'expired' },
  { value: 'cancelled', label: 'cancelled' },
  { value: 'pending_renewal', label: 'pending_renewal' },
]

export default function SubscriptionsTab({ user, onChanged }) {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [items, setItems] = useState([])
  const [plans, setPlans] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState(null)
  const [busy, setBusy] = useState(false)

  // Inline create form state
  const [creating, setCreating] = useState(false)
  const [draft, setDraft] = useState({ plan_id: '', valid_until_date: '', auto_renew: true, status: 'active' })

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchUserSubscriptions(user.id)
      .then(data => setItems(data.items || []))
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [user.id])

  useEffect(() => { load() }, [load])
  useEffect(() => { fetchPlans().then(p => setPlans(Array.isArray(p) ? p : [])).catch(() => {}) }, [])

  function openCreate() {
    const defaultDate = new Date()
    defaultDate.setDate(defaultDate.getDate() + 30)
    setDraft({
      plan_id: plans[0]?.id || '',
      valid_until_date: defaultDate.toISOString().slice(0, 10),
      auto_renew: true,
      status: 'active',
    })
    setCreating(true)
  }

  async function submitCreate(e) {
    e.preventDefault()
    setBusy(true)
    try {
      await saveSubscription({
        user_db_id: user.id,
        plan_id: Number(draft.plan_id) || null,
        valid_until: draft.valid_until_date ? new Date(draft.valid_until_date).toISOString() : null,
        auto_renew: !!draft.auto_renew,
        status: draft.status,
      })
      toast.success(t.create + ' OK')
      setCreating(false)
      load()
      onChanged?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  async function handleDelete() {
    setBusy(true)
    try {
      await deleteSubscription(confirm.id)
      toast.success(t.delete + ' OK')
      setConfirm(null)
      load()
      onChanged?.()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-primary">{t.drawerTabSubs}</h3>
        {can('subscriptions.write') && !creating && (
          <Button size="sm" variant="primary" onClick={openCreate} icon={<Plus className="w-3.5 h-3.5" />}>
            {t.drawerAddSub || '+ Подписка'}
          </Button>
        )}
      </div>

      {creating && (
        <form onSubmit={submitCreate} className="rounded-md border border-default bg-elevated p-3 space-y-2">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            <label className="text-xs text-tertiary">
              {t.fieldPlan || 'Тариф'}
              <Select value={draft.plan_id} onChange={e => setDraft({ ...draft, plan_id: e.target.value })} className="w-full mt-1">
                <option value="">—</option>
                {plans.map(p => <option key={p.id} value={p.id}>{p.name} ({p.price_rub}₽ / {p.duration_days}д)</option>)}
              </Select>
            </label>
            <label className="text-xs text-tertiary">
              {t.fieldValidUntil || 'Действует до'}
              <Input type="date" value={draft.valid_until_date} onChange={e => setDraft({ ...draft, valid_until_date: e.target.value })} className="w-full mt-1" />
            </label>
            <label className="text-xs text-tertiary">
              {t.fieldStatus || 'Статус'}
              <Select value={draft.status} onChange={e => setDraft({ ...draft, status: e.target.value })} className="w-full mt-1">
                {STATUS_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </Select>
            </label>
            <label className="text-xs text-tertiary flex items-center gap-2 mt-5">
              <input type="checkbox" checked={draft.auto_renew} onChange={e => setDraft({ ...draft, auto_renew: e.target.checked })} />
              {t.fieldAutoRenew || 'Авто-продление'}
            </label>
          </div>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" type="button" onClick={() => setCreating(false)}>{t.cancel}</Button>
            <Button size="sm" variant="primary" type="submit" loading={busy}>{t.create}</Button>
          </div>
        </form>
      )}

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
      ) : items.length === 0 ? (
        <EmptyState title={t.noSubscriptions || 'Подписок нет'} description={t.noSubscriptionsDesc || 'Добавьте первую через кнопку выше'} />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th>{t.fieldPeriod || 'Период'}</Th>
              <Th>{t.fieldPlan || 'План'}</Th>
              <Th align="right">{t.fieldPrice || 'Цена'}</Th>
              <Th>{t.fieldPromo || 'Промокод'}</Th>
              <Th>{t.fieldStatus || 'Статус'}</Th>
              <Th></Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map(s => (
              <Tr key={s.id}>
                <Td className="text-xs">
                  {formatDateShort(s.created_at)} → {formatDateShort(s.valid_until)}
                </Td>
                <Td>{s.plan_name || s.plan || '—'}</Td>
                <Td mono align="right">{s.price_rub != null ? `${s.price_rub}₽` : '—'}</Td>
                <Td mono className="text-xs">{s.promocode || '—'}</Td>
                <Td><Badge variant={STATUS_VARIANT[s.status] || 'default'}>{s.status}</Badge></Td>
                <Td align="right">
                  {can('subscriptions.write') && (
                    <button
                      onClick={() => setConfirm(s)}
                      aria-label={t.delete}
                      className="text-tertiary hover:text-danger transition p-1 rounded hover:bg-hover"
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  )}
                </Td>
              </Tr>
            ))}
          </Tbody>
        </Table>
      )}

      <ConfirmDialog
        open={!!confirm}
        message={t.deleteSubscriptionConfirm ? t.deleteSubscriptionConfirm(confirm?.id) : `Удалить подписку #${confirm?.id}?`}
        onConfirm={handleDelete}
        onCancel={() => setConfirm(null)}
        loading={busy}
      />
    </div>
  )
}
```

### - [ ] Step 3.5 — Create `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx`

Two stacked sections. Traffic chart is CSS-only (no chart library): scale each day's bytes against max value, render as vertical bar via inline `height` style. Below: total / limit % progress. Country breakdown only when `by_country.length > 0` (spec: v1 returns empty array — render «Нет данных по странам»).

```jsx
import { useState, useEffect, useCallback, useMemo } from 'react'
import { useT } from '../../i18n/index.jsx'
import { fetchUserTraffic, fetchUserPayments } from '../../api/admin.js'
import { formatBytes, formatDate } from '../../utils/format.js'
import { Badge } from '../ui/index.js'

export default function TrafficPaymentsTab({ user }) {
  const { t } = useT()
  const [traffic, setTraffic] = useState(null)
  const [payments, setPayments] = useState([])
  const [loadingT, setLoadingT] = useState(true)
  const [loadingP, setLoadingP] = useState(true)
  const [error, setError] = useState('')

  const loadTraffic = useCallback(() => {
    setLoadingT(true)
    fetchUserTraffic(user.id, 30)
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

  const maxDayBytes = useMemo(() => {
    if (!traffic?.by_day?.length) return 0
    return Math.max(...traffic.by_day.map(d => d.bytes || 0), 1)
  }, [traffic])

  const usedPct = traffic && traffic.limit_bytes > 0
    ? Math.min(100, Math.round((traffic.total_bytes / traffic.limit_bytes) * 100))
    : null

  return (
    <div className="space-y-6">
      {/* ── Traffic section ─────────────────────────────────────── */}
      <section>
        <header className="flex items-center justify-between mb-2">
          <h3 className="text-sm font-semibold text-primary">{t.drawerTrafficTitle || 'Трафик за 30 дней'}</h3>
          {traffic && (
            <span className="text-xs text-tertiary font-mono tabular-nums">
              {formatBytes(traffic.total_bytes)}
              {traffic.limit_bytes > 0 && ` / ${formatBytes(traffic.limit_bytes)} (${usedPct}%)`}
            </span>
          )}
        </header>

        {loadingT ? (
          <p className="text-sm text-tertiary">{t.loading || 'Загрузка…'}</p>
        ) : !traffic || !traffic.by_day?.length ? (
          <p className="text-sm text-tertiary">{t.drawerNoTraffic || 'За последние 30 дней трафика нет.'}</p>
        ) : (
          <>
            {/* Daily bar chart — pure CSS */}
            <div className="flex items-end gap-px h-24 bg-canvas border border-default rounded p-2">
              {traffic.by_day.map(d => {
                const pct = maxDayBytes > 0 ? (d.bytes / maxDayBytes) * 100 : 0
                return (
                  <div
                    key={d.day}
                    title={`${d.day}: ${formatBytes(d.bytes)}`}
                    className="flex-1 bg-info/60 hover:bg-info rounded-sm transition min-w-0"
                    style={{ height: `${Math.max(pct, 2)}%` }}
                  />
                )
              })}
            </div>

            {/* Limit overall progress bar */}
            {traffic.limit_bytes > 0 && (
              <div className="mt-2">
                <div className="h-1.5 bg-canvas border border-default rounded-full overflow-hidden">
                  <div
                    className={`h-full transition-all ${usedPct >= 90 ? 'bg-danger' : usedPct >= 70 ? 'bg-warning' : 'bg-info'}`}
                    style={{ width: `${usedPct}%` }}
                  />
                </div>
              </div>
            )}

            {/* By-country breakdown (v1: always empty) */}
            <div className="mt-4">
              <h4 className="text-xs uppercase tracking-wide text-tertiary mb-2">
                {t.drawerByCountry || 'По странам'}
              </h4>
              {traffic.by_country?.length > 0 ? (
                <div className="space-y-1">
                  {traffic.by_country.map(c => (
                    <div key={c.country} className="flex items-center justify-between text-sm">
                      <span className="text-primary">{c.country}</span>
                      <span className="font-mono text-tertiary tabular-nums">{formatBytes(c.bytes)}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <p className="text-xs text-tertiary italic">
                  {t.drawerByCountryEmpty || 'Распределение по странам пока недоступно'}
                </p>
              )}
            </div>
          </>
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

### - [ ] Step 3.6 — Evidence

Run:
```
cd admin_web && npm run build
```
Expected: build succeeds with no errors, no unresolved imports. The drawer is not yet wired into a page (that's Task 4), so only compile-correctness is verified here.

If `t.drawerTabProfile` / `t.drawerTabSubs` / `t.drawerTabTraffic` are missing in i18n, the build still compiles (object lookups become `undefined` and render as empty string). i18n keys are added in Task 4.1. To smoke-test rendering before then, add a temporary `<UserDrawer user={{id: 1, username: 'test', user_id: 'abc-123', role: 'user', is_blocked: false}} onClose={() => {}} />` to `App.jsx` and `npm run dev` — remove the temp mount before commit.

### - [ ] Step 3.7 — Commit

```bash
git add admin_web/src/api/admin.js admin_web/src/components/UserDrawer.jsx admin_web/src/components/user-drawer/
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): UserDrawer + Profile/Subscriptions/Traffic tabs

Composition layer over the new Drawer primitive. UserDrawer owns active-tab
state, devices modal, block toggle and delete action. Three focused tab
components handle Profile (inline-edit), Subscriptions (history + inline
create form) and Traffic+Payments (CSS-only chart + payment list).

Adds three thin API wrappers (fetchUserSubscriptions, fetchUserPayments,
fetchUserTraffic) reusing the existing apiFetch/pagedFetch helpers — no new
auth plumbing.

by_country breakdown gracefully degrades to «нет данных» until backend can
join traffic_stats with server country.

Beads: heyearthproject-fijd
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: UsersPage rewrite (~60 min)

**Beads:** `heyearthproject-hiav`

**Files:**
- Modify (full rewrite): `admin_web/src/pages/UsersPage.jsx`
- Modify: `admin_web/src/i18n/ru.js`
- Modify: `admin_web/src/i18n/en.js`
- Modify: `admin_web/src/utils/format.js` (add `formatRelativeRu`)

Notes on scope:
- `fetchUsersPaged` in `api/admin.js` is already a pass-through over `pagedFetch('/users', params)` — it forwards whatever params we pass. No change required; the new `status=expiring|no_plan` and new sort columns (`last_seen`, `traffic_30d_bytes`) flow through automatically once Phase A backend lands.
- Status-tab live counts are intentionally **deferred** to a future iteration. v1 renders the tab labels without counts (see comment in code). Live counts would require either 4 extra paged fetches on mount or a new `/users/status-counts` endpoint — both out of scope.

### - [ ] Step 4.1 — Add i18n keys

Open `admin_web/src/i18n/ru.js` and add inside the existing translation object (sorted alphabetically alongside neighbours):

```js
// Drawer tabs
drawerTabProfile: 'Профиль',
drawerTabSubs: 'Подписки',
drawerTabTraffic: 'Трафик и платежи',
drawerAddSub: '+ Подписка',
drawerTrafficTitle: 'Трафик за 30 дней',
drawerPaymentsTitle: 'Платежи',
drawerByCountry: 'По странам',
drawerByCountryEmpty: 'Распределение по странам пока недоступно',
drawerNoTraffic: 'За последние 30 дней трафика нет.',
drawerNoPayments: 'Платежей нет.',

// Status filter tabs (replaces filterActive/filterBlocked which stay for back-compat)
statusActive: 'Активные',
statusExpiring: 'Истекают',
statusNoPlan: 'Без подписки',
statusBlocked: 'Заблокированные',

// New columns
columnLastSeen: 'Был онлайн',
columnTraffic30d: 'Трафик 30д',
columnValidUntil: 'Действует до',

// Subscription field labels (used by SubscriptionsTab)
fieldPlan: 'Тариф',
fieldValidUntil: 'Действует до',
fieldAutoRenew: 'Авто-продление',
fieldStatus: 'Статус',
fieldPeriod: 'Период',
fieldPrice: 'Цена',
fieldPromo: 'Промокод',

// Relative time labels
relMinAgo: 'мин',
relHourAgo: 'ч',
relDayAgo: 'дн',
```

Mirror in `admin_web/src/i18n/en.js`:

```js
drawerTabProfile: 'Profile',
drawerTabSubs: 'Subscriptions',
drawerTabTraffic: 'Traffic & Payments',
drawerAddSub: '+ Subscription',
drawerTrafficTitle: 'Traffic — last 30 days',
drawerPaymentsTitle: 'Payments',
drawerByCountry: 'By country',
drawerByCountryEmpty: 'Per-country breakdown is not available yet',
drawerNoTraffic: 'No traffic in the last 30 days.',
drawerNoPayments: 'No payments.',
statusActive: 'Active',
statusExpiring: 'Expiring',
statusNoPlan: 'No plan',
statusBlocked: 'Blocked',
columnLastSeen: 'Last seen',
columnTraffic30d: 'Traffic 30d',
columnValidUntil: 'Valid until',
fieldPlan: 'Plan',
fieldValidUntil: 'Valid until',
fieldAutoRenew: 'Auto-renew',
fieldStatus: 'Status',
fieldPeriod: 'Period',
fieldPrice: 'Price',
fieldPromo: 'Promo',
relMinAgo: 'min',
relHourAgo: 'h',
relDayAgo: 'd',
```

### - [ ] Step 4.2 — Add `formatRelativeRu` helper to `admin_web/src/utils/format.js`

Append to the existing file. Returns «2 мин» / «3 ч» / «6 дн» / `dd.MM.yy` if >30 days, «—» if empty.

```js
/**
 * Russian-localized relative time label suitable for table cells.
 * < 60 sec        → "1 мин"  (rounds up to 1)
 * < 60 min        → "12 мин"
 * < 24 h          → "3 ч"
 * <= 30 d         → "6 дн"
 * > 30 d or empty → falls back to formatDateShort / "—"
 */
export function formatRelativeRu(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const diffSec = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000))
  if (diffSec < 60)        return '1 мин'
  if (diffSec < 3600)      return `${Math.floor(diffSec / 60)} мин`
  if (diffSec < 86400)     return `${Math.floor(diffSec / 3600)} ч`
  if (diffSec < 86400 * 30) return `${Math.floor(diffSec / 86400)} дн`
  return formatDateShort(iso)
}
```

### - [ ] Step 4.3 — Rewrite `admin_web/src/pages/UsersPage.jsx`

Full replacement. Note that the existing CrudDialog-based create flow is preserved (admins still need a way to create a new user not driven by a row click) — it's just one button in `<PageHeader actions>`. Everything else (edit, delete, devices) moves into the drawer.

```jsx
import { useState, useEffect, useCallback, useMemo } from 'react'
import { Users, MoreHorizontal } from 'lucide-react'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchUsersPaged, createUser } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import { useHotkey } from '../hooks/useHotkey.js'
import CrudDialog, { FormField, TextInput, Checkbox, SelectInput } from '../components/CrudDialog.jsx'
import UserDrawer from '../components/UserDrawer.jsx'
import { formatDateShort, formatBytes, formatRelativeRu } from '../utils/format.js'
import {
  Button,
  Input,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  Badge,
  TableSkeleton,
  EmptyState,
} from '../components/ui/index.js'

// Status filter tabs. v1 ships without live counts — labels only.
// To add counts later, hit /users/status-counts endpoint (out of scope).
const STATUS_TABS = [
  { value: '',         key: 'filterAll' },
  { value: 'active',   key: 'statusActive' },
  { value: 'expiring', key: 'statusExpiring' },
  { value: 'no_plan',  key: 'statusNoPlan' },
  { value: 'blocked',  key: 'statusBlocked' },
]

function statusDot(user) {
  if (user.is_blocked) return 'bg-danger'
  if (!user.valid_until) return 'bg-tertiary'
  const expires = new Date(user.valid_until).getTime()
  const now = Date.now()
  if (expires < now) return 'bg-tertiary'
  if (expires - now < 7 * 86400000) return 'bg-warning'
  return 'bg-success'
}

export default function UsersPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()

  const [f, setF] = useUrlState({
    search: '', status: '', sort: 'last_seen', dir: 'desc', page: 1,
    open: '', tab: 'profile',
  })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selectedIndex, setSelectedIndex] = useState(-1)
  const pageSize = 20

  const [searchInput, setSearchInput] = useState(f.search)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchUsersPaged({ page: f.page, pageSize, search: f.search, status: f.status, sort: f.sort, dir: f.dir })
      .then(data => {
        setItems(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [f.page, f.search, f.status, f.sort, f.dir])

  useEffect(() => { load() }, [load])

  // Create dialog (single remaining out-of-drawer entry point)
  const [createOpen, setCreateOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [newUsername, setNewUsername] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState('user')
  const [newBlocked, setNewBlocked] = useState(false)

  function openCreate() {
    setNewUsername(''); setNewPassword(''); setNewRole('user'); setNewBlocked(false)
    setCreateOpen(true)
  }

  async function handleCreate() {
    setSaving(true)
    try {
      const payload = { username: newUsername, role: newRole, is_blocked: newBlocked }
      if (newPassword) payload.password = newPassword
      await createUser(payload)
      toast.success(t.create + ' OK')
      setCreateOpen(false)
      load()
    } catch (err) {
      toast.error(err.message)
    } finally {
      setSaving(false)
    }
  }

  // Drawer: derive selected user object from items + URL state
  const selectedUser = useMemo(() => {
    if (!f.open) return null
    const id = Number(f.open)
    return items.find(u => u.id === id) || null
  }, [items, f.open])

  function openDrawer(user, tab = 'profile') {
    setF({ open: String(user.id), tab })
  }
  function closeDrawer() {
    setF({ open: '', tab: '' })
  }

  // J/K row navigation + Enter to open
  useHotkey({ key: 'j' }, () => {
    if (f.open || createOpen) return
    setSelectedIndex(i => Math.min(items.length - 1, i + 1))
  })
  useHotkey({ key: 'k' }, () => {
    if (f.open || createOpen) return
    setSelectedIndex(i => Math.max(0, i - 1))
  })
  useHotkey({ key: 'Enter' }, () => {
    if (f.open || createOpen) return
    if (selectedIndex >= 0 && items[selectedIndex]) {
      openDrawer(items[selectedIndex])
    }
  })

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-4">
      <PageHeader
        title={t.usersTitle + (total > 0 ? ` (${total})` : '')}
        subtitle={t.usersExportHint}
        actions={can('users.write') ? (
          <Button variant="primary" onClick={openCreate}>+ {t.createUser}</Button>
        ) : null}
      />

      {/* Status filter tabs */}
      <div className="flex flex-wrap items-center gap-1 border-b border-default">
        {STATUS_TABS.map(tab => {
          const active = f.status === tab.value
          return (
            <button
              key={tab.value || 'all'}
              onClick={() => setF({ status: tab.value, page: 1 })}
              className={`px-3 py-1.5 text-sm font-medium rounded-t transition relative ${
                active ? 'text-accent bg-accent/10' : 'text-secondary hover:text-primary hover:bg-hover'
              }`}
            >
              {t[tab.key]}
              {active && <span className="absolute left-0 right-0 -bottom-px h-0.5 bg-accent" />}
            </button>
          )
        })}
      </div>

      {/* Search */}
      <form onSubmit={onSearchSubmit} className="flex flex-col sm:flex-row sm:items-center gap-2 max-w-md">
        <Input
          type="text"
          placeholder={t.search}
          value={searchInput}
          onChange={e => setSearchInput(e.target.value)}
          className="w-full sm:flex-1"
        />
        <Button type="submit" variant="secondary" className="w-full sm:w-auto">{t.search}</Button>
      </form>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={7} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={<Users className="w-12 h-12" />}
          title={t.noUsers}
          description={t.noUsersDesc}
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="username" sortState={sortState} onSort={onSort}>{t.fieldUsername}</Th>
              <Th>{t.userPlan}</Th>
              <Th>{t.fieldStatus || 'Статус'}</Th>
              <Th sortKey="valid_until" sortState={sortState} onSort={onSort}>{t.columnValidUntil}</Th>
              <Th sortKey="last_seen" sortState={sortState} onSort={onSort}>{t.columnLastSeen}</Th>
              <Th sortKey="created_at" sortState={sortState} onSort={onSort}>{t.fieldCreatedAt}</Th>
              <Th sortKey="traffic_30d_bytes" sortState={sortState} onSort={onSort}>{t.columnTraffic30d}</Th>
              <Th></Th>
            </Tr>
          </Thead>
          <Tbody>
            {items.map((u, idx) => {
              const isSelected = idx === selectedIndex
              const trafficPct = u.traffic_limit_bytes > 0
                ? Math.min(100, Math.round((u.traffic_30d_bytes / u.traffic_limit_bytes) * 100))
                : 0
              return (
                <Tr
                  key={u.id}
                  onClick={() => openDrawer(u)}
                  className={`cursor-pointer ${isSelected ? 'bg-accent/10 border-l-2 border-l-accent' : 'hover:bg-hover'}`}
                >
                  <Td>
                    <div className="flex flex-col">
                      <span className="text-primary text-sm">{u.username || '—'}</span>
                      <span className="font-mono text-xs text-tertiary">{u.user_id}</span>
                    </div>
                  </Td>
                  <Td>{u.plan ? <Badge variant="accent">{u.plan}</Badge> : <span className="text-tertiary">—</span>}</Td>
                  <Td>
                    <span className={`inline-block w-2 h-2 rounded-full ${statusDot(u)}`} aria-label={u.is_blocked ? 'blocked' : 'active'} />
                  </Td>
                  <Td mono className="text-xs whitespace-nowrap">{u.valid_until ? formatDateShort(u.valid_until) : '—'}</Td>
                  <Td className="text-xs text-tertiary whitespace-nowrap">{formatRelativeRu(u.last_seen)}</Td>
                  <Td mono className="text-xs text-tertiary whitespace-nowrap">{formatDateShort(u.created_at)}</Td>
                  <Td>
                    <div className="flex flex-col gap-1 min-w-[120px]">
                      <div className="h-1 bg-canvas border border-default rounded-full overflow-hidden">
                        <div
                          className={`h-full transition-all ${trafficPct >= 90 ? 'bg-danger' : trafficPct >= 70 ? 'bg-warning' : 'bg-info'}`}
                          style={{ width: `${trafficPct}%` }}
                        />
                      </div>
                      <span className="text-xs text-tertiary font-mono tabular-nums">
                        {formatBytes(u.traffic_30d_bytes || 0)}
                        {u.traffic_limit_bytes > 0 && ` / ${formatBytes(u.traffic_limit_bytes)}`}
                      </span>
                    </div>
                  </Td>
                  <Td align="right">
                    <button
                      onClick={e => { e.stopPropagation(); openDrawer(u) }}
                      className="opacity-0 group-hover:opacity-100 hover:bg-hover text-tertiary rounded p-1 transition"
                      aria-label={t.actions || 'Действия'}
                    >
                      <MoreHorizontal className="w-3.5 h-3.5" />
                    </button>
                  </Td>
                </Tr>
              )
            })}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={f.page <= 1} onClick={() => setF({ page: f.page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{f.page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={f.page >= totalPages} onClick={() => setF({ page: f.page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
        </div>
      )}

      {/* Drawer */}
      <UserDrawer
        user={selectedUser}
        defaultTab={f.tab || 'profile'}
        onClose={closeDrawer}
        onUserMutated={load}
      />

      {/* Create dialog — only out-of-drawer flow that remains */}
      <CrudDialog
        open={createOpen}
        title={t.createUser}
        onClose={() => setCreateOpen(false)}
        onSubmit={handleCreate}
        loading={saving}
      >
        <FormField label={t.fieldUsername}>
          <TextInput value={newUsername} onChange={setNewUsername} autoFocus />
        </FormField>
        <FormField label={t.password}>
          <TextInput value={newPassword} onChange={setNewPassword} type="password" />
        </FormField>
        <FormField label={t.fieldRole}>
          <SelectInput value={newRole} onChange={setNewRole} options={[{ value: 'user', label: 'User' }, { value: 'premium', label: 'Premium' }]} />
        </FormField>
        <Checkbox label={t.fieldBlocked} checked={newBlocked} onChange={setNewBlocked} />
      </CrudDialog>
    </div>
  )
}
```

### - [ ] Step 4.4 — Evidence

Run:
```
cd admin_web && npm run build
```
Expected: clean build, no warnings about missing translation keys or unused imports.

Manual smoke (dev server, optional but recommended):
```
cd admin_web && npm run dev
```
Open `http://localhost:5173/users`:
1. Master loads with 7 columns (Пользователь, План, Статус, Действует до, Был онлайн, Создан, Трафик 30д).
2. Click any row → drawer slides in from right, URL updates to `?open=<id>&tab=profile`.
3. Refresh page → drawer reopens on same user (URL state round-trip).
4. ESC closes drawer → URL params cleared.
5. Click status tab «Истекают» → URL updates to `?status=expiring`, list refetches.
6. Press J/K to move row selection (highlighted with accent border), Enter opens drawer.
7. On 375px (Chrome devtools mobile emulation): drawer becomes fullscreen, status tabs wrap.

If `selectedUser` is `null` when `f.open` is set (e.g. user not in current page), the drawer simply does not render — the row will be on another page; user can re-click after pagination. This is the simplest fix for v1; a future iteration could fetch the single user on miss.

### - [ ] Step 4.5 — Commit

```bash
git add admin_web/src/pages/UsersPage.jsx admin_web/src/i18n/ru.js admin_web/src/i18n/en.js admin_web/src/utils/format.js
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): UsersPage rewrite — 7 cols + status tabs + drawer + URL state

Replaces the old Users page entirely. New 7-column layout shows username +
UUID, current plan, status dot, valid_until, last_seen (relative), created
date and an inline 30-day traffic bar. Default sort flips to last_seen DESC
to surface active users first.

Status filter tabs (Все / Активные / Истекают / Без подписки /
Заблокированные) replace the old 3-button group. v1 ships without live
counts on tabs — labels only.

Row click opens UserDrawer. URL state extends with ?open=<id>&tab=<...> so
deep-linking and refresh restore the drawer. J/K navigates rows, Enter
opens, ESC closes.

The previous in-row edit / delete / block buttons all migrated into the
drawer header — only "Создать пользователя" remains at page level.

i18n adds 24 keys (drawer tabs, status filter labels, new columns,
relative-time labels). formatRelativeRu helper added to utils/format.js.

Beads: heyearthproject-hiav
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```
## Task 5: SubscriptionsPage refactor to «История подписок» + sidebar relabel (~30 min)

**Beads:** `heyearthproject-i76g`

**Files:**
- Modify: `admin_web/src/pages/SubscriptionsPage.jsx`
- Modify: `admin_web/src/components/Sidebar.jsx`
- Modify: `admin_web/src/components/commands.js`
- Modify: `admin_web/src/i18n/ru.js`
- Modify: `admin_web/src/i18n/en.js`

### Goal

Repurpose `/subscriptions` as a **global subscription history / audit view**:
- Drop the «+ Создать подписку» button (subscription creation now lives inside `UserDrawer`'s «Подписки» tab from Task 4).
- Drop the inline edit dialog (editing also belongs in the drawer).
- Keep inline delete (admin power-user feature, with confirm).
- Each row becomes a navigation target: click → `/users?open=<user_db_id>&tab=subscriptions`.
- Add 2 new client-side filters: «Истекают в течение N дней» + «С промокодом» checkbox.
- Rename in sidebar + Cmd+K + page header: «Подписки» → «История подписок».

### - [ ] Step 5.1 — Rewrite `admin_web/src/pages/SubscriptionsPage.jsx`

Full replacement. Drops the Dialog import + `editing` state + create/edit handlers. Adds `useNavigate`, new filter fields (`expiring_days`, `with_promo`), client-side filter pass before render.

```jsx
import { useState, useEffect, useCallback, useMemo } from 'react'
import { CreditCard } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { useT } from '../i18n/index.jsx'
import { useAuth } from '../auth/AuthContext.jsx'
import { useToast } from '../components/Toast.jsx'
import { fetchSubscriptionsPaged, deleteSubscription, fetchPlans } from '../api/admin.js'
import { useUrlState } from '../hooks/useUrlState.js'
import ConfirmDialog from '../components/ConfirmDialog.jsx'
import { formatDateShort } from '../utils/format.js'
import {
  Button,
  Input,
  Select,
  PageHeader,
  Table,
  Thead,
  Tbody,
  Tr,
  Th,
  Td,
  Badge,
  TableSkeleton,
  EmptyState,
} from '../components/ui/index.js'

const STATUS_VARIANT = {
  active: 'success',
  expired: 'danger',
  cancelled: 'default',
  pending_renewal: 'warning',
}

export default function SubscriptionsPage() {
  const { t } = useT()
  const { can } = useAuth()
  const toast = useToast()
  const navigate = useNavigate()

  const [f, setF] = useUrlState({
    search: '', plan_id: '', status: '',
    expiring_days: '', with_promo: '',
    sort: 'valid_until', dir: 'desc', page: 1,
  })
  const sortState = { column: f.sort, direction: f.dir }
  const onSort = (col) => setF({ sort: col, dir: f.sort === col && f.dir === 'asc' ? 'desc' : 'asc', page: 1 })

  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const pageSize = 20

  const [plans, setPlans] = useState([])
  const [confirm, setConfirm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [searchInput, setSearchInput] = useState(f.search)

  const load = useCallback(() => {
    setLoading(true)
    setError('')
    fetchSubscriptionsPaged({
      page: f.page, pageSize, search: f.search,
      plan_id: f.plan_id, status: f.status,
      sort: f.sort, dir: f.dir,
    })
      .then(data => {
        setItems(data.items || [])
        setTotal(data.total || 0)
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [f.page, f.search, f.plan_id, f.status, f.sort, f.dir])

  useEffect(() => { load() }, [load])

  useEffect(() => {
    fetchPlans().then(p => setPlans(Array.isArray(p) ? p : [])).catch(() => {})
  }, [])

  // Client-side filter — backend doesn't support expiring_in_days / with_promo yet.
  // Applied after fetch on the current page (acceptable for audit view).
  const visibleItems = useMemo(() => {
    let out = items
    if (f.expiring_days) {
      const days = parseInt(f.expiring_days, 10)
      if (days > 0) {
        const now = Date.now()
        const cutoff = now + days * 86400000
        out = out.filter(s => {
          if (!s.valid_until) return false
          const ts = new Date(s.valid_until).getTime()
          return ts >= now && ts <= cutoff
        })
      }
    }
    if (f.with_promo === '1') {
      out = out.filter(s => !!s.promocode_used)
    }
    return out
  }, [items, f.expiring_days, f.with_promo])

  async function handleDelete() {
    setSaving(true)
    try {
      await deleteSubscription(confirm.id)
      toast.success('Удалено')
      setConfirm(null)
      load()
    } catch (err) { toast.error(err.message) }
    finally { setSaving(false) }
  }

  function openUserDrawer(s) {
    if (!s.user_db_id) return
    navigate(`/users?open=${s.user_db_id}&tab=subscriptions`)
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  function onSearchSubmit(e) {
    e.preventDefault()
    setF({ search: searchInput, page: 1 })
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title="История подписок"
        subtitle="Полная история подписок всех пользователей — для аудита и анализа. Создание новых подписок — на странице пользователя."
      />

      {/* Filters */}
      <div className="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-3">
        <form onSubmit={onSearchSubmit} className="flex flex-col sm:flex-row sm:items-center gap-2 w-full sm:flex-1 sm:min-w-[200px] sm:max-w-md">
          <Input
            type="text"
            placeholder={t.search}
            value={searchInput}
            onChange={e => setSearchInput(e.target.value)}
            className="w-full sm:flex-1"
          />
          <Button type="submit" variant="secondary" className="w-full sm:w-auto">{t.search}</Button>
        </form>
        <Select
          value={f.plan_id}
          onChange={e => setF({ plan_id: e.target.value, page: 1 })}
          className="w-full sm:w-48"
          aria-label="План"
        >
          <option value="">Все планы</option>
          {plans.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
        </Select>
        <Select
          value={f.status}
          onChange={e => setF({ status: e.target.value, page: 1 })}
          className="w-full sm:w-48"
          aria-label="Статус"
        >
          <option value="">Все статусы</option>
          <option value="active">Active</option>
          <option value="expired">Expired</option>
          <option value="cancelled">Cancelled</option>
          <option value="pending_renewal">Pending Renewal</option>
        </Select>
        <Select
          value={f.expiring_days}
          onChange={e => setF({ expiring_days: e.target.value, page: 1 })}
          className="w-full sm:w-48"
          aria-label="Истекают в течение"
        >
          <option value="">Истекают: любые</option>
          <option value="3">Истекают за 3 дня</option>
          <option value="7">Истекают за 7 дней</option>
          <option value="14">Истекают за 14 дней</option>
          <option value="30">Истекают за 30 дней</option>
        </Select>
        <label className="flex items-center gap-1.5 text-sm text-secondary cursor-pointer select-none">
          <input
            type="checkbox"
            checked={f.with_promo === '1'}
            onChange={e => setF({ with_promo: e.target.checked ? '1' : '', page: 1 })}
            className="rounded"
          />
          С промокодом
        </label>
      </div>

      {error && <p className="text-danger text-sm">{error}</p>}

      {loading ? (
        <TableSkeleton rows={10} cols={7} />
      ) : visibleItems.length === 0 ? (
        <EmptyState
          icon={<CreditCard className="w-12 h-12" />}
          title="Нет подписок"
          description={
            f.expiring_days || f.with_promo === '1' || f.search || f.plan_id || f.status
              ? 'По текущим фильтрам ничего не найдено'
              : 'История подписок появится здесь, как только пользователи начнут оформлять планы'
          }
        />
      ) : (
        <Table>
          <Thead>
            <Tr>
              <Th sortKey="user_id" sortState={sortState} onSort={onSort}>Пользователь</Th>
              <Th sortKey="plan_id" sortState={sortState} onSort={onSort}>План</Th>
              <Th>Статус</Th>
              <Th sortKey="valid_until" sortState={sortState} onSort={onSort}>Действует до</Th>
              <Th align="right">Оплата</Th>
              <Th>Промокод</Th>
              <Th align="center">Авто</Th>
              <Th align="right">Действия</Th>
            </Tr>
          </Thead>
          <Tbody>
            {visibleItems.map((s, i) => {
              const isExpired = s.valid_until && new Date(s.valid_until) < new Date()
              const statusKey = isExpired ? 'expired' : (s.status || 'active')
              return (
                <Tr
                  key={s.id ?? `${s.user_db_id}-${i}`}
                  className={`cursor-pointer hover:bg-hover ${isExpired ? 'opacity-60' : ''}`}
                  onClick={() => openUserDrawer(s)}
                >
                  <Td mono className="text-xs text-accent">
                    {(s.user_id || '').slice(0, 16)}{s.user_id && s.user_id.length > 16 ? '…' : ''}
                  </Td>
                  <Td>
                    {s.plan_name
                      ? <Badge variant="accent">{s.plan_name}</Badge>
                      : <span className="text-tertiary text-xs">—</span>}
                  </Td>
                  <Td>
                    <Badge variant={STATUS_VARIANT[statusKey] || 'default'}>{statusKey}</Badge>
                  </Td>
                  <Td mono>{formatDateShort(s.valid_until)}</Td>
                  <Td align="right" mono>
                    {s.price_paid > 0
                      ? <span className="text-success font-medium">{s.price_paid}₽</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td>
                    {s.promocode_used
                      ? <span className="font-mono text-xs text-warning">{s.promocode_used}</span>
                      : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td align="center">
                    {s.auto_renew ? <span className="text-success">✓</span> : <span className="text-tertiary">—</span>}
                  </Td>
                  <Td align="right">
                    <div className="flex gap-1 justify-end" onClick={e => e.stopPropagation()}>
                      <Button size="sm" variant="ghost" onClick={() => openUserDrawer(s)}>
                        Открыть юзера
                      </Button>
                      {can('subscriptions.write') && (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-danger"
                          onClick={() => setConfirm(s)}
                          aria-label="Удалить"
                        >
                          ✕
                        </Button>
                      )}
                    </div>
                  </Td>
                </Tr>
              )
            })}
          </Tbody>
        </Table>
      )}

      {totalPages > 1 && (
        <div className="flex items-center justify-between">
          <span className="text-sm text-tertiary tabular-nums">{total} записей</span>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={f.page <= 1} onClick={() => setF({ page: f.page - 1 })} aria-label="Предыдущая страница">«</Button>
            <span className="text-sm text-secondary tabular-nums">{f.page} / {totalPages}</span>
            <Button variant="secondary" size="sm" disabled={f.page >= totalPages} onClick={() => setF({ page: f.page + 1 })} aria-label="Следующая страница">»</Button>
          </div>
        </div>
      )}

      {confirm && (
        <ConfirmDialog
          open={true}
          message={`Удалить подписку ${confirm.user_id || confirm.user_db_id}?`}
          onConfirm={handleDelete}
          onCancel={() => setConfirm(null)}
          loading={saving}
        />
      )}
    </div>
  )
}
```

Key invariants:
- `Dialog` and `saveSubscription` imports removed. `useNavigate` added.
- `editing` state removed. `confirm` state retained for delete.
- `actions` prop removed from `<PageHeader>` — no create button.
- Row click → `openUserDrawer(s)` → navigates with `?open=<id>&tab=subscriptions`.
- Action cell uses `e.stopPropagation()` so delete button doesn't trigger row navigation.
- Client-side filters (`expiring_days`, `with_promo`) apply to the already-fetched page. Acceptable for v1 — backend extension is out of scope for this sub-project per spec.

### - [ ] Step 5.2 — Relabel sidebar entry

Edit `admin_web/src/components/Sidebar.jsx`. The nav entry already references the i18n key `navSubscriptions` — relabel happens via i18n in step 5.4. **No code change to Sidebar.jsx itself.** Verify the entry stays at line 26:

```jsx
{ to: '/subscriptions', icon: CreditCard, label: 'navSubscriptions', perm: 'subscriptions.read' },
```

If a teammate later asks "почему «История подписок» под «Пользователи»?" — that grouping is intentional (it's still user-centric data); no group reshuffle.

### - [ ] Step 5.3 — Relabel Cmd+K entry

Edit `admin_web/src/components/commands.js`. Change line 20.

Old:
```jsx
  { id: 'go-subscriptions', label: 'Подписки',               icon: CreditCard,      kind: 'page', href: '/subscriptions', group: 'Пользователи' },
```

New:
```jsx
  { id: 'go-subscriptions', label: 'История подписок',       icon: CreditCard,      kind: 'page', href: '/subscriptions', group: 'Пользователи' },
```

Keep the `id` (`go-subscriptions`) so recents/hotkey users don't lose muscle memory.

### - [ ] Step 5.4 — Update i18n keys

Edit `admin_web/src/i18n/ru.js`. Replace the `navSubscriptions` value:

Old (line 27):
```js
  navSubscriptions: 'Подписки',
```

New:
```js
  navSubscriptions: 'История подписок',
```

Edit `admin_web/src/i18n/en.js`. Replace line 27:

Old:
```js
  navSubscriptions: 'Subscriptions',
```

New:
```js
  navSubscriptions: 'Subscription History',
```

The other keys (`subscriptionsTitle`, `saveSubscription`, `editSubscription`, `noSubscriptions`) remain untouched — they're still used by `UserDrawer`'s Подписки tab.

### - [ ] Step 5.5 — Evidence: build clean

Run:
```bash
cd /c/Users/alex/Documents/heyearthproject/admin_web && npm run build
```

Expected: exit code 0. The build summary lists the new `index-*.js` hash. Note this hash for Task 6 verification.

### - [ ] Step 5.6 — Evidence: dead reference scan

Run:
```bash
cd /c/Users/alex/Documents/heyearthproject && grep -rn "saveSubscription\|openCreate\|_existing" admin_web/src/pages/SubscriptionsPage.jsx
```

Expected: zero matches. Confirms the create/edit dialog code path is fully gone from SubscriptionsPage (still allowed in UserDrawer's SubscriptionsTab from Task 3).

Run:
```bash
grep -rn "label: 'Подписки'\|label:\"Подписки\"" admin_web/src/components/
```

Expected: zero matches (the Cmd+K label was the last hard-coded «Подписки» string in components/).

### - [ ] Step 5.7 — Commit

```bash
git add admin_web/src/pages/SubscriptionsPage.jsx \
        admin_web/src/components/commands.js \
        admin_web/src/i18n/ru.js \
        admin_web/src/i18n/en.js
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): SubscriptionsPage → История подписок (audit view, no create)

Repurpose /subscriptions as a global subscription audit/history view.
Creation now lives inside UserDrawer's Subscriptions tab (Task 3-4).

Changes:
- Drop «+ Создать подписку» button and Dialog-based edit flow.
- Add useNavigate; row click + «Открыть юзера» button route to
  /users?open=<user_db_id>&tab=subscriptions.
- Add 2 client-side filters: «Истекают в течение N дней» (3/7/14/30)
  and «С промокодом» checkbox. Both apply post-fetch to current page.
- Keep inline delete (admin power-user feature) with stopPropagation
  so it doesn't trigger row navigation.
- Update PageHeader title/subtitle to «История подписок».
- Relabel Cmd+K entry `go-subscriptions` from «Подписки» to
  «История подписок» (keep id for muscle memory).
- Relabel i18n key navSubscriptions in ru.js (Подписки → История
  подписок) and en.js (Subscriptions → Subscription History).

Backend untouched — `expiring_in_days` / `with_promocode` filters are
client-side in v1 (audit view, acceptable on paginated current view).

Beads: heyearthproject-i76g
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Deploy + smoke (~40 min)

**Beads:** `heyearthproject-2fyl`

**Files:** none modified — this task is execution + verification + push only.

### Goal

Push backend (new ListUsers fields + new traffic endpoint from Task 1) and admin_web (Drawer primitive + UserDrawer + UsersPage rewrite + История подписок) to production. Verify end-to-end through curl smoke and (if available) chrome MCP browser checks. Close all 7 Beads tasks. Push `main`.

No new commits in this task — it consumes the 5 commits from Tasks 1-5 and closes the epic.

### - [ ] Step 6.1 — Pre-deploy: confirm working tree state

```bash
cd /c/Users/alex/Documents/heyearthproject && git status && git log --oneline -8
```

Expected: working tree clean. Top 5 commits are Tasks 1-5 from this plan (in chronological order: backend → Drawer primitive → UserDrawer composition → UsersPage rewrite → SubscriptionsPage refactor), with the prior commit being whatever was on `main` before this plan started. If working tree is dirty — abort, finish/revert before deploying.

### - [ ] Step 6.2 — Backup production

```bash
cd /c/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --backup
```

Expected ending line: `=== Backup complete: /opt/heyearth/backups/backup-YYYYMMDD-HHMMSS.tar.gz ===`. Record the backup filename — recovery point if smoke fails.

### - [ ] Step 6.3 — Stop service + remove old binary

Per the deploy gotcha in `CLAUDE.md` (SFTP hangs if binary is in use):

```bash
cd /c/Users/alex/Documents/heyearthproject/server/backend && \
  go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api && echo "service stopped, binary removed"'
```

Expected last line: `service stopped, binary removed`. If SSH drops («connection lost»), retry — `systemctl stop` is idempotent and `rm -f` is safe on missing file.

### - [ ] Step 6.4 — Fast binary deploy

```bash
cd /c/Users/alex/Documents/heyearthproject/server/backend && go run quick_deploy.go
```

Expected: `Загружено за Xs` line. SSH may drop on the post-upload status check (known issue per memory) — don't panic, verify in 6.5.

### - [ ] Step 6.5 — Verify service is back up

```bash
cd /c/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --status
```

Expected:
- `heyearth-api: Active: active (running)`
- recent journalctl lines include `Database migrations applied successfully` and `HTTP server on :8000`
- final Health line: `Health: {"status":"ok"}`

If the status check shows `Active: failed` — pull logs immediately:
```bash
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 80 --no-pager'
```
Most likely cause for a fresh failure: `users_handlers.go` SQL syntax error from Task 1 (new LATERAL joins). Fix on dev, rebuild, redo 6.3-6.5.

### - [ ] Step 6.6 — Curl smoke: new fields in /api/admin/users

```bash
TOKEN=$(curl -s -X POST https://admin.hiearth.ru/api/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"HeYearth2024!"}' | python -c "import sys,json; print(json.load(sys.stdin)['token'])")

curl -s "https://admin.hiearth.ru/api/admin/users?limit=2" \
  -H "Authorization: Bearer $TOKEN" | \
  python -c "import sys,json; d=json.load(sys.stdin); i=d['items'][0]; print('has last_seen key:', 'last_seen' in i); print('has traffic_30d_bytes:', 'traffic_30d_bytes' in i); print('has traffic_limit_bytes:', 'traffic_limit_bytes' in i); print('sample:', {k: i.get(k) for k in ['id','username','last_seen','traffic_30d_bytes','traffic_limit_bytes']})"
```

Expected: all three keys `True`. Sample row shows real values (e.g. `traffic_30d_bytes: 1234567`, `last_seen: '2026-05-25T...'` or `None`).

### - [ ] Step 6.7 — Curl smoke: new traffic endpoint

```bash
USER_ID=$(curl -s "https://admin.hiearth.ru/api/admin/users?limit=1" \
  -H "Authorization: Bearer $TOKEN" | \
  python -c "import sys,json; print(json.load(sys.stdin)['items'][0]['id'])")

echo "Probing /api/admin/users/$USER_ID/traffic?days=30"
curl -s "https://admin.hiearth.ru/api/admin/users/$USER_ID/traffic?days=30" \
  -H "Authorization: Bearer $TOKEN" | \
  python -c "import sys,json; d=json.load(sys.stdin); print('keys:', sorted(d.keys())); print('user_db_id matches:', d.get('user_db_id') is not None); print('days:', d.get('days')); print('by_day len:', len(d.get('by_day', []))); print('by_country (expected empty in v1):', d.get('by_country'))"
```

Expected:
- `keys: ['by_country', 'by_day', 'days', 'limit_bytes', 'total_bytes', 'user_db_id']`
- `days: 30`
- `by_day len: 30` (or less if user is new)
- `by_country: []` — empty in v1 per spec (traffic_stats has no country linkage)

### - [ ] Step 6.8 — Curl smoke: status filter extensions

```bash
curl -s "https://admin.hiearth.ru/api/admin/users?status=expiring&limit=5" \
  -H "Authorization: Bearer $TOKEN" | \
  python -c "import sys,json; d=json.load(sys.stdin); print('expiring total:', d['total']); print('first item valid_until:', (d['items'][0] if d['items'] else {}).get('valid_until'))"

curl -s "https://admin.hiearth.ru/api/admin/users?status=no_plan&limit=5" \
  -H "Authorization: Bearer $TOKEN" | \
  python -c "import sys,json; d=json.load(sys.stdin); print('no_plan total:', d['total'])"
```

Expected: both return integer `total` ≥ 0 with no HTTP error (no 400/500). The first item under `status=expiring` (if any) has a `valid_until` within 7 days of now.

### - [ ] Step 6.9 — Deploy admin_web + marketing site

```bash
cd /c/Users/alex/Documents/heyearthproject/server/backend && go run ./cmd/deploy --site
```

Expected log fragments:
- `[admin-web] vite build complete` with `dist/assets/index-<hash>.js`
- `[admin-web] uploaded to /var/www/heyearth`
- `[vue-site] uploaded to /var/www/heyearth-site`

Record the admin_web bundle hash — used in 6.10. SSH may drop after final upload; verify via curl in 6.10.

### - [ ] Step 6.10 — Verify new admin bundle is live

```bash
curl -s https://admin.hiearth.ru/ | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1
```

Expected: the same `index-<hash>.js` that the build reported in 6.9. If hashes differ → CDN/nginx cache; flush via:
```bash
go run ./cmd/deploy --ssh 'nginx -s reload && echo ok'
```
and re-curl. Persistent mismatch indicates upload to wrong path — re-run 6.9.

### - [ ] Step 6.11 — Chrome MCP smoke (browser-driven E2E)

If `mcp__claude-in-chrome` is connected, run the following sequence. If not connected, note «skipped: no connected browser» and treat the curl smokes (6.6-6.8 + 6.10) as the gate.

Browser steps:

1. Navigate to `https://admin.hiearth.ru/users` in incognito.
2. Verify the master table renders with 7 columns: «Пользователь», «План», «Статус», «Действует до», «Был онлайн», «Создан», «Трафик 30д». Status filter tabs visible at top.
3. Verify the «Трафик 30д» cell shows an inline progress bar (not just a number) for users with an active plan.
4. Click the first user row. A drawer slides in from the right (above md). Header shows username + UUID + Devices/Block/Delete buttons. «Профиль» tab is active.
5. Switch to «Подписки» tab — history table renders with a «+ Подписка» button at top right.
6. Switch to «Трафик + Платежи» tab — a chart renders for the last 30 days; payments list below.
7. Press ESC. Drawer closes. URL `?open=…&tab=…` params are cleared.
8. Verify back-button: open drawer, then browser-back. Drawer should close (history-aware) — if not, note as known issue for follow-up.
9. Resize viewport to 375px (iPhone SE width). Click a user row again. Drawer is now fullscreen modal (no master list visible underneath).
10. Navigate to `https://admin.hiearth.ru/subscriptions` (or use Cmd+K → «История подписок»). Verify:
    - Title is «История подписок», no «+ Создать» button.
    - Filter row shows search + plan + status + «Истекают за N» + «С промокодом» checkbox.
11. Click a subscription row. URL changes to `/users?open=<id>&tab=subscriptions` and the drawer opens directly on the «Подписки» tab.
12. Take a screenshot of `/users` at 1440px and at 375px. Take a screenshot of `/subscriptions` at 1440px. Attach screenshots to the Beads close reason (or paste paths).

If any step fails: drop into `superpowers:systematic-debugging` for that specific failure and patch before closing Beads.

### - [ ] Step 6.12 — Close Beads tasks + epic + remember + push

```bash
bd close heyearthproject-bcgm --reason "Backend deployed, ListUsers extended with last_seen / traffic_30d_bytes / traffic_limit_bytes, GET /api/admin/users/{id}/traffic live, status=expiring|no_plan filters accepted (smoke 6.6-6.8 green)."

bd close heyearthproject-vuqa --reason "Drawer primitive added (admin_web/src/components/ui/Drawer.jsx), npm run build clean, used by UserDrawer."

bd close heyearthproject-fijd --reason "UserDrawer + Profile/Subscriptions/Traffic+Payments tabs deployed, bundle hash <hash from 6.9> live at admin.hiearth.ru."

bd close heyearthproject-hiav --reason "UsersPage rewritten: 7 columns incl. inline traffic bar, status tabs, drawer wiring + URL state round-trip (?open=&tab=) verified at desktop and mobile widths."

bd close heyearthproject-i76g --reason "SubscriptionsPage → История подписок: no create/edit dialog, row click navigates to /users drawer, expiring/with_promo client filters, sidebar+commands+i18n relabeled."

bd close heyearthproject-2fyl --reason "Deploy + smoke complete. Bundle <hash> live, /api/admin/users returns new fields, /api/admin/users/{id}/traffic returns valid v1 shape (by_country=[]), status=expiring|no_plan accepted, chrome MCP E2E (or curl-only fallback) passed."

bd close heyearthproject-unpl --reason "Users + Subscriptions UX merged — Master + Drawer pattern, mobile fullscreen, /subscriptions → История подписок. Spec docs/superpowers/specs/2026-05-25-users-subs-merge-design.md. Plan docs/superpowers/plans/2026-05-25-users-subs-merge.md. All 6 sub-tasks shipped end-to-end on hiearth.ru."

bd remember --key users-drawer-pattern "Users + Subscriptions UX merged 2026-05-25 (epic heyearthproject-unpl). Master+Drawer pattern at /users. ListUsersHandler in users_handlers.go extended with last_seen MAX(user_devices.last_seen_at), traffic_30d_bytes SUM(traffic_stats.bytes_used over last 30 days), traffic_limit_bytes from current plan. New endpoint GET /api/admin/users/{id}/traffic?days=N returns {user_db_id, days, limit_bytes, total_bytes, by_day:[{day,bytes}], by_country:[]}. by_country always [] in v1 (traffic_stats has no server_id/country linkage). Status filter extended: 'expiring' = valid_until BETWEEN NOW() AND NOW()+7d, 'no_plan' = no active sub. Sort whitelist: last_seen, traffic_30d_bytes. Frontend: Drawer primitive in components/ui/ (right slide-in above md, fullscreen below md, reuses Dialog pattern). UserDrawer composes Drawer + 3 tabs (Profile/Subscriptions/Traffic+Payments). Subscription creation now happens inside drawer tab, not via /subscriptions. /subscriptions renamed to История подписок (audit view, no create, navigates back to users drawer via ?open=&tab=subscriptions)."

git push origin main
```

Expected from `git push`: 5 commits pushed, `main -> main` updated. No conflicts (working alone on this epic).

### - [ ] Step 6.13 — Post-push sanity

```bash
curl -sI https://admin.hiearth.ru/ | head -3
curl -s https://hiearth.ru/api/v1/health
```

Expected: `200 OK` from admin, `{"status":"ok"}` from public health. If admin returns 502 — nginx tried to proxy to a restarted-too-slowly heyearth-api; wait 10s and re-curl. Persistent 502 → fall back via `go run ./cmd/deploy --restart` and re-verify 6.6-6.8.

Epic closed. Done.

## Self-review

**Spec coverage** (`docs/superpowers/specs/2026-05-25-users-subs-merge-design.md`):
- ✅ Master with 7 columns + inline traffic bar → T4 step 4.2
- ✅ Status filter tabs (Все/Активные/Истекают/Без подписки/Заблокированные) → T1 step 1.3 (backend) + T4 step 4.2 (frontend)
- ✅ Drawer primitive with mobile-fullscreen → T2 step 2.1
- ✅ 3 tabs (Профиль / Подписки / Трафик+Платежи) → T3 steps 3.3, 3.4, 3.5
- ✅ Devices quick action in drawer header → T3 step 3.2
- ✅ Inline subscription create in drawer (no separate `user_db_id` field) → T3 step 3.4
- ✅ URL state for drawer (`?open=<id>&tab=<name>`) → T4 step 4.2
- ✅ Default sort `last_seen DESC` → T1 step 1.2 + T4 step 4.2
- ✅ Backend `last_seen` + `traffic_30d_bytes` + `traffic_limit_bytes` → T1 steps 1.1, 1.4
- ✅ Backend `/api/admin/users/{id}/traffic` endpoint → T1 step 1.6 + route in step 1.7
- ✅ «История подписок» rename + audit-only behavior → T5 steps 5.1, 5.4
- ✅ Sidebar + Cmd+K relabel → T5 steps 5.5, 5.6
- ✅ Row click in /subscriptions-history → /users?open=&tab=subscriptions → T5 step 5.3
- ✅ J/K navigation + Enter open + ESC close → T2 step 2.1 (ESC) + T4 step 4.2 (J/K + Enter)
- ✅ Deploy + curl smoke for new fields → T6 steps 6.6, 6.7, 6.8
- ✅ Chrome MCP smoke at 375px and 1440px → T6 step 6.11

**Placeholder scan:** no «TBD» / «TODO» / «implement later» in any task. Every step has either a concrete diff pattern (find / replace) or full code block. Where exact line numbers might shift across the codebase, grep commands precede the edit so the engineer locates the line first.

**Type / API consistency:**
- `last_seen` returns RFC3339 string or empty string consistently in backend → consumed by `formatRelativeRu(iso)` helper in frontend → displayed as «2 мин» / «3 ч» / «6 дн».
- `traffic_30d_bytes` is `int64` in backend → consumed by inline progress bar percentage calc in frontend (`pct = limit > 0 ? min(100, used/limit*100) : 0`).
- `traffic_limit_bytes` of `0` means unlimited or no plan — both display as «—» on the master row and «Нет лимита» in the drawer's traffic tab.
- `by_country` is always `[]` in v1 (no `server_id` column in `traffic_stats`); frontend handles empty array gracefully.
- Drawer URL params (`open` + `tab`) consistent between master direct-navigation and «История подписок» row click navigation.
- `fetchUserSubscriptions` / `fetchUserPayments` use existing `/api/admin/subscriptions?user_db_id=` and `/api/admin/payments?user_db_id=` query patterns — verified against current handlers.
- Tabs identifiers ('profile' | 'subscriptions' | 'traffic') used consistently in URL state, ProfileTab/SubscriptionsTab/TrafficPaymentsTab file names, and tabs strip rendering.

**Risk acknowledgements (from spec, re-confirmed in plan):**
- **Performance:** new LATERAL joins (last_seen MAX, traffic_30d SUM) add I/O on every users list query. Mitigated by existing indexes on `user_devices(user_id)` and `traffic_stats(user_id, recorded_at)`. If p95 latency on /api/admin/users exceeds 500ms in prod, fall back to a precomputed `users.last_seen_cache` column (out of scope for this plan).
- **Drawer state vs URL:** opening drawer for user X mutates URL params. Closing drawer must clean params. Back-button behavior tested in T6 chrome MCP smoke step 6.11.
- **«История подписок» dead-end:** clicking a row routes to /users with drawer open; if target user was deleted between page loads, drawer no-renders (selected user not in current page). Documented as v1 limitation; future iteration fetches single user on miss.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-25-users-subs-merge.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — 6 tasks, mostly self-contained. T1 (backend) and T2 (Drawer primitive) can run in parallel (no shared files). T3 (UserDrawer + tabs) depends on T2 done. T4 (UsersPage rewrite) depends on T1 + T3 done. T5 (SubscriptionsPage refactor) can run in parallel with T3/T4. T6 (deploy + smoke) is the final gate after T4 and T5.

**2. Inline Execution** — execute in this session with checkpoints. Same dependency order: T1 || T2 → T3 → T4 || T5 → T6. Recommended only if you want to stay involved at every step.

**Which approach?**
