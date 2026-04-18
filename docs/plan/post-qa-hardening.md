---
id: post-qa-hardening
beads_id: heyearthproject-9gl
prd: ../prd/post-qa-hardening.prd.md
status: draft
created: 2026-04-18
---

# Plan: Post-QA hardening

## Контекст из кодовой базы

### Dashboard metrics (группа A)
- `server/backend/internal/admin/dashboard_handlers.go:10-66` — `OverviewHandler`
  - line 17: `active_users_count = COUNT(*) FROM user_subscriptions WHERE valid_until > NOW()`
  - line 20: `groups_count = COUNT(*) FROM user_groups`
  - lines 51-57: `revenue_month = SUM(amount) WHERE status='completed' AND confirmed_at >= date_trunc('month',NOW())` — **нет test-фильтра**
- `server/backend/internal/admin/traffic_handlers.go:158-196` — `TrafficChartHandler` возвращает `{daily: [{date, upload, download}]}`. SQL корректен, данные живые (145k rows, latest 60 sec назад).
- `server/backend/internal/admin/payments_handlers.go:14-89` — `ListPaymentsHandler` не возвращает aggregate; header-сумма считается на фронте (или не считается). Нужно добавить aggregate в backend либо считать на фронте с тем же фильтром.
- `admin_web/src/pages/DashboardPage.jsx:13-34` — `TrafficMiniChart`:
  - parent: `<div className="flex items-end gap-[2px] h-24">` (96px) ✓
  - inner wrapper: `<div className="flex-1 flex flex-col items-center group relative">` — **нет h-full!**
  - colored bar: `<div style={{height: `${h}%`}}>` — % от родителя = 0 потому что у wrapper нет высоты.

### SOCKS5 / traffic limits (группа B)
- `server/backend/cmd/api/xray_sync.go:228-238` — SOCKS5 inbound на 0.0.0.0:1080, auth=password.
- `server/backend/cmd/api/xray_sync.go:411-422` — routing: если `len(exitTags) == 0` — блокируем SOCKS5/HTTP. Сейчас 2 ноды healthy, блока нет.
- `server/backend/internal/biz/user.go:150-151` — **VLESS** клиенты фильтруются: `AND (us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)`. ОК.
- `server/backend/internal/biz/user.go:190-212` — **SOCKS/HTTP accounts** НЕ фильтруются traffic_limit. **ЭТО ГЛАВНЫЙ БАГ.**
- `server/backend/cmd/api/traffic_stats.go:115-150` — `checkTrafficLimits()` только **логирует** превышение, никого не отключает; вызывает `RequestXraySync()` но синк не режет SOCKS, см. выше.
- `server/backend/cmd/api/subscription_handler.go:231-250` — `buildSubscriptionUserinfo` корректно строит header `upload=X; download=Y; total=Z` если `trafficLimit != nil`. **Работает**.
- `server/backend/cmd/api/client_api.go:235-244` — SOCKS5 URI: `socks://%s:%s@%s:%s` **без `#tag`**. В vless URIs тег есть (`#HeYearth-Home` etc.). Клиенты могут путаться.
- `site/src/pages/DashboardPage.jsx` — есть блок subscription URL + Happ deeplink, но нет прямого отображения SOCKS5 login/password.
- `site/src/pages/PaymentsHistoryPage.jsx` — есть, но это про платежи.

### Auth form (группа C)
- `site/src/pages/SettingsPage.jsx:59-62` — проверка:
  ```js
  if (usernameChanged && !profile?.has_password && !changingPassword) {
    setErr('Чтобы задать имя пользователя, также установите пароль')
    return
  }
  ```
- Backend `/api/v1/user/profile` PATCH принимает любые поля независимо. Frontend блокирует зря.

### Magic link (группа D)
- `site/src/pages/DashboardPage.jsx:~359` — единственная кнопка «Открыть в Happ»: `happ://add/?url=<encoded_url>`.
- Dropdown/menu компонента нет в site (в отличие от admin_web, где есть `SelectInput`). Нужно сделать inline.

### Похожие паттерны в репозитории
- `admin_web/src/components/StatCard.jsx` — теперь поддерживает `to=` prop и wraps в `<Link>`. Pattern можно не повторять.
- `site/src/pages/PaymentsHistoryPage.jsx` — clickable rows с `<Link>` — good reference для UI style.
- `server/backend/cmd/api/subscription_handler.go` имеет `buildClashConfig` / `buildV2RayConfig` — хорошее место для fragment tagging.

## Архитектурное решение

Разбиваем работу на **4 группы** по сфере ответственности. Внутри группы задачи можно делать последовательно, между группами — параллельно (разные файлы).

**Группа A — Dashboard numbers + chart** (backend + admin_web):
- SQL-правки в `dashboard_handlers.go` (active_users, revenue).
- Удаление Groups-карточки в `DashboardPage.jsx`.
- CSS-fix `TrafficMiniChart`: `h-full` на inner wrapper.
- PaymentsPage header: добавить aggregate endpoint `/api/admin/payments/summary?test=false&status=succeeded` ИЛИ считать на фронте.

**Группа B — SOCKS5 UX + Traffic limits** (backend + site):
- B1. Фильтр `traffic_used < traffic_limit` в `ListSOCKSAccounts` и `ListHTTPProxyAccounts` (biz/user.go).
- B2. `#HeYearth-SOCKS5` / `#HeYearth-HTTP` фрагменты в URIs (client_api.go + subscription_handler.go).
- B3. Отдельный блок SOCKS5 credentials в site/DashboardPage с copy-кнопками.
- B4. Traffic-баннер на /traffic site (progressbar + цветные пороги).
- **Rotate subscribe_token** — **отклонено** (open question из PRD): сделаем кнопку «Перечитать подписку» которая просто обновит `/api/v1/user/connections` + скопирует новую ссылку с cache-buster параметром `?v=timestamp`. Это заставит клиента перечитать.

**Группа C — Auth form fix** (site only):
- Удалить stale-проверку в `SettingsPage.jsx`. Тривиально.

**Группа D — Multi-client deeplinks** (site only):
- Dropdown/inline buttons на /dashboard site с 5-6 опциями.

**Почему эта декомпозиция:** задачи в группах независимы по файлам, каждая группа < 2 часов работы, можно параллелить через `/execute` для отдельных подзадач. Migrations не нужны — всё чистый SQL-query / code changes.

Отклонено:
- **Rotate subscribe_token при нажатии «обновить»** — требует backend-изменений, UUID uniqueness, update всех старых клиентов → перебор для MVP. Вместо: cache-buster query.
- **Forced disconnect при over-limit (немедленно, не ждать sync cycle)** — требует Xray API manipulation + hysteria2 API (нет у нас). 5 мин latency приемлемо.
- **Единая «Traffic Limit» permission** — пока не нужно, логика в biz/.

## Затрагиваемые файлы

### Создать
- (нет новых файлов — всё правки)

### Изменить
- `server/backend/internal/admin/dashboard_handlers.go` — новая формула active_users, test-filter revenue
- `server/backend/internal/admin/payments_handlers.go` — aggregate total endpoint (или параметр к existing)
- `server/backend/internal/biz/user.go` — фильтр traffic_limit в SOCKS/HTTP account queries
- `server/backend/cmd/api/client_api.go` — fragment tags для SOCKS/HTTP URIs
- `server/backend/cmd/api/subscription_handler.go` — fragment tags, если там отдельно
- `admin_web/src/pages/DashboardPage.jsx` — удалить Groups card, h-full fix
- `admin_web/src/pages/PaymentsPage.jsx` — header sum (test-filtered)
- `site/src/pages/DashboardPage.jsx` — SOCKS5 block + multi-deeplink menu
- `site/src/pages/ConnectionsPage.jsx` (если нужно) — baseline для SOCKS5 credentials
- `site/src/api/user.js` — если потребуется новый endpoint для SOCKS credentials (вероятно уже есть в /connections)
- `site/src/pages/SettingsPage.jsx` — удалить stale-проверку
- `site/src/pages/` — новая страница/секция для traffic-баннера (либо inline в DashboardPage)

### Удалить
- (ничего)

## Декомпозиция (подзадачи)

### Группа A — Dashboard (≈1.5 часа, 5 задач)
1. **A1. active_users SQL fix** (15 мин)
   - Файл: `dashboard_handlers.go:17`
   - Заменить: `COUNT(DISTINCT user_id) FROM traffic_stats WHERE recorded_at > NOW() - INTERVAL '24 hours' AND (upload_bytes > 0 OR download_bytes > 0)`
   - Evidence: `curl -H "Bearer $T" /api/admin/overview | jq .active_users_count` — число меньше `users_count`

2. **A2. revenue_month test filter** (10 мин)
   - Файл: `dashboard_handlers.go:51-57`
   - Добавить: `AND COALESCE(test, FALSE) = FALSE`
   - Evidence: сравнить с `SELECT SUM(amount) FROM payments WHERE status='completed' AND NOT COALESCE(test,FALSE) AND confirmed_at >= date_trunc('month',NOW())` на проде

3. **A3. PaymentsPage sum filter** (20 мин)
   - Файл: `admin_web/src/pages/PaymentsPage.jsx` + backend если нужно
   - Либо add aggregate endpoint либо считать на фронте из filtered list. Проще: добавить в ListPaymentsHandler параметр `exclude_test=true` + возвращать total в response.
   - Evidence: на /payments header сумма == revenue на dashboard.

4. **A4. Remove Groups card** (5 мин)
   - Файл: `admin_web/src/pages/DashboardPage.jsx`
   - Удалить строку cards array с `dashGroups`.
   - Evidence: visual на /dashboard

5. **A5. TrafficMiniChart CSS fix** (10 мин)
   - Файл: `admin_web/src/pages/DashboardPage.jsx:13-34`
   - Поменять: `<div className="flex-1 flex flex-col items-center group relative">` → `<div className="flex-1 flex flex-col items-end h-full group relative">`
   - Evidence: visual — столбцы видны с высотой пропорциональной данным.

### Группа B — SOCKS5 + Traffic (≈3 часа, 6 задач)
6. **B1. Filter SOCKS/HTTP accounts by traffic_used** (30 мин)
   - Файл: `internal/biz/user.go:190-212` (функция генерации accounts).
   - Добавить `AND (us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)` в JOIN/WHERE.
   - Evidence: искусственно `UPDATE user_subscriptions SET traffic_used=traffic_limit+1 WHERE user_id=106; triggered RequestXraySync; cat /opt/heyearth/xray/config.json | jq '.inbounds[]|select(.tag=="socks5-proxy").settings.accounts | map(.user) | contains(["qa-socks-test-20260418"])'` → false.

7. **B2. Fragment tags in SOCKS/HTTP URIs** (15 мин)
   - Файл: `cmd/api/client_api.go:235-244` + subscription_handler.go где тоже строится
   - `socks://user:pw@host:1080#HeYearth-SOCKS5`, `http://user:pw@host:8080#HeYearth-HTTP`
   - Evidence: `curl /api/v1/user/subscribe/<token> | base64 -d | grep '#HeYearth-SOCKS5'`

8. **B3. SOCKS5 block in site Dashboard** (45 мин)
   - Файл: `site/src/pages/DashboardPage.jsx`
   - Показывать для юзеров с socks-allowed plan: «Прокси»: host:port, login, password — с кнопками copy. Скрыть если только vless план.
   - Evidence: залогинен как socks-select user → вижу блок; залогинен как vless-reality → не вижу.

9. **B4. Traffic banner на site /traffic** (40 мин)
   - Файл: find existing traffic page in site/src/pages (`ConnectionsPage.jsx`? или новая?)
   - Progressbar: зелёный <80%, жёлтый 80-99%, красный 100%. Текст «Использовано X из Y ГБ»
   - Evidence: QA user с traffic_used=0.9*limit → жёлтый; =1.0*limit → красный + надпись «закончился».

10. **B5. Тест enforcement end-to-end** (20 мин) — не код, просто verification
    - `UPDATE user_subscriptions SET traffic_used=traffic_limit WHERE user_id=106`
    - `curl --socks5 ...@hiearth.ru:1080 ifconfig.me` → должен fail через ≤5 мин
    - `journalctl -u xray --since "5m ago" | grep qa-socks-test` — no new SOCKS5 accepted
    - Evidence: документировано в этой задаче

11. **B6. Кнопка "Обновить подписку"** (20 мин)
    - Файл: `site/src/pages/DashboardPage.jsx`
    - Кнопка «Обновить ссылку» вычисляет новую URL с `?v=<timestamp>` и toast «Обновите подписку в приложении». Или просто copy.
    - Evidence: клик → clipboard содержит обновлённую URL с cache-buster.

### Группа C — Auth form (≈15 мин, 1 задача)
12. **C1. Remove stale username-password check** (10 мин)
    - Файл: `site/src/pages/SettingsPage.jsx:59-62`
    - Удалить блок проверки.
    - Evidence: manual — задать только username без пароля → save success.

### Группа D — Multi-client deeplinks (≈45 мин, 1 задача)
13. **D1. Dropdown/buttons for deeplinks** (40 мин)
    - Файл: `site/src/pages/DashboardPage.jsx`
    - Заменить одну кнопку «Открыть в Happ» на menu:
      - Happ: `happ://add/?url=<url>`
      - V2RayTun: `v2raytun://import?url=<url>`
      - V2RayNG: `v2rayng://install-sub?url=<url>`
      - Streisand: `streisand://import/<base64url>`
      - Clash/ClashMeta: `clash://install-config?url=<url>`
      - Shadowrocket: `sub://<base64url>`
    - + fallback «Скопировать ссылку».
    - Evidence: visual проверка всех 6 deeplinks в devtools (href атрибут корректный).

**Итого**: 13 подзадач, ~5-6 часов работы.

**Граф зависимостей:**
```
A1, A2, A3, A4, A5    — параллельны, не зависят друг от друга
B1 → B5 (verification)
B2 — независима от B1
B3 — независима от B1/B2
B4 — независима
B6 — зависит от B4 (UI на той же странице)
C1 — независима
D1 — независима
```

## Test strategy

Evidence-driven (нет unit-тестов). Итоговый набор верификаций перед commit:

```bash
# Build
cd server/backend && go build ./... && echo OK
cd admin_web && npm run build && echo OK
cd site && npm run build && echo OK

# Deploy
cd server/backend && go run quick_deploy.go
cd server/backend && go run ./cmd/deploy --site --admin-web

# Verify A — Dashboard
TOKEN=$(curl -s -X POST https://admin.hiearth.ru/api/admin/login -d '{"username":"admin","password":"HeYearth2024!"}' -H "Content-Type: application/json" | jq -r .token)
curl -s -H "Authorization: Bearer $TOKEN" https://admin.hiearth.ru/api/admin/overview | jq '{active: .active_users_count, users: .users_count, revenue: .revenue_month}'
# Expect active < users, revenue matches test-excluded sum

# Verify B — SOCKS5 + traffic
curl -sS --max-time 15 --socks5 qa-socks-test-20260418:84b62bf6-c888-4517-b47b-65189347018c@hiearth.ru:1080 https://ifconfig.me
# Expect: 89.124.89.126 или 155.212.225.172 (exit node IP)

# Verify subscription URL
curl -s https://hiearth.ru/api/v1/user/subscribe/<qa-token> | base64 -d | grep '#HeYearth-SOCKS5'
# Expect: socks URI with #HeYearth-SOCKS5 fragment

# Verify traffic enforcement
sudo -u postgres psql heyearth -c "UPDATE user_subscriptions SET traffic_used=traffic_limit WHERE user_id=106"
# Wait ≤5 min
curl -sS --max-time 5 --socks5 qa-socks-test-20260418:84b62bf6-...@hiearth.ru:1080 https://ifconfig.me
# Expect: connection refused / auth failure

# Verify C + D: manual в browser
open https://hiearth.ru/settings  # save username only → no error
open https://hiearth.ru/dashboard  # dropdown «Добавить в приложение» → 6 опций
```

## Rollback plan

Если что-то сломалось на проде:
1. `systemctl stop heyearth-api`
2. SSH: `cp /opt/heyearth/mainserver/heyearth-api.bak /opt/heyearth/mainserver/heyearth-api`
3. `systemctl start heyearth-api`
4. Site/admin_web — `git revert <commit>`, `go run ./cmd/deploy --site --admin-web`
5. Миграций нет → DB rollback не нужен.

В частности, если пункт B1 сломал enforcement (SOCKS вообще перестал работать для живых юзеров) — сразу revert. Тест покажет в течение 5 мин.

## Deployment notes

- Нет новых env переменных.
- Нет изменений nginx.
- Требуется: `systemctl restart heyearth-api` (через `quick_deploy.go`).
- Admin web + site — через `go run ./cmd/deploy --admin-web --site`.
- Xray перезапускается автоматически через `RequestXraySync()` (debounced). Можно не трогать.
- После деплоя удалить QA-юзера: `DELETE FROM user_subscriptions WHERE user_id=106; DELETE FROM users WHERE id=106;`
