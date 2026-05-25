# Users + Subscriptions UX Merge — Design Spec

**Beads epic:** [heyearthproject-unpl](https://heyearthproject-unpl)
**Date:** 2026-05-25
**Prerequisite:** Sub-project A (remove user_groups) already shipped — commits 4d1c857 / 9a86445 / aa098c7. This spec assumes `users_groups` is gone, premium derives from EXISTS active sub, `user_subscriptions` has SERIAL `id` PK.

## Problem

Today the admin has two top-level pages: «Пользователи» and «Подписки». To answer "what's the subscription history of alice@example.com" an admin must:
1. Find alice in /users, copy her DB id from the row.
2. Navigate to /subscriptions, paste the id, filter.
3. To extend her subscription — manually re-enter the DB id again in a Create dialog.

User feedback (verbatim): «подписки и пользователи можно же спокойно объединить в единую историю, максимально удобное взаимодействие между ними».

## Goal

A single user-centric workflow where opening a user exposes everything about them — current plan, history, traffic, payments, devices — without leaving the master list. Reduce page transitions to zero for the common "look at user X" flow.

## Architecture — Master + Drawer (Linear/Vercel style)

```
┌──────────────────────────────────┬─────────────────────────────────┐
│  /users  Master list             │  Drawer (slide-in from right)   │
│  ────────────────                │  ──────────────────────         │
│  Filters: tabs + search          │  Header: ✕ · Devices · Удалить  │
│  Table: 7 cols + traffic bar     │  Tabs: Профиль · Подписки ·     │
│  Sort default: last_seen ↓       │        Трафик+Платежи           │
│  Row click → opens drawer        │  Content (per-tab)              │
│                                  │                                 │
│  J/K navigates rows              │  ESC closes                     │
│  Enter opens drawer              │                                 │
└──────────────────────────────────┴─────────────────────────────────┘
```

On **mobile (<768px)** the drawer becomes a fullscreen modal — master list is hidden underneath. Reuses existing `<Dialog>` primitive's fullscreen behavior on small screens. Above md the drawer is a right-side slide-in panel ~560px wide.

The `/subscriptions` sidebar entry is **renamed to «История подписок»** and becomes a global audit view: filter by plan / status / expires-soon / promocode. Clicking a row routes back to /users with the drawer open on the target user, Подписки tab pre-selected.

## Master List

### Columns (7 + 1 hidden actions)

| Col | Source | Format | Sort |
|-----|--------|--------|------|
| Пользователь | `username` + `user_id` (UUID truncated) | username top line, UUID below in mono small | `username` |
| План | `current_plan` (LATERAL JOIN existing) | Badge if has plan, «—» otherwise | `plan_id` |
| Статус | derived: active / expired / blocked | Colored dot only (legend tooltip) | — |
| Действует до | `valid_until` | `dd.MM.yy` (mono); «—» if no plan | `valid_until` |
| Был онлайн | NEW: `last_seen` = MAX(`user_devices.last_seen_at`) | «2 мин» / «3 ч» / «6 дн» | `last_seen` |
| Создан | `created_at` | `dd.MM.yy` | `created_at` |
| Трафик 30д | NEW: `traffic_30d_bytes` + `traffic_limit_bytes` | inline progress bar + «65 / 100 ГБ» | `traffic_30d_bytes` |

Quick action (no separate column): hovering a row shows a small ⋯ icon at the right edge → block/unblock toggle, copy UUID, delete. The big surface (rest of the row) opens the drawer.

### Filters

Replaces the current «filterAll / filterActive / filterBlocked» buttons with status-tabs at the top:

| Tab | Predicate | Count |
|-----|-----------|-------|
| Все | — | total |
| Активные | `valid_until > NOW()` AND NOT blocked | count |
| Истекают | `valid_until BETWEEN NOW() AND NOW()+7d` | count |
| Без подписки | no `user_subscriptions` row OR all expired | count |
| Заблокированные | `is_blocked = TRUE` | count |

Plus the existing search box (username + UUID prefix).

### Default sort

`last_seen DESC`. New users without `last_seen` go to the bottom (NULL LAST).

## Drawer

**Width:** `max-w-xl` (~560px) above md, fullscreen on <md. Slides in from right with `transform translate-x-0/translate-x-full` transition. Backdrop dim. ESC closes. Body scroll lock.

### Header

```
┌────────────────────────────────────────────────────┐
│  alice                                          ✕  │
│  a3f4-5e6c-7b8a-… · premium · создан 12.03         │
│  ────────────────────────────────────────────────  │
│  [Devices 2]  [Заблокировать]  [Удалить юзера]    │
└────────────────────────────────────────────────────┘
```

The «Devices» button opens the existing devices modal (no rewrite). «Заблокировать» is the same toggle as the master row's quick action. «Удалить» asks confirm.

### Tabs

#### Tab 1 — Профиль

```
Username        [alice                              ]
Password        [(leave blank to keep)              ]
Role            [user / premium ▼]
Telegram ID     12345678 (mono)
Создан          12.03.26 14:23
Обновлён        20.05.26 09:11
────
[Сохранить]
```

Edit-in-place — no separate dialog. Pencil icon next to each editable field to enter edit mode, save commits via existing `PATCH /api/admin/users/{id}`.

#### Tab 2 — Подписки

History table (newest first):

```
Период              План          Цена    Промокод    Статус   Действия
12.03.26 → 12.06.26 all-incl.     300₽    —           active   [ред.] [уд.]
14.12.25 → 14.03.26 socks-basic   100₽    NEWYEAR     expired  [уд.]
01.10.25 → 01.12.25 free-trial    0₽      WELCOME     expired  [уд.]
```

Top-right: **[+ Подписка]** button. Opens an inline form (not a separate modal) inside the drawer:

```
Тариф         [all-inclusive (300₽ / 30 дн.) ▼]
Действует до  [12.06.2026 📅]
Авто-продление [✓]
Статус        [active ▼]
[Отмена] [Создать]
```

`user_db_id` is implicit from the drawer context — no manual entry. Uses existing `POST /api/admin/subscriptions` with `user_db_id` from the open drawer state.

#### Tab 3 — Трафик + Платежи

Two stacked sections:

**Трафик за 30 дней** — line chart, daily bytes, dotted line showing plan limit. Below: «Всего: 65 ГБ / 100 ГБ (65%)» + breakdown by exit-node country (mini table).

**Платежи** — chronological list:

```
20.05.26 14:23  +300₽   YooKassa · subscription · all-inclusive    succeeded
14.12.25 09:11  +100₽   YooKassa · subscription · socks-basic      succeeded
14.12.25 09:11  -50₽    промокод NEWYEAR (50%)                     applied
```

Uses existing `GET /api/admin/payments?user_id={user_id}` + new `GET /api/admin/users/{id}/traffic?days=30`.

## История подписок (renamed /subscriptions)

Refactor of the existing SubscriptionsPage:

- **Title:** «История подписок» (was «Подписки»)
- **Sidebar label:** same — and in Cmd+K rename `go-subscriptions` → keep id, change label
- **Filters:** plan_id (existing) + status (existing) + new «Истекают в течение N дней» + new «С промокодом» (free text)
- **Remove** the «+ Создать подписку» button (creation now belongs to the user's drawer)
- **Each row** is a subscription record — click anywhere on the row → navigate to `/users?open={user_db_id}&tab=subscriptions`. The /users page on mount reads the URL params and auto-opens the drawer.
- **Per-row actions:** только [Открыть юзера] (no inline edit/delete — these belong in the drawer). Optionally a tiny [✕] for deletion with confirm, since power users want it.

## Frontend Files

### Create
- `admin_web/src/components/ui/Drawer.jsx` — new primitive. Right-aligned slide-in panel. Fullscreen on <md. Props: `open`, `onClose`, `title`, `subtitle`, `actions` (header right side), `children`. Body scroll-lock. ESC close. Backdrop click close.
- `admin_web/src/components/UserDrawer.jsx` — composition of Drawer + tabs router + tab content. Props: `userId` (or null = closed), `defaultTab`, `onClose`.
- `admin_web/src/components/user-drawer/ProfileTab.jsx`
- `admin_web/src/components/user-drawer/SubscriptionsTab.jsx`
- `admin_web/src/components/user-drawer/TrafficPaymentsTab.jsx`

### Modify
- `admin_web/src/pages/UsersPage.jsx` — replaces existing. New columns, status tabs, drawer integration. URL state extends with `open=<user_db_id>&tab=<profile|subscriptions|traffic>`.
- `admin_web/src/pages/SubscriptionsPage.jsx` — refactor to "История подписок". Drop create button. Row click → navigate to /users with params.
- `admin_web/src/components/Sidebar.jsx` — relabel «Подписки» → «История подписок»
- `admin_web/src/components/commands.js` — relabel `go-subscriptions` Cmd+K entry
- `admin_web/src/i18n/ru.js` + `en.js` — new keys (drawer titles, tab labels, button labels, history filter labels)
- `admin_web/src/api/admin.js` — add `fetchUserSubscriptions(userDbId)` wrapper, `fetchUserPayments(userDbId)`, `fetchUserTraffic(userDbId, days)`. Thin wrappers around existing endpoints with query params.

### Delete

Nothing yet — all current files are reused or refactored. Old PageHeader-create dialog in SubscriptionsPage goes away as code, but file stays.

## Backend Changes

### Modify

**`internal/admin/users_handlers.go` — `ListUsersHandler`**

Extend the SELECT to add 3 new fields per row:

```go
SELECT u.id, u.user_id, ..., (existing fields),
  COALESCE(ls.last_seen, NULL) AS last_seen,
  COALESCE(t30.total_bytes, 0) AS traffic_30d_bytes,
  COALESCE(p.traffic_limit_bytes, 0) AS traffic_limit_bytes
FROM users u
LEFT JOIN LATERAL (...existing current sub join...) us ON TRUE
LEFT JOIN plans p ON p.id = us.plan_id
LEFT JOIN LATERAL (
  SELECT MAX(last_seen_at) AS last_seen
  FROM user_devices ud
  WHERE ud.user_id = u.id
) ls ON TRUE
LEFT JOIN LATERAL (
  SELECT SUM(bytes_used) AS total_bytes
  FROM traffic_stats ts
  WHERE ts.user_id = u.id AND ts.day >= CURRENT_DATE - INTERVAL '30 days'
) t30 ON TRUE
WHERE ... ORDER BY ... LIMIT ... OFFSET ...
```

New sort whitelist entries: `last_seen`, `traffic_30d_bytes`.

New filter param `status` extended:
- `expiring` → `valid_until BETWEEN NOW() AND NOW() + INTERVAL '7 days'`
- `no_plan` → `valid_until IS NULL OR valid_until < NOW()`

Response shape adds `last_seen`, `traffic_30d_bytes`, `traffic_limit_bytes` per item.

**`internal/admin/users_handlers.go` — new `ListUserTrafficHandler`**

`GET /api/admin/users/{id}/traffic?days=30` →

```json
{
  "user_db_id": 42,
  "days": 30,
  "limit_bytes": 107374182400,
  "total_bytes": 69793218560,
  "by_day": [{"day":"2026-04-26","bytes":1234567}, ...],
  "by_country": [{"country":"NL","bytes":40000000000}, {"country":"DE","bytes":29000000000}]
}
```

Permission: `users.read`.

**`internal/admin/routes.go`**

Add route handling for `/api/admin/users/{id}/traffic` next to the existing `/devices` suffix branch.

### Schema (no migration needed)

All required tables exist: `user_devices.last_seen_at`, `traffic_stats(user_id, day, bytes_used)`, `plans.traffic_limit_bytes`. **No migration in this sub-project.**

## Acceptance Criteria

1. `/users` shows 7 columns including inline traffic bar and «Был онлайн».
2. Status tabs at the top filter rows (Все / Активные / Истекают / Без подписки / Заблокированные) with live counts.
3. Default sort is `last_seen DESC`.
4. Clicking any row opens the drawer with Профиль tab selected.
5. Drawer header shows username + UUID + Devices quick button + Block toggle + Delete user.
6. Profile tab allows inline edit of username / password / role / telegram_id, save → row updates in master without page reload.
7. Подписки tab shows full subscription history for that user (including expired). «+ Подписка» button inline (no `user_db_id` field — implicit).
8. Трафик+Платежи tab shows 30d chart + breakdown by exit country + payment list.
9. ESC closes drawer. Backdrop click closes drawer. ✕ button closes drawer.
10. On <768px the drawer is fullscreen modal (uses existing Dialog fullscreen behavior).
11. URL state: `?open=42&tab=subscriptions` opens drawer for user_db_id=42 with Подписки tab on direct navigation.
12. «История подписок» (formerly /subscriptions) loses the «+ Создать» button. Each row has [Открыть юзера] action that routes to `/users?open=<id>&tab=subscriptions`.
13. Keyboard: J/K navigates rows in master, Enter opens drawer, ESC closes it.
14. Master list `npm run build` clean, backend `go build ./...` clean, `go vet` clean.
15. After deploy: chrome MCP smoke at 375px (mobile fullscreen drawer) and 1440px (drawer slide-in).

## Out of Scope (future work)

- Bulk actions (select multiple users, extend N subscriptions, mass block)
- Column picker / hideable columns
- Saved filter presets ("expiring this week" as a saved view)
- Audit log tab («кто из админов когда что менял у этого юзера»)
- CSV export of filtered master list

## Risks

- **Performance:** ListUsers gets 2 new LATERAL joins (last_seen MAX, traffic_30d SUM). On 1000+ users with 100k+ traffic_stats rows the SUM may slow. Mitigation: index `traffic_stats(user_id, day)` exists (check); fall back to a precomputed `users.last_seen_cache` column if necessary (out of scope for v1, instrument first).
- **Drawer state vs URL:** opening drawer for user X mutates URL params. Closing drawer should clean params. Browser back-button should also close drawer (history entry). Easy to get wrong; needs explicit tests of back-button behavior.
- **«История подписок» traffic redirect:** clicking a row in /subscriptions redirects to /users — if user has been deleted between page loads, drawer opens on missing user. Mitigation: ListUsersHandler 404 when target user missing; UserDrawer shows EmptyState with «Юзер удалён».
- **Mobile drawer:** Dialog primitive already supports fullscreen; reusing it for Drawer means Drawer is essentially a Dialog variant. Risk of feature drift between Dialog and Drawer — acceptable, they can share base class.

## Self-review

**Spec coverage:** core problem (combine users + subscriptions) addressed via Master+Drawer pattern with tabs covering all 3 user-feedback items.

**Placeholder scan:** no TBD / TODO. Backend changes specify exact SQL fragments. Frontend file list is concrete.

**Internal consistency:**
- `last_seen` derivation: defined once (MAX of user_devices.last_seen_at), referenced same way in master sort, drawer header, and «Был онлайн» column.
- Drawer URL params (`open` + `tab`) consistent between master direct-navigation and «История подписок» row click.
- `traffic_30d_bytes` definition unambiguous (SUM(bytes_used) over last 30 days for that user).

**Ambiguity check:** «Истекают» tab defined exactly as `valid_until BETWEEN NOW() AND NOW()+7d` — explicit window. «Без подписки» includes both never-subscribed and expired users; explicit.

**Scope check:** single implementation plan. Frontend file count (~5 create + 6 modify), backend touches 2 files. Fits one plan.

## Execution

Next step: invoke `superpowers:writing-plans` to decompose into tasks. Plan likely 5-6 tasks:

1. Backend — ListUsers extension + new traffic endpoint (~60 min)
2. Frontend — Drawer ui primitive (~45 min)
3. Frontend — UserDrawer composition + 3 tab components (~90 min)
4. Frontend — UsersPage rewrite with new columns, status tabs, drawer wiring, URL state (~60 min)
5. Frontend — SubscriptionsPage refactor to «История подписок» + sidebar relabel (~30 min)
6. Deploy + smoke (mobile fullscreen drawer, desktop slide-in, URL state round-trip, /sub-history → drawer navigation) (~40 min)

Total estimate: ~5 hours of focused work.
