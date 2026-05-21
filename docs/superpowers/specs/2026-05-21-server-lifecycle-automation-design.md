---
title: Server Lifecycle Automation (sub-project A)
status: design
created: 2026-05-21
parent_initiative: "Turn P1 lessons into permanent features (A → B → D → C)"
beads_epic: TBD (will be created at writing-plans step)
prior_incident: hysteria2-client-1.service (Latvia ghost) ran 45 days against a dead host
---

# Server Lifecycle Automation

## Problem

When an admin removes/disables an exit-server in the panel, hub-side state (systemd unit, hysteria2 client config, running process, Xray balancer entry) keeps living and accumulating damage. Real incident (May 2026): `hysteria2-client-1.service` for Latvia kept running 45 days after the server was marked offline in the DB, retrying a dead host and contributing to wave-pattern N/A for ~100 clients.

This is the **foundation** for the broader "harden the system" initiative — once server CRUD has a real lifecycle with provisioning + de-provisioning automation, the other sub-projects (SOCKS5 removal, MTProto perk, per-plan rate-limit) can rely on it instead of asking admins to do manual cleanup.

## Scope

### In scope
* DB schema: lifecycle_state column on `servers` table + `app_settings.subscription_version`
* Backend lifecycle FSM: `pending → provisioning → online | failed → deprovisioning → offline | deleted`
* Hub-side provisioning + de-provisioning via local `os/exec` (heyearth-api runs ON the hub)
* Exit-side teardown — best-effort SSH with 10-sec timeout, warning on failure
* Naming convention: ALL exit tunnels = `hysteria2-client-<server_id>.service` (legacy `hysteria2-client.service` renamed)
* Background "Ghost Reconciler" goroutine — scans hub for orphan systemd units vs DB rows every 30 min
* Admin UI changes: lifecycle badge, 4 buttons (Provision / Disable / Delete / Re-Provision), live progress modal, confirm dialog with type-to-confirm for Delete
* User-side: subscription_version bump on every lifecycle transition + ETag header + site polling + toast on change
* Migration script for legacy `hysteria2-client.service` → `hysteria2-client-<id>.service`
* Evidence-driven verification (no automated tests in this repo)

### Out of scope (separate cycles)
* Flutter app — Flutter changes for subscription_version polling go to a later cycle
* SOCKS5 removal (sub-project B)
* MTProto integration (sub-project C)
* Per-plan rate-limit (sub-project D)
* Automatic blacklisting of repeatedly-failed exit-nodes
* GUI for browsing archived servers (CLI access via ServerFileBrowserHandler is enough for now)

## Architecture

### Lifecycle states

```
                   ┌─────────┐
                   │ pending │  ← admin creates row (no hub-side action yet)
                   └────┬────┘
                        │  admin clicks "Provision"
                        ▼
                  ┌─────────────┐
                  │ provisioning│  ← async, progress streamed via SSE
                  └──────┬──────┘
                         │
                ┌────────┴──────┐
                ▼               ▼
            ┌────────┐      ┌────────┐
            │ online │      │ failed │  ← retry via Re-Provision or Delete
            └────┬───┘      └────────┘
                 │ admin clicks "Disable" / "Delete"
                 ▼
            ┌──────────────┐
            │deprovisioning│  ← async, progress
            └──────┬───────┘
                   │
            ┌──────┴───────┐
            ▼              ▼
       ┌─────────┐    ┌─────────┐
       │ offline │    │ deleted │  ← row gone, full backup in archive
       │ (in DB) │    │ (no row)│
       └─────────┘    └─────────┘
```

### Hub-side teardown (guaranteed)
1. `RequestXraySync()` FIRST — remove from balancer so no new sessions route through it
2. Wait 3 sec (drain in-flight tunneling)
3. `systemctl stop hysteria2-client-<id>`
4. Archive `client-<id>.yaml` + `.service` + last 100 journal lines → `/opt/heyearth/state/server-archive/<id>-<ts>/`
5. `systemctl disable` + RM .service file + `daemon-reload`

### Exit-side teardown (best-effort, 10s timeout)
- SSH → `systemctl stop xray hysteria2-server node-agent`
- If unreachable or timeout → warning in log, do not block hub cleanup

### Naming convention
- Pattern: `hysteria2-client-<server_id>.service` + `/opt/heyearth/hysteria/client-<server_id>.yaml`
- Pattern already used by `cmd/deploy/main.go:2101` for new exits
- One-time migration of legacy `hysteria2-client.service` (Amsterdam, id=2) → `hysteria2-client-2.service`

### Safety net — Ghost Reconciler
- Background goroutine in `cmd/api/main.go`, runs every 30 min
- Lists `/etc/systemd/system/hysteria2-client-*.service` files → extracts IDs
- Cross-references with `servers` table (lifecycle_state)
- Reports:
  - **Orphan**: service file present but row gone or lifecycle ∈ {offline, deleted} → WARN, optionally auto-cleanup (toggle via `app_settings.ghost_auto_cleanup`)
  - **Missing**: row lifecycle=online but no service file → WARN, optionally auto-repair (`app_settings.ghost_auto_repair`)
- Default: auto-cleanup ON, auto-repair OFF (auto-repair would re-run provisioning which is heavier — better to alert admin)

### User-side propagation
- `app_settings.subscription_version` integer, bumped by `biz.BumpSubscriptionVersion()` on every lifecycle transition
- `GET /api/v1/me/connections` adds `X-Subscription-Version: N` response header + same field in JSON body
- `site/src/pages/DashboardPage.jsx` polls every 30 sec via React Query; compares to last-stored value; on mismatch → toast "Подписка обновлена администратором" + auto-refresh subscription URI
- Flutter changes deferred to separate cycle (out of scope here)

## Components

### Backend (new files)
- `internal/biz/server_lifecycle.go` — FSM, `Provision()`, `Deprovision(mode)`, `TransitionState()`, `ProvisionEvent` type
- `internal/biz/hub_provisioner.go` — local os/exec ops: write config, install systemd, start/stop/remove, archive
- `internal/biz/exit_teardown.go` — best-effort SSH teardown with timeout
- `cmd/api/ghost_reconciler.go` — 30-min scan loop, alert + optional auto-cleanup
- `migrations/000040_server_lifecycle.up.sql` — schema changes + backfill

### Backend (modified)
- `internal/admin/servers_handlers.go`:
  - `DeleteServerHandler` — now calls `biz.Deprovision(id, "hard")` async, on success does `DELETE FROM servers`
  - `ServerActionHandler` — new actions `deprovision_soft` / `deprovision_hard`
  - Streaming progress reuses existing `ServerActionProgressHandler`
- `cmd/api/main.go` — register `StartGhostReconciler()`
- `internal/biz/server.go` — extend `ListHealthyExitNodes` to filter by `lifecycle_state='online'` (defensive)

### Deploy-time
- `cmd/deploy/migrate-hysteria-naming.sh` — one-time on hub: rename legacy `hysteria2-client.service` to `hysteria2-client-2.service` (Amsterdam), detect via existing config

### Frontend admin (`admin_web/`)
- `pages/ServerDetailPage.tsx` — lifecycle badge, 4 action buttons, type-to-confirm Delete dialog, live progress modal (reuse existing for setup-exit-node)
- `pages/ServersPage.tsx` — Lifecycle column with colored badge

### Frontend site (`site/`)
- `pages/DashboardPage.jsx` — "Available locations" widget, React-Query polling, toast on subscription_version change
- `api/user.js` — connections helper with ETag handling

### Public API
- `GET /api/v1/me/connections` — add `subscription_version` to JSON + `X-Subscription-Version` header

## Data flow — provisioning

```
Admin → POST /api/admin/servers/<id>/actions { action:"provision" }
  → ServerActionHandler → biz.Provision(id, progressCh)
    → TransitionState(pending → provisioning)  [atomic]
    → SSH exit-VPS: deploy --setup-exit-node
    → write client-<id>.yaml on hub
    → install + daemon-reload + enable + start hysteria2-client-<id>
    → biz.BumpSubscriptionVersion()
    → RequestXraySync()  [add to balancer]
    → TransitionState(provisioning → online)
    → progress event {status:"done"}
```

## Data flow — deprovisioning (hard)

```
Admin → confirm dialog → DELETE /api/admin/servers/<id>
  → DeleteServerHandler → biz.Deprovision(id, "hard", progressCh)
    → TransitionState(online → deprovisioning)
    → RequestXraySync()  [remove from balancer FIRST]
    → wait 3 sec
    → systemctl stop hysteria2-client-<id>
    → archive .yaml + .service + journal-snapshot
    → systemctl disable + rm .service + daemon-reload
    → SSH exit-VPS teardown (10s timeout, warn-only)
    → biz.BumpSubscriptionVersion()
    → DELETE FROM servers WHERE id=$1
    → progress event {status:"done"}
```

## Data flow — Ghost Reconciler (every 30 min)

```
ls /etc/systemd/system/hysteria2-client-*.service → IDs from filenames
  ▼
SELECT id, lifecycle_state FROM servers WHERE is_exit_node=true
  ▼
diff:
  orphan unit (file exists, row missing or lifecycle ∈ offline/deleted):
    WARN log + (if ghost_auto_cleanup=true) auto-deprovision hub-side
  missing unit (row says online, no file):
    WARN log + (if ghost_auto_repair=true) auto-provision hub-side
```

## Error handling

| Stage | Failure | Behavior |
|---|---|---|
| `TransitionState(from→to)` | Concurrent operation | Atomic SQL guards via `WHERE lifecycle_state=$from`; if RowsAffected=0 → `ErrInvalidTransition`; idempotent. |
| Provision: SSH exit-VPS | Timeout / unreachable | Provision fails → `TransitionState(provisioning→failed)`; UI shows red badge + Retry; hub-side untouched (no ghosts). |
| Provision/Deprovision: local os/exec | Permission / FS errors | Hard fail with rollback of partial steps. If archive step fails — do NOT delete originals. |
| Deprovision: RequestXraySync | Xray config gen failed | Log + alert. Continue with hub-side cleanup. Reconciler catches the desync. |
| Deprovision: SSH exit-VPS | Unreachable | Warning logged, continue hub-side. |
| Deprovision: hard DELETE | FK violation | `ON DELETE CASCADE` on junction tables; explicit cascade in `plan_servers`. |
| BumpSubscriptionVersion | DB error | Non-blocking; retries on next transition. |

## Rollback

- **Provision failed** → state goes to `failed`. Admin retries via "Re-Provision" or removes row via Delete (Delete in `failed` state is hub-side no-op since nothing was created).
- **Deprovision failed mid-flight** (rare) → archive done, stop failed. We try `kill -9` then RM. Manual recovery possible via SSH to hub if needed (the archive directory holds the rollback artifacts).

## Idempotency

Every hub-side operation checks state before acting:
```go
if exec.Command("systemctl", "is-active", "--quiet", unitName).Run() == nil {
    // already active, skip
}
```
Retry of an interrupted flow is safe.

## Verification (evidence-driven)

### After provision (id=N)
```bash
psql heyearth -c "SELECT lifecycle_state FROM servers WHERE id=N;"           # → online
systemctl status hysteria2-client-N                                          # → active
ls /opt/heyearth/hysteria/client-N.yaml                                      # exists
grep -c "exit-N" /opt/heyearth/xray/config.json                              # ≥ 1
psql -c "SELECT value FROM app_settings WHERE key='subscription_version';"   # N+1
```

### After deprovision hard (id=N)
```bash
psql heyearth -c "SELECT count(*) FROM servers WHERE id=N;"                  # 0
systemctl status hysteria2-client-N                                          # "could not be found"
ls /opt/heyearth/hysteria/client-N.yaml                                      # No such file
ls /opt/heyearth/state/server-archive/N-*/                                   # has artefacts
grep -c "exit-N" /opt/heyearth/xray/config.json                              # 0
ps -ef | grep "client-N" | grep -v grep                                      # empty
```

### Ghost Reconciler
```bash
journalctl -u heyearth-api --since "1 hour ago" | grep ghost-reconciler
# → "[ghost-reconciler] scan: N units, N rows, 0 orphans, 0 missing"
```

### User-side
```bash
curl -H "Authorization: Bearer <user-jwt>" https://hiearth.ru/api/v1/me/connections -I
# → X-Subscription-Version: N+1
```
Site DashboardPage shows toast within 30 sec polling cycle.

## Migration plan for legacy ghost

After deploy, run once on hub:
```bash
bash /opt/heyearth/scripts/migrate-hysteria-naming.sh
# Stops hysteria2-client.service if exists
# Identifies which exit-id its config points to
# Renames to hysteria2-client-<id>.service
# Updates lifecycle_state to 'online' (idempotent backfill)
```

## Risk register

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | Hub-local `os/exec` requires root | heyearth-api already runs as root for systemd ops; verify on deploy |
| R-02 | DELETE FROM servers with active sessions | `RequestXraySync` first → balancer no longer routes → drain 3 sec → safe |
| R-03 | Concurrent provision attempts | Atomic state transitions; UI also disables button when in_progress |
| R-04 | Archive directory grows unbounded | Cleanup older than 90 days via app_settings.server_archive_retention_days |
| R-05 | Ghost Reconciler auto-cleanup wrong unit | `lifecycle_state` check is the gate; default auto-repair OFF (admin alert only) |
| R-06 | Migration script breaks current Amsterdam tunnel | Script is idempotent + has dry-run mode; backup before rename |
| R-07 | subscription_version overflow | INT4 = 2.1B headroom; not realistic concern |

## Out-of-scope reminders

- **Flutter** — separate cycle, polling pattern same as site
- **SOCKS5 / MTProto / rate-limit** — separate sub-projects B/D/C
- **Multi-region admin UI** — current admin can manage all servers from single panel; no change

## Deliverable

This design is the input to `superpowers:writing-plans` which will produce an implementation plan in `docs/superpowers/plans/`. Implementation plan will be broken into 8-12 tasks of 15-45 min each, with evidence commands per task. Beads epic + sub-tasks will be created at planning step.
