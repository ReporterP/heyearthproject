# Remove user_groups Everywhere — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Полностью убрать `user_groups` концепт — DB tables `user_groups` + `server_groups` drop'ятся, `user_subscriptions` получает SERIAL id PK (теряет group_id), `promocodes.group_id_granted` → `plan_id_granted` (с backfill на free-trial), всё group-related код удаляется из backend + frontend, premium-статус derive через "exists active subscription".

**Architecture:** Migration 000047 делает атомарный ALTER+DROP в одной транзакции PostgreSQL. Backend pipeline удаляет `groups_handlers.go` целиком + строки в 8 других файлах + переписывает 3 SQL запроса (ListUsers IsPremium, biz/payments INSERT, biz/user.go::ActivateFreeTrialFromPromocode + ApplyFreePlan). Frontend удаляет `GroupsPage.jsx` + строки в 8 других файлах + переключает `PromocodesPage` форму на Plan picker.

**Tech Stack:** PostgreSQL 15, Go 1.21+ (raw `database/sql + lib/pq`), React 19 + Vite + Tailwind v4 (JSX). No new dependencies. Evidence: `go build ./... && go vet ./...` + `npm run build` + chrome MCP smoke.

**Beads epic:** `heyearthproject-yvnw`
**Spec:** `docs/superpowers/specs/2026-05-25-remove-user-groups-design.md`

---

## File map

**DB:**
- Create: `server/backend/migrations/000047_remove_groups.up.sql`
- Create: `server/backend/migrations/000047_remove_groups.down.sql`

**Backend deletions:**
- Delete: `server/backend/internal/admin/groups_handlers.go`

**Backend modifications:**
- `cmd/api/main.go` — drop 2 `/api/admin/groups*` routes
- `internal/admin/auth.go` — drop 2 entries from `permissionCatalog`
- `internal/admin/users_handlers.go` — drop subquery `group_id`, LEFT JOIN user_groups; rewrite IsPremium derivation
- `internal/admin/subscriptions_handlers.go` — drop group_id from filters/SELECT/INSERT/scan; add `id` SERIAL field
- `internal/admin/promocodes_handlers.go` — `group_id_granted` → `plan_id_granted` everywhere; JOIN plans вместо user_groups
- `internal/admin/dashboard_handlers.go` — drop `GroupsCount` KPI
- `internal/admin/types.go` — drop `Group` struct + group fields from `UserRow`/`SubscriptionRow`/`PromocodeRow`
- `internal/biz/user.go` — rewrite `ListUsers` (drop JOIN user_groups, EXISTS premium derivation); rewrite `ActivateFreeTrialFromPromocode` to use plan_id_granted
- `internal/biz/payments.go` — `ActivateSubscriptionWithPlan` INSERT drops group_id
- `internal/biz/server.go` — drop `ListServersByGroup` if used elsewhere; legacy `server_groups` reads removed
- `internal/biz/client_identity.go` — rewrite `ListUserSubscriptions` (no JOIN user_groups); replace hardcoded `'Free' group` lookup with free-trial plan_id
- `cmd/api/payment_handlers.go` — promocode redemption uses `plan_id_granted`

**Frontend deletions:**
- Delete: `admin_web/src/pages/GroupsPage.jsx`

**Frontend modifications:**
- `admin_web/src/main.jsx` — drop GroupsPage import + Route
- `admin_web/src/components/Sidebar.jsx` — drop nav item
- `admin_web/src/components/commands.js` — drop go-groups command
- `admin_web/src/i18n/ru.js` + `en.js` — drop `navGroups` and any leftover group keys
- `admin_web/src/api/admin.js` — drop groups CRUD helpers if any
- `admin_web/src/pages/UsersPage.jsx` — drop group filter+column
- `admin_web/src/pages/SubscriptionsPage.jsx` — drop group filter+column+form field
- `admin_web/src/pages/PromocodesPage.jsx` — form: Group Select → Plan Select; column "Группа" → "Тариф"

---

## Task 1: Migration 000047 (15 min)

**Files:**
- Create: `server/backend/migrations/000047_remove_groups.up.sql`
- Create: `server/backend/migrations/000047_remove_groups.down.sql`

**Beads:** `heyearthproject-grp1`

- [ ] **Step 1.1** — Create `server/backend/migrations/000047_remove_groups.up.sql`:

```sql
-- 000047_remove_groups.up.sql
-- Remove the entire user_groups concept. Groups have been superseded by plans:
-- premium status = any active subscription, server access = plan_servers,
-- promocode payload = plan_id (not group_id).

-- 1. user_subscriptions: new SERIAL id PK, drop group_id column.
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_pkey;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS id SERIAL PRIMARY KEY;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS group_id;

-- 2. promocodes: replace group_id_granted with plan_id_granted.
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS plan_id_granted INTEGER REFERENCES plans(id);

-- Best-effort backfill: existing promocodes default to free-trial plan.
UPDATE promocodes
SET plan_id_granted = (SELECT id FROM plans WHERE slug = 'free-trial' LIMIT 1)
WHERE plan_id_granted IS NULL;

ALTER TABLE promocodes DROP COLUMN IF EXISTS group_id_granted;

-- 3. server_groups: legacy junction, replaced by plan_servers.
DROP TABLE IF EXISTS server_groups;

-- 4. Drop user_groups table itself.
DROP TABLE IF EXISTS user_groups;

-- 5. Drop groups.read/write permissions (cascade removes admin_role_permissions rows).
DELETE FROM admin_permissions
WHERE permission_key IN ('groups.read', 'groups.write');
```

- [ ] **Step 1.2** — Create `server/backend/migrations/000047_remove_groups.down.sql` (best-effort skeleton):

```sql
-- 000047_remove_groups.down.sql
-- Best-effort skeleton restoration. Data is permanently lost.

CREATE TABLE IF NOT EXISTS user_groups (
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    is_premium BOOLEAN DEFAULT FALSE,
    company_id INT,
    tenant_id INTEGER REFERENCES tenants(id)
);

CREATE TABLE IF NOT EXISTS server_groups (
    server_id INT REFERENCES servers(id) ON DELETE CASCADE,
    group_id  INT REFERENCES user_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (server_id, group_id)
);

ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS group_id INTEGER;
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_pkey;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS id;
-- Original (user_id, group_id) PK NOT restored (group_id is NULL).

ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS group_id_granted INTEGER;
ALTER TABLE promocodes DROP COLUMN IF EXISTS plan_id_granted;

INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('groups.read',  'Groups Read',  'View user groups'),
    ('groups.write', 'Groups Write', 'Manage user groups')
ON CONFLICT (permission_key) DO NOTHING;
```

- [ ] **Step 1.3** — Evidence: `cd server/backend && go build ./...` exits 0 (migration files are read at runtime; this just confirms no incidental breakage).

- [ ] **Step 1.4** — Commit:

```bash
git add server/backend/migrations/000047_remove_groups.up.sql server/backend/migrations/000047_remove_groups.down.sql
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(db): migration 000047 — remove user_groups everywhere

Drops user_groups + server_groups tables. user_subscriptions gets
SERIAL id PK (old (user_id, group_id) PK blocked multi-history per
user). promocodes.group_id_granted → plan_id_granted (existing rows
backfilled to free-trial plan as safest default). groups.read/write
permissions removed (cascades to admin_role_permissions).

Migration is destructive; down skeleton recreates structure but
data is permanently lost.

Beads: heyearthproject-grp1
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Backend cleanup (90 min)

**Files:**
- Delete: `server/backend/internal/admin/groups_handlers.go`
- Modify: `server/backend/cmd/api/main.go`
- Modify: `server/backend/internal/admin/auth.go`
- Modify: `server/backend/internal/admin/users_handlers.go`
- Modify: `server/backend/internal/admin/subscriptions_handlers.go`
- Modify: `server/backend/internal/admin/promocodes_handlers.go`
- Modify: `server/backend/internal/admin/dashboard_handlers.go`
- Modify: `server/backend/internal/admin/types.go`
- Modify: `server/backend/internal/biz/user.go`
- Modify: `server/backend/internal/biz/payments.go`
- Modify: `server/backend/internal/biz/server.go`
- Modify: `server/backend/internal/biz/client_identity.go`
- Modify: `server/backend/cmd/api/payment_handlers.go`

**Beads:** `heyearthproject-grp2`

### Order of operations

Backend is fragile to half-done cleanups. Do edits in this order so build stays green where possible:

1. Delete groups_handlers.go file (the rest of backend already compiles without it after we drop the route registration).
2. Drop route registrations from main.jsx.
3. Drop permissionCatalog entries.
4. Strip Group struct + group fields from types.go.
5. Update biz/user.go (premium derivation + ActivateFreeTrialFromPromocode).
6. Update biz/payments.go (ActivateSubscriptionWithPlan INSERT).
7. Update biz/client_identity.go (ListUserSubscriptions + ApplyFreePlan).
8. Update biz/server.go (drop ListServersByGroup helper).
9. Update admin/users_handlers.go (drop JOIN + premium derivation).
10. Update admin/subscriptions_handlers.go (drop group_id everywhere; add id field).
11. Update admin/promocodes_handlers.go (group_id_granted → plan_id_granted everywhere).
12. Update admin/dashboard_handlers.go (drop GroupsCount).
13. Update cmd/api/payment_handlers.go (promocode redemption uses plan_id_granted).
14. `go build ./... && go vet ./...` — fix any remaining undefined refs by deleting them.

### Step 2.1 — Delete `groups_handlers.go`

```bash
git rm server/backend/internal/admin/groups_handlers.go
```

### Step 2.2 — `cmd/api/main.go` — drop routes

Read main.jsx to find. Grep:
```bash
grep -n "/api/admin/groups" server/backend/cmd/api/main.go
```

Remove the 2 `http.HandleFunc(...)` lines (one for `/api/admin/groups`, one for `/api/admin/groups/`).

### Step 2.3 — `internal/admin/auth.go` — drop permissionCatalog entries

Find lines:
```go
{Key: "groups.read", Name: "Groups Read", Description: "View groups"},
{Key: "groups.write", Name: "Groups Write", Description: "Create and update groups"},
```

Delete both.

### Step 2.4 — `internal/admin/types.go` — strip group fields

Read the file. Find:
```go
GroupID    int    `json:"group_id"`
GroupName  string `json:"group_name"`
```

Both occurrences. Delete the lines.

Also find `GroupIDs []int` references (lines 174, 202) — these are for the now-deleted Groups page payload (UserCreatePayload, etc). Delete them.

If there's a standalone `type Group struct { ... }` definition — delete the whole struct.

### Step 2.5 — `internal/biz/user.go` — rewrite ListUsers + ActivateFreeTrialFromPromocode

Find the `ListUsers` SQL (around line 140-150):
```go
LEFT JOIN user_subscriptions us ON us.user_id = u.id
LEFT JOIN user_groups ug ON ug.id = us.group_id ...
```

Rewrite to drop JOINs and use EXISTS for premium:
- Replace the `JOIN user_groups ug ON ug.id = us.group_id` line with `-- (removed, premium derived from EXISTS active subscription)` (or just delete it).
- Replace any `bool_or(ug.is_premium)` aggregation with:
```sql
COALESCE((SELECT TRUE FROM user_subscriptions us
          WHERE us.user_id = u.id AND us.valid_until > CURRENT_TIMESTAMP
          LIMIT 1), FALSE) AS is_premium
```

Find the `ActivateFreeTrialFromPromocode` (around line 281-330):
- Change SELECT from `COALESCE(group_id_granted, 1)` → `COALESCE(plan_id_granted, (SELECT id FROM plans WHERE slug='free-trial' LIMIT 1))`.
- Change INSERT from `(user_id, group_id, valid_until, ...)` ON CONFLICT (user_id, group_id) → `(user_id, plan_id, valid_until, ...)` ON CONFLICT DO NOTHING.

If the function `ListUserSubscriptions` at line ~258 has `JOIN user_groups ug` — drop JOIN, replace `COALESCE(p.name, ug.name, 'standard')` with `COALESCE(p.name, 'standard')`.

### Step 2.6 — `internal/biz/payments.go` — ActivateSubscriptionWithPlan INSERT

Find lines 103-105:
```go
INSERT INTO user_subscriptions (user_id, group_id, valid_until, auto_renew, plan, price_rub)
...
ON CONFLICT (user_id, group_id) DO UPDATE
```

Rewrite:
```go
INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, plan, price_rub)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id, plan_id) DO UPDATE
SET valid_until = EXCLUDED.valid_until,
    auto_renew = EXCLUDED.auto_renew,
    plan = EXCLUDED.plan,
    price_rub = EXCLUDED.price_rub
```

NOTE: We need a unique index/constraint on `(user_id, plan_id)` for ON CONFLICT to work. The migration created `id SERIAL PRIMARY KEY` but did NOT add `UNIQUE(user_id, plan_id)`. Decision: if multiple subscriptions to the same plan are an OK historical model (spec says "id PK allows clean subscription history"), then change INSERT to NOT use ON CONFLICT — just plain INSERT. But that risks duplicate active subs.

Recommended approach: change INSERT to upsert based on `valid_until > NOW()`:
```go
// Try update first
res, err := db.DB.Exec(`
    UPDATE user_subscriptions
    SET valid_until = $1, auto_renew = $2, plan = $3, price_rub = $4
    WHERE user_id = $5 AND plan_id = $6 AND valid_until > CURRENT_TIMESTAMP
`, validUntil, autoRenew, planName, priceRub, userID, planID)
if affected, _ := res.RowsAffected(); affected > 0 { return nil }

// No active sub for this plan — insert new
_, err = db.DB.Exec(`
    INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, plan, price_rub)
    VALUES ($1, $2, $3, $4, $5, $6)
`, userID, planID, validUntil, autoRenew, planName, priceRub)
```

This preserves history (expired subs aren't UPDATEd, new sub gets new id).

### Step 2.7 — `internal/biz/client_identity.go` — rewrite

Find `ListUserSubscriptions` (around line 336):
```go
SELECT us.group_id, COALESCE(p.name, ug.name, 'standard'), ...
JOIN user_groups ug ON ug.id = us.group_id
```

Drop the JOIN and the GroupID/GroupName fields. Replace with:
```go
SELECT us.id, us.plan_id, COALESCE(p.name, 'standard'), us.valid_until, us.auto_renew, us.valid_until > NOW(), p.price_rub
FROM user_subscriptions us
LEFT JOIN plans p ON p.id = us.plan_id
WHERE us.user_id = $1
ORDER BY us.valid_until DESC
```

Update struct to drop `GroupID` / `GroupName` (lines 58-59).

Find `ApplyFreePlan` around line 474:
```go
var freeGroupID int
err := db.DB.QueryRow(`SELECT id FROM user_groups WHERE name = 'Free' LIMIT 1`).Scan(&freeGroupID)
...
INSERT INTO user_subscriptions (user_id, group_id, valid_until) VALUES ($1, $2, $3)
ON CONFLICT (user_id, group_id) DO NOTHING
```

Rewrite:
```go
var freeTrialPlanID int
if err := db.DB.QueryRow(`SELECT id FROM plans WHERE slug = 'free-trial' LIMIT 1`).Scan(&freeTrialPlanID); err != nil {
    return fmt.Errorf("free-trial plan not found: %w", err)
}

_, err := db.DB.Exec(`
    INSERT INTO user_subscriptions (user_id, plan_id, valid_until)
    VALUES ($1, $2, $3)
    ON CONFLICT DO NOTHING
`, userDBID, freeTrialPlanID, validUntil)
```

### Step 2.8 — `internal/biz/server.go` — drop legacy

Find:
```go
WHERE sg.group_id = $1 AND s.status != 'offline'
```

This is in a `ListServersByGroup` function (or similar). Since `server_groups` table is gone, this function is dead. Find callers via:
```bash
grep -rn "ListServersByGroup\|ListServersForGroup" server/backend/
```

If callers exist — replace with `ListServersForPlan(planID)` (using plan_servers table). If no callers (Xray sync already migrated to plan_servers in earlier cycle) — delete the function entirely.

### Step 2.9 — `internal/admin/users_handlers.go` — drop JOIN

Find lines 75-81:
```go
SELECT us2.group_id, us2.plan_id, us2.valid_until ...
LEFT JOIN user_groups ug ON ug.id = us.group_id
```

Replace the subquery to drop group_id from SELECT. The JOIN line — drop entirely.

If `group_id` was in `allowedSort`:
```go
allowedSort := map[string]string{
    ...
    "group_id": "us.group_id",  // DROP THIS LINE
    ...
}
```

Drop the `group_id` filter parsing if exists (`?group_id=`).

In the Scan call: drop `&row.GroupID, &row.GroupName` args. Update column count.

### Step 2.10 — `internal/admin/subscriptions_handlers.go` — drop group_id, add id

Multiple sites (lines 36, 56-88, 105-133, 154):

Line 36 — drop `"group_id": "us.group_id"` from allowedSort.

Lines 56-88 — SELECT/Scan:
```go
// OLD:
SELECT us.user_id, u.user_id, us.group_id, ug.name, us.valid_until, us.auto_renew, ...
JOIN user_groups ug ON ug.id = us.group_id

// NEW:
SELECT us.id, us.user_id, u.user_id, us.plan_id, COALESCE(p.name, 'standard'), us.valid_until, us.auto_renew, ...
LEFT JOIN plans p ON p.id = us.plan_id
```

Scan call: replace `&item.GroupID, &item.GroupName` with `&item.ID, &item.PlanName` (or whatever fields are now in the row struct after types.go update).

Lines 105-133 — Create handler:
```go
// OLD validation:
if payload.UserID == 0 || payload.GroupID == 0 || ... {
    writeAdminError(w, http.StatusBadRequest, "user_db_id, group_id and valid_until are required")
}

// NEW:
if payload.UserID == 0 || payload.PlanID == 0 || strings.TrimSpace(payload.ValidUntil) == "" {
    writeAdminError(w, http.StatusBadRequest, "user_db_id, plan_id and valid_until are required")
}
```

INSERT:
```go
// NEW:
INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, status)
VALUES ($1, $2, $3, $4, $5)
RETURNING id
```

Line 154 — DELETE handler:
```go
// OLD: DELETE FROM user_subscriptions WHERE user_id = $1 AND group_id = $2
// NEW: DELETE FROM user_subscriptions WHERE id = $1
```

Route /delete now takes `id` URL param instead of (user_id, group_id) pair.

### Step 2.11 — `internal/admin/promocodes_handlers.go` — group_id_granted → plan_id_granted

Lines 17-18, 39, 73-81, 103, 153-157, 193-208:

Struct fields:
```go
// OLD:
GroupID   int    `json:"group_id_granted"`
GroupName string `json:"group_name"`
// NEW:
PlanID   int    `json:"plan_id_granted"`
PlanName string `json:"plan_name"`
```

SELECT (line 73-81):
```go
// OLD:
SELECT p.code, COALESCE(p.group_id_granted, 0), COALESCE(ug.name, ''), ...
LEFT JOIN user_groups ug ON ug.id = p.group_id_granted

// NEW:
SELECT p.code, COALESCE(p.plan_id_granted, 0), COALESCE(pl.name, ''), ...
LEFT JOIN plans pl ON pl.id = p.plan_id_granted
```

Scan: `&item.GroupID, &item.GroupName` → `&item.PlanID, &item.PlanName`.

INSERT (line 153-157):
```go
INSERT INTO promocodes (code, plan_id_granted, duration_days, max_uses, expires_at, ...)
VALUES ($1, NULLIF($2, 0), $3, $4, $5, ...)
```

UPDATE (line 193-208):
```go
plan_id_granted = NULLIF($1, 0),
```

### Step 2.12 — `internal/admin/dashboard_handlers.go` — drop GroupsCount

Find:
```go
{&overview.GroupsCount, `SELECT COUNT(*) FROM user_groups`},
```

Delete this line. Also drop `GroupsCount int` from the `OverviewResponse` struct if defined in the same file (or in types.go).

### Step 2.13 — `cmd/api/payment_handlers.go` — promocode redemption

Find where promocode is redeemed (search for `group_id_granted` or `promocodes`):
```bash
grep -n "group_id_granted\|plan_id_granted\|ActivateFreeTrialFromPromocode" server/backend/cmd/api/payment_handlers.go
```

Replace any `group_id_granted` references with `plan_id_granted` and adjust INSERT into user_subscriptions accordingly (drop group_id, use plan_id).

### Step 2.14 — Evidence

```bash
cd server/backend && go build ./...
```
Expected: zero output, exit 0.

Any compile error remaining = clear instruction (e.g. `undefined: GroupID`, `undefined: user_groups`). Fix by removing the reference.

```bash
cd server/backend && go vet ./...
```
Expected: zero output (pre-existing `dunning_cron.go:130` warning OK).

### Step 2.15 — Commit

```bash
git add server/backend/cmd/api/main.go server/backend/cmd/api/payment_handlers.go server/backend/internal/admin/auth.go server/backend/internal/admin/users_handlers.go server/backend/internal/admin/subscriptions_handlers.go server/backend/internal/admin/promocodes_handlers.go server/backend/internal/admin/dashboard_handlers.go server/backend/internal/admin/types.go server/backend/internal/biz/user.go server/backend/internal/biz/payments.go server/backend/internal/biz/server.go server/backend/internal/biz/client_identity.go
git rm server/backend/internal/admin/groups_handlers.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(backend): remove user_groups from all handlers and biz layer

Delete: groups_handlers.go (entire file).
Drop: /api/admin/groups routes, groups.read/write permissions in
permissionCatalog, GroupsCount overview KPI, server_groups reads.

Rewrite premium derivation: was JOIN user_groups + bool_or(is_premium),
now EXISTS active subscription. Applies to biz/user.go::ListUsers,
client_identity.go::ListUserSubscriptions, admin/users_handlers.go,
admin/subscriptions_handlers.go.

Subscriptions handler: new SERIAL id PK. INSERT/SELECT/Scan/DELETE all
use id instead of (user_id, group_id) pair.

Promocodes handler: group_id_granted → plan_id_granted; JOIN plans
instead of user_groups; struct fields renamed.

biz/payments.go ActivateSubscriptionWithPlan: upsert via WHERE
valid_until>NOW() match (preserves history when new sub starts).

biz/client_identity.go ApplyFreePlan: SELECT free-trial plan_id
(was hardcoded 'Free' user_groups name lookup).

Beads: heyearthproject-grp2
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Frontend cleanup (60 min)

**Files:**
- Delete: `admin_web/src/pages/GroupsPage.jsx`
- Modify: `admin_web/src/main.jsx`
- Modify: `admin_web/src/components/Sidebar.jsx`
- Modify: `admin_web/src/components/commands.js`
- Modify: `admin_web/src/i18n/ru.js`
- Modify: `admin_web/src/i18n/en.js`
- Modify: `admin_web/src/api/admin.js`
- Modify: `admin_web/src/pages/UsersPage.jsx`
- Modify: `admin_web/src/pages/SubscriptionsPage.jsx`
- Modify: `admin_web/src/pages/PromocodesPage.jsx`

**Beads:** `heyearthproject-grp3`

### Step 3.1 — Delete GroupsPage

```bash
git rm admin_web/src/pages/GroupsPage.jsx
```

### Step 3.2 — `main.jsx`

Find and delete:
```jsx
import GroupsPage from './pages/GroupsPage.jsx'
```

Find and delete the route:
```jsx
<Route path="/groups" element={<GroupsPage/>}/>
```

### Step 3.3 — `Sidebar.jsx`

Find in the `groups` const array under ПОЛЬЗОВАТЕЛИ section:
```jsx
{ to: '/groups', icon: UsersRound, label: 'navGroups', perm: 'groups.read' },
```

Delete this item.

Check if `UsersRound` from lucide-react is still used elsewhere in the file (other nav items). Grep:
```bash
grep -c "UsersRound" admin_web/src/components/Sidebar.jsx
```
If count is 1 (only the import line remains) — remove `UsersRound` from the import block.

### Step 3.4 — `commands.js`

Find:
```js
{ id: 'go-groups', label: 'Группы', icon: UsersRound, kind: 'page', href: '/groups', group: 'Пользователи' },
```

Delete the line. Same UsersRound cleanup as Step 3.3.

### Step 3.5 — `i18n/ru.js` + `i18n/en.js`

Grep for group-related keys:
```bash
grep -n "navGroups\|fieldGroup\|groupTitle\|groupCreate\|groupEdit\|groupName" admin_web/src/i18n/ru.js admin_web/src/i18n/en.js
```

Delete every match. For each — delete the entire line (since these are object key-value pairs).

### Step 3.6 — `api/admin.js`

Grep:
```bash
grep -n "groups\|Group" admin_web/src/api/admin.js
```

Delete helpers like `listGroups()`, `getGroup()`, `createGroup()`, `updateGroup()`, `deleteGroup()` if they exist.

### Step 3.7 — `UsersPage.jsx`

Read the file. Find:
- `useUrlState({ ..., group_id: '', ... })` → remove `group_id`.
- `<Select>` for group filter in the filter row → remove.
- `<Th>Группа</Th>` and corresponding `<Td>` → remove.
- Any `groups` state + `fetch('/api/admin/groups')` on mount → remove. Drop `useEffect` if it was only for that.

### Step 3.8 — `SubscriptionsPage.jsx`

Same pattern:
- `useUrlState({ ..., group_id: '', ... })` → remove `group_id`.
- Group Select in filter → remove.
- Group column → remove.
- Form fields: replace `group_id` with `plan_id` (form for create-subscription).
- Form Select: fetch from `/api/admin/plans` instead of `/api/admin/groups`.

### Step 3.9 — `PromocodesPage.jsx`

Form refactor: Group Select → Plan Select.

Read the file. Find the form's Group field (likely uses state like `form.group_id_granted`).

Changes:
- `form.group_id_granted` → `form.plan_id_granted` everywhere.
- Fetch `/api/admin/plans` instead of `/api/admin/groups` (or use existing plans hook if any).
- Display column "Группа" header → "Тариф".
- Show `plan_name` instead of `group_name` in table rows.

### Step 3.10 — Evidence

```bash
cd admin_web && npm run build
```
Expected: `✓ built in Xs`, exit 0.

If build fails:
- "GroupsPage is not defined" → check main.jsx for stale import.
- "UsersRound is not defined" → revert the import removal in Sidebar/commands if still referenced.
- "navGroups is not a key" → that's only a runtime issue from a missed Sidebar entry; harmless at build time, but indicates missing cleanup.

### Step 3.11 — Commit

```bash
git rm admin_web/src/pages/GroupsPage.jsx
git add admin_web/src/main.jsx admin_web/src/components/Sidebar.jsx admin_web/src/components/commands.js admin_web/src/i18n/ru.js admin_web/src/i18n/en.js admin_web/src/api/admin.js admin_web/src/pages/UsersPage.jsx admin_web/src/pages/SubscriptionsPage.jsx admin_web/src/pages/PromocodesPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): remove user_groups everywhere from frontend

Delete: GroupsPage.jsx (entire page).
Drop: main.jsx import+route, Sidebar nav entry, Cmd+K go-groups
command, navGroups i18n keys, api/admin.js groups CRUD helpers.

UsersPage / SubscriptionsPage drop the group filter and group column.
SubscriptionsPage's create-subscription form swaps the Group dropdown
for a Plan dropdown.

PromocodesPage's form switches Group Select for Plan Select (fetch
/api/admin/plans); display column header "Группа" → "Тариф", shows
plan name instead of group name.

Beads: heyearthproject-grp3
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Deploy + smoke (40 min)

**Beads:** `heyearthproject-grp4`

No commits (verification only). Final commit is the push at the end.

### Step 4.1 — Pre-deploy SQL check

Verify free-trial plan exists (the backfill SET in migration depends on it):

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT id, slug FROM plans WHERE slug = 'free-trial';\\\"\""
```

Expected: one row like `1|free-trial`. If empty — STOP and create the plan first (out of scope for this cycle).

### Step 4.2 — Deploy backend

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
cd server/backend && go run quick_deploy.go
```

Expected ending: `heyearth-api: ACTIVE ✓`, `Health: {"status":"ok"}`, migration 000047 visible in logs.

If migration fails mid-deploy — STOP. Read journal:
```bash
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 50 --no-pager'
```
Common failures: ON DELETE CASCADE issue (some FK referencing user_groups not declared with cascade), or backfill SQL syntax. Fix migration file, re-deploy.

### Step 4.3 — Verify migration applied

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT to_regclass('user_groups'), to_regclass('server_groups');\\\"\""
```
Expected: `|` (both NULL — tables dropped).

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT column_name FROM information_schema.columns WHERE table_name='user_subscriptions' AND column_name IN ('id','group_id') ORDER BY column_name;\\\"\""
```
Expected: `id` (only id; group_id absent).

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT column_name FROM information_schema.columns WHERE table_name='promocodes' AND column_name LIKE '%granted%';\\\"\""
```
Expected: `plan_id_granted`.

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT permission_key FROM admin_permissions WHERE permission_key LIKE 'groups.%';\\\"\""
```
Expected: empty.

### Step 4.4 — Deploy site

```bash
cd server/backend && go run ./cmd/deploy --site
```

SSH may drop in final stage — retry up to 3 times. Verify bundle deployed:
```bash
curl -s https://admin.hiearth.ru/ | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1
```

### Step 4.5 — Curl smoke

```bash
TOKEN=$(curl -s -X POST https://admin.hiearth.ru/api/admin/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<PASSWORD>"}' | python -c "import sys,json; print(json.load(sys.stdin)['token'])")

# Groups route gone
curl -s -o /dev/null -w "%{http_code}\n" "https://admin.hiearth.ru/api/admin/groups" -H "Authorization: Bearer $TOKEN"
# Expected: 404

# Users without group_id
curl -s "https://admin.hiearth.ru/api/admin/users?limit=2" -H "Authorization: Bearer $TOKEN" | python -c "import sys,json; d=json.load(sys.stdin); print('user fields:', list(d['items'][0].keys()) if d.get('items') else 'no users')"
# Expected: list does NOT contain "group_id" or "group_name". Contains "is_premium".

# Subscriptions have id
curl -s "https://admin.hiearth.ru/api/admin/subscriptions?limit=1" -H "Authorization: Bearer $TOKEN" | python -c "import sys,json; d=json.load(sys.stdin); print('sub fields:', list(d['items'][0].keys()) if d.get('items') else 'no subs')"
# Expected: contains "id", "plan_id"; NO "group_id" or "group_name".

# Promocodes have plan
curl -s "https://admin.hiearth.ru/api/admin/promocodes?limit=1" -H "Authorization: Bearer $TOKEN" | python -c "import sys,json; d=json.load(sys.stdin); print('promo fields:', list(d['items'][0].keys()) if d.get('items') else 'no promo')"
# Expected: contains "plan_id_granted", "plan_name"; NO "group_id_granted" or "group_name".
```

### Step 4.6 — Chrome MCP visual smoke

Use `mcp__claude-in-chrome`. Open https://admin.hiearth.ru/ (hard refresh first).

1. **Sidebar** — verify «Группы» nav item is gone. Expected groups list: Дашборд / Трафик / Пользователи / Подписки / Промокоды / [no Группы] / Серверы / ...
2. **Cmd+K** — type «гру…», no «Группы» match. Type «поль» → «Пользователи» appears.
3. **Manual /groups URL** — navigate to `/groups`. Expected: blank page or fallback (no route → react-router renders nothing / 404).
4. **/users** — page renders without Group column or Group filter Select. Status badges still work. Premium-status badge correct on at least one user with active sub.
5. **/subscriptions** — no Group column. Create-subscription dialog has Plan dropdown, no Group dropdown.
6. **/promocodes** — list shows «Тариф» column with plan name. Click "+ Создать промокод" → dialog has Plan Select. Create a test promo with free-trial plan → save → row appears with plan name.

### Step 4.7 — Close epic + push + memory

```bash
bd close heyearthproject-yvnw --reason "user_groups removed everywhere: migration 000047 applied; backend handlers/biz cleaned; frontend GroupsPage deleted + UsersPage/SubsPage/PromocodesPage updated; premium derives from EXISTS active sub; chrome MCP smoke passed."
bd remember --key user-groups-removed "user_groups concept fully removed 2026-05-25. Migration 000047: user_groups + server_groups tables dropped, user_subscriptions PK is now SERIAL id (was user_id, group_id), promocodes.group_id_granted → plan_id_granted (backfill = free-trial plan id), groups.read/write permissions removed. Premium status now derives via EXISTS (SELECT 1 FROM user_subscriptions WHERE user_id=u.id AND valid_until>NOW()). All admin/biz/frontend group references purged. NOTE: do not confuse with direct_route_services.group_name which is a service category (russia/vk/yandex), unrelated."
git push origin main
```

---

## Self-review

**Spec coverage** (`docs/superpowers/specs/2026-05-25-remove-user-groups-design.md`):
- ✅ Migration 000047 (drop tables, new PK, plan_id_granted backfill, permissions cleanup) → T1.
- ✅ Backend handler + biz cleanup → T2.
- ✅ Premium derivation rewrite (EXISTS active sub) → T2 step 2.5.
- ✅ Frontend GroupsPage delete + dependent page updates → T3.
- ✅ Deploy + smoke (curl + chrome MCP) → T4.
- ✅ All 11 acceptance criteria covered across T1-T4.

**Placeholder scan:** no "TBD" / "TODO" / "implement later". Every edit has either a concrete diff pattern (find / replace) or full code block. Where exact line numbers may shift across the codebase, grep commands precede the edit so the engineer locates the line first.

**Type / API consistency:**
- `plan_id_granted` int field consistent between migration, backend SELECT/INSERT, frontend form payload.
- Subscription `id` int PK consistent between migration (SERIAL), biz upsert pattern, admin handler list/create/delete, frontend column.
- `is_premium` always derived via subquery, never JOIN.
- Beads sub-IDs (`heyearthproject-grp1..grp4`) are placeholders — orchestrator substitutes real `bd create` IDs.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-25-remove-user-groups.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — 4 tasks, each self-contained. T1 is trivial migration, T2 is the big backend pipeline (one subagent does the whole 14-edit chain to keep build green), T3 is frontend cleanup, T4 is deploy + verify.
2. **Inline Execution** — execute in this session with checkpoints.

**Which approach?**
