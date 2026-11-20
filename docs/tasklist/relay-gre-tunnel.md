---
id: relay-gre-tunnel
beads_id: heyearthproject-x9n9
plan: ../plan/relay-gre-tunnel.md
status: in_progress
created: 2026-11-20
---

# Tasklist: GRE-туннель вход ↔ хаб

## Фаза 1: код
- [ ] **heyearthproject-w4x1** — генераторы туннеля (адреса, юниты, nft, nginx, доверие)
  - Файлы: `server/backend/internal/config/relay_tunnel.go`, `relay_nft.go`, `relay_stream.go`
  - Verify: `go test ./internal/config/`
- [ ] **heyearthproject-1pop** — шаги рецепта: хаб и вход, preflight, deprovision (blocks: w4x1)
  - Файлы: `server/backend/internal/provision/relay_tunnel.go`, `relay.go`, `relay_stream.go`
  - Verify: `go test ./internal/provision/`
- [ ] **heyearthproject-cfpt** — монитор проверяет :2443, wiring `ServerID`
  - Файлы: `server/backend/cmd/api/entry_monitor.go`, `provision_wiring.go`
  - Verify: `go test ./cmd/api/ -run 'Entry|Relay'`
- [ ] **heyearthproject-onmd** — allow-скрипт хаба знает адреса туннелей
  - Файлы: `server/backend/cmd/deploy/ops_hardening.go`
  - Verify: `go test ./cmd/deploy/ -run DDoS`
- [ ] **heyearthproject-2fc3** — `deploy --relay-tunnel` с матрицей и предохранителями (blocks: 1pop)
  - Файлы: `server/backend/cmd/deploy/relay_tunnel.go`, `relay_matrix.py`, `main.go`
  - Verify: `go test ./cmd/deploy/ -run RelayTunnel`

## Фаза 2: выдача
- [ ] **heyearthproject-2pcf** — runbook, CLAUDE.md, ревью, слияние в main, деплой на вход 6 по «да» владельца, итог в инциденте
  - Verify: матрица после ≥ 99 % (TCP 2443 и UDP), `deploy --status`

## Verification log
