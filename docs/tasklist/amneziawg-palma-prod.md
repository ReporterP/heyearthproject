---
id: amneziawg-palma-prod
beads_id: heyearthproject-98ng
plan: ../plan/amneziawg-palma-prod.md
prd: ../prd/amneziawg-palma-prod.prd.md
status: in_progress
created: 2026-06-08
---

# Tasklist: AmneziaWG (Пальма) production rollout

Зеркало Beads для человекочитаемой навигации. Single source of truth — `bd show heyearthproject-98ng`.

**RED LINE:** не трогать Reality/Happ/Hysteria2-Exit. AWG — изолированный второй потребитель SOCKS-туннеля.

## Задачи (линейная цепочка T1→T8)

- [ ] **heyearthproject-4y47** — T1: Hub infra (amneziawg-tools + awg-quick up awg0 + ip_forward)
  - Файлы: `cmd/deploy/amneziawg_hub.go`
  - Verify: `--ssh "ip link show awg0"` UP, `--ssh "ss -ulnp | grep 51820"` listening, `--ssh "awg show awg0"`

- [ ] **heyearthproject-plyd** — T2: Split-routing через xray (socks-awg inbound + tun2socks + policy route) [blocks: T1]
  - Файлы: `cmd/api/xray_sync.go` (аддитивный socks-awg inbound), `cmd/deploy/awg_exit_route.go` (new), `cmd/deploy/main.go`
  - Verify: **split как Happ** — foreign→Amsterdam, RU-сайт→Hub direct (Москва); `ip rule show | grep 10.66.66`; table main не тронут; Reality не затронут

- [ ] **heyearthproject-5ol2** — T3: Фикс awg_sync.go (wg→awg syncconf, real cold-start, traffic-limit WHERE) [blocks: T2]
  - Файлы: `cmd/api/awg_sync.go`
  - Verify: `go build ./cmd/api/...` exit 0; over-limit peer снимается

- [ ] **heyearthproject-3ops** — T4: AWG traffic collector (awg_stats.go, dual-write) [blocks: T3]
  - Файлы: `cmd/api/awg_stats.go` (new), `cmd/api/main.go`
  - Verify: `go build`; после прокачки `traffic_used` растёт; `[awg-stats]` в логе

- [ ] **heyearthproject-uix8** — T5: Endpoint /api/v1/user/awg-config + connections flag [blocks: T4]
  - Файлы: `cmd/api/main.go`
  - Verify: premium → config_text, free-trial → 403; `go build`

- [ ] **heyearthproject-anpw** — T6: Кабинет — блок Пальмы (site ConnectionsPage) QR+файл+deeplink [blocks: T5]
  - Файлы: `site/src/api/user.js`, `site/src/pages/ConnectionsPage.jsx`
  - Verify: `npm run build`; блок виден только при amnezia_available; Reality-блок без изменений

- [ ] **heyearthproject-62kj** — T7: Gating fix (300/3000 only) + admin verify [blocks: T6]
  - Файлы: prod SQL `plans`, `admin_web/PlansPage.jsx` (verify only)
  - Verify: `SELECT amnezia_wg_enabled` FALSE на id 1,4; TRUE на 5,6; admin checkbox round-trips

- [ ] **heyearthproject-ge1e** — T8: Deploy всего + E2E [blocks: T7]
  - Файлы: deploy commands
  - Verify: money-shot Amsterdam IP (USER-ASSISTED); traffic_used растёт; free-trial 403; RED LINE — 4 Reality URI / 0 amneziawg в Happ-подписке, xray accepted идёт

## RED LINE регрессия (проверяется в T8)
- [ ] Happ-подписка отдаёт те же 4 Reality URI, без `amneziawg://`
- [ ] Reality (Берёза/Дуб) на Tele2 работает — xray accepted-коннекты идут
- [ ] Hub→Exit Hysteria2 туннель здоров

## Verification log (2026-06-09)
- **T1** ✓ c33c1e6 — amneziawg-tools built (2.15.0 era), awg-quick oneshot unit, awg0 UP, :51820 listening, ip_forward=1. (deploy required T3 renderer fix first)
- **T3** ✓ ebb4aba — CPS moved to [Interface] (global, GetServerCPSParams), awg syncconf + cold-start (awg-quick), traffic-limit WHERE. Fixed root cause Palma never came up (CPS-in-[Peer] rejected by `awg setconf`). awg0.conf valid, peers applied.
- **T2** ✓ 91ecd41/c3d108b/ee393b5 — socks-awg inbound + hev-socks5-tunnel 2.15.0 + policy routing table 100. **Split verified: dzen.ru/mail.ru → direct-freedom (Moscow), ipify → exit-2 (Amsterdam)**, как Happ. Fixed: user-bound rules gotcha (added inboundTag:[socks-awg] split rules); cap-sandbox broke hev (dropped); git tag 2.7.7→2.15.0. peer 10.66.66.2 → awgtun. table main + Reality untouched.
- **T5** ✓ f259b77 — /api/v1/user/awg-config (401 no-token, gated), amnezia_available flag.
- **T6** ✓ d70b516 — Пальма card (lazy QR+conf+deeplink), site build OK, deployed.
- **T7** ✓ — gating SQL (0/250→FALSE, 300/3000→TRUE), peer count 5→4, admin checkbox verified.
- **T4** ✓ eb9cba5 — awg_stats.go collector (dual-write traffic_stats + traffic_used), started, reads `awg show transfer` (4 peers).
- **RED LINE held throughout:** Happ subscription = 4 vless / 0 amneziawg; Reality SOCKS 1090 → Amsterdam; table main default unchanged; all 6 services active.
- **T8** ◐ pending: live AmneziaWG-client money-shot (foreign→Amsterdam, RU→Hub direct, traffic_used grows) — USER-ASSISTED via cabinet config.
