---
id: relay-gre-tunnel
beads_id: heyearthproject-x9n9
plan: ../plan/relay-gre-tunnel.md
status: done
created: 2026-11-20
---

# Tasklist: GRE-туннель вход ↔ хаб

## Фаза 1: код
- [x] **heyearthproject-w4x1** — генераторы туннеля (адреса, юниты, nft, nginx, доверие)
  - Файлы: `server/backend/internal/config/relay_tunnel.go`, `relay_nft.go`, `relay_stream.go`
  - Verify: `go test ./internal/config/`
- [x] **heyearthproject-1pop** — шаги рецепта: хаб и вход, preflight, deprovision (blocks: w4x1)
  - Файлы: `server/backend/internal/provision/relay_tunnel.go`, `relay.go`, `relay_stream.go`
  - Verify: `go test ./internal/provision/`
- [x] **heyearthproject-cfpt** — монитор проверяет :2443, wiring `ServerID`
  - Файлы: `server/backend/cmd/api/entry_monitor.go`, `provision_wiring.go`
  - Verify: `go test ./cmd/api/ -run 'Entry|Relay'`
- [x] **heyearthproject-onmd** — allow-скрипт хаба знает адреса туннелей
  - Файлы: `server/backend/cmd/deploy/ops_hardening.go`
  - Verify: `go test ./cmd/deploy/ -run DDoS`
- [x] **heyearthproject-2fc3** — `deploy --relay-tunnel` с матрицей и предохранителями (blocks: 1pop)
  - Файлы: `server/backend/cmd/deploy/relay_tunnel.go`, `relay_matrix.py`, `main.go`
  - Verify: `go test ./cmd/deploy/ -run RelayTunnel`

## Фаза 2: выдача
- [x] **heyearthproject-2pcf** — runbook, CLAUDE.md, ревью, слияние в main, деплой на вход 6 по «да» владельца, итог в инциденте
  - Verify: матрица после ≥ 99 % (TCP 2443 и UDP), `deploy --status`

## Verification log
- **w4x1** (20.11): `go test ./internal/config/` ok; лаборатория в docker (2 контейнера, настоящий GRE, nginx 1.27 с golden-конфигом): через туннель хаб видит адрес туннеля входа; туннель мёртв — 2 тайм-аута по 1 с, затем backup; туннель вернулся — снова туннель.
- **1pop** (20.11): `go test ./internal/provision/` ok (свежая установка → повтор без изменений, откаты шагов туннеля, отказ при пересечении пула, откат при смене маршрута хаба, старый allow-скрипт, deprovision).
- **cfpt** (20.11): `TestEntryMonitorRelay2443`: туннель лёг (443 жив, 2443 нет) → unhealthy за 3 проверки; одна потеря 2443 → повтор, вход здоров.
- **onmd** (20.11): `TestDDoSAllowScriptTunnelPeers`: адрес туннеля в allow4, чужой point-to-point peer — нет.
- **2fc3** (20.11): тесты скриптов, решения и Python-матрицы (против TLS и UDP-ответчика на Go); контейнерная лаборатория apply: успех, плохой замер «после» (все файлы вернулись побайтно), сдвиг маршрута хаба (откат до входа).
- **2pcf** (20.11): ревью (7 находок, исправлены), слито в main; прод: API `a9098c8` ~15:58 (health 200), `--relay-tunnel 6` ~16:10 — TCP 2443 0→100/100, UDP 0→100/100, :443 входа 0→40/40; `--path-matrix 6` через выход :443 50/50, :2443 50/50.
