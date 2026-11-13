---
id: hub-spoke-fix
beads_id: heyearthproject-rqn
prd: ../prd/hub-spoke-fix.prd.md
status: approved
created: 2026-04-06
---

# Hub-and-Spoke Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix both exit nodes to serve traffic, fix health monitor bugs, expose diagnostic info in admin UI.

**Architecture:** Health monitor always updates `last_tunnel_check` (current bug: only updates for healthy). New `tunnel_error` column stores probe failure reason. Admin API and UI expose this. Latvia re-provisioned with new stack. Existing `setup_exit_node` action used for re-provisioning (already idempotent).

**Tech Stack:** Go backend (raw SQL, database/sql), PostgreSQL migrations, React 19 (JSX, Tailwind v4)

**Verification approach:** No automated tests. Every task verified by `go build ./...`, `curl`, `deploy --status`, and `journalctl` evidence.

---

## File Structure

### Create
- `server/backend/migrations/000023_tunnel_error.up.sql`
- `server/backend/migrations/000023_tunnel_error.down.sql`

### Modify
- `server/backend/cmd/api/xray_sync.go` — fix `last_tunnel_check` bug + pass error msg from `checkTunnelHealth`
- `server/backend/internal/biz/server.go` — add `UpdateTunnelStatusWithError()`, add `TunnelError` to struct + query
- `server/backend/internal/admin/types.go` — add `TunnelError` to `serverResponse`
- `server/backend/internal/admin/servers_handlers.go` — include `tunnel_error` in `loadServerByID` SELECT + Scan
- `admin_web/src/pages/ServersPage.jsx` — show `tunnel_error` on cards/rows
- `admin_web/src/i18n/ru.js` — add "Причина:" label
- `admin_web/src/i18n/en.js` — add "Reason:" label

---

### Task 1: Fix `last_tunnel_check` always-update bug

**Files:**
- Modify: `server/backend/cmd/api/xray_sync.go:785-788`

- [ ] **Step 1: Identify the bug**

Line 785 has `else if newStatus == "healthy"` — this means `last_tunnel_check` is NOT updated when a node is `unhealthy` and stays `unhealthy`. The check timestamp stagnates.

- [ ] **Step 2: Fix the condition**

Change:
```go
		} else if newStatus == "healthy" {
			// Обновляем last_tunnel_check и latency даже без смены статуса
			_ = biz.UpdateTunnelStatusWithLatency(node.ID, newStatus, latencyMs)
		}
```

To:
```go
		} else {
			// Обновляем last_tunnel_check и latency при КАЖДОЙ проверке, независимо от статуса
			_ = biz.UpdateTunnelStatusWithLatency(node.ID, newStatus, latencyMs)
		}
```

- [ ] **Step 3: Verify compilation**

Run: `cd server/backend && go build ./...`
Expected: no output (success)

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/xray_sync.go
git commit -m "fix: always update last_tunnel_check regardless of tunnel status"
```

---

### Task 2: Add `tunnel_error` column (migration 000023)

**Files:**
- Create: `server/backend/migrations/000023_tunnel_error.up.sql`
- Create: `server/backend/migrations/000023_tunnel_error.down.sql`

- [ ] **Step 1: Create up migration**

```sql
-- Stores the human-readable reason for unhealthy tunnel status
ALTER TABLE servers ADD COLUMN IF NOT EXISTS tunnel_error TEXT NOT NULL DEFAULT '';
```

- [ ] **Step 2: Create down migration**

```sql
ALTER TABLE servers DROP COLUMN IF EXISTS tunnel_error;
```

- [ ] **Step 3: Commit**

```bash
git add server/backend/migrations/000023_tunnel_error.*
git commit -m "feat: add servers.tunnel_error column for diagnostic info"
```

---

### Task 3: Populate `tunnel_error` from health monitor

**Files:**
- Modify: `server/backend/internal/biz/server.go` — add `UpdateTunnelStatusWithError()`
- Modify: `server/backend/cmd/api/xray_sync.go` — change `checkTunnelHealth` signature to return error message, pass it through

- [ ] **Step 1: Add `UpdateTunnelStatusWithError` in biz/server.go**

After existing `UpdateTunnelStatusWithLatency`:

```go
func UpdateTunnelStatusWithError(serverID int, status string, latencyMs int, tunnelError string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		SET tunnel_status = $1, latency_ms = $2, tunnel_error = $3,
		    last_tunnel_check = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, status, latencyMs, tunnelError, serverID)
	return err
}
```

- [ ] **Step 2: Change `checkTunnelHealth` signature**

In `xray_sync.go`, change:
```go
func checkTunnelHealth(node biz.ExitNodeTunnel) (string, int) {
```
To:
```go
func checkTunnelHealth(node biz.ExitNodeTunnel) (status string, latencyMs int, errMsg string) {
```

Update each `return "unhealthy", 0` to include a specific message:
- VLESS TLS failure: `return "unhealthy", 0, fmt.Sprintf("TLS handshake to %s:%d failed", node.IPAddress, node.TunnelPort)`
- TCP connect failure: `return "unhealthy", 0, fmt.Sprintf("%s port %d: connection refused or timeout", node.TunnelProtocol, node.TunnelPort)`
- Hysteria2 local SOCKS: `return "unhealthy", 0, fmt.Sprintf("hysteria2 local SOCKS port %d not responding", hy2Port)`
- Success: `return "healthy", latencyMs, ""`

- [ ] **Step 3: Update `checkAllTunnels` callers**

Change:
```go
probeResult, latencyMs := checkTunnelHealth(node)
```
To:
```go
probeResult, latencyMs, probeError := checkTunnelHealth(node)
```

Replace all `biz.UpdateTunnelStatusWithLatency(node.ID, ...)` calls with:
```go
biz.UpdateTunnelStatusWithError(node.ID, newStatus, latencyMs, probeError)
```

(Keep `UpdateTunnelStatusWithLatency` for backward compat — it's used in `syncMetrics` and other places.)

- [ ] **Step 4: Verify compilation**

Run: `cd server/backend && go build ./...`
Expected: no output (success)

- [ ] **Step 5: Commit**

```bash
git add server/backend/internal/biz/server.go server/backend/cmd/api/xray_sync.go
git commit -m "feat: health monitor records tunnel_error with probe failure reason"
```

---

### Task 4: Expose `tunnel_error` in admin API

**Files:**
- Modify: `server/backend/internal/admin/types.go` — add field
- Modify: `server/backend/internal/admin/servers_handlers.go` — include in SELECT/Scan

- [ ] **Step 1: Add `TunnelError` to serverResponse**

In `types.go`, after `TunnelSSPass`:
```go
TunnelError    string `json:"tunnel_error,omitempty"`
```

- [ ] **Step 2: Include in `loadServerByID` queries**

In `servers_handlers.go`, add `COALESCE(s.tunnel_error, '')` to both SELECT branches of `loadServerByID` and corresponding Scan.

Also add to `GetServersHandler` SELECT for list view (so cards show error too).

- [ ] **Step 3: Include in `sshToServer` query (optional but consistent)**

No — sshToServer doesn't need tunnel_error. Skip.

- [ ] **Step 4: Verify compilation**

Run: `cd server/backend && go build ./...`
Expected: no output

- [ ] **Step 5: Verify API returns new field**

After deploy: `curl -s https://hiearth.ru/api/admin/servers/1 -H "Authorization: Bearer <token>" | jq .tunnel_error`
Expected: `"shadowsocks port 8388: connection refused or timeout"` (or similar)

- [ ] **Step 6: Commit**

```bash
git add server/backend/internal/admin/types.go server/backend/internal/admin/servers_handlers.go
git commit -m "feat: expose tunnel_error in admin API server response"
```

---

### Task 5: Show `tunnel_error` in admin UI

**Files:**
- Modify: `admin_web/src/pages/ServersPage.jsx`
- Modify: `admin_web/src/i18n/ru.js`
- Modify: `admin_web/src/i18n/en.js`

- [ ] **Step 1: Add i18n strings**

In `ru.js` add: `serverTunnelError: 'Причина'`
In `en.js` add: `serverTunnelError: 'Reason'`

- [ ] **Step 2: Show tunnel_error in ServerCard**

In `ServersPage.jsx`, after `<TunnelBadge>` section, add:
```jsx
{srv.tunnel_status === 'unhealthy' && srv.tunnel_error && (
  <p className="text-xs text-red-400 mt-1">{t.serverTunnelError}: {srv.tunnel_error}</p>
)}
```

- [ ] **Step 3: Show tunnel_error in ServerRow**

In the table row, after `<TunnelBadge>`:
```jsx
{srv.tunnel_error && srv.tunnel_status === 'unhealthy' && (
  <span className="text-xs text-red-400 ml-2" title={srv.tunnel_error}>
    {srv.tunnel_error.length > 30 ? srv.tunnel_error.substring(0, 30) + '...' : srv.tunnel_error}
  </span>
)}
```

- [ ] **Step 4: Verify frontend builds**

Run: `cd admin_web && npm run build`
Expected: success

- [ ] **Step 5: Commit**

```bash
git add admin_web/src/pages/ServersPage.jsx admin_web/src/i18n/ru.js admin_web/src/i18n/en.js
git commit -m "feat: show tunnel_error reason in admin server cards"
```

---

### Task 6: Deploy backend + frontend changes

- [ ] **Step 1: Build and deploy backend**

```bash
cd server/backend
go run quick_deploy.go
```

Expected: "Деплой завершён!" with health check OK

- [ ] **Step 2: Verify migration applied**

```bash
go run ./cmd/deploy --ssh 'source /opt/heyearth/mainserver/api.env && psql "$DATABASE_URL" -c "SELECT column_name FROM information_schema.columns WHERE table_name='\''servers'\'' AND column_name='\''tunnel_error'\'';"'
```

Expected: `tunnel_error` row visible

- [ ] **Step 3: Verify last_tunnel_check updates for Latvia**

Wait 60 seconds (2 health-check cycles), then:
```bash
go run ./cmd/deploy --ssh 'source /opt/heyearth/mainserver/api.env && psql "$DATABASE_URL" -c "SELECT id, name, tunnel_status, tunnel_error, last_tunnel_check FROM servers WHERE is_exit_node=true;"'
```

Expected: Latvia's `last_tunnel_check` is recent (within last minute) and `tunnel_error` contains error text

- [ ] **Step 4: Deploy admin_web**

```bash
go run ./cmd/deploy --admin-web
```

Or rebuild locally + deploy:
```bash
cd admin_web && npm run build && cd ../server/backend && go run ./cmd/deploy --admin-web
```

- [ ] **Step 5: Commit deploy marker (optional)**

Not needed — code was already committed in tasks 1-5.

---

### Task 7: Re-provision Latvia exit node

**Prerequisites:** Tasks 1-6 deployed and working.

- [ ] **Step 1: Backup Latvia's current state**

```bash
go run ./cmd/deploy --ssh 'ssh -o StrictHostKeyChecking=no root@192.0.2.172 "tar -czf /root/latvia-backup-$(date +%F).tgz /opt/heyearth /etc/systemd/system/heyearth-* 2>/dev/null || true; echo BACKUP_DONE"'
```

OR use admin UI: Servers → Latvia → action "Setup exit node" with confirmation dialog.

- [ ] **Step 2: Run provisioning**

**Option A — from admin UI:**
In admin panel → Servers → Latvia card → click "Setup exit node" button → confirm → watch progress dialog

**Option B — from CLI:**
```bash
cd server/backend
go run ./cmd/deploy --setup-exit-node 192.0.2.172:22 --exit-node-password <PASSWORD_FROM_DB>
```

Wait 3-5 minutes for completion.

- [ ] **Step 3: Verify Latvia is now healthy**

Wait 2 minutes for health monitor to cycle:
```bash
go run ./cmd/deploy --ssh 'source /opt/heyearth/mainserver/api.env && psql "$DATABASE_URL" -c "SELECT id, name, tunnel_status, tunnel_error, tunnel_protocol, tunnel_port, last_tunnel_check FROM servers WHERE is_exit_node=true;"'
```

Expected: Both nodes `healthy`, Latvia's tunnel_protocol updated to `hysteria2`, tunnel_port updated (50443 or 443), tunnel_error = ''

- [ ] **Step 4: Verify Xray config has 2 exit nodes**

```bash
go run ./cmd/deploy --ssh 'journalctl -u heyearth-api -n 5 --no-pager | grep "exit-нод"'
```

Expected: `2 exit-нод` in the xray-sync log line

- [ ] **Step 5: Verify Xray config JSON has both outbounds**

```bash
go run ./cmd/deploy --ssh 'python3 -c "import json; c=json.load(open('\''/opt/heyearth/xray/config.json'\'')); exits=[o for o in c['\''outbounds'\''] if o['\''tag'\''].startswith('\''exit-'\'')]; print(f'\''Exit outbounds: {len(exits)}'\''); [print(f'\''  {o[\"tag\"]}: {o[\"protocol\"]}'\'') for o in exits]"'
```

Expected:
```
Exit outbounds: 2
  exit-1: shadowsocks (or hysteria2)
  exit-2: socks (hysteria2 local client)
```

---

### Task 8: Final end-to-end verification

- [ ] **Step 1: Full status check**

```bash
go run ./cmd/deploy --status
```

Expected: Both API and Xray active, 2 exit nodes in config, health checks recent

- [ ] **Step 2: Balancer selector check**

```bash
go run ./cmd/deploy --ssh 'python3 -c "import json; c=json.load(open('\''/opt/heyearth/xray/config.json'\'')); r=c['\''routing'\'']; b=r.get('\''balancers'\'',[]); [print(f'\''{bal[\"tag\"]}: {len(bal[\"selector\"])} tags, strategy={bal[\"strategy\"]}'\'') for bal in b]"'
```

Expected: `exit-balancer: 2+ tags, strategy={"type": "random"}`

- [ ] **Step 3: Admin UI shows both nodes healthy**

Open `https://admin.hiearth.ru/servers` in browser.
Expected: 2 server cards, both with green "healthy" badge, no `tunnel_error` text, fresh `last_tunnel_check`.

- [ ] **Step 4: Close beads epic**

```bash
bd close heyearthproject-rqn --reason "Both exit nodes healthy, balancer distributes traffic, tunnel_error exposed in UI"
```

---

## Rollback Plan

If Latvia re-provisioning breaks the server:
1. SSH to Latvia: `ssh root@192.0.2.172`
2. Restore backup: `cd / && tar -xzf /root/latvia-backup-*.tgz`
3. Restart services: `systemctl restart xray hysteria2 node-agent 2>/dev/null || true`
4. In DB: `UPDATE servers SET tunnel_protocol='shadowsocks', tunnel_port=8388 WHERE id=1`

If code changes break admin API:
1. Revert to previous binary: use git + redeploy
2. Migration 000023 is backward-compatible (ADD COLUMN) — no rollback needed

## Deployment Notes

- Migration 000023 applies automatically on API restart
- Backend deploy via `quick_deploy.go` (stop → upload → restart)
- Frontend deploy via `deploy --admin-web` (npm build locally → SFTP upload to /var/www/heyearth/)
- Latvia re-provisioning via admin UI button "Setup exit node" (preferred) or CLI `deploy --setup-exit-node`
