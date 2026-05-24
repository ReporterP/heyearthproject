# Direct Routing Plan-Level Toggle — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-service `plan_direct_services` junction with a single `plans.direct_routing_enabled` BOOLEAN flag, surface it as a checkbox in the admin plan editor and as a "Доступ к РФ-сервисам" feature line on marketing/payment cards.

**Architecture:** ALTER TABLE adds the bool, backfill sets TRUE for any plan that currently has ≥1 active attached service, DROP TABLE removes the junction. Biz query helpers switch from JOIN-on-junction to a simple bool-gated lookup that returns all active services. Admin handlers shed the `/plans` sub-resource, the `PlanIDs` payload field, and the `set_plans` bulk action. Admin UI loses PlanSelector entirely and gains one checkbox in PlansPage. Marketing site reads the new bool from `/api/v1/plans` and adds one feature line.

**Tech Stack:** Go 1.21+ (`database/sql` + `lib/pq`, no ORM), PostgreSQL 15, React 19 (JSX, no TypeScript), Vite, Tailwind v4. Evidence-driven verification (`go build ./...`, `go vet`, `npm run build`, `curl`).

**Beads epic:** `heyearthproject-c3d7`
**Spec:** `docs/superpowers/specs/2026-05-24-direct-routing-plan-level-design.md`

---

## File map

**Backend (Go):**
- Create: `server/backend/migrations/000046_direct_routing_per_plan.up.sql`
- Create: `server/backend/migrations/000046_direct_routing_per_plan.down.sql`
- Modify: `server/backend/internal/biz/plans.go` (add field + SQL columns)
- Modify: `server/backend/internal/biz/direct_routes_query.go` (rewrite both queries + stats)
- Modify: `server/backend/internal/biz/direct_routes.go` (delete `SetServicePlans`, `GetServicePlanIDs`)
- Modify: `server/backend/internal/biz/direct_routes_import.go` (delete `BulkSetPlans`, `attachDefaultPlans`, `DefaultPlanIDs`)
- Modify: `server/backend/internal/admin/direct_routes_handler.go` (drop `/plans` sub-resource, drop `PlanIDs` and `default_plan_ids` fields, drop `set_plans` action)
- Modify: `server/backend/internal/admin/plans_handlers.go` (parse + forward `direct_routing_enabled`; verify also runs `biz.NotifySubscriptionChange()` after updates)

**Admin web (React/JSX):**
- Modify: `admin_web/src/pages/PlansPage.jsx` (add checkbox + send field)
- Modify: `admin_web/src/pages/DirectRoutesPage.jsx` (remove Plans column + set_plans bulk, add info banner)
- Modify: `admin_web/src/components/DirectRouteEditDialog.jsx` (remove PlanSelector usage)
- Modify: `admin_web/src/components/DirectRoutesImportDialog.jsx` (remove PlanSelector + default_plan_ids)
- Delete: `admin_web/src/components/PlanSelector.jsx`

**Marketing site (React/JSX):**
- Modify: `site/src/pages/LandingPage.jsx` (add feature line ~line 246)
- Modify: `site/src/pages/PaymentPage.jsx` (add feature line ~line 202)
- Modify: `site/src/pages/DashboardPage.jsx` (only if it renders plan features)

---

## Phase 1 — Backend

### Task 1: Migration 000046 — ALTER + backfill + DROP junction (15 min)

**Files:**
- Create: `server/backend/migrations/000046_direct_routing_per_plan.up.sql`
- Create: `server/backend/migrations/000046_direct_routing_per_plan.down.sql`

- [ ] **Step 1.1 — Write `.up.sql`:**

```sql
-- 000046_direct_routing_per_plan.up.sql
-- Replaces the per-service junction table with a single boolean flag on plans.
-- Plans that previously had >=1 active attached service flip to TRUE; the
-- junction is then dropped entirely (no realistic case for per-plan curation).

ALTER TABLE plans ADD COLUMN IF NOT EXISTS direct_routing_enabled BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE plans SET direct_routing_enabled = TRUE
WHERE id IN (
  SELECT DISTINCT pds.plan_id
  FROM plan_direct_services pds
  JOIN direct_route_services s ON s.id = pds.service_id
  WHERE s.is_active = TRUE
);

DROP TABLE IF EXISTS plan_direct_services;
```

- [ ] **Step 1.2 — Write `.down.sql`:**

```sql
-- 000046_direct_routing_per_plan.down.sql
-- Recreates the junction table and backfills attachments for plans that currently
-- have direct_routing_enabled=TRUE, then drops the bool column.

CREATE TABLE IF NOT EXISTS plan_direct_services (
    plan_id    INT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    service_id INT NOT NULL REFERENCES direct_route_services(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, service_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_direct_services_service ON plan_direct_services(service_id);

INSERT INTO plan_direct_services (plan_id, service_id)
SELECT p.id, s.id
FROM plans p
CROSS JOIN direct_route_services s
WHERE p.direct_routing_enabled = TRUE AND s.is_active = TRUE
ON CONFLICT DO NOTHING;

ALTER TABLE plans DROP COLUMN IF EXISTS direct_routing_enabled;
```

- [ ] **Step 1.3 — Evidence: full build still passes** (no Go code changed yet, just confirms no incidental breakage).

```bash
cd server/backend && go build ./...
```
Expected: zero output, exit 0.

- [ ] **Step 1.4 — Commit:**

```bash
git add server/backend/migrations/000046_direct_routing_per_plan.up.sql server/backend/migrations/000046_direct_routing_per_plan.down.sql
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(routing): migration 000046 — plan-level direct routing flag

ALTERs plans with direct_routing_enabled BOOLEAN, backfills TRUE for
plans with any active attached service, then drops plan_direct_services.

Beads: heyearthproject-c3d7
EOF
)"
```

---

### Task 2: biz layer — add field, rewrite query helpers, delete junction-era APIs (30 min)

**Files:**
- Modify: `server/backend/internal/biz/plans.go`
- Modify: `server/backend/internal/biz/direct_routes_query.go`
- Modify: `server/backend/internal/biz/direct_routes.go`
- Modify: `server/backend/internal/biz/direct_routes_import.go`

- [ ] **Step 2.1 — `plans.go`: add field to `Plan` struct.**

Insert `DirectRoutingEnabled bool \`json:"direct_routing_enabled"\`` after `IsActive`. Located around line 24 in current file. Resulting struct fragment:

```go
type Plan struct {
    ID                int      `json:"id"`
    Slug              string   `json:"slug"`
    Name              string   `json:"name"`
    Description       string   `json:"description"`
    PriceRub          int      `json:"price_rub"`
    DurationDays      int      `json:"duration_days"`
    TrafficLimitBytes *int64   `json:"traffic_limit_bytes"`
    AllowedProtocols  []string `json:"allowed_protocols"`
    Features          string   `json:"features"`
    IsActive          bool     `json:"is_active"`
    DirectRoutingEnabled bool  `json:"direct_routing_enabled"`
    SortOrder         int      `json:"sort_order"`
    AutoRenewDefault      bool   `json:"auto_renew_default"`
    MaxActivationsPerUser *int  `json:"max_activations_per_user,omitempty"`
    CreatedAt             string `json:"created_at"`
    UpdatedAt             string `json:"updated_at"`
    ServerIDs []int `json:"server_ids,omitempty"`
}
```

- [ ] **Step 2.2 — `plans.go`: update SELECT queries to include the column.**

Every `SELECT ... FROM plans` query must include `direct_routing_enabled` in the column list and `&p.DirectRoutingEnabled` in the `rows.Scan(...)` / `QueryRow(...).Scan(...)` arguments. Currently the file has two main read paths: `ListActivePlans` (line ~35) and `ListAllPlansForAdmin` (or equivalent — search for `SELECT id, slug, name, description, price_rub`).

Pattern for each SELECT:

```go
rows, err := db.DB.Query(`
    SELECT id, slug, name, description, price_rub, duration_days, traffic_limit_bytes,
           allowed_protocols, COALESCE(features::text, '{}'), is_active, direct_routing_enabled, sort_order, auto_renew_default,
           max_activations_per_user, created_at, updated_at
    FROM plans WHERE is_active = TRUE
    ORDER BY sort_order, price_rub
`)
// ...
err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.PriceRub, &p.DurationDays,
    &p.TrafficLimitBytes, pq.Array(&p.AllowedProtocols), &p.Features,
    &p.IsActive, &p.DirectRoutingEnabled, &p.SortOrder, &p.AutoRenewDefault,
    &p.MaxActivationsPerUser, &p.CreatedAt, &p.UpdatedAt)
```

Apply to all `SELECT ... FROM plans` callsites in `plans.go` (and any sibling like `GetPlanByID`, `GetPlanBySlug`).

- [ ] **Step 2.3 — `plans.go`: update INSERT (CreatePlan) and UPDATE (UpdatePlan).**

`CreatePlan` (line ~142):

```go
err := db.DB.QueryRow(`
    INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes,
                       allowed_protocols, features, is_active, direct_routing_enabled, sort_order,
                       auto_renew_default, max_activations_per_user)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13)
    RETURNING id
`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
    pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.DirectRoutingEnabled,
    p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser).Scan(&id)
```

`UpdatePlan` (line ~161):

```go
_, err := db.DB.Exec(`
    UPDATE plans SET slug=$1, name=$2, description=$3, price_rub=$4, duration_days=$5,
           traffic_limit_bytes=$6, allowed_protocols=$7, features=$8::jsonb, is_active=$9,
           direct_routing_enabled=$10, sort_order=$11, auto_renew_default=$12,
           max_activations_per_user=$13, updated_at=CURRENT_TIMESTAMP
    WHERE id = $14
`, p.Slug, p.Name, p.Description, p.PriceRub, p.DurationDays, p.TrafficLimitBytes,
    pq.Array(p.AllowedProtocols), p.Features, p.IsActive, p.DirectRoutingEnabled,
    p.SortOrder, p.AutoRenewDefault, p.MaxActivationsPerUser, id)
```

- [ ] **Step 2.4 — `direct_routes_query.go`: rewrite `GetDirectRouteServicesForPlan`.**

Full new function body:

```go
func GetDirectRouteServicesForPlan(planID int) ([]DirectRouteService, error) {
    if planID <= 0 {
        return nil, fmt.Errorf("get services for plan: plan id is required")
    }
    var enabled bool
    err := db.DB.QueryRow(
        `SELECT direct_routing_enabled FROM plans WHERE id = $1`, planID).Scan(&enabled)
    if errors.Is(err, sql.ErrNoRows) {
        return []DirectRouteService{}, nil
    }
    if err != nil {
        return nil, fmt.Errorf("read plan flag: %w", err)
    }
    if !enabled {
        return []DirectRouteService{}, nil
    }

    rows, err := db.DB.Query(`
        SELECT s.id, s.slug, s.name, s.group_name, s.source,
               s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
               s.timeout_sec, s.is_active, s.notes,
               s.created_at::text, s.updated_at::text
        FROM direct_route_services s
        WHERE s.is_active = TRUE
        ORDER BY s.group_name, s.name
    `)
    if err != nil {
        return nil, fmt.Errorf("list active services: %w", err)
    }
    defer rows.Close()

    out := []DirectRouteService{}
    for rows.Next() {
        var s DirectRouteService
        var domains, ip4, ip6, cidr4, cidr6, dns pq.StringArray
        if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.GroupName, &s.Source,
            &domains, &ip4, &ip6, &cidr4, &cidr6, &dns,
            &s.TimeoutSec, &s.IsActive, &s.Notes, &s.CreatedAt, &s.UpdatedAt); err != nil {
            return nil, fmt.Errorf("scan service: %w", err)
        }
        s.Domains = []string(domains)
        s.IP4 = []string(ip4)
        s.IP6 = []string(ip6)
        s.CIDR4 = []string(cidr4)
        s.CIDR6 = []string(cidr6)
        s.DNSServers = []string(dns)
        out = append(out, s)
    }
    return out, rows.Err()
}
```

Add `"database/sql"` and `"errors"` imports if missing.

- [ ] **Step 2.5 — `direct_routes_query.go`: rewrite `GetPlanIDsWithDirectRoutes`.**

```go
func GetPlanIDsWithDirectRoutes() ([]int, error) {
    rows, err := db.DB.Query(`
        SELECT id FROM plans
        WHERE direct_routing_enabled = TRUE AND is_active = TRUE
        ORDER BY id
    `)
    if err != nil {
        return nil, fmt.Errorf("plan ids with direct routes: %w", err)
    }
    defer rows.Close()
    out := []int{}
    for rows.Next() {
        var pid int
        if err := rows.Scan(&pid); err != nil {
            return nil, fmt.Errorf("scan plan id: %w", err)
        }
        out = append(out, pid)
    }
    return out, rows.Err()
}
```

- [ ] **Step 2.6 — `direct_routes_query.go`: update `GetDirectRoutesStats` plans_count query.**

Replace the existing `plans_count` query with:

```go
var plansCount int
if err := db.DB.QueryRow(`
    SELECT COUNT(*) FROM plans
    WHERE direct_routing_enabled = TRUE AND is_active = TRUE
`).Scan(&plansCount); err != nil {
    return nil, fmt.Errorf("plans count: %w", err)
}
stats["plans_count"] = plansCount
```

- [ ] **Step 2.7 — `direct_routes.go`: delete `SetServicePlans` and `GetServicePlanIDs`.**

Remove both functions wholesale. No callers remain after Task 3.

- [ ] **Step 2.8 — `direct_routes_import.go`: drop bulk-set-plans + default-plan logic.**

Delete:
1. `BulkSetPlans` function (entire body).
2. `attachDefaultPlans` helper.
3. The `DefaultPlanIDs []int` field from `ImportOptions`.
4. Both call sites in `ImportDirectRoutesJSON` that invoke `attachDefaultPlans(tx, ...)`.

`ImportOptions` becomes:

```go
type ImportOptions struct {
    OverwriteExisting bool
    ActivateNew       bool
    DryRun            bool
}
```

- [ ] **Step 2.9 — Evidence: build + vet clean.**

```bash
cd server/backend && go build ./... && go vet ./internal/biz/...
```
Expected: zero output, exit 0 for both.

- [ ] **Step 2.10 — Commit.**

```bash
git add server/backend/internal/biz/plans.go server/backend/internal/biz/direct_routes_query.go server/backend/internal/biz/direct_routes.go server/backend/internal/biz/direct_routes_import.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(routing): switch biz layer to plan-level direct routing bool

- Plan struct gets DirectRoutingEnabled field; SELECT/INSERT/UPDATE in
  plans.go now read/write it.
- GetDirectRouteServicesForPlan and GetPlanIDsWithDirectRoutes work off
  plans.direct_routing_enabled instead of the dropped junction table;
  enabled plans get the full list of active services.
- GetDirectRoutesStats.plans_count counts enabled plans directly.
- Removed SetServicePlans / GetServicePlanIDs / BulkSetPlans /
  attachDefaultPlans / ImportOptions.DefaultPlanIDs — junction is gone.

Beads: heyearthproject-c3d7
EOF
)"
```

---

### Task 3: admin handlers — drop /plans sub-resource, wire new field in plans handler (20 min)

**Files:**
- Modify: `server/backend/internal/admin/direct_routes_handler.go`
- Modify: `server/backend/internal/admin/plans_handlers.go`

- [ ] **Step 3.1 — `direct_routes_handler.go`: drop `PlanIDs` from payloads.**

Remove `PlanIDs []int \`json:"plan_ids,omitempty"\`` from `DirectRoutePayload`. Remove `PlanIDs []int \`json:"plan_ids,omitempty"\`` from `BulkActionPayload`.

In `CreateDirectRouteHandler`, delete the entire block that runs `biz.SetServicePlans(id, p.PlanIDs)` after create. The handler ends with the audit log + 201 response.

In `UpdateDirectRouteHandler`, audit log call no longer needs `plan_ids` in details — keep just `slug`, `name`, `group`.

- [ ] **Step 3.2 — `direct_routes_handler.go`: drop `SetServicePlansHandler` function entirely.**

Remove the function (around the part that does `var p struct { PlanIDs []int ... }` decode and calls `biz.SetServicePlans`).

- [ ] **Step 3.3 — `direct_routes_handler.go`: drop `set_plans` case from `BulkActionHandler` switch.**

The switch in `BulkActionHandler`:

```go
switch p.Action {
case "activate":
    err = biz.BulkSetActive(p.IDs, true)
case "deactivate":
    err = biz.BulkSetActive(p.IDs, false)
case "delete":
    err = biz.BulkDelete(p.IDs)
default:
    writeAdminError(w, http.StatusBadRequest, "unknown action: "+p.Action)
    return
}
```

(removed: `case "set_plans"` and `plan_ids` from audit log payload — keep just `ids` and `count`.)

- [ ] **Step 3.4 — `direct_routes_handler.go`: drop `DefaultPlanIDs` from `ImportPayload`.**

```go
type ImportPayload struct {
    JSON              string `json:"json"`
    OverwriteExisting bool   `json:"overwrite_existing"`
    ActivateNew       bool   `json:"activate_new"`
    DryRun            bool   `json:"dry_run"`
}
```

In `ImportDirectRoutesHandler`, the `biz.ImportOptions` struct literal drops the `DefaultPlanIDs` field. Audit log payload drops `default_plan_ids`.

- [ ] **Step 3.5 — `direct_routes_handler.go`: drop "plans" sub-resource from `DirectRouteItemRouter`.**

The switch on `sub`:

```go
switch sub {
case "":
    // ... GET / PUT / DELETE handlers (unchanged) ...
default:
    writeAdminError(w, http.StatusNotFound, "unknown sub-resource: "+sub)
}
```

Drop the `case "plans"` branch entirely.

- [ ] **Step 3.6 — `direct_routes_handler.go`: drop `plan_ids` from `GetDirectRouteHandler` response.**

Replace the multi-fetch logic:

```go
func GetDirectRouteHandler(w http.ResponseWriter, r *http.Request, id int) {
    svc, err := biz.GetDirectRouteServiceByID(id)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            writeAdminError(w, http.StatusNotFound, "direct route not found")
            return
        }
        writeAdminError(w, http.StatusInternalServerError, err.Error())
        return
    }
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(map[string]any{"service": svc})
}
```

- [ ] **Step 3.7 — `plans_handlers.go`: parse and forward `direct_routing_enabled`.**

In the Create plan handler payload struct (and the parallel Update struct), add:

```go
DirectRoutingEnabled bool `json:"direct_routing_enabled"`
```

Then propagate to the `biz.Plan` value being passed into `biz.CreatePlan` / `biz.UpdatePlan`:

```go
p := &biz.Plan{
    // ... existing fields ...
    DirectRoutingEnabled: payload.DirectRoutingEnabled,
}
```

- [ ] **Step 3.8 — `plans_handlers.go`: trigger Xray sync after plan mutation.**

In both Create and Update handlers, after a successful `biz.CreatePlan` / `biz.UpdatePlan`, add:

```go
go biz.NotifySubscriptionChange()
```

This ensures `direct_routing_enabled` toggle takes effect within the 2-second debounce.

- [ ] **Step 3.9 — Evidence: build + vet clean.**

```bash
cd server/backend && go build ./... && go vet ./internal/admin/...
```
Expected: zero output, exit 0.

- [ ] **Step 3.10 — Commit.**

```bash
git add server/backend/internal/admin/direct_routes_handler.go server/backend/internal/admin/plans_handlers.go
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin): plan-level direct routing toggle in HTTP layer

direct_routes_handler.go: removed SetServicePlansHandler, plans
sub-resource, PlanIDs/DefaultPlanIDs payload fields, and set_plans
bulk case. GetDirectRouteHandler stops returning plan_ids.

plans_handlers.go: accepts direct_routing_enabled in Create/Update
payloads and triggers NotifySubscriptionChange so Xray rebuilds.

Beads: heyearthproject-c3d7
EOF
)"
```

---

## Phase 2 — Admin UI

### Task 4: PlansPage checkbox (15 min)

**Files:**
- Modify: `admin_web/src/pages/PlansPage.jsx`

- [ ] **Step 4.1 — Add field to the form default state (around line 41).**

Find the line:
```jsx
is_active: true, sort_order: 0, auto_renew_default: true, server_ids: [],
```
Insert before `server_ids`:
```jsx
direct_routing_enabled: false,
```

So the line becomes:
```jsx
is_active: true, direct_routing_enabled: false, sort_order: 0, auto_renew_default: true, server_ids: [],
```

- [ ] **Step 4.2 — Add the checkbox UI next to `is_active`/`auto_renew_default` checkboxes (around line 257-261).**

Insert this block immediately after the `auto_renew_default` checkbox row:

```jsx
<label className="flex items-start gap-2 mt-2">
  <input
    type="checkbox"
    checked={!!editing.direct_routing_enabled}
    onChange={e => setEditing({...editing, direct_routing_enabled: e.target.checked})}
    className="rounded mt-1"
  />
  <span>
    <span className="block">Доступ к РФ-сервисам</span>
    <span className="block text-xs text-text-secondary">
      Трафик к российским банкам, госуслугам и маркетплейсам идёт напрямую, минуя VPN.
    </span>
  </span>
</label>
```

- [ ] **Step 4.3 — Verify field is sent in POST/PUT body.**

PlansPage typically does `JSON.stringify(editing)` for body. Since `editing` now carries `direct_routing_enabled`, the field auto-forwards. If the page uses field whitelisting (e.g. `{slug: editing.slug, name: editing.name, ...}`), add `direct_routing_enabled: editing.direct_routing_enabled` to the whitelist explicitly.

Quick check:
```bash
grep -n "JSON.stringify(editing)\|JSON.stringify({" admin_web/src/pages/PlansPage.jsx
```
If `JSON.stringify(editing)` — done; otherwise add field to the projection.

- [ ] **Step 4.4 — Render the bool in the read-only plan summary card (around lines 100-124, near `Автопродление`).**

Insert after the `Автопродление` paragraph:

```jsx
<p>Доступ к РФ-сервисам: <span className="text-text-primary">{plan.direct_routing_enabled ? 'Включён' : 'Выключен'}</span></p>
```

- [ ] **Step 4.5 — Evidence: build passes.**

```bash
cd admin_web && npm run build
```
Expected: `✓ built in Xs`, exit 0.

- [ ] **Step 4.6 — Commit.**

```bash
git add admin_web/src/pages/PlansPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): direct-routing checkbox in plan editor

Adds 'Доступ к РФ-сервисам' checkbox to the plan create/edit form and
surfaces the flag on the read-only plan card.

Beads: heyearthproject-c3d7
EOF
)"
```

---

### Task 5: DirectRoutesPage cleanup + EditDialog cleanup (20 min)

**Files:**
- Modify: `admin_web/src/pages/DirectRoutesPage.jsx`
- Modify: `admin_web/src/components/DirectRouteEditDialog.jsx`

- [ ] **Step 5.1 — `DirectRoutesPage.jsx`: add info banner at top of returned JSX (right after the header `flex items-center justify-between` block).**

```jsx
<div className="rounded border border-sky-700/40 bg-sky-900/20 p-3 text-sm text-slate-300">
  Сервисы видны пользователям тарифа, у которого включена опция
  «Доступ к РФ-сервисам» в настройках тарифа.
  <a href="/plans" className="ml-1 text-sky-400 hover:underline">Настроить тарифы →</a>
</div>
```

(If router uses `<Link>` instead of `<a>`, prefer that — but `<a href="/plans">` is safe and works.)

- [ ] **Step 5.2 — `DirectRoutesPage.jsx`: drop `set_plans` from bulk button bar (if present).**

In the bulk-actions section (`selected.size > 0` block), the current buttons are Activate / Deactivate / Delete. The plan's prior version did NOT include a `set_plans` button in this bar (it was only in the table edit per row), so usually no change here — but verify by grep:

```bash
grep -n "set_plans" admin_web/src/pages/DirectRoutesPage.jsx
```
If any match exists, remove that button + its handler reference.

- [ ] **Step 5.3 — `DirectRouteEditDialog.jsx`: remove `PlanSelector` import.**

Find the import line:
```jsx
import PlanSelector from './PlanSelector'
```
Delete it.

- [ ] **Step 5.4 — `DirectRouteEditDialog.jsx`: remove plan-related state and effects.**

Delete:
- `const [planIDs, setPlanIDs] = useState([])`
- The `fetchPlanIDs` function (entire body).
- The `fetchPlanIDs(service.id)` call inside the `useEffect` that initialises form.
- The reset `setPlanIDs([])` inside the same effect's else branch.

- [ ] **Step 5.5 — `DirectRouteEditDialog.jsx`: remove `plan_ids` from request payload.**

Inside `handleSubmit`, the payload object had `plan_ids: planIDs`. Delete that line.

- [ ] **Step 5.6 — `DirectRouteEditDialog.jsx`: remove the second `PUT /id/plans` fetch in the edit branch.**

Delete the entire block:

```jsx
const respP = await fetch(`/api/admin/direct-routes/${service.id}/plans`, {
  method: 'PUT',
  headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
  body: JSON.stringify({ plan_ids: planIDs }),
})
if (!respP.ok) {
  const err = await respP.json().catch(() => ({ error: respP.statusText }))
  throw new Error(err.error || respP.statusText)
}
```

- [ ] **Step 5.7 — `DirectRouteEditDialog.jsx`: drop `<PlanSelector ... />` usage in JSX.**

Find:
```jsx
<Row label="Планы">
  <PlanSelector value={planIDs} onChange={setPlanIDs} />
</Row>
```
Delete it.

- [ ] **Step 5.8 — Evidence: build passes.**

```bash
cd admin_web && npm run build
```
Expected: `✓ built in Xs`, exit 0. (If a stale `import PlanSelector` line was left somewhere, Vite errors here — go fix it.)

- [ ] **Step 5.9 — Commit.**

```bash
git add admin_web/src/pages/DirectRoutesPage.jsx admin_web/src/components/DirectRouteEditDialog.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): drop per-service plan attachment from UI

DirectRoutesPage: info banner directing admins to plan settings;
removed Plans column logic (column itself was not yet wired into table).
EditDialog: dropped PlanSelector, plan_ids state, the second PUT to
/id/plans, and all references.

Beads: heyearthproject-c3d7
EOF
)"
```

---

### Task 6: ImportDialog cleanup + delete PlanSelector.jsx (10 min)

**Files:**
- Modify: `admin_web/src/components/DirectRoutesImportDialog.jsx`
- Delete: `admin_web/src/components/PlanSelector.jsx`

- [ ] **Step 6.1 — `DirectRoutesImportDialog.jsx`: remove `PlanSelector` import.**

Delete:
```jsx
import PlanSelector from './PlanSelector'
```

- [ ] **Step 6.2 — `DirectRoutesImportDialog.jsx`: remove `defaultPlanIDs` state.**

Delete:
```jsx
const [defaultPlanIDs, setDefaultPlanIDs] = useState([])
```
Also delete its reset in the `useEffect` close-handler.

- [ ] **Step 6.3 — `DirectRoutesImportDialog.jsx`: remove `default_plan_ids` from request body.**

In `runImport`, the body object has:
```jsx
default_plan_ids: defaultPlanIDs,
```
Delete that line.

- [ ] **Step 6.4 — `DirectRoutesImportDialog.jsx`: remove PlanSelector JSX block.**

Delete:
```jsx
<div className="pt-2">
  <div className="text-xs text-slate-400 mb-1">Привязать импортированные записи к планам</div>
  <PlanSelector value={defaultPlanIDs} onChange={setDefaultPlanIDs} />
</div>
```

Optionally replace with a one-line hint above the action buttons:

```jsx
<div className="mt-2 text-xs text-slate-400">
  Импорт добавляет сервисы в общий каталог. Видимость пользователям задаётся в настройках тарифа.
</div>
```

- [ ] **Step 6.5 — Delete `PlanSelector.jsx`.**

```bash
git rm admin_web/src/components/PlanSelector.jsx
```

- [ ] **Step 6.6 — Evidence: build passes (no missing imports anywhere).**

```bash
cd admin_web && npm run build
```
Expected: `✓ built in Xs`, exit 0.

- [ ] **Step 6.7 — Commit.**

```bash
git add admin_web/src/components/DirectRoutesImportDialog.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(admin-web): import dialog drops plan attachment + remove PlanSelector

ImportDialog: removed defaultPlanIDs state, PlanSelector usage, and
default_plan_ids field from the request body. Replaced with a one-line
hint pointing admins to plan settings.

PlanSelector.jsx: deleted (no remaining consumers).

Beads: heyearthproject-c3d7
EOF
)"
```

---

## Phase 3 — Marketing site

### Task 7: Surface "Доступ к РФ-сервисам" on cards + verify API exposes the field (25 min)

**Files:**
- Modify: `site/src/pages/LandingPage.jsx`
- Modify: `site/src/pages/PaymentPage.jsx`
- Modify (optional): `site/src/pages/DashboardPage.jsx`

- [ ] **Step 7.1 — `LandingPage.jsx`: add feature line in the per-plan features push block.**

At line ~246 (after `if (pf.priority_support) planFeatures.push('Приоритетная поддержка')`), add:

```jsx
if (p.direct_routing_enabled) planFeatures.push('Доступ к РФ-сервисам')
```

So the block becomes:

```jsx
const pf = typeof p.features === 'string' ? JSON.parse(p.features || '{}') : (p.features || {})
if (pf.traffic_masking) planFeatures.push('Маскировка трафика')
if (pf.priority_support) planFeatures.push('Приоритетная поддержка')
if (p.direct_routing_enabled) planFeatures.push('Доступ к РФ-сервисам')
```

- [ ] **Step 7.2 — `PaymentPage.jsx`: add feature line in the existing features array.**

At line ~202 (after `if (planFeatures.priority_support) features.push('Приоритетная поддержка')`), add:

```jsx
if (plan.direct_routing_enabled) features.push('Доступ к РФ-сервисам')
```

- [ ] **Step 7.3 — `DashboardPage.jsx`: check whether it renders plan feature list.**

```bash
grep -n "features\|feature_list\|allowed_protocols\|priority_support\|traffic_masking" site/src/pages/DashboardPage.jsx | head -10
```

If it has a similar features-push pattern, add the same line: `if (subscription.plan?.direct_routing_enabled) features.push('Доступ к РФ-сервисам')`.

If DashboardPage doesn't show plan features, skip this step.

- [ ] **Step 7.4 — Verify public `/api/v1/plans` exposes the new field.**

The Plan struct has `json:"direct_routing_enabled"` tag and the SELECT now reads the column. So if the public endpoint serialises `biz.Plan` directly, the field appears automatically. Check the handler:

```bash
grep -rn "ListActivePlans\|biz.Plan\|/api/v1/plans" server/backend/cmd/api/*.go | head -10
```

Look for whether it uses `json.Marshal(plans)` or a projection. If projection, add `direct_routing_enabled` explicitly.

Direct verification after deploy (Task 8 covers this); preview now by:

```bash
cd server/backend && go build ./...
```
Expected: zero output, exit 0.

- [ ] **Step 7.5 — Evidence: site build passes.**

```bash
cd site && npm run build
```
Expected: `✓ built in Xs`, exit 0.

- [ ] **Step 7.6 — Commit.**

```bash
git add site/src/pages/LandingPage.jsx site/src/pages/PaymentPage.jsx site/src/pages/DashboardPage.jsx
git -c commit.gpgsign=false commit -m "$(cat <<'EOF'
feat(site): surface 'Доступ к РФ-сервисам' on plan cards

LandingPage + PaymentPage pricing cards add a feature line whenever
plan.direct_routing_enabled is true. DashboardPage mirrors it if the
active-plan feature list exists.

Beads: heyearthproject-c3d7
EOF
)"
```

---

## Phase 4 — Deploy + verify

### Task 8: Deploy backend + site + verify acceptance criteria (25 min)

- [ ] **Step 8.1 — Stop and redeploy backend (migration auto-applies on start).**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
cd server/backend && go run quick_deploy.go
```
Expected: `heyearth-api: ACTIVE ✓`.

- [ ] **Step 8.2 — Verify migration 000046 applied: column exists, table dropped.**

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT column_name FROM information_schema.columns WHERE table_name='plans' AND column_name='direct_routing_enabled';\\\"\""
```
Expected: `direct_routing_enabled`.

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT to_regclass('plan_direct_services');\\\"\""
```
Expected: empty (table dropped — `to_regclass` returns NULL).

- [ ] **Step 8.3 — Verify backfill: the four prior-active plans should have the flag = TRUE.**

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT id, slug, direct_routing_enabled FROM plans WHERE is_active = TRUE ORDER BY id;\\\"\""
```
Expected output:
```
1|free-trial|t
4|vless-reality|t
5|all-inclusive|t
6|annual-premium|t
```

- [ ] **Step 8.4 — Deploy site (admin_web + site).**

```bash
cd server/backend && go run ./cmd/deploy --site
```
Expected: rsync output, ends with site deployed.

- [ ] **Step 8.5 — Verify the public /api/v1/plans exposes the new field.**

```bash
curl -s https://hiearth.ru/api/v1/plans | python -c "import json,sys; data=json.load(sys.stdin); [print(p['slug'], p.get('direct_routing_enabled')) for p in (data if isinstance(data, list) else data.get('plans', []))]"
```
Expected: prints `slug True` for plans 1/4/5/6.

If the field doesn't appear, the public handler uses a projection — re-run Step 7.4 and add the field to the projection, rebuild, redeploy.

- [ ] **Step 8.6 — Verify subscription deeplink still works for an existing user on a flag-enabled plan.**

```bash
cd server/backend && go run ./cmd/deploy --ssh "su - postgres -c \"psql -d heyearth -tA -c \\\"SELECT u.subscribe_token::text FROM users u JOIN user_subscriptions us ON us.user_id = u.id WHERE us.valid_until > NOW() AND us.plan_id IS NOT NULL LIMIT 1;\\\"\""
```
Save token as `$TOK`. Then:

```bash
cd server/backend && go run ./cmd/deploy --ssh "curl -s 'https://hiearth.ru/api/v1/user/subscribe/$TOK' | wc -c"
```
Expected: 1.5–1.7 million bytes (proves Happ routing deeplink still embedded).

- [ ] **Step 8.7 — Manual smoke in admin UI.**

1. Log in to https://admin.hiearth.ru.
2. Open **Тарифы** → edit any of plans 4/5/6 → confirm the checkbox **"Доступ к РФ-сервисам"** is checked.
3. Uncheck it on one plan, save → confirm the plan card shows "Выключен"; check `journalctl -u heyearth-api` shows an `xray-sync` triggered.
4. Re-check the box, save → confirm Xray syncs again.
5. Open **Прямая маршрутизация** → confirm info banner is visible, table works, no "Plans" column or `set_plans` bulk button.
6. Open **Edit** on a service row → confirm no plan-attachment block in the dialog.
7. Open **Импорт JSON** → confirm no plan multi-select in the dialog.

- [ ] **Step 8.8 — Visual smoke on marketing site.**

1. Open https://hiearth.ru/ in incognito.
2. Scroll to pricing — confirm paid plan cards show "Доступ к РФ-сервисам" in the feature list.
3. Click the plan → land on the payment page — confirm the same feature line appears.

- [ ] **Step 8.9 — Close epic + push.**

```bash
bd close heyearthproject-c3d7 --reason "Plan-level direct routing flag deployed. Junction dropped. UI cleanup complete. Site cards show feature."
git push origin main
```

---

## Self-review

**Spec coverage:**
- ✅ Migration 000046 (T1)
- ✅ biz layer rewrite + DirectRoutingEnabled field (T2)
- ✅ Removed functions: SetServicePlans, GetServicePlanIDs, BulkSetPlans, attachDefaultPlans, ImportOptions.DefaultPlanIDs (T2)
- ✅ Admin handler cleanup: /plans sub-resource, PlanIDs, default_plan_ids, set_plans case (T3)
- ✅ plans_handlers wires the new field + NotifySubscriptionChange (T3)
- ✅ PlansPage checkbox (T4)
- ✅ DirectRoutesPage info banner + cleanup (T5)
- ✅ EditDialog cleanup (T5)
- ✅ ImportDialog cleanup (T6)
- ✅ PlanSelector.jsx deletion (T6)
- ✅ LandingPage + PaymentPage feature line (T7)
- ✅ DashboardPage conditional update (T7)
- ✅ Public /api/v1/plans field exposure verification (T7)
- ✅ Acceptance criteria #1-#11 all covered by T8 verification steps

**Placeholder scan:** no "TBD"/"TODO"/"implement later"/"similar to" patterns remain. Every code step has either a full code block or a clear surgical patch.

**Type consistency:** `DirectRoutingEnabled` (Go), `direct_routing_enabled` (JSON/SQL/JSX) consistent across all tasks. `GetDirectRouteServicesForPlan` signature unchanged so xray_sync and happ_routing callers don't need touching.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-24-direct-routing-plan-level.md`. Two execution options:**

1. **Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration. Best for this plan since tasks 2-3 are slightly intricate and 4-7 are independent.
2. **Inline Execution** — execute tasks in this session using executing-plans, batch with checkpoints. Lower latency for follow-ups but heavier on context.

**Which approach?**
