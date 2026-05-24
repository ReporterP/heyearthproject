# Remove user_groups Everywhere (design)

**Status:** approved
**Date:** 2026-05-25
**Author:** alex (with Claude)

## Problem

`user_groups` — legacy concept от плана 0, до того как ввели `plans`. Сейчас группы дублируют функцию planов: premium/non-premium, per-group server access (через `server_groups`), per-group promocode grants. Это создаёт двойную систему сущностей и неудобный admin UX:
- `GroupsPage` — отдельная страница без realistic use case (только 2 системные группы: free-trial + premium).
- `UsersPage` имеет filter по group_id, но в реальности все пользователи имеют одинаковый group через subscription — group_id ничего не различает.
- `PromocodesPage` form требует выбрать "какую group выдать" — но всегда выбирается одна из двух системных.
- `user_subscriptions` имеет PRIMARY KEY `(user_id, group_id)` — но `group_id` бессмыслен, plan_id уже несёт всю информацию.
- Premium-статус derive через chain `user → subscription → user_groups.is_premium` — лишний JOIN на каждом запросе.

Пользователь просит полностью убрать концепт групп: frontend, backend, DB. Никакой обратной совместимости. Premium-статус derive как "user has any active subscription".

## Goal

1. **DB** (migration 000047): drop `user_groups`, `server_groups` tables. Drop `users_subscriptions.group_id` column; add `id SERIAL PRIMARY KEY`. Replace `promocodes.group_id_granted` with `plan_id_granted`. Drop `groups.read`/`groups.write` permissions.
2. **Backend**: remove all groups-related handlers/biz/types. Update remaining queries to drop JOINs on user_groups. Premium derivation = EXISTS active subscription.
3. **Frontend**: remove GroupsPage, Sidebar entry, Cmd+K command, i18n keys. UsersPage / SubscriptionsPage drop group filter+column. PromocodesPage form replaces Group dropdown with Plan dropdown.
4. **Deploy + smoke**: chrome MCP проверяет что /groups → 404, no group filter UI anywhere, promocode form has plan picker.

## Non-goals

- Tenant-level `tenants` table — это другая сущность (B2B companies), не trogaem.
- Direct routing `direct_route_services.group_name` — это категория сервисов (russia/vk/yandex), не user groups. Не трогаем.
- Backend `server_groups` cleanup audit — если где-то ещё используется (Xray sync уже на plan_servers), удалим в коде.
- Audit-log historic `action LIKE 'groups.%'` rows — оставляем как read-only history.
- Sub-project B (Users+Subs UX merge) — отдельный brainstorm-cycle.
- Tests — у нас их нет; evidence-driven verification.

## Stack constraints

- PostgreSQL 15, migration runs auto on backend startup.
- Go backend, raw `database/sql + lib/pq`. No ORM migrations to fix.
- React 19 + Vite + Tailwind v4 admin. JSX.
- No backend automated tests. Evidence: `go build ./... && go vet ./...` + `npm run build` + chrome MCP smoke.
- All themes/accents/responsive done — no work there.

## Architecture / scope

```
DB:
  + 000047_remove_groups.up.sql (ALTER + DROP)
  + 000047_remove_groups.down.sql (skeleton recreate — best-effort)

Backend (delete):
  - server/backend/internal/admin/groups_handlers.go
  - any biz/group.go (if standalone — likely inline in user.go)

Backend (modify):
  - cmd/api/main.go (routes drop)
  - cmd/api/payment_handlers.go (promo code redemption uses plan_id_granted)
  - cmd/api/xray_sync.go (if any direct user_groups reads — likely already migrated)
  - internal/admin/auth.go (permissionCatalog: drop groups.* entries)
  - internal/admin/users_handlers.go (drop group filter/scan)
  - internal/admin/subscriptions_handlers.go (drop group_id; add id)
  - internal/admin/promocodes_handlers.go (group_id_granted → plan_id_granted)
  - internal/admin/dashboard_handlers.go (drop "users by group" KPI if any)
  - internal/admin/types.go (drop Group struct + group fields from UserRow/SubscriptionRow)
  - internal/biz/user.go (drop GroupID/GroupName from User struct; rewrite IsPremium derivation)
  - internal/biz/payments.go (ActivateSubscriptionWithPlan: drop group_id from INSERT)
  - internal/biz/server.go (drop server_groups queries if any)
  - internal/biz/client_identity.go (drop group logic if any)
  - internal/biz/plans.go (no change expected, verify)

Frontend (delete):
  - admin_web/src/pages/GroupsPage.jsx

Frontend (modify):
  - admin_web/src/main.jsx (drop import + route)
  - admin_web/src/components/Sidebar.jsx (drop nav item; drop UsersRound import if unused)
  - admin_web/src/components/commands.js (drop go-groups command)
  - admin_web/src/i18n/ru.js + en.js (drop navGroups; check for other group-related keys)
  - admin_web/src/api/admin.js (drop groups CRUD helpers if any)
  - admin_web/src/pages/UsersPage.jsx (drop filter group_id + column)
  - admin_web/src/pages/SubscriptionsPage.jsx (drop filter group_id + column)
  - admin_web/src/pages/PromocodesPage.jsx (group_id_granted → plan_id_granted; form Plan dropdown)
```

## DB migration (000047)

`server/backend/migrations/000047_remove_groups.up.sql`:

```sql
-- 000047_remove_groups.up.sql
-- Remove the entire user_groups concept. Groups have been superseded by plans:
-- premium status now = any active subscription, server access = plan_servers,
-- promocode payload = plan_id (not group_id).

-- 1. user_subscriptions: new SERIAL id PK, drop group_id column.
-- The old PK (user_id, group_id) blocked having multiple historical subs per
-- user; new id PK allows clean subscription history.
ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_pkey;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS id SERIAL PRIMARY KEY;
ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS group_id;

-- 2. promocodes: replace group_id_granted with plan_id_granted.
ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS plan_id_granted INTEGER REFERENCES plans(id);

-- Best-effort backfill: existing promocodes with group_id_granted get
-- mapped to free-trial plan (safest default — minimum entitlement).
UPDATE promocodes
SET plan_id_granted = (SELECT id FROM plans WHERE slug = 'free-trial' LIMIT 1)
WHERE plan_id_granted IS NULL;

ALTER TABLE promocodes DROP COLUMN IF EXISTS group_id_granted;

-- 3. server_groups: legacy junction, no longer used by Xray sync (now plan_servers).
DROP TABLE IF EXISTS server_groups;

-- 4. Drop user_groups table itself.
DROP TABLE IF EXISTS user_groups;

-- 5. Drop groups.read/write permissions (cascade removes admin_role_permissions rows).
DELETE FROM admin_permissions
WHERE permission_key IN ('groups.read', 'groups.write');
```

`server/backend/migrations/000047_remove_groups.down.sql`:

```sql
-- 000047_remove_groups.down.sql
-- Best-effort skeleton restoration. Data is permanently lost — this is for
-- structural compatibility only and should not be relied upon.

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
-- The original (user_id, group_id) PK cannot be restored safely because
-- group_id is now NULL for all rows. Skipping — manual fix required.

ALTER TABLE promocodes ADD COLUMN IF NOT EXISTS group_id_granted INTEGER;
ALTER TABLE promocodes DROP COLUMN IF EXISTS plan_id_granted;

INSERT INTO admin_permissions (permission_key, name, description) VALUES
    ('groups.read',  'Groups Read',  'View user groups'),
    ('groups.write', 'Groups Write', 'Manage user groups')
ON CONFLICT (permission_key) DO NOTHING;
```

## Backend changes

### Premium derivation

Currently in `internal/biz/user.go::ListUsers`:
```sql
SELECT u.id, u.user_id, ..., COALESCE(bool_or(g.is_premium), FALSE) AS is_premium
FROM users u
LEFT JOIN user_subscriptions us ON us.user_id = u.id
LEFT JOIN user_groups g ON g.id = us.group_id
GROUP BY u.id
```

New:
```sql
SELECT u.id, u.user_id, ...,
       COALESCE((SELECT TRUE FROM user_subscriptions us
                 WHERE us.user_id = u.id
                   AND us.valid_until > CURRENT_TIMESTAMP
                 LIMIT 1), FALSE) AS is_premium
FROM users u
```

Apply same pattern to `ListActiveVlessClients` and any other queries that need `is_premium`.

### Plain deletions

- `internal/admin/groups_handlers.go` — entire file removed.
- `cmd/api/main.go` — drop the 2 `http.HandleFunc("/api/admin/groups...")` registrations.
- `internal/admin/auth.go::permissionCatalog` — remove the 2 `groups.read`/`groups.write` entries.

### Edits

- `internal/admin/users_handlers.go::ListUsersHandler`:
  - Drop `?group_id=` filter parsing.
  - Drop `group_id`/`group_name` from SELECT and Scan args.
  - Update `allowedSort` map: remove `group_id` if present.
- `internal/admin/subscriptions_handlers.go`:
  - Drop `group_id` from SELECT/INSERT/Scan; add `id`.
  - List response shape: `[{id, user_id, plan_id, valid_until, auto_renew, ...}]`.
- `internal/admin/promocodes_handlers.go`:
  - All occurrences of `group_id_granted` → `plan_id_granted`.
  - List/Get response includes `plan_id_granted`, `plan_name` (via JOIN plans).
  - Create/Update payload accepts `plan_id_granted` int.
- `internal/admin/types.go`:
  - Remove `Group` struct if present (it's the API DTO for user_groups).
  - Strip group fields from `UserRow` / `SubscriptionRow` / `PromocodeRow`.
- `internal/admin/dashboard_handlers.go`:
  - If KPI "users_by_group" exists — remove. Add nothing.
- `internal/biz/user.go::User` struct:
  - Drop `GroupID int` and `GroupName string`.
  - Keep `IsPremium bool` (now derived).
- `internal/biz/payments.go::ActivateSubscriptionWithPlan`:
  - INSERT into user_subscriptions: drop `group_id` column from query.
- `cmd/api/payment_handlers.go`:
  - Promocode redemption: replace `INSERT ... group_id` with `INSERT ... plan_id` from `promocodes.plan_id_granted`.
- `internal/biz/server.go`:
  - If `replacePlanServers` or sibling references `server_groups` — remove.
- `internal/biz/client_identity.go`:
  - Drop any group-related logic.

### Build verification
```bash
cd server/backend && go build ./... && go vet ./...
```
Any compile error remaining = clear instruction (typed type `Group` undefined, `GroupID` undefined, etc.) — fix by removing the reference.

## Frontend changes

### Plain deletions

- `admin_web/src/pages/GroupsPage.jsx` — entire file.

### Edits

- `main.jsx`:
  - Remove `import GroupsPage from './pages/GroupsPage.jsx'`.
  - Remove `<Route path="/groups" element={<GroupsPage/>}/>`.

- `Sidebar.jsx`:
  - Remove nav item `{ to: '/groups', icon: UsersRound, label: 'navGroups', perm: 'groups.read' }`.
  - Remove `UsersRound` from lucide-react import if unused elsewhere in file.

- `components/commands.js`:
  - Remove `{ id: 'go-groups', label: 'Группы', icon: UsersRound, kind: 'page', href: '/groups', group: 'Пользователи' }`.
  - Remove UsersRound import if unused.

- `i18n/ru.js` + `i18n/en.js`:
  - Remove key `navGroups`.
  - Grep for any other group-related keys: `groupsTitle`, `groupNew`, `groupName`, `fieldGroup`. Remove those that are only used by the deleted page.

- `api/admin.js`:
  - Remove any `listGroups`/`getGroup`/`createGroup`/`updateGroup`/`deleteGroup` helpers.

- `pages/UsersPage.jsx`:
  - Remove `group_id` from `useUrlState` default.
  - Remove Group column from `<Table>`.
  - Remove Group Select from filter row.
  - Remove `groups` fetch on mount.

- `pages/SubscriptionsPage.jsx`:
  - Remove `group_id` from `useUrlState`.
  - Remove Group column from Table.
  - Remove Group fetch + Select from EditDialog if any.

- `pages/PromocodesPage.jsx`:
  - Form: replace Group Select with Plan Select (fetch from `/api/admin/plans`).
  - Field name: `group_id_granted` → `plan_id_granted` in form state + payload.
  - Display column "Группа" → "Тариф" (plan name).

- `pages/DirectRoutesPage.jsx`:
  - **Не трогаем**. The grep hit `group_name` which is a direct_route_service category (russia/vk/yandex), unrelated to user_groups.

### Build verification

```bash
cd admin_web && npm run build
```
Errors will be self-explanatory (`GroupsPage is not defined`, missing import etc.) — fix by removing the corresponding line.

## Deploy + smoke

1. **Pre-deploy SQL check**: verify free-trial plan exists.
2. Stop backend, deploy backend (`quick_deploy.go`) — migration auto-applies.
3. Verify migration:
   - `to_regclass('user_groups')` → NULL.
   - `to_regclass('server_groups')` → NULL.
   - `SELECT column_name FROM information_schema.columns WHERE table_name='user_subscriptions' AND column_name IN ('id','group_id')` → only `id`.
   - `SELECT column_name FROM information_schema.columns WHERE table_name='promocodes' AND column_name LIKE '%granted%'` → `plan_id_granted`.
   - `SELECT permission_key FROM admin_permissions WHERE permission_key LIKE 'groups.%'` → empty.
4. Deploy site.
5. Chrome MCP smoke (~5 mins):
   - Login as admin.
   - Sidebar: no «Группы» item. ✓
   - Cmd+K: search «гру…» — no «Группы» match. ✓
   - `/groups` URL → blank or 404 (since route removed). ✓
   - `/users` page: no Group column, no Group filter. Premium badge still works (based on derived flag). ✓
   - `/subscriptions` page: no Group column. Add Subscription dialog has no Group field. ✓
   - `/promocodes` page: column shows Plan instead of Group; create promo dialog has Plan dropdown. ✓
   - Create a test promo with free-trial plan → activate → user gets a subscription on free-trial. ✓
   - `/api/admin/groups` (curl) → 404. ✓

## Acceptance criteria

1. ✅ Migration 000047 applied: `user_groups`, `server_groups` dropped; `user_subscriptions.id` SERIAL PK with no group_id; `promocodes.plan_id_granted` exists with no group_id_granted; `groups.*` permissions removed.
2. ✅ `cd server/backend && go build ./... && go vet ./...` clean.
3. ✅ `cd admin_web && npm run build` clean.
4. ✅ Sidebar has no «Группы» nav item.
5. ✅ Cmd+K has no go-groups command.
6. ✅ UsersPage: no Group filter / column. Premium status renders correctly (derived).
7. ✅ SubscriptionsPage: no Group column / form field.
8. ✅ PromocodesPage: Plan Select replaces Group Select in form; display column shows plan name.
9. ✅ `curl /api/admin/groups` returns 404 (route removed).
10. ✅ End-to-end: create + redeem a plan-based promocode → user gets active subscription with `plan_id` set, `valid_until` correct, premium-derived = true.
11. ✅ `IsPremium` derivation in `ListUsers` returns TRUE for users with active subs, FALSE otherwise.

## Risks

- 🟡 **Migration ordering**: `DROP COLUMN group_id` from user_subscriptions is destructive. If any backend code still references it after deploy, queries break. Mitigation: backend changes go in the SAME commit pipeline; deploy stops the api before migration runs.
- 🟡 **Premium derivation regression**: ListUsers query rewrite changes from `LEFT JOIN user_groups + bool_or` to `EXISTS subquery`. Subtle bug risk if old query produced unexpected behaviour (e.g. expired-sub user counted as premium). Acceptance criteria #11 catches this.
- 🟡 **Promocode backfill mapping**: existing promocodes mapped to free-trial plan. If user had created a "premium-grant" promo (group_id_granted = id of premium group), the redemption now grants free-trial instead — entitlement downgrade. Admin can manually update those after migration.
- 🟢 **i18n cleanup**: leftover keys are harmless (unused). Grep finds them; remove for cleanliness.
- 🟢 **server_groups DROP**: Xray sync verified to use `plan_servers` now (Phase 1 task 3 of plans-flexible cycle), so dropping the table is safe.
