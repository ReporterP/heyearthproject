---
id: amneziawg-palma-prod
beads_id: heyearthproject-98ng
plan: ../plan/amneziawg-palma-prod.md
prd: ../prd/amneziawg-palma-prod.prd.md
status: ready
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

## Verification log
_(заполнится по мере закрытия задач)_
