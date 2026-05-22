---
title: MTProto auto-deploy on all exits + balancer (sub-project B.2)
status: design
created: 2026-05-22
parent_initiative: "Turn P1 lessons into permanent features (A → B+C → B.2 → D)"
beads_epic: TBD (created at writing-plans step)
relates_to: heyearthproject-i9fv (B+C — closed)
---

# MTProto Everywhere — auto-deploy + balancer

## Problem

B+C deployed but **MTProto-proxy daemon is not installed anywhere yet**. Endpoint `/api/v1/user/mtproto` returns a single static `mtproto_host` from app_settings — if you set that to one IP, all users get that IP, and if it goes down everyone loses the perk. Manual install via `install-mtproxy.sh` on each new exit-node is friction.

User wants: **install mtproto-proxy on every exit-node automatically** (current Amsterdam + any future ones), and have the endpoint pick a **healthy** exit at request time. Without manual ops.

## Scope

### In scope
* DB migration 000042: add `mtproto_status`, `mtproto_last_check`, `mtproto_installed_at` to `servers` table; remove obsolete `mtproto_host` / re-purpose `mtproto_port` as global default.
* Extend `runSetupExitNode` (existing 7-step async provisioning) with an 8th step: `install_mtproxy` — runs the existing `install-mtproxy.sh` over SSH with shared secret passed as argument.
* Extend `install-mtproxy.sh` to accept a 2nd argument `SECRET` — if provided, use it instead of generating one. Backward-compatible (no arg → generates as before).
* Extend Ghost Reconciler (30-min loop) with a 4th category: **`mtproto_missing`** — exit-nodes with `lifecycle_state='online' AND mtproto_installed_at IS NULL`. If toggle `mtproto_auto_install=true`, fire the install step asynchronously.
* Extend tunnel health monitor with per-exit TCP-probe on the global `mtproto_port` (default 8443). Hysteresis: 3 fails → `mtproto_status='unhealthy'`; 2 successes → `'healthy'`.
* New `biz.PickMTProtoExit() (host, port string, ok bool)` — weighted random pick among `mtproto_status='healthy'` exits using existing `load_weight`.
* Update `/api/v1/user/mtproto` to use `PickMTProtoExit()` instead of static `mtproto_host`. Returns `enabled:false` if no healthy exit.
* Update admin `MTProtoPage`: drop Host/Port input fields (now per-exit, auto), keep Enabled/Secret/SponsorChannel/Toggle for auto-install.
* Update admin `ServerDetailPage`: show MTProto status badge + "Переустановить mtproxy" action button.
* Update admin `ServersPage`: column or inline badge for `mtproto_status`.

### Out of scope (separate cycles)
* Sub-project D — per-plan rate-limit.
* Per-server `mtproto_secret` rotation (we use shared secret across all exits).
* Telegram-side load metrics (MTProto-proxy emits stats but we don't ingest them).
* Geo-aware selection (prefer-by-country) — for now random/weighted is enough.

## Architecture

### State model

```
                            servers (per row)
                            ─────────────────
                            mtproto_status       'unknown' | 'installing' | 'healthy' | 'unhealthy'
                            mtproto_last_check   timestamp of last TCP probe
                            mtproto_installed_at NULL if never installed; timestamp once install succeeds

                            app_settings (global)
                            ─────────────────────
                            mtproto_enabled         'true'|'false'  — user-facing toggle
                            mtproto_secret          shared 32-hex     — applied to every daemon
                            mtproto_port            default '8443'    — same on all exits
                            mtproto_sponsor_channel optional tag
                            mtproto_auto_install    'true'|'false'    — gates Ghost Reconciler auto-install
```

### Auto-install paths

```
Path A — Provisioning a NEW exit (admin clicks "Setup Exit Node"):
  runSetupExitNode(server) → 7 existing steps →
    Step 8: install_mtproxy
      ssh exit "bash /tmp/install-mtproxy.sh <port> <secret>"
      on success: UPDATE servers SET mtproto_status='healthy',
                                     mtproto_installed_at=NOW() WHERE id=$1

Path B — Backfill for EXISTING exits (Ghost Reconciler, 30 min loop):
  for each exit where lifecycle_state='online' AND mtproto_installed_at IS NULL:
    if app_settings.mtproto_auto_install='true':
      mark mtproto_status='installing'
      same SSH script as Path A
      report success/failure

Path C — Manual reinstall (admin button in ServerDetailPage):
  POST /api/admin/servers/<id>/actions { action: "reinstall_mtproxy" }
  same SSH script as Path A
```

### Health-check loop

`StartTunnelHealthMonitor` runs every 30s. Extension:
```
for each exit with mtproto_installed_at IS NOT NULL:
  tcp_dial(exit.ip_address, mtproto_port, 3s_timeout)
  update fail/success counters
  on 3 fails → mtproto_status='unhealthy'
  on 2 successes from unhealthy → mtproto_status='healthy'
  update servers.mtproto_last_check = NOW()
```

### Balancer

```go
func PickMTProtoExit() (host, port string, ok bool) {
    // SELECT id, ip_address, load_weight FROM servers
    // WHERE is_exit_node AND lifecycle_state='online' AND mtproto_status='healthy'
    rows := query(...)
    if len(rows) == 0 { return "", "", false }

    // Weighted random — duplicate entries by load_weight, pick random index
    pool := make([]row, 0)
    for _, r := range rows {
        w := r.load_weight; if w < 1 { w = 1 }
        for i := 0; i < w; i++ { pool = append(pool, r) }
    }
    chosen := pool[rand.Intn(len(pool))]
    return chosen.ip_address, GetSettingOrDefault("mtproto_port", "8443"), true
}
```

### Endpoint update

```go
// GET /api/v1/user/mtproto
func handleUserMTProto(w http.ResponseWriter, r *http.Request) {
    // ... existing auth + active-subscription check stays ...
    if !mtproto_enabled || secret == "" {
        return { enabled: false }
    }
    host, port, ok := biz.PickMTProtoExit()
    if !ok {
        return { enabled: false, reason: "Временно недоступно" }
    }
    return {
        enabled: true,
        host: host,
        port: port,
        secret: secret,
        tg_deeplink: "tg://proxy?server=" + host + "&port=" + port + "&secret=" + secret,
        t_me_link: "https://t.me/proxy?server=" + host + "&port=" + port + "&secret=" + secret,
    }
}
```

## Components

### Backend (Go)
- **Modify** `cmd/api/client_api.go` (or `cmd/api/main.go` per Task 4 finding) — `handleUserMTProto` uses `biz.PickMTProtoExit()`.
- **Create** `internal/biz/mtproto.go` — `PickMTProtoExit()`, `ListExitsNeedingMTProto()`, `MarkMTProtoStatus(serverID, status string) error`.
- **Modify** `internal/admin/servers_handlers.go::runSetupExitNode` — add Step 8 `install_mtproxy`.
- **Modify** `internal/admin/servers_handlers.go::ServerActionHandler` — add `reinstall_mtproxy` action.
- **Modify** `cmd/api/ghost_reconciler.go` — add `mtproto_missing` reconciliation.
- **Modify** `cmd/api/xray_sync.go` (or wherever StartTunnelHealthMonitor lives) — add mtproto TCP probe.
- **Create** `migrations/000042_mtproto_per_exit.up.sql` + `.down.sql`.

### Script
- **Modify** `cmd/deploy/install-mtproxy.sh` — accept `[$2 = secret]` arg, skip secret generation if provided.

### Admin (React)
- **Modify** `admin_web/src/pages/MTProtoPage.jsx` — remove Host/Port fields, add `auto_install` toggle, add Reinstall-all button.
- **Modify** `admin_web/src/pages/ServerDetailPage.jsx` — add MTProto status badge + "Переустановить" button.
- **Modify** `admin_web/src/pages/ServersPage.jsx` — show `mtproto_status` next to `tunnel_status`.

### User-site
- No changes. TelegramProxyButton already calls `/api/v1/user/mtproto` and reads `host/port/secret/tg_deeplink/t_me_link` from response.

## Data flow

### Provisioning new exit (full lifecycle)
```
Admin clicks "Setup Exit Node" → runSetupExitNode(server)
   step 1-7: existing (network/xray/hysteria2/firewall/...)
   step 8 install_mtproxy:
      SCP install-mtproxy.sh → exit-VPS:/tmp/
      ssh exit "bash /tmp/install-mtproxy.sh ${MTPROTO_PORT} ${MTPROTO_SECRET}"
      capture exit code
      on success: UPDATE servers SET mtproto_status='healthy', mtproto_installed_at=NOW()
      on fail: UPDATE servers SET mtproto_status='unhealthy'; log error
   biz.BumpSubscriptionVersion()
```

### Backfill via Ghost Reconciler
```
every 30 min:
  ... existing checks ...
  exits_needing_mtproto = SELECT * FROM servers
    WHERE is_exit_node AND lifecycle_state='online' AND mtproto_installed_at IS NULL
  if len > 0 AND app_settings.mtproto_auto_install='true':
    for each: spawn same install routine (idempotent — script handles "already installed")
```

### Health-check
```
every 30s in StartTunnelHealthMonitor:
  for each exit with mtproto_installed_at IS NOT NULL:
    ok := tcp_dial(exit.ip, mtproto_port, 3s)
    if ok:
      fail_count[exit.id] = 0; success_count[exit.id]++
      if success_count >= 2 AND mtproto_status != 'healthy':
        MarkMTProtoStatus(id, 'healthy')
    else:
      fail_count[exit.id]++; success_count[exit.id] = 0
      if fail_count >= 3 AND mtproto_status != 'unhealthy':
        MarkMTProtoStatus(id, 'unhealthy')
    UPDATE servers SET mtproto_last_check=NOW() WHERE id=$1
```

## Migration

```sql
-- 000042_mtproto_per_exit.up.sql
ALTER TABLE servers
  ADD COLUMN IF NOT EXISTS mtproto_status TEXT NOT NULL DEFAULT 'unknown'
    CHECK (mtproto_status IN ('unknown', 'installing', 'healthy', 'unhealthy')),
  ADD COLUMN IF NOT EXISTS mtproto_last_check TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS mtproto_installed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_servers_mtproto_health
  ON servers(mtproto_status) WHERE is_exit_node = true;

-- New global setting: gate for auto-install via Ghost Reconciler
INSERT INTO app_settings (key, value) VALUES ('mtproto_auto_install', 'true')
  ON CONFLICT (key) DO NOTHING;

-- Drop now-obsolete per-host setting (was static, replaced by per-exit)
DELETE FROM app_settings WHERE key = 'mtproto_host';
```

### Down migration
```sql
DELETE FROM app_settings WHERE key = 'mtproto_auto_install';
DROP INDEX IF EXISTS idx_servers_mtproto_health;
ALTER TABLE servers
  DROP COLUMN IF EXISTS mtproto_installed_at,
  DROP COLUMN IF EXISTS mtproto_last_check,
  DROP COLUMN IF EXISTS mtproto_status;
INSERT INTO app_settings (key, value) VALUES ('mtproto_host', '') ON CONFLICT DO NOTHING;
```

## Error handling

| Stage | Failure | Behavior |
|---|---|---|
| Step 8 `install_mtproxy` during runSetupExitNode | SSH/build error | Step marked 'error', `mtproto_status='unhealthy'`. Exit-node still becomes online — MTProto is bonus, not blocker. Admin sees red badge + can retry via Reinstall button. |
| `PickMTProtoExit()` | No healthy exits | Endpoint returns `{enabled:false, reason:"Временно недоступно"}`. Site hides Telegram block gracefully. |
| Ghost Reconciler install attempt fails | SSH timeout / build error | Log error, set `mtproto_status='unhealthy'`, do NOT loop-retry — wait for next 30-min cycle. |
| TCP probe timeout | Network blip | Hysteresis (3 fails) prevents flapping. |
| Secret rotation | Old daemons still serve old secret | Admin regenerates secret → frontend gets new deeplink → users with old TG-configured proxy retry → fail → re-fetch. Within ~30 sec of cache TTL. Reconciler ensures new secret reaches each daemon via reinstall on next pass. |

## Risk register

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | Auto-install during reconciler hammers exit-VPS | Rate-limit: max 1 install per reconciler tick (30 min). |
| R-02 | install-mtproxy.sh not idempotent if rerun | Script already uses `[[ -f $BIN ]]` guard for binary, generates secret only if `secret` file missing. Add: if 2nd arg passed, ALWAYS overwrite the local secret file → next restart picks it up. |
| R-03 | mtproto-proxy daemon crashes silently | systemd Restart=on-failure + RestartSec=5. Health-check catches sustained outage. |
| R-04 | Shared secret leak via admin UI | Admin UI shows secret in form — already accepted risk in B+C. Only RBAC admins can read settings. |
| R-05 | Telegram blocks shared secret across many exits | Secret rotation flow already supported (admin click → reconciler propagates within 30 min). |
| R-06 | Exit-node firewall not opening :8443 | install-mtproxy.sh already runs `ufw allow 8443/tcp || true`. Verify in step 8 outcome. |
| R-07 | One unhealthy exit drags balancer | Pool filters by `mtproto_status='healthy'`, so the bad exit is auto-excluded. |
| R-08 | `mtproto_port` change requires re-running script on each exit | Acceptable — port changes are rare. Admin clicks "Reinstall all" after editing port. |

## Verification (evidence-driven)

After deploy + first reconciler tick:

```bash
# 1. Migration applied
psql heyearth -c "\d servers" | grep mtproto
# → mtproto_status, mtproto_last_check, mtproto_installed_at present

# 2. Auto-install setting on
psql heyearth -c "SELECT value FROM app_settings WHERE key='mtproto_auto_install';"
# → true

# 3. Reconciler scheduled install for Amsterdam
journalctl -u heyearth-api --since "5 minutes ago" | grep ghost-reconciler | grep mtproto

# 4. After ~5-10 min wait, Amsterdam has daemon running
ssh root@89.124.89.126 'systemctl is-active mtproxy && ss -tln | grep :8443'
# → active + LISTEN

# 5. Server row reflects install
psql heyearth -c "SELECT id, name, mtproto_status, mtproto_installed_at FROM servers WHERE is_exit_node;"
# → status=healthy, installed_at NOT NULL

# 6. Endpoint serves deeplink with REAL exit host
curl -H "Authorization: Bearer <user-jwt>" https://hiearth.ru/api/v1/user/mtproto | python3 -m json.tool
# → enabled=true, host=89.124.89.126, port=8443, secret=<shared>

# 7. Telegram smoke (manual)
# Open tg://... on phone → confirm proxy → traffic flows
```

## Rollback

- **Backend revert**: `git revert <merge>` + redeploy. Migration 000042 columns can stay (NULL-safe). Down migration available.
- **Disable everywhere**: admin sets `mtproto_enabled=false`. Endpoint returns `enabled:false`. Users see no button.
- **Disable auto-install only**: admin sets `mtproto_auto_install=false`. Existing daemons keep running, reconciler stops adding to new exits.
- **Per-exit removal**: admin clicks Reinstall? No — needs a separate "Uninstall MTProto" action. Out of scope. Workaround: SSH manual `systemctl stop mtproxy && systemctl disable mtproxy`.

## Out of scope reminders
- Sub-project D (per-plan rate-limit)
- Per-server secret rotation
- Geo-aware exit selection (use existing weighted random)
- mtproto-proxy stats ingestion
- Flutter app changes

## Deliverable

This design feeds `superpowers:writing-plans` to produce ~8-10 task plan. Beads epic + sub-tasks at planning step.
