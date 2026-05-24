# Direct Routing — Plan-Level Toggle (design)

**Status:** approved
**Date:** 2026-05-24
**Author:** alex (with Claude)
**Related epic:** continuation of `heyearthproject-agh4` (direct routing infrastructure)

## Problem

After the initial direct-routing rollout we have 86 services in `direct_route_services` and a `plan_direct_services` junction table that ties each service to N plans. Admin UX is broken: attaching all 86 services to a new plan means 86 separate clicks. The PlanSelector multi-select inside `DirectRouteEditDialog` and `DirectRoutesImportDialog` adds extra friction without buying anything users actually want — paying customers always get the full bypass list.

On the marketing site and user dashboard there is currently no signal that a plan includes Russian-services bypass. Users cannot see the feature on pricing cards, so it never enters the buying decision.

## Goal

Replace the per-service plan attachment with a single boolean flag on the plan:

```
plans.direct_routing_enabled BOOLEAN NOT NULL DEFAULT FALSE
```

If `TRUE`, the plan grants access to **all currently active** `direct_route_services`. Admin toggles one checkbox in the plan editor. Pricing cards on landing/payment pages show a `Доступ к РФ-сервисам` feature line whenever the flag is set.

The junction table `plan_direct_services` is removed entirely (YAGNI: no realistic case for per-plan curated subsets).

## Non-goals

- Per-plan curated subsets of direct services (use the bool, or split into separate groups later if ever needed).
- Per-user opt-in toggles (out of scope; a separate "routing profiles" feature is parked).
- Auto-domain discovery (separate spec, follows this one).
- Migration of the source `ip-list.json` archive (already committed; nothing to re-import).

## Data model

Migration `000046_direct_routing_per_plan`:

```sql
-- up
ALTER TABLE plans ADD COLUMN direct_routing_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill: every plan that has at least one active attached service becomes TRUE.
UPDATE plans SET direct_routing_enabled = TRUE
WHERE id IN (
  SELECT DISTINCT pds.plan_id
  FROM plan_direct_services pds
  JOIN direct_route_services s ON s.id = pds.service_id
  WHERE s.is_active = TRUE
);

DROP TABLE IF EXISTS plan_direct_services;
```

```sql
-- down
CREATE TABLE IF NOT EXISTS plan_direct_services (
    plan_id    INT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    service_id INT NOT NULL REFERENCES direct_route_services(id) ON DELETE CASCADE,
    PRIMARY KEY (plan_id, service_id)
);
CREATE INDEX IF NOT EXISTS idx_plan_direct_services_service ON plan_direct_services(service_id);

-- Recreate attachments for every plan that has the flag enabled,
-- attaching every currently-active service.
INSERT INTO plan_direct_services (plan_id, service_id)
SELECT p.id, s.id
FROM plans p
CROSS JOIN direct_route_services s
WHERE p.direct_routing_enabled = TRUE AND s.is_active = TRUE
ON CONFLICT DO NOTHING;

ALTER TABLE plans DROP COLUMN IF EXISTS direct_routing_enabled;
```

The four currently active plans (1, 4, 5, 6) all have ≥1 attached active service, so all four flip to `TRUE` immediately and user-facing behaviour does not change.

## Backend changes

### `internal/biz/direct_routes_query.go`

```go
func GetDirectRouteServicesForPlan(planID int) ([]DirectRouteService, error) {
    if planID <= 0 {
        return nil, fmt.Errorf("plan id required")
    }
    var enabled bool
    err := db.DB.QueryRow(
        `SELECT direct_routing_enabled FROM plans WHERE id = $1`, planID).Scan(&enabled)
    if errors.Is(err, sql.ErrNoRows) {
        return []DirectRouteService{}, nil
    }
    if err != nil {
        return nil, fmt.Errorf("plan flag: %w", err)
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
        ORDER BY s.group_name, s.name`)
    // ...scan into []DirectRouteService...
}

func GetPlanIDsWithDirectRoutes() ([]int, error) {
    rows, err := db.DB.Query(
        `SELECT id FROM plans WHERE direct_routing_enabled = TRUE AND is_active = TRUE ORDER BY id`)
    // ...
}
```

`GetDirectRoutesStats()` updates `plans_count` query: `SELECT COUNT(*) FROM plans WHERE direct_routing_enabled = TRUE AND is_active = TRUE`.

### Removed functions

- `biz.SetServicePlans`
- `biz.GetServicePlanIDs`
- `biz.BulkSetPlans`
- `biz.attachDefaultPlans`
- `biz.ImportOptions.DefaultPlanIDs` field

### `internal/biz/plans.go`

Add field `DirectRoutingEnabled bool \`json:"direct_routing_enabled"\`` to the `Plan` struct. Update `SELECT` and `INSERT`/`UPDATE` builders to read/write the new column.

### `internal/admin/direct_routes_handler.go`

- Remove `SetServicePlansHandler` and the route `PUT /api/admin/direct-routes/{id}/plans` (also remove the `"plans"` sub-resource case from `DirectRouteItemRouter`).
- Remove `PlanIDs` from `DirectRoutePayload`.
- Remove case `"set_plans"` from `BulkActionHandler` switch (return 400 for it explicitly to surface client-side staleness).
- Remove `DefaultPlanIDs` field from `ImportPayload` and its forwarding into `biz.ImportOptions`.
- `GetDirectRouteHandler` stops returning `plan_ids` — single-object response is just `{"service": {...}}`.

### `internal/admin/plans_handlers.go`

Extend Create and Update handlers to accept `direct_routing_enabled` (bool) in payload, validate to bool, write to DB. Call `biz.NotifySubscriptionChange()` after any update that changes the flag (Xray sync must rebuild because the user→plan→direct rule chain changed).

## Admin UI changes

### `admin_web/src/pages/PlansPage.jsx`

In the plan edit form, add a checkbox row near other toggles (e.g. `is_active`):

```jsx
<label className="flex items-start gap-2">
  <input
    type="checkbox"
    className="mt-1"
    checked={!!form.direct_routing_enabled}
    onChange={(e) => setForm({...form, direct_routing_enabled: e.target.checked})}
  />
  <span>
    <span className="block text-slate-100">Доступ к РФ-сервисам</span>
    <span className="block text-xs text-slate-400">
      Трафик к российским банкам, госуслугам, маркетплейсам идёт напрямую,
      минуя VPN.
    </span>
  </span>
</label>
```

Form submit POST/PUT includes `direct_routing_enabled` in body.

### `admin_web/src/pages/DirectRoutesPage.jsx`

- Remove the `Plans` column from the services table (no per-service attachment any more).
- Remove `set_plans` from bulk action menu.
- Add an info banner near page header:

```jsx
<div className="rounded border border-sky-700/40 bg-sky-900/20 p-3 text-sm text-slate-300">
  Сервисы видны пользователям тарифа, у которого включена опция
  «Доступ к РФ-сервисам» в настройках тарифа.
  <Link to="/plans" className="ml-1 text-sky-400 hover:underline">Настроить тарифы →</Link>
</div>
```

### `admin_web/src/components/DirectRouteEditDialog.jsx`

- Remove `PlanSelector` import and usage.
- Remove `planIDs` state, `fetchPlanIDs` effect, and the second PUT to `/{id}/plans` after save.
- Remove `plan_ids` from the payload object.
- Dialog becomes one-step: edit service fields → POST/PUT → done.

### `admin_web/src/components/DirectRoutesImportDialog.jsx`

- Remove `PlanSelector` import and `defaultPlanIDs` state.
- Remove `default_plan_ids` from request body.
- Step text: "Импортируйте список сервисов. Чтобы они стали доступны пользователям,
включите «Доступ к РФ-сервисам» в нужных тарифах."

### `admin_web/src/components/PlanSelector.jsx`

Delete the file. No remaining consumers.

## Marketing site changes

### `site/src/pages/LandingPage.jsx`

Inside the plan-card features rendering loop, after the existing feature pushes:

```jsx
if (p.direct_routing_enabled) {
  planFeatures.push('Доступ к РФ-сервисам')
}
```

(Adjust to the exact variable names used in the existing render block; the project already extracts `pf = ... features` near line 244.)

### `site/src/pages/PaymentPage.jsx` (~line 195-202)

```jsx
if (planFeatures.traffic_masking) features.push('Маскировка трафика')
if (planFeatures.priority_support) features.push('Приоритетная поддержка')
if (plan.direct_routing_enabled) features.push('Доступ к РФ-сервисам')
```

### `site/src/pages/DashboardPage.jsx`

If a plan-feature list is rendered for the active subscription, mirror the same addition. Otherwise no change.

### Public `/api/v1/plans`

Verify the public plans endpoint returns the new `direct_routing_enabled` field. Two possibilities:

1. It serialises the whole `Plan` struct → field appears automatically once `biz.Plan` is updated.
2. It uses a hand-crafted projection → add the field explicitly.

If projection is field-whitelisted, extend it.

## Behaviour cross-check (regressions to avoid)

- **Xray sync**: `GetPlanIDsWithDirectRoutes` and `GetDirectRouteServicesForPlan` continue to feed the existing per-plan rule construction in `cmd/api/xray_sync.go`. The new query path returns the same data (all active services) when the flag is true. No code change needed in `xray_sync.go`.
- **Happ deeplink**: same path through `BuildHappRoutingDeeplinkForPlan` → same data returned. No change.
- **Subscription size**: unchanged (~1.6 MB with current 86 services).
- **Direct-routes admin endpoints**: all `direct-routes` CRUD endpoints continue to work, just without the `/plans` sub-resource and `plan_ids` field.

## Acceptance criteria

1. After deploy, all four currently active plans (1, 4, 5, 6) show `direct_routing_enabled = TRUE` in the database.
2. `DROP TABLE plan_direct_services` succeeded — table does not exist.
3. Admin PlansPage shows the "Доступ к РФ-сервисам" checkbox; toggling it on/off persists and triggers a Xray rebuild (visible in `journalctl -u heyearth-api`).
4. Admin DirectRoutesPage no longer shows a "Plans" column or a "set_plans" bulk action.
5. EditDialog and ImportDialog no longer ask for plan attachment.
6. `PlanSelector.jsx` is deleted from the repo.
7. Landing page card for a paid plan with the flag on shows the line "Доступ к РФ-сервисам" in its feature list.
8. Payment page card shows the same line.
9. Public `/api/v1/plans` response includes `direct_routing_enabled: true` for the four paid plans.
10. Subscription deeplink for a user on any plan with the flag on still contains a populated `DirectSites`/`DirectIp` list (~20K domains, ~12K IPs), proving the bypass continues to work.
11. Disabling the flag on a plan (set FALSE) causes the next-fetched subscription for users on that plan to contain an **empty** routing deeplink (or no deeplink at all if MTProto exit IPs are also absent).

## Out of scope / follow-up

- **Auto-domain discovery** (Feature B from the brainstorming) — separate spec.
- **Per-user direct routing profiles** — parked.
- **Per-group toggle** (e.g. "only russia group, not vk") — parked; brainstorm only if real demand appears.
