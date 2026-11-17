# Express (Hysteria2) — Integration with Xray Architecture

**Beads epic:** `heyearthproject-3pc0` (extending Express phase)
**Date:** 2026-05-26
**Status:** Draft → for user review
**Predecessor:** `docs/research/express-hysteria2-architecture-gaps.md` (10 gaps identified)

## Problem

Hysteria2 (Express) lives as a parallel subsystem to Xray. It works in preview, but:
- Quota не работает (Express юзер качает без учёта cycle limit)
- Один exit без auto-failover (упадёт NL — Express ляжет)
- RU трафик через NL (yandex.ru медленно + геоблоки)
- Salamander obfs hardcoded — нет DPI rotation
- Hub→Exit tunnel = manual process, не в `deploy --status`
- `hysteria2_sync.go` — отдельный 5-min periodic, не реагирует на subscription change

## Goal

Сделать Express полностью интегрированной частью существующей Xray-архитектуры. Те же машины identity, quota, exit selection, failover, DPI rotation. Один источник истины (БД + biz layer). Никакого parallel runtime.

## Architecture Decisions

### A1 — Adaptive tunnel

```
on sync trigger:
  active_exits = SELECT * FROM servers WHERE is_active AND tunnel_health='healthy'
  N = len(active_exits)
  if N == 1:
    mode = "shared"
    hysteria2-server outbound -> socks5://127.0.0.1:1090  (existing hysteria2-client.service)
    stop+disable hysteria2-client-express.service if present
  else:  # N >= 2
    mode = "dedicated"
    best = min(active_exits, key=last_rtt_ms)
    write /etc/hysteria/client-express.yaml targeting best.ip:50443
    write/enable hysteria2-client-express.service (SOCKS5 :1091)
    hysteria2-server outbound -> socks5://127.0.0.1:1091
```

- Tunnel-monitor (existing, 30s) обновляет `servers.last_rtt_ms` + tunnel_health
- При смене best-RTT (на >20% разницы — hysteresis) sync переписывает `client-express.yaml` + restart service
- `servers.last_rtt_ms` column add via migration 000049

### A2 — Quota integration via polling

```yaml
# hysteria2 server.yaml additions:
trafficStats:
  listen: 127.0.0.1:7653
  secret: <random-32b-hex>
```

```go
// cmd/api/hysteria2_stats.go (new file)
func StartHysteria2StatsLoop() {
  ticker := time.NewTicker(60 * time.Second)
  prev := map[string]userCounter{}
  for range ticker.C {
    resp, _ := http.Get("http://127.0.0.1:7653/traffic",
      Header: {"Authorization": "<secret>"})
    cur := parseHysteria2Stats(resp.Body)
    for uuid, c := range cur {
      d := c.diff(prev[uuid])
      if d.up == 0 && d.down == 0 { continue }
      db.Exec(`INSERT INTO traffic_stats(user_id, upload_bytes, download_bytes)
               VALUES ($1, $2, $3)`, uuid, d.up, d.down)
    }
    prev = cur
  }
}
```

- Existing biz/traffic.go::IsCycleExceeded работает без изменений
- Auth backend перед `ok:true` вызывает `biz.IsCycleExceeded(userDBID, userUUID)` → если true → `{ok:false}`

### A3 — Sync integration (kill parallel runtime)

```go
// internal/biz/sync.go — single orchestrator (new)
func RequestSync() {
  syncDebouncer.Schedule(2*time.Second, func() {
    RequestXraySync()        // existing
    RequestHysteria2Sync()   // existing — но теперь вызывается из orchestrator
  })
}

func NotifySubscriptionChange() {
  RequestSync()
}
```

- `cmd/api/hysteria2_sync.go::SyncHysteria2Config` — adopt atomic temp+rename pattern from xray_sync
- Validation step: capture stdout+stderr, timeout 2s (current 500ms слишком короткий)
- Cert-perm bug pre-fixed by `setup-hysteria2-hub` обновлением (ssl-cert group)

### A4 — RU direct-route

```yaml
# hysteria2 server.yaml additions:
acl:
  geoip: /etc/hysteria/geoip.dat
  inline:
    - direct(geoip:ru)
    - via_exit(all)
```

- Setup script downloads `geoip.dat` from official Hysteria2 release on first install
- Auto-update geoip.dat monthly (cron)

### A5 — DPI rotation (auto-tuner extension)

```go
// cmd/api/auto_tuner.go (extension)
func runAutoTuner() {
  // ... existing Xray tuning ...
  
  // NEW: Hysteria2 obfs rotation (every Nth iteration — weekly)
  if iter%42 == 0 {  // 42 * 4h = 168h = 1 week
    newObfs := randHex(32)
    db.Exec("UPDATE app_settings SET value=$1 WHERE key='hysteria2_obfs_password'", newObfs)
    db.Exec("UPDATE app_settings SET value=value::int+1 WHERE key='hysteria2_config_version'")
    RequestHysteria2Sync()
  }
}
```

- Юзеры получают новый URI при следующем fetch `/api/v1/user/subscribe/<token>` (Happ переподтягивает по `expire` timestamp)

### A6 — `deploy --status` Express section

```
=== Express (Hysteria2) ===
  server: active, UDP :50443, clients: 12
  tunnel mode: dedicated (2 healthy exits)
  bound exit: NL 192.0.2.126 (RTT 18ms)
  config_version: 23
  obfs rotation: 4 days ago
```

## API Changes

### `biz.ValidateHysteria2Auth(password string)` (modify)
Add quota check:
```go
func ValidateHysteria2Auth(password string) AuthResult {
  // existing password match...
  if cycleExceeded, _ := IsCycleExceeded(userDBID, userUUID); cycleExceeded {
    return AuthResult{OK: false}
  }
  return AuthResult{OK: true, UserID: userUUID}
}
```

### New: `biz.SelectExpressExit() *Server`
Returns best-RTT healthy exit for dedicated tunnel mode.

### New: `biz.GetExpressTunnelMode() string` — "shared" | "dedicated"

## Schema Changes (migration 000049)

```sql
-- Add RTT tracking for exit selection
ALTER TABLE servers ADD COLUMN IF NOT EXISTS last_rtt_ms INTEGER DEFAULT NULL;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS last_rtt_at TIMESTAMPTZ DEFAULT NULL;

-- Hysteria2 stats API secret + tunnel mode tracking
INSERT INTO app_settings (key, value)
VALUES ('hysteria2_stats_secret', encode(gen_random_bytes(32), 'hex')),
       ('hysteria2_tunnel_mode', 'shared')
ON CONFLICT (key) DO NOTHING;
```

## Acceptance Criteria

1. Express трафик считается в `traffic_stats` — over-quota юзер автоматически cut off в течение 60s
2. При одной exit-ноде Express использует ту же `hysteria2-client.service` что и Xray
3. При двух+ exit-нодах Express имеет dedicated tunnel к best-RTT exit, auto-failover на смену best
4. RU-домены (yandex.ru / vk.com / habr.com) через Express идут direct (без NL hop)
5. Obfs password ротируется auto-tuner'ом раз в неделю; happ получает новый URI на следующий subscribe fetch
6. `deploy --status` показывает Express секцию (tunnel mode, exit, RTT, config version)
7. `biz.NotifySubscriptionChange()` триггерит ОБА sync (Xray + Hysteria2) через единый orchestrator
8. Hysteria2 validation step capture-it stdout+stderr с 2s timeout — больше не false-fail на cert perm
9. Plan gating остаётся выключенным (preview); `plans.express_enabled` column сохраняется для будущего

## Out of Scope (future)

- Per-plan bandwidth (sub override через `bandwidth: {up,down}` per-user auth response)
- Multiple hysteria2 inbounds (порт-диверсификация)
- Replace manual `client-2.yaml` с systemd unit (require operator confirmation на boxes)

## Risks

- Migration 000049 ALTER servers — на проде ~10 строк, micro-сек lock
- Auto-tuner obfs rotation = breaks existing happ subscriptions on the spot. Mitigation: happ has 60s reconnect grace; users impacted ≤1 cycle
- Adaptive tunnel switching (shared ↔ dedicated) при смене exit count — может вызвать 3-5s простой Express. Acceptable.
- `geoip.dat` ~5MB binary в /etc/hysteria/ — добавляет к setup time. Cache + auto-update OK.

## Tasks Outline (will be expanded in writing-plans skill)

| # | Task | Estimate |
|---|---|---|
| T1 | Migration 000049 (servers.last_rtt_ms + app_settings) | 15 min |
| T2 | biz/sync.go orchestrator + NotifySubscriptionChange wiring | 30 min |
| T3 | hysteria2_sync.go — atomic write + better validation | 45 min |
| T4 | Auth backend cycle-quota check | 30 min |
| T5 | hysteria2_stats.go collector (polling) | 60 min |
| T6 | Adaptive tunnel logic (shared vs dedicated) | 90 min |
| T7 | RU direct-route via geoip.dat | 45 min |
| T8 | Auto-tuner obfs rotation extension | 30 min |
| T9 | `deploy --status` Express section | 30 min |
| T10 | Deploy + smoke (over-quota cutoff, exit-add adaptive switch, RU direct) | 60 min |

Total: ~7 hours focused work.
