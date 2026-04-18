---
id: post-qa-hardening
beads_id: heyearthproject-9gl
plan: ../plan/post-qa-hardening.md
status: in_progress
created: 2026-04-18
---

# Tasklist: Post-QA hardening

Живой список подзадач. Sync с Beads (epic `heyearthproject-9gl`).

## Group A — Dashboard numbers + chart
- [ ] **heyearthproject-ulnp (A1)** — active_users_count SQL → 24h-traffic-based
  - Файл: `server/backend/internal/admin/dashboard_handlers.go:17`
  - Verify: `curl -H "Bearer $T" https://admin.hiearth.ru/api/admin/overview | jq '{a:.active_users_count, u:.users_count}'` — `a < u`
- [ ] **heyearthproject-wo2v (A2)** — revenue_month exclude test payments
  - Файл: `server/backend/internal/admin/dashboard_handlers.go:51-57`
  - Verify: compare with `psql -c "SELECT SUM(amount) FROM payments WHERE status='completed' AND NOT COALESCE(test,FALSE) AND confirmed_at >= date_trunc('month',NOW())"`
- [ ] **heyearthproject-dcre (A3)** — PaymentsPage header sum filter
  - Файлы: `admin_web/src/pages/PaymentsPage.jsx`, possibly `server/backend/internal/admin/payments_handlers.go`
  - Verify: header sum == dashboard revenue_month
- [ ] **heyearthproject-njg1 (A4)** — remove Groups card
  - Файл: `admin_web/src/pages/DashboardPage.jsx`
  - Verify: visual — no "Группы" card on /dashboard
- [ ] **heyearthproject-ppj0 (A5)** — TrafficMiniChart CSS `h-full`
  - Файл: `admin_web/src/pages/DashboardPage.jsx:13-34`
  - Verify: visual — bars visible with varying heights

## Group B — SOCKS5 UX + Traffic limits
- [ ] **heyearthproject-q8j1 (B1)** — SOCKS/HTTP accounts filter by traffic_limit
  - Файл: `server/backend/internal/biz/user.go:190-212`
  - Verify: SET traffic_used=traffic_limit+1 for qa-user; wait sync; `jq` config.json shows user NOT in socks5-proxy accounts
- [ ] **heyearthproject-bu1h (B2)** — `#HeYearth-SOCKS5` / `#HeYearth-HTTP` fragments in URIs
  - Файлы: `server/backend/cmd/api/client_api.go:235-244`, `subscription_handler.go`
  - Verify: `curl /api/v1/user/subscribe/<t> | base64 -d | grep '#HeYearth-SOCKS5'`
- [ ] **heyearthproject-m9vp (B3)** — SOCKS5 credentials block in site Dashboard
  - Файл: `site/src/pages/DashboardPage.jsx`
  - Verify: QA user (socks-select) → block visible with copy buttons; vless-only user → hidden
- [ ] **heyearthproject-45nt (B4)** — Traffic banner on site /traffic (progressbar)
  - Файл: `site/src/pages/ConnectionsPage.jsx` or new section
  - Verify: QA user traffic_used=0.9*limit → yellow; =limit → red "закончился"
- [ ] **heyearthproject-ul9l (B5)** — E2E verify enforcement (blocks: B1, B4)
  - Verify: documented curl test sequence, no code
- [ ] **heyearthproject-87e8 (B6)** — "Обновить подписку" button with cache-buster (blocks: B4)
  - Файл: `site/src/pages/DashboardPage.jsx`
  - Verify: click → clipboard has subscription URL with `?v=<timestamp>`

## Group C — Auth form fix
- [ ] **heyearthproject-vnin (C1)** — remove stale username-password validation
  - Файл: `site/src/pages/SettingsPage.jsx:59-62`
  - Verify: manual — save username-only on settings → no error

## Group D — Multi-client deeplinks
- [ ] **heyearthproject-aolw (D1)** — dropdown with 6 app deeplinks
  - Файл: `site/src/pages/DashboardPage.jsx`
  - Verify: devtools — all 6 href attributes correct

## Side quests
- (пока пусто; добавим если найдём по ходу)

## Deferred
- Rotate subscribe_token при «обновить» — не делаем, используем cache-buster query
- Forced disconnect при over-limit (немедленно, без ожидания sync) — не делаем, 5 мин latency ок
- Email notification о закончившемся трафике — отдельный эпик

## Verification log
_(заполняем по мере закрытия)_

## QA test user on prod
Для тестирования traffic enforcement и SOCKS5:
```
user_id = qa-socks-test-20260418
db_id = 106
vless_uuid / socks password = 84b62bf6-c888-4517-b47b-65189347018c
subscribe_token = 57fa765c-9286-483e-89a8-f64e5e19b646
plan = socks-select (100 GB/30d)
```

После эпика удалить:
```sql
DELETE FROM user_subscriptions WHERE user_id=106;
DELETE FROM users WHERE id=106;
```
