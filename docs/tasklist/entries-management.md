---
id: entries-management
beads_id: heyearthproject-ifb8
plan: ../plan/entries-management.md
status: in_progress
created: 2026-11-20
---

# Tasklist: Управление резервными входами на «Входах»

Зеркало Beads (эпик `heyearthproject-ifb8`, цепочка `blocks` по порядку). Подробности шагов — в плане.

## Фаза 1: GRE по выбору
- [x] **heyearthproject-2f7v** — T1 рецепт входа без туннеля по умолчанию, `servers.relay_tunnel`, снятие туннеля, `--relay-tunnel` ставит флаг
  - Файлы: `migrations/000096_*`, `internal/provision/relay*.go`, `cmd/api/provision_wiring.go`, `cmd/deploy/relay_tunnel.go`
  - Verify: `go test ./internal/provision/ ./cmd/api/ ./cmd/deploy/`
- [x] **heyearthproject-vvpf** — T2 галочка «Туннель до Hub» в мастере
  - Файлы: `internal/admin/provision_handlers.go`, `admin_web/src/components/nodes/{AddNodeWizard.jsx,wizardModel.js}`
  - Verify: `npm test`, `go test ./internal/admin/`

## Фаза 2: ротация и проверки
- [x] **heyearthproject-loi4** — T3 `entry_state.rotation`, списки входов, активация нового входа в `test`, вход 6 → `online` + `off`
  - Verify: `go test ./internal/biz/ ./internal/provision/` (pg-тесты с `TEST_DATABASE_URL`)
- [x] **heyearthproject-8qzz** — T4 подписка, автоподмена и тревоги по ротации
  - Verify: `go test ./cmd/api/`
- [x] **heyearthproject-pi4x** — T5 задачи `pathcheck` и `tunnelcheck`
  - Verify: `go test ./internal/provision/ ./cmd/deploy/`
- [x] **heyearthproject-72lo** — T6 API действий по входу
  - Verify: `go test ./cmd/api/ -run 'Rotation|Tunnel|Entries'`

## Фаза 3: стабильность и UI
- [x] **heyearthproject-3bno** — T7 `entry_probe_samples`, 1 ч / 24 ч, тревога `relay-<id>-unstable` (закрывает мониторинг из `0kat`)
  - Verify: `go test ./cmd/api/ ./internal/biz/`
- [x] **heyearthproject-uv92** — T8 таблица «Входов» и панель входа
  - Verify: `npm test && npm run build`
- [x] **heyearthproject-r7ar** — T9 удаление с «Входов», финал мастера
  - Verify: `npm test && npm run build`
- [ ] **heyearthproject-rcyt** — T10 документы, ревью, слияние в `main`, деплой по «да» владельца
  - Verify: `go test ./cmd/... ./internal/...`, `deploy --status`, скриншоты 1440/375

## Side quests
- автоматический вывод входа по доле потерь (порог 25 % из `0kat`) — завести после T7
