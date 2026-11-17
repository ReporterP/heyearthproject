# Direct Routing Infrastructure — Design Spec

**Date:** 2026-05-23
**Status:** approved (pending implementation plan)
**Beads epic:** `heyearthproject-agh4`
**Related future work:** Routing profiles per-user/per-tenant

---

## Problem

В системе HiEarth весь user-трафик уходит через exit-ноды за рубежом. Это ломает доступ к российским сервисам (банки, госуслуги, маркетплейсы, картам, доставке), которые блокируют иностранные IP по гео-фильтрам. Пользователю приходится вручную выключать VPN, что подрывает ценностное предложение продукта.

Нужна инфраструктура для управления списком IP/доменов/CIDR, которые должны идти **в обход VPN** (direct egress), плюс админ-UI для CRUD-операций и bulk-импорта.

Источник данных: `ip-list.json` от внешнего сообщества — 86 сервисов в 3 группах (russia/vk/yandex), ~20K доменов, ~9K IPv4, ~2K IPv6, ~50 CIDR. Это базовый список; админ должен иметь возможность дополнять, выключать, и подгружать обновлённые версии файла.

---

## Goal

1. Schema + biz module для хранения direct-routes по сервисам.
2. Two-way integration:
   - **Happ routing profile** (на устройстве пользователя — `happ://routing/onadd/<b64>` deeplink в подписке).
   - **Xray Hub routing rules** (для не-Happ клиентов: V2RayNG, Clash, прямой SOCKS5/HTTP на Hub).
3. Per-plan привязка: каждый сервис может быть включён в один или несколько планов (free-trial может не получать bypass; платные — получают).
4. Admin UI (`admin_web`): список, фильтры, поиск, CRUD-форма, bulk-операции, JSON-импорт двух форматов.
5. Seed: импорт исходного `ip-list.json` после развёртывания инфраструктуры.

---

## Out of Scope (явное "будущее")

- Routing profiles per-user (юзер сам выбирает какие группы получают direct).
- Использование per-service DNS-серверов в Xray dns-секции (сейчас сохраняем, не используем).
- Auto-update direct-list из внешнего GitHub-репо.
- Per-service статистика (counter "сколько раз шли direct через 2gis").

---

## Architecture

### Data Model

Две новых таблицы (миграция 000028):

```sql
CREATE TABLE direct_route_services (
    id            SERIAL PRIMARY KEY,
    slug          TEXT NOT NULL UNIQUE,
    name          TEXT NOT NULL,
    group_name    TEXT NOT NULL DEFAULT 'other',
    domains       TEXT[] NOT NULL DEFAULT '{}',
    ip4           TEXT[] NOT NULL DEFAULT '{}',
    ip6           TEXT[] NOT NULL DEFAULT '{}',
    cidr4         TEXT[] NOT NULL DEFAULT '{}',
    cidr6         TEXT[] NOT NULL DEFAULT '{}',
    dns_servers   TEXT[] NOT NULL DEFAULT '{}',
    timeout_sec   INT NOT NULL DEFAULT 3600,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    source        TEXT NOT NULL DEFAULT 'manual',
    notes         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_drs_group ON direct_route_services(group_name);
CREATE INDEX idx_drs_active ON direct_route_services(is_active) WHERE is_active = TRUE;

CREATE TABLE plan_direct_services (
    plan_id            INT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    direct_service_id  INT NOT NULL REFERENCES direct_route_services(id) ON DELETE CASCADE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, direct_service_id)
);
CREATE INDEX idx_pds_plan ON plan_direct_services(plan_id);
CREATE INDEX idx_pds_service ON plan_direct_services(direct_service_id);

-- Settings + RBAC
INSERT INTO app_settings (key, value) VALUES ('direct_routes_version', '1')
ON CONFLICT (key) DO NOTHING;

INSERT INTO admin_permissions (key, description) VALUES
  ('routing.direct.view',   'Просмотр списка direct-routes'),
  ('routing.direct.manage', 'CRUD и импорт direct-routes')
ON CONFLICT DO NOTHING;
-- Привязка к super_admin делается в той же миграции
```

**Design decisions:**

- `TEXT[]` (нативный PostgreSQL array) вместо отдельной entries-таблицы — один сервис всегда читается целиком, никогда не ищем "найди сервис по конкретному IP".
- Hard delete с `ON DELETE CASCADE` — soft delete не нужен (есть `is_active`).
- `dns_servers` хранится "as-is" из JSON (формат `host[:port]`) — на будущее.

### Integration points

| Слой | Файл | Что делает |
|---|---|---|
| biz | `internal/biz/direct_routes.go` (новый) | CRUD + bulk import + per-plan querying |
| biz | `internal/biz/happ_routing.go` (extend) | `BuildHappRoutingDeeplinkForPlan(planID, mtprotoIPs)` собирает DirectIp+DirectSites по плану |
| API | `cmd/api/subscription_handler.go` (extend) | Подставляет `planID` в новый билдер |
| API | `cmd/api/xray_sync.go` (extend) | Добавляет `direct-freedom` outbound + per-plan user routing rules |
| Admin | `internal/admin/direct_routes_handler.go` (новый) | 10 HTTP endpoints |
| Web | `admin_web/src/pages/DirectRoutesPage.tsx` (новый) | UI |
| Web | `admin_web/src/components/DirectRouteEditDialog.tsx` (новый) | модалка edit |
| Web | `admin_web/src/components/DirectRoutesImportDialog.tsx` (новый) | модалка import |
| Web | `admin_web/src/components/PlanSelector.tsx` (новый) | multi-select планов |

---

## Component: biz/direct_routes.go

```go
type DirectRouteService struct {
    ID, Timeout                    int
    Slug, Name, GroupName, Source, Notes string
    Domains, IP4, IP6, CIDR4, CIDR6, DNSServers []string
    IsActive                       bool
    CreatedAt, UpdatedAt           time.Time
    PlanIDs                        []int
}

// CRUD
func ListDirectRouteServices(filters DirectRouteFilters) ([]DirectRouteService, error)
func GetDirectRouteServiceByID(id int) (*DirectRouteService, error)
func CreateDirectRouteService(s *DirectRouteService) (int, error)
func UpdateDirectRouteService(s *DirectRouteService) error
func DeleteDirectRouteService(id int) error
func SetServicePlans(serviceID int, planIDs []int) error

// Bulk
func BulkSetActive(ids []int, isActive bool) error
func BulkDelete(ids []int) error
func BulkSetPlans(ids []int, planIDs []int) error
func ImportDirectRoutesJSON(jsonBytes []byte, opts ImportOptions) (ImportResult, error)

// Query consumers
func GetDirectRouteServicesForPlan(planID int) ([]DirectRouteService, error)
func GetDirectRoutesByPlan() (map[int][]DirectRouteService, error)
func GetDirectRoutesStats() (DirectRouteStats, error)

// Invalidation
func BumpDirectRoutesVersion() error
```

Каждый mutating call в конце вызывает:
```go
_ = BumpDirectRoutesVersion()
RequestXraySync()
```

`BumpDirectRoutesVersion` так же бампит `subscription_version` в app_settings — клиенты увидят красный баннер "обновите подписку".

---

## Component: admin/direct_routes_handler.go

| Метод | Endpoint | Permission |
|---|---|---|
| GET | `/admin/api/direct-routes` | `routing.direct.view` |
| GET | `/admin/api/direct-routes/:id` | `routing.direct.view` |
| GET | `/admin/api/direct-routes/stats` | `routing.direct.view` |
| GET | `/admin/api/direct-routes/groups` | `routing.direct.view` |
| POST | `/admin/api/direct-routes` | `routing.direct.manage` |
| PUT | `/admin/api/direct-routes/:id` | `routing.direct.manage` |
| DELETE | `/admin/api/direct-routes/:id` | `routing.direct.manage` |
| PUT | `/admin/api/direct-routes/:id/plans` | `routing.direct.manage` |
| POST | `/admin/api/direct-routes/bulk` | `routing.direct.manage` |
| POST | `/admin/api/direct-routes/import` | `routing.direct.manage` |

`/import` принимает `multipart/form-data` (файл) ИЛИ `application/json` (raw в теле). `MaxBytesReader` поднимается до 5 MB для этого endpoint.

Каждая mutation пишет в `admin_audit_log`: `action=direct_routes.{create|update|delete|bulk_*|import|set_plans}` с payload (slug, diff, или сводка для bulk).

---

## Component: Xray Hub integration (extend `xray_sync.go`)

После `block`-rules и перед traffic-masking добавляется блок direct:

```go
planDirects, _ := biz.GetDirectRoutesByPlan()
hasAnyDirect := len(planDirects) > 0

if hasAnyDirect {
    outbounds = append(outbounds, map[string]interface{}{
        "tag":      "direct-freedom",
        "protocol": "freedom",
        "settings": map[string]interface{}{"domainStrategy": "UseIP"},
    })
}

for planID, services := range planDirects {
    var userEmails []string
    for _, c := range activeClients {
        if c.PlanID == planID {
            userEmails = append(userEmails, c.UserID)
        }
    }
    if len(userEmails) == 0 { continue }

    var domains, ips []string
    for _, svc := range services {
        if !svc.IsActive { continue }
        for _, d := range svc.Domains { domains = append(domains, "domain:"+d) }
        ips = append(ips, svc.IP4...)
        ips = append(ips, svc.IP6...)
        ips = append(ips, svc.CIDR4...)
        ips = append(ips, svc.CIDR6...)
    }

    if len(domains) > 0 {
        rules = append(rules, map[string]interface{}{
            "type": "field", "user": userEmails,
            "domain": domains, "outboundTag": "direct-freedom",
        })
    }
    if len(ips) > 0 {
        rules = append(rules, map[string]interface{}{
            "type": "field", "user": userEmails,
            "ip": ips, "outboundTag": "direct-freedom",
        })
    }
}
```

**Финальный порядок rules:**

```
1. api-in → api                                       (prepended last)
2. block: private IP
3. block: bittorrent
4. block: xmpp 5222/5223
5. DIRECT: user-of-plan-X + domains → direct-freedom    ← NEW
6. DIRECT: user-of-plan-X + ip      → direct-freedom    ← NEW
7. traffic-masking: domain → mask-N
8. per-plan: user → planN-balancer
9. default: tcp,udp → exit-balancer
```

Direct-rules вставляются ДО block-fallback (когда `len(exitTags)==0`) — это значит даже при падении всех exit-нод российские сервисы продолжают работать через Hub.

---

## Component: Happ profile integration

Новая функция в `internal/biz/happ_routing.go`:

```go
func BuildHappRoutingDeeplinkForPlan(planID int, mtprotoIPs []string) string {
    services, _ := GetDirectRouteServicesForPlan(planID)

    var directIP []string
    for _, ip := range mtprotoIPs {
        directIP = append(directIP, ip+"/32")
    }
    var directSites []string
    for _, svc := range services {
        directIP = append(directIP, svc.IP4...)
        directIP = append(directIP, svc.IP6...)
        directIP = append(directIP, svc.CIDR4...)
        directIP = append(directIP, svc.CIDR6...)
        for _, d := range svc.Domains {
            directSites = append(directSites, "domain:"+d)
        }
    }
    if len(directIP) == 0 && len(directSites) == 0 {
        return ""
    }
    profile := happRoutingProfile{
        Name: "HeYearth Smart Routing", GlobalProxy: "true",
        DnsHosts: map[string]string{},
        DirectSites: directSites, DirectIp: directIP,
        ProxySites: []string{}, ProxyIp: []string{},
        BlockSites: []string{}, BlockIp: []string{},
        DomainStrategy: "AsIs",
    }
    js, err := json.Marshal(profile)
    if err != nil { return "" }
    return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(js)
}
```

Старая `BuildHappRoutingDeeplink(directIPs []string)` сохраняется для обратной совместимости (просто вызывает новую с `planID=0`, что означает "только MTProto IPs без direct-services").

В `cmd/api/subscription_handler.go` строка ~106 меняется:

```go
mtprotoIPs, _ := biz.ListMTProtoExitIPs()
planID := /* подгрузить из user_subscriptions */
if deeplink := biz.BuildHappRoutingDeeplinkForPlan(planID, mtprotoIPs); deeplink != "" {
    uris = append(uris, deeplink)
}
```

---

## Component: Admin UI

### Page: `admin_web/src/pages/DirectRoutesPage.tsx`

```
┌─ Header  Прямая маршрутизация    [+ Добавить]  [Импорт JSON]  ─────────┐
│ [Filters]  Группа: [все▾]  Статус: [все▾]  🔍 Поиск...                  │
│ [Stats]   Активных: N  Выключено: N  Всего IP: N  Доменов: N            │
│ [Bulk bar] [✓ выбрано N]  [Активировать] [Деактивировать] [Удалить] ... │
│ [Table]                                                                  │
│  ┌─┬──────────┬────────┬──────┬──────┬──────┬─────────┬────────┐        │
│  │☐│ Slug     │ Группа │ Дом. │ IP4  │ IP6  │ Планы   │        │        │
│  │✓│ 2gis.ru  │ russia │ 335  │ 131  │ 3    │ 3 плана │ ✏️ 🗑️ │        │
│  │..                                                                  │ │
│  └─┴──────────┴────────┴──────┴──────┴──────┴─────────┴────────┘        │
│ [Pagination] ◀ 1 2 3 ▶                                                   │
└──────────────────────────────────────────────────────────────────────────┘
```

### Component: `DirectRouteEditDialog.tsx`

Модалка с полями: slug, name, group_name, is_active, plans (multi-select), domains (textarea), ip4, ip6, cidr4, cidr6, dns_servers, timeout, source (readonly), notes.

Клиентская validation: формат, дубли в array, парсинг IP/CIDR (через `ipaddr.js` или native regex). На submit показываем backend warnings.

### Component: `DirectRoutesImportDialog.tsx`

Flow:
1. Drag&drop / file picker → читаем JSON.
2. Preview (server `dry_run=true`): "Будет создано: 86, Обновлено: 0, Пропущено: 0, Ошибок: 0".
3. `PlanSelector`: "Привязать ко всем создаваемым сервисам" (default — пусто).
4. Опции: `☐ Перезаписать существующие`, `☑ Активировать новые`.
5. Кнопка "Импортировать" → результат с детальным отчётом.

### i18n

Префикс `direct_routes.*` в `ru.json`/`en.json` (~40 ключей).

### Sidebar / Router

Новый пункт меню "Прямая маршрутизация" (permission gate `routing.direct.view`). Route `/direct-routes` → `DirectRoutesPage`.

---

## JSON Import Formats

### Format 1: `ip-list.json` (исходный)

```json
{
  "2gis.ru": {
    "name": "2gis.ru", "group": "russia",
    "domains": ["api.2gis.ru", ...],
    "dns": ["77.88.8.88:53", ...], "timeout": 3600,
    "ip4": [...], "ip6": [...], "cidr4": [...], "cidr6": [...],
    "external": {...},   // игнорируется
    "replace": {...}     // игнорируется
  }
}
```

### Format 2: HiEarth native (для export/re-import)

```json
{
  "version": 1,
  "services": [
    {"slug":"2gis.ru","name":"...","group":"russia","domains":[...],...}
  ]
}
```

### Detection

- Top-level массив или `{version, services:[]}` → Format 2.
- Top-level объект с под-объектами имеющими `name`+`group`+`domains` → Format 1.
- Иначе → 400 + сообщение.

### Validation (per-entry)

| Поле | Правило | Невалидное значение |
|---|---|---|
| `slug` | `^[a-z0-9.-]+$`, unique | вся запись skipped (запись в warnings) |
| `group_name` | lowercase, `[a-z0-9_-]+`, default `other` | normalize |
| `domains[]` | `^[a-z0-9.*-]+\.[a-z]{2,}$`, wildcard→strip `*.` prefix, dedup | drop element + warning |
| `ip4[]` | `net.ParseIP() != nil && len(4)` | drop element |
| `ip6[]` | `net.ParseIP() != nil && len(16)` | drop element |
| `cidr4[]` / `cidr6[]` | `net.ParseCIDR()` + family check | drop element |
| `dns_servers[]` | `^[a-z0-9.-]+(:[0-9]{1,5})?$`, store as-is | drop element |
| `timeout_sec` | clamp [60, 86400], default 3600 | clamp |
| `external`, `replace`, прочее | silently ignored | — |

### Transaction strategy

```
BEGIN
  for each entry:
    INSERT … ON CONFLICT (slug) DO UPDATE (если OverwriteExisting=TRUE)
    либо INSERT … ON CONFLICT (slug) DO NOTHING
    если DefaultPlanIDs != пусто:
      INSERT INTO plan_direct_services
COMMIT
BumpDirectRoutesVersion()
RequestXraySync()
```

С `DryRun=true` — `ROLLBACK` в конце.

### Result

```go
type ImportResult struct {
    Created, Updated, Skipped int
    Errors []ImportError    // {Slug, Field, Value, Message}
    Warnings []string
}
```

---

## Edge Cases & Failure Modes

| Сценарий | Поведение |
|---|---|
| Невалидный JSON | 400 + сообщение с line/col |
| Один сервис из 86 имеет невалидный slug | Skip-with-warning (валидные применяются) |
| Импорт во время debounced Xray sync | Coalesce в следующий sync |
| Xray reject новый config | Atomic write fail, старый config жив, alert в log, admin видит warning |
| Удаление сервиса используемого планом | `ON DELETE CASCADE` чистит junction, RequestXraySync rebuild |
| 0 healthy exit-нод | Direct-rules сохраняются (применяются до block-fallback) — российские сервисы работают через Hub даже когда VPN down |
| Anonymous SOCKS5 inbound (без user-context) | Rule с `user:[...]` не сработает → fallback на default balancer (block) |
| Большой план (~30K доменов в одном rule) | Xray trie справится, JSON-config распухает до ~3 MB |
| Concurrent admin edits | Last-write-wins (acceptable для редкой админ-активности) |

---

## Deployment Sequence

```
1. cd server/backend && go build ./...                  # compile check
2. go run quick_deploy.go                               # deploy binary + миграция 000028
3. systemctl status heyearth-api                        # service up
4. go run ./cmd/deploy --site                           # deploy admin_web
5. Admin login → "Прямая маршрутизация" → Импорт JSON   # seed ip-list.json
6. Verify подписка: curl -H "Authorization: ..." \
      https://hiearth.ru/api/v1/user/subscribe/$TOKEN | \
      grep "happ://routing/onadd/" | … base64 -d | jq
7. Verify Xray config:
      go run ./cmd/deploy --ssh \
      'cat /opt/heyearth/xray/config.json | jq ".routing.rules[] | select(.outboundTag == \"direct-freedom\")"'
8. Functional smoke: Happ-client opens 2gis.ru → loads.
                     V2RayNG opens 2gis.ru → loads via Hub direct.
```

---

## Verification Checklist (evidence-driven)

- [ ] `go build ./...` — clean
- [ ] Миграция 000028 применилась, таблицы существуют
- [ ] `GET /admin/api/direct-routes/stats` → 200
- [ ] Создание сервиса через UI → запись в БД, RequestXraySync вызван
- [ ] Импорт `ip-list.json` через UI: created=86, errors=[]
- [ ] Привязка к плану (например `all-inclusive`) → подписка пользователя этого плана содержит `happ://routing/onadd/<b64>` с populated DirectIp/DirectSites
- [ ] `/opt/heyearth/xray/config.json` содержит outbound `direct-freedom` и rules с `outboundTag: "direct-freedom"` для users этого плана
- [ ] Из Happ на телефоне открыть 2gis.ru → загружается (через локальный direct)
- [ ] Из V2RayNG на телефоне открыть 2gis.ru → загружается (через Hub direct, IP 198.51.100.236)
- [ ] Из браузера через SOCKS5 на Hub → 2gis.ru загружается
- [ ] google.com через Happ → грузится через VPN (не direct — не в списке)

---

## Files Touched (summary)

| Категория | Файл | Действие |
|---|---|---|
| Migration | `server/backend/migrations/000028_direct_routes.up.sql` | NEW |
| Migration | `server/backend/migrations/000028_direct_routes.down.sql` | NEW |
| Backend biz | `server/backend/internal/biz/direct_routes.go` | NEW |
| Backend biz | `server/backend/internal/biz/happ_routing.go` | EXTEND |
| Backend API | `server/backend/cmd/api/subscription_handler.go` | EXTEND |
| Backend API | `server/backend/cmd/api/xray_sync.go` | EXTEND |
| Admin handlers | `server/backend/internal/admin/direct_routes_handler.go` | NEW |
| Admin router | `server/backend/internal/admin/router.go` | EXTEND |
| Admin UI page | `admin_web/src/pages/DirectRoutesPage.tsx` | NEW |
| Admin UI comp | `admin_web/src/components/DirectRouteEditDialog.tsx` | NEW |
| Admin UI comp | `admin_web/src/components/DirectRoutesImportDialog.tsx` | NEW |
| Admin UI comp | `admin_web/src/components/PlanSelector.tsx` | NEW |
| Admin UI i18n | `admin_web/src/i18n/ru.json` + `en.json` | EXTEND |
| Admin UI router | `admin_web/src/App.tsx` (или main router) | EXTEND |
| Admin UI sidebar | `admin_web/src/components/Sidebar.tsx` (или эквивалент) | EXTEND |
| Docs | `docs/research/direct-routing.md` | NEW |
| Docs | `docs/runbooks/import-direct-routes.md` | NEW |
| Seed | (через UI после deploy) | manual |

---

**End of design spec.**
