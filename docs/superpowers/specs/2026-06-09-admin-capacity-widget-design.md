---
id: admin-capacity-widget
beads_id: heyearthproject-7ufk
title: Admin capacity widget — Hub+Exit headroom monitor
status: approved
created: 2026-06-09
---

# Admin capacity widget — Hub + Exit headroom monitor

## Цель

Дать админу в реальном времени видеть запас ёмкости инфраструктуры: CPU, трафик
vs квота, число активных подписок vs расчётный потолок, себестоимость на юзера,
и светофор «пора ли добавлять сервер». Цель — не пропустить момент масштабирования
(CPU/трафик-bottleneck) и видеть unit-экономику.

## Архитектура

**Read-only, аддитивно.** Новый эндпоинт `GET /api/admin/capacity` + блок на
`DashboardPage`. Ничего не пишет в БД, не трогает метрик-коллектор / серверы /
биллинг / VPN-сервисы.

### Data flow
```
DashboardPage → GET /api/admin/capacity (admin JWT)
   ├─ Hub: heyearth-api читает ЛОКАЛЬНО (он сам на Hub)
   │     /proc/loadavg  → CPU load (1-min)
   │     /proc/meminfo  → RAM used %
   │     /proc/net/dev  → RX+TX bytes; /proc/uptime → days → GB/день
   │     ss / DB        → active subs (COUNT user_subscriptions active)
   └─ Exit: servers.load_metrics jsonb {cpu_usage, memory_usage, active_connections}
            (уже собирается node-agent'ом каждые 10с)
```

### Константы расчёта (по тарифу vdsina, одинаковы Hub/Exit)
- `cores = 2`
- `quotaTB = 32` → `quotaGBday = 32*1024/30 ≈ 1092 GB/день`
- `costRubMonth = 1200` (40₽/день × 30) на сервер
- `targetLoad = 1.4` (70% от 2 ядер — безопасный потолок до лагов)

### Формулы потолка (Hub)
- `cpuPct = load1 / cores * 100`
- `maxSubsCPU = activeSubs * (targetLoad / max(load1, 0.01))`
- `maxSubsTraffic = activeSubs * (quotaGBday / max(gbPerDay, 1))`
- `maxSubs = floor(min(maxSubsCPU, maxSubsTraffic))` (если activeSubs=0 → maxSubs=0, fillPct=0)
- `fillPct = activeSubs / maxSubs * 100` (0 если maxSubs=0)
- `costPerSub = costRubMonth / max(activeSubs, 1)`
- `verdict`: `ok` (<60%) / `warn` (60-85%) / `critical` (>85%)

Exit verdict — по `cpu_usage`: ok <60, warn 60-85, critical >85.

## Компоненты

| Файл | Ответственность |
|---|---|
| `internal/admin/capacity_handler.go` (new) | `HandleCapacity` — собирает Hub local /proc + Exit load_metrics, считает формулы, JSON |
| `internal/admin/routes.go` (modify) | регистрация `GET /api/admin/capacity` (admin-auth middleware) |
| `admin_web/src/api.js` (modify) | `getCapacity()` helper |
| `admin_web/src/pages/DashboardPage.jsx` (modify) | блок «Ёмкость инфраструктуры»: Hub+Exit карточки, прогресс-бары, verdict, cost/user |

## JSON-контракт
```json
{
  "hub": {
    "cpu_load": 0.42, "cpu_cores": 2, "cpu_pct": 21,
    "ram_pct": 26,
    "traffic_gb_day": 136, "quota_gb_day": 1092, "traffic_pct": 12,
    "active_subs": 62, "max_subs": 200, "fill_pct": 31,
    "cost_rub_month": 1200, "cost_per_sub": 39,
    "verdict": "ok"
  },
  "exit": {
    "name": "Амстердам 32Tb", "online": true,
    "cpu_pct": 18, "ram_pct": 22, "active_connections": 40,
    "verdict": "ok"
  }
}
```

## Error handling
- Hub `/proc` read fail → поле `null`, не 500 (best-effort, виджет показывает «н/д»).
- Exit offline / load_metrics пуст → `online:false`, метрики `null`.
- Эндпоинт всегда 200 с тем что удалось собрать (кроме auth — 401).

## Out of scope
- Запись/история ёмкости (только snapshot on-request).
- Алерты/уведомления (только визуальный verdict).
- Параметры в БД (константы в коде; вынести в app_settings — позже).
- Изменение метрик-коллектора, серверов, биллинга, VPN.
- Графики/тренды (только текущие значения + бары).

## Verification (evidence)
- `go build ./...` exit 0
- `curl /api/admin/capacity` с admin JWT → JSON с hub+exit
- `curl` без auth → 401
- `cd admin_web && npm run build` exit 0
- Визуально: блок на admin.hiearth.ru виден, числа правдоподобны (active_subs=62, fill ~31%, verdict ok)
- RED LINE: Reality/AWG/Бамбук/Happ работают, метрик-коллектор не тронут

## Риски
- 🟡 `/proc/net/dev` даёт трафик с boot — при долгом uptime среднее занижено vs текущий пик. Приемлемо для headroom-индикатора (не биллинг).
- 🟢 Линейная экстраполяция CPU-потолка — груба, но честно помечена как оценка.
