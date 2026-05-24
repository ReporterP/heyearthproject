---
id: direct-routing
beads_id: heyearthproject-agh4
plan: ../superpowers/plans/2026-05-24-direct-routing-infrastructure.md
prd: ../prd/direct-routing.prd.md
spec: ../superpowers/specs/2026-05-23-direct-routing-infrastructure-design.md
status: in_progress
created: 2026-05-24
---

# Tasklist: Direct Routing Infrastructure

16 задач в 5 фазах. Primary source — Beads (`bd show heyearthproject-agh4`).

## Phase 1 — Backend Foundation (~115 min)
- [ ] **heyearthproject-5bak** (T1) — Migration 000044: tables + permissions + setting
  - Файлы: `server/backend/migrations/000044_direct_routing.{up,down}.sql`
  - Verify: `go build ./...` + (optional) `psql -d heyearth -f migrations/000044_direct_routing.up.sql`
- [ ] **heyearthproject-p78f** (T2, blocks: T1) — `biz/direct_routes.go` CRUD
  - Файлы: `server/backend/internal/biz/direct_routes.go`
  - Verify: `go build ./...` + `go vet ./internal/biz/...`
- [ ] **heyearthproject-q0e2** (T3, blocks: T2) — `biz/direct_routes_import.go` bulk + JSON import
  - Файлы: `server/backend/internal/biz/direct_routes_import.go`
  - Verify: `go build ./...` + `go vet`
- [ ] **heyearthproject-yqdp** (T4, blocks: T2) — `biz/direct_routes_query.go` consumers
  - Файлы: `server/backend/internal/biz/direct_routes_query.go`
  - Verify: `go build ./...`

## Phase 2 — VPN Integration (~85 min)
- [ ] **heyearthproject-47jx** (T5, blocks: T4) — Extend `happ_routing.go` with `BuildHappRoutingDeeplinkForPlan`
  - Файлы: `server/backend/internal/biz/happ_routing.go`
  - Verify: `go build` + decode resulting `happ://routing/onadd/<b64>` contains both DirectIp + DirectSites
- [ ] **heyearthproject-7uiw** (T6, blocks: T5) — Wire `subscription_handler.go` to new builder
  - Файлы: `server/backend/cmd/api/subscription_handler.go` (lines ~99-109)
  - Verify: `go build`
- [ ] **heyearthproject-5c25** (T7, blocks: T4) — Extend `xray_sync.go`: direct-freedom outbound + rules
  - Файлы: `server/backend/cmd/api/xray_sync.go` (insertions ~line 288 and ~line 304)
  - Verify: `go build` + после deploy `jq '.outbounds[]|select(.tag=="direct-freedom")' /opt/heyearth/xray/config.json`

## Phase 3 — Admin HTTP Layer (~65 min)
- [ ] **heyearthproject-748o** (T8, blocks: T3) — `internal/admin/direct_routes_handler.go` — 10 endpoints
  - Файлы: `server/backend/internal/admin/direct_routes_handler.go`
  - Verify: `go build` + `go vet ./internal/admin/...`
- [ ] **heyearthproject-ai71** (T9, blocks: T8) — Wire routes in `internal/admin/routes.go`
  - Файлы: `server/backend/internal/admin/routes.go`
  - Verify: `grep -n '"/api/admin/direct-routes' internal/admin/routes.go` → 6 matches

## Phase 4 — Admin UI (~115 min)
- [ ] **heyearthproject-cxl9** (T10, blocks: T9) — `components/PlanSelector.jsx` multi-select
  - Файлы: `admin_web/src/components/PlanSelector.jsx`
  - Verify: `npm run build` success
- [ ] **heyearthproject-lcd4** (T11, blocks: T10) — `components/DirectRouteEditDialog.jsx` CRUD modal
  - Файлы: `admin_web/src/components/DirectRouteEditDialog.jsx`
  - Verify: `npm run build`
- [ ] **heyearthproject-e5jc** (T12, blocks: T10) — `components/DirectRoutesImportDialog.jsx` import modal
  - Файлы: `admin_web/src/components/DirectRoutesImportDialog.jsx`
  - Verify: `npm run build`
- [ ] **heyearthproject-ryrc** (T13, blocks: T11) — `pages/DirectRoutesPage.jsx` + routing + sidebar + i18n
  - Файлы: `admin_web/src/pages/DirectRoutesPage.jsx`, `main.jsx`, `components/Sidebar.jsx`, `i18n/{ru,en}.json`
  - Verify: `npm run build` + visual sidebar link visible

## Phase 5 — Deploy + Seed + Verify (~120 min)
- [ ] **heyearthproject-fxoj** (T14, blocks: T7) — Deploy backend + curl smoke 10 endpoints
  - Verify: `quick_deploy.go` + `go run ./cmd/deploy --status` + curl create/read/delete cycle
- [ ] **heyearthproject-kz9n** (T15, blocks: T13) — Deploy admin_web + import ip-list.json via UI
  - Файлы: `docs/research/ip-list-2026-05-24.json` (committed snapshot)
  - Verify: 86 services imported, stats show plans_count=4, attached to plans 3,4,5,6
- [ ] **heyearthproject-lzus** (T16, blocks: T15) — End-to-end verification
  - Verify: Xray config has direct-freedom outbound + ≥4 direct rules + subscription decode shows ≥15k DirectSites + manual Happ smoke

## Side quests
- (none yet — добавлять через `bd create ... --type bug --dep discovered-from`)

## Deferred
- Per-user routing profiles (пользователь сам управляет direct-списком) — отдельная фича на потом.
- Auto-update direct-list из community-источника (GitHub-репо + cron import).
- Per-service traffic statistics.
- Wildcard SNI matching (полагаемся на Xray `domain:` prefix subdomain matching).

## Verification log
Заполняется по мере выполнения:
- **T1**: pending
- **T2**: pending
- (etc)
