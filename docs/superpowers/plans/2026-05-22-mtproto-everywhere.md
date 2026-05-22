# MTProto Everywhere — auto-deploy + balancer — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make MTProto-proxy auto-install on every exit-node (existing + future), health-checked, and serve `/api/v1/user/mtproto` from a weighted balancer of healthy exits.

**Architecture:** Per-server `mtproto_status` columns in `servers` table; shared secret in `app_settings.mtproto_secret`. Step 8 `install_mtproxy` added to existing `runSetupExitNode` async flow. Ghost Reconciler 30-min loop backfills exits where `mtproto_installed_at IS NULL`. TCP probe :8443 piggybacks on `StartTunnelHealthMonitor` with 3/2 hysteresis. New `biz.PickMTProtoExit()` does weighted-random over `load_weight` among healthy exits. `install-mtproxy.sh` extended to accept optional secret argument (idempotent).

**Tech Stack:** Go 1.21+, PostgreSQL 14+, systemd, TelegramMessenger/MTProxy binary, React 19 (JSX), Tailwind v4.

**Spec:** `docs/superpowers/specs/2026-05-22-mtproto-everywhere-design.md`

**Beads epic:** to be created in Task 0.

**Production:** hub 45.150.39.236, exit Amsterdam id=2 (89.124.89.126). Shared secret `26c834d4b1b097dfde254de8614047d7` already in DB (`app_settings.mtproto_secret`).

**Deploy:** `cd server/backend && go run quick_deploy.go` for binary+migrations, `go run ./cmd/deploy --site --admin-web` for frontend, `go run ./cmd/deploy --ssh '...'` for hub commands.

**Verification:** Evidence-driven (no automated tests). Each task ends with SSH/SQL.

---

## File Structure

### Create
* `server/backend/migrations/000042_mtproto_per_exit.up.sql`
* `server/backend/migrations/000042_mtproto_per_exit.down.sql`
* `server/backend/internal/biz/mtproto.go` — `PickMTProtoExit`, `ListExitsNeedingMTProto`, `MarkMTProtoStatus`, `InstallMTProtoOnExit` (SSH helper)

### Modify
* `server/backend/cmd/deploy/install-mtproxy.sh` — accept optional `$2` SECRET
* `server/backend/internal/admin/servers_handlers.go` — extend `runSetupExitNode` with Step 8; add `reinstall_mtproxy` action; helper `runReinstallMTProto`
* `server/backend/cmd/api/ghost_reconciler.go` — add `mtproto_missing` reconciliation
* `server/backend/cmd/api/xray_sync.go` — extend `StartTunnelHealthMonitor` with TCP probe :8443
* `server/backend/cmd/api/main.go` — update `handleUserMTProto` to use `biz.PickMTProtoExit`
* `admin_web/src/pages/MTProtoPage.jsx` — drop Host/Port, add auto_install toggle + Reinstall-all
* `admin_web/src/pages/ServerDetailPage.jsx` — MTProto status badge + Reinstall button
* `admin_web/src/pages/ServersPage.jsx` — mtproto_status badge next to TunnelBadge

---

## Task 0: Create Beads epic (5 min)

- [ ] **Step 1: Create epic**

```bash
cd C:\Users\alex\Documents\heyearthproject
bd create --title="MTProto auto-deploy on all exits + balancer (B.2)" \
  --description="Per-server mtproto_status; auto-install via runSetupExitNode + Ghost Reconciler; weighted balancer in PickMTProtoExit; TCP-probe health-check. Spec: docs/superpowers/specs/2026-05-22-mtproto-everywhere-design.md. Plan: docs/superpowers/plans/2026-05-22-mtproto-everywhere.md" \
  --type=epic --priority=1
```
Expected: prints new ID like `heyearthproject-XXXX`. Record as `EPIC_ID`.

- [ ] **Step 2: Link to closed B+C epic**

```bash
bd dep add <EPIC_ID> heyearthproject-i9fv --type=related
```

---

## Task 1: Migration 000042 — schema + auto-install setting (15 min)

**Files:**
- Create: `server/backend/migrations/000042_mtproto_per_exit.up.sql`
- Create: `server/backend/migrations/000042_mtproto_per_exit.down.sql`

- [ ] **Step 1: Write up migration**

File: `server/backend/migrations/000042_mtproto_per_exit.up.sql`

```sql
-- MTProto Everywhere (sub-project B.2)
-- Per-server health + install state, plus a global toggle for the
-- Ghost Reconciler auto-install behavior.

ALTER TABLE servers
  ADD COLUMN IF NOT EXISTS mtproto_status TEXT NOT NULL DEFAULT 'unknown'
    CHECK (mtproto_status IN ('unknown', 'installing', 'healthy', 'unhealthy')),
  ADD COLUMN IF NOT EXISTS mtproto_last_check TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS mtproto_installed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_servers_mtproto_health
  ON servers(mtproto_status) WHERE is_exit_node = true;

-- Toggle gating Ghost Reconciler auto-install for existing exits without daemon.
INSERT INTO app_settings (key, value) VALUES ('mtproto_auto_install', 'true')
  ON CONFLICT (key) DO NOTHING;

-- Drop now-obsolete static host (replaced by per-exit selection via balancer).
-- mtproto_port stays as a global default port (same on every exit).
DELETE FROM app_settings WHERE key = 'mtproto_host';
```

- [ ] **Step 2: Write down migration**

File: `server/backend/migrations/000042_mtproto_per_exit.down.sql`

```sql
DELETE FROM app_settings WHERE key = 'mtproto_auto_install';
DROP INDEX IF EXISTS idx_servers_mtproto_health;
ALTER TABLE servers
  DROP COLUMN IF EXISTS mtproto_installed_at,
  DROP COLUMN IF EXISTS mtproto_last_check,
  DROP COLUMN IF EXISTS mtproto_status;
INSERT INTO app_settings (key, value) VALUES ('mtproto_host', '') ON CONFLICT DO NOTHING;
```

- [ ] **Step 3: Build**

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 4: Commit**

```bash
git add server/backend/migrations/000042_mtproto_per_exit.up.sql \
        server/backend/migrations/000042_mtproto_per_exit.down.sql
git commit -m "feat(migration): mtproto per-exit columns + auto_install setting"
```

---

## Task 2: install-mtproxy.sh — accept optional SECRET arg (15 min)

**Files:**
- Modify: `server/backend/cmd/deploy/install-mtproxy.sh`

**Goal:** Backward-compatible — without a 2nd arg it generates a secret (existing behavior). With `$2` provided, it OVERWRITES the local secret file so the daemon serves the supplied shared secret.

- [ ] **Step 1: Inspect the script's secret-generation block**

```bash
grep -n "generating user secret\|head -c 16\|/opt/mtproxy/secret" server/backend/cmd/deploy/install-mtproxy.sh
```
Expected: find the `if [[ ! -f "$WORKDIR/secret" ]]` block.

- [ ] **Step 2: Replace the secret block with arg-aware version**

In `server/backend/cmd/deploy/install-mtproxy.sh`, locate the current block (around the "generating user secret" comment):

```bash
if [[ ! -f "$WORKDIR/secret" ]]; then
  echo "[install-mtproxy] generating user secret"
  head -c 16 /dev/urandom | xxd -ps > "$WORKDIR/secret"
fi
SECRET="$(cat "$WORKDIR/secret")"
```

Replace with:

```bash
if [[ -n "${2:-}" ]]; then
  echo "[install-mtproxy] using SECRET provided via arg"
  printf '%s\n' "$2" > "$WORKDIR/secret"
elif [[ ! -f "$WORKDIR/secret" ]]; then
  echo "[install-mtproxy] generating user secret"
  head -c 16 /dev/urandom | xxd -ps > "$WORKDIR/secret"
fi
SECRET="$(cat "$WORKDIR/secret" | tr -d '\n\r ')"
```

This:
1. Accepts `bash install-mtproxy.sh 8443 abc123...def456` — secret OVERWRITES local file.
2. Falls back to local file or fresh generation if no arg.
3. Strips whitespace/newlines from secret (defensive).

- [ ] **Step 3: Update final usage line**

Find at top of script:
```bash
# Run as root. Usage: bash install-mtproxy.sh [PORT]
```

Replace with:
```bash
# Run as root. Usage: bash install-mtproxy.sh [PORT] [SECRET]
#   PORT defaults to 8443. SECRET defaults to existing file or fresh random.
```

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/deploy/install-mtproxy.sh
git commit -m "feat(ops): install-mtproxy.sh accepts optional SECRET arg (idempotent)"
```

---

## Task 3: biz/mtproto.go — balancer + helpers (30 min)

**Files:**
- Create: `server/backend/internal/biz/mtproto.go`

- [ ] **Step 1: Create the file**

File: `server/backend/internal/biz/mtproto.go`

```go
// Package biz — MTProto-proxy helpers: balancer (PickMTProtoExit),
// install-state queries (ListExitsNeedingMTProto), status mutation
// (MarkMTProtoStatus), and the SSH-based installer (InstallMTProtoOnExit).
//
// "Healthy" definition: is_exit_node=TRUE AND lifecycle_state='online' AND
// mtproto_status='healthy'. The Tunnel-health-monitor maintains the status
// via per-server TCP probe on the global mtproto_port.
package biz

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand"
	"os/exec"
	"time"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

// MTProtoExit — minimal row returned by the picker.
type MTProtoExit struct {
	ID         int
	IPAddress  string
	LoadWeight int
}

// PickMTProtoExit returns a weighted-random healthy exit's host:port to use
// for a Telegram MTProto deeplink. `ok` is false if no healthy exit exists.
//
// Port comes from app_settings.mtproto_port (default 8443) — same on every exit.
func PickMTProtoExit() (host, port string, ok bool) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(load_weight, 1)
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
	`)
	if err != nil {
		log.Printf("[mtproto] PickMTProtoExit query: %v", err)
		return "", "", false
	}
	defer rows.Close()

	var pool []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight); err != nil {
			continue
		}
		w := ex.LoadWeight
		if w < 1 {
			w = 1
		}
		for i := 0; i < w; i++ {
			pool = append(pool, ex)
		}
	}
	if len(pool) == 0 {
		return "", "", false
	}
	chosen := pool[rand.Intn(len(pool))]
	port = GetSettingOrDefault("mtproto_port", "8443")
	return chosen.IPAddress, port, true
}

// ListExitsNeedingMTProto returns online exits that have no daemon installed
// yet (mtproto_installed_at IS NULL). Used by Ghost Reconciler.
func ListExitsNeedingMTProto() ([]MTProtoExit, error) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(load_weight, 1)
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_installed_at IS NULL
		   AND mtproto_status != 'installing'
	`)
	if err != nil {
		return nil, fmt.Errorf("list exits needing mtproto: %w", err)
	}
	defer rows.Close()

	var out []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight); err != nil {
			continue
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

// MarkMTProtoStatus sets mtproto_status and updates last_check timestamp.
// If status == 'healthy' or 'unhealthy', mtproto_installed_at is set to NOW()
// when previously NULL (first successful install / first observation).
func MarkMTProtoStatus(serverID int, status string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		   SET mtproto_status = $1,
		       mtproto_last_check = NOW(),
		       mtproto_installed_at = COALESCE(mtproto_installed_at,
		                                       CASE WHEN $1 IN ('healthy','unhealthy') THEN NOW() END),
		       updated_at = NOW()
		 WHERE id = $2
	`, status, serverID)
	return err
}

// InstallMTProtoOnExit SSHes into the exit-node and runs install-mtproxy.sh
// with the shared secret. Caller already set mtproto_status='installing'.
// Returns nil on success; on error caller should MarkMTProtoStatus(id, 'unhealthy').
//
// The script source is uploaded fresh every call (it's tiny) — keeps the exit
// always running the current version of the installer.
func InstallMTProtoOnExit(serverID int, ipAddress, sshUser, sshPassword string, sshPort int, scriptBytes []byte) error {
	port := GetSettingOrDefault("mtproto_port", "8443")
	secret := GetSettingOrDefault("mtproto_secret", "")
	if secret == "" {
		return fmt.Errorf("mtproto_secret is empty in app_settings")
	}

	cfg := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.Password(sshPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", ipAddress, sshPort)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	// Upload script via SFTP-less heredoc-base64 trick — small file.
	uploadSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh upload session: %w", err)
	}
	defer uploadSession.Close()

	// base64 encode locally
	b64Cmd := exec.Command("base64", "-w0")
	b64Cmd.Stdin = bytes.NewReader(scriptBytes)
	var b64Out bytes.Buffer
	b64Cmd.Stdout = &b64Out
	if err := b64Cmd.Run(); err != nil {
		return fmt.Errorf("base64 local: %w", err)
	}
	uploadCmd := fmt.Sprintf(
		"echo '%s' | base64 -d > /tmp/install-mtproxy.sh && chmod +x /tmp/install-mtproxy.sh",
		b64Out.String(),
	)
	if err := uploadSession.Run(uploadCmd); err != nil {
		return fmt.Errorf("ssh upload exec: %w", err)
	}

	// Run script with port + secret
	runSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh run session: %w", err)
	}
	defer runSession.Close()
	var stdout, stderr bytes.Buffer
	runSession.Stdout = &stdout
	runSession.Stderr = &stderr
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runSession.Run(fmt.Sprintf("bash /tmp/install-mtproxy.sh %s %s", port, secret)) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("install-mtproxy script failed: %w; stderr=%s", err, stderr.String())
		}
	case <-ctx.Done():
		return fmt.Errorf("install-mtproxy timeout after 5min")
	}

	log.Printf("[mtproto] InstallMTProtoOnExit server=%d (%s): success", serverID, ipAddress)
	return nil
}
```

- [ ] **Step 2: Verify build**

```bash
cd server/backend && go build ./...
```
Expected: silent. If `bytes`, `context`, `exec`, or `time` are unused → trim. The above uses all of them.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/mtproto.go
git commit -m "feat(biz): mtproto balancer + SSH installer (PickMTProtoExit, InstallMTProtoOnExit)"
```

---

## Task 4: Extend runSetupExitNode — Step 8 install_mtproxy (30 min)

**Files:**
- Modify: `server/backend/internal/admin/servers_handlers.go`

**Goal:** Add an 8th step to the existing `runSetupExitNode(server)` async function. Read the existing 7 steps to understand the `ProvisionStep` / `progress.Steps[idx]` pattern. The new step calls `biz.InstallMTProtoOnExit` and updates `mtproto_status` on result.

- [ ] **Step 1: Read existing runSetupExitNode skeleton**

```bash
grep -n "ProvisionStep\|progress.Steps\|setup_hub_client\|update_credentials" server/backend/internal/admin/servers_handlers.go | head -20
```
Note the lines where steps are appended and the helper used to mark step status (`setStep` or similar).

- [ ] **Step 2: Add the new step entry**

Find the line:
```go
{Name: "setup_hub_client", Description: "Настройка Hysteria2 клиента на Hub", Status: "pending"},
```
Add a new entry directly after it:
```go
{Name: "install_mtproxy", Description: "Установка MTProto-прокси (Telegram бонус)", Status: "pending"},
```

This adds the visible 8th step in the admin progress modal.

- [ ] **Step 3: Locate where the 7th step (setup_hub_client) completes**

Search for the block that marks step 6 (`update_credentials`) done and starts the existing 7th step `setup_hub_client`. After `setStep(7, "ok", "")` (or whatever the existing index for the last step is — index 6 if 0-based for "setup_hub_client"), insert the new Step 8 logic.

Find where the function ends successfully — look for:
```go
progress.Finished = true
provisionProgress.Store(server.ID, progress)
log.Printf("[provision] Сервер %d (%s): провижининг завершён успешно", server.ID, server.Name)
```

Right BEFORE these lines, insert the install_mtproxy step:

```go
	// Step 8 (idx 7): install_mtproxy — Telegram MTProto bonus.
	// Failure here is NON-FATAL: provisioning succeeds, mtproto stays unhealthy.
	if err := biz.MarkMTProtoStatus(server.ID, "installing"); err != nil {
		log.Printf("[provision] mark mtproto installing %d: %v", server.ID, err)
	}
	scriptBytes, scriptErr := os.ReadFile("/opt/heyearth/scripts/install-mtproxy.sh")
	if scriptErr != nil {
		// Fallback: read from binary's working dir (development)
		scriptBytes, scriptErr = os.ReadFile("server/backend/cmd/deploy/install-mtproxy.sh")
	}
	if scriptErr != nil {
		setStep(7, "error", "install-mtproxy.sh not found on hub: "+scriptErr.Error())
		_ = biz.MarkMTProtoStatus(server.ID, "unhealthy")
	} else {
		sshPort := server.SshPort
		if sshPort == 0 {
			sshPort = 22
		}
		sshUser := server.SshUser
		if sshUser == "" {
			sshUser = "root"
		}
		sshPass := server.SshPassword
		// Decrypt enc if needed — existing pattern from sshDialForServer
		if sshPass == "" && len(server.SshPasswordEnc) > 0 {
			dec, decErr := biz.DecryptSSHPassword(server.SshPasswordEnc)
			if decErr == nil {
				sshPass = dec
			}
		}
		if instErr := biz.InstallMTProtoOnExit(server.ID, server.IpAddress, sshUser, sshPass, sshPort, scriptBytes); instErr != nil {
			setStep(7, "error", instErr.Error())
			_ = biz.MarkMTProtoStatus(server.ID, "unhealthy")
			log.Printf("[provision] install_mtproxy %d (%s): %v", server.ID, server.Name, instErr)
		} else {
			setStep(7, "ok", "")
			_ = biz.MarkMTProtoStatus(server.ID, "healthy")
			biz.BumpSubscriptionVersion()
		}
	}
```

**NOTE on field names:** the `server` variable is `serverResponse` (struct). Field names like `SshPort`, `SshUser`, `SshPassword`, `SshPasswordEnc`, `IpAddress` may differ. Run:
```bash
grep -n "type serverResponse\|SshPort\|SshUser\|IpAddress" server/backend/internal/admin/servers_handlers.go | head -10
```
and adapt the variable accesses to match.

**NOTE on `biz.DecryptSSHPassword`**: If a different helper exists (e.g. `crypto.DecryptSSHPassword` or inline ECB), grep and use what's already there. Match the pattern used by `sshDialForServer`.

**NOTE on `os` import**: add to the import block at the top of `servers_handlers.go` if not already present.

- [ ] **Step 4: Build**

```bash
cd server/backend && go build ./...
```
Expected: silent. If `os` undefined → add to imports. If `serverResponse` lacks a field → match actual struct.

- [ ] **Step 5: Commit**

```bash
git add server/backend/internal/admin/servers_handlers.go
git commit -m "feat(provision): step 8 install_mtproxy in runSetupExitNode"
```

---

## Task 5: ServerActionHandler — reinstall_mtproxy action (20 min)

**Files:**
- Modify: `server/backend/internal/admin/servers_handlers.go`

**Goal:** Add a new admin action `reinstall_mtproxy` that runs the same SSH installer asynchronously, with progress reuse.

- [ ] **Step 1: Find ServerActionHandler switch**

```bash
grep -n "case \"setup_exit_node" server/backend/internal/admin/servers_handlers.go
```
Find the switch block in `ServerActionHandler`.

- [ ] **Step 2: Add new case**

Locate the existing case:
```go
case "setup_exit_node", "reprovision":
    if v, ok := provisionProgress.Load(serverID); ok {
        pp := v.(*ProvisionProgress)
        if !pp.Finished {
            writeAdminError(w, http.StatusConflict, "Provisioning already in progress")
            return
        }
    }
    go runSetupExitNode(server)
    writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Provisioning started"})
    return
```

Add directly below:
```go
case "reinstall_mtproxy":
    if v, ok := provisionProgress.Load(serverID); ok {
        pp := v.(*ProvisionProgress)
        if !pp.Finished {
            writeAdminError(w, http.StatusConflict, "Action already in progress")
            return
        }
    }
    go runReinstallMTProto(server)
    writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "MTProto reinstall started"})
    return
```

- [ ] **Step 3: Add runReinstallMTProto helper**

Append near `runHubProvisionOnly` (or similar single-purpose async helpers) in `servers_handlers.go`:

```go
// runReinstallMTProto re-runs the install-mtproxy.sh on the exit's VPS.
// Reuses provisionProgress with a single-step progress for UI continuity.
func runReinstallMTProto(server serverResponse) {
	progress := &ProvisionProgress{
		ServerID: server.ID,
		Action:   "reinstall_mtproxy",
		Steps: []ProvisionStep{
			{Name: "install_mtproxy", Description: "Переустановка MTProto-прокси", Status: "running"},
		},
		Started: time.Now(),
	}
	provisionProgress.Store(server.ID, progress)
	finalize := func(status, msg string) {
		progress.Steps[0].Status = status
		if msg != "" {
			progress.Steps[0].Error = msg
		}
		progress.Finished = true
		provisionProgress.Store(server.ID, progress)
	}

	if err := biz.MarkMTProtoStatus(server.ID, "installing"); err != nil {
		log.Printf("[reinstall-mtproxy] mark installing %d: %v", server.ID, err)
	}
	scriptBytes, scriptErr := os.ReadFile("/opt/heyearth/scripts/install-mtproxy.sh")
	if scriptErr != nil {
		scriptBytes, scriptErr = os.ReadFile("server/backend/cmd/deploy/install-mtproxy.sh")
	}
	if scriptErr != nil {
		_ = biz.MarkMTProtoStatus(server.ID, "unhealthy")
		finalize("error", "install-mtproxy.sh not found on hub: "+scriptErr.Error())
		return
	}
	sshPort := server.SshPort
	if sshPort == 0 {
		sshPort = 22
	}
	sshUser := server.SshUser
	if sshUser == "" {
		sshUser = "root"
	}
	sshPass := server.SshPassword
	if sshPass == "" && len(server.SshPasswordEnc) > 0 {
		dec, decErr := biz.DecryptSSHPassword(server.SshPasswordEnc)
		if decErr == nil {
			sshPass = dec
		}
	}
	if instErr := biz.InstallMTProtoOnExit(server.ID, server.IpAddress, sshUser, sshPass, sshPort, scriptBytes); instErr != nil {
		_ = biz.MarkMTProtoStatus(server.ID, "unhealthy")
		finalize("error", instErr.Error())
		return
	}
	_ = biz.MarkMTProtoStatus(server.ID, "healthy")
	biz.BumpSubscriptionVersion()
	finalize("ok", "")
	log.Printf("[reinstall-mtproxy] %d (%s): success", server.ID, server.Name)
}
```

Adapt field names to match `serverResponse` struct (same caveats as Task 4 Step 3).

- [ ] **Step 4: Build**

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 5: Commit**

```bash
git add server/backend/internal/admin/servers_handlers.go
git commit -m "feat(admin): reinstall_mtproxy action + runReinstallMTProto helper"
```

---

## Task 6: Ghost Reconciler — backfill mtproto_missing (25 min)

**Files:**
- Modify: `server/backend/cmd/api/ghost_reconciler.go`

**Goal:** Every 30-min reconciler tick, check `ListExitsNeedingMTProto()` and (if `mtproto_auto_install=true`) fire async install for ONE exit per tick (rate-limit to avoid hammering).

- [ ] **Step 1: Inspect StartGhostReconciler loop body**

```bash
grep -n "scan\|orphan\|missing\|app_settings\|ghost_auto" server/backend/cmd/api/ghost_reconciler.go | head -15
```
Familiarize with the existing pattern: it logs counts, optionally takes action.

- [ ] **Step 2: Add MTProto reconciliation**

In `server/backend/cmd/api/ghost_reconciler.go`, find the main loop body (after the 30-min sleep). Append a new section after existing orphan/missing checks, before the next `time.Sleep`:

```go
		// ── MTProto backfill ──
		// Auto-install mtproto-proxy on exits that don't have it yet.
		// Rate-limited to 1 install per tick (30 min) to avoid hammering.
		autoInstall := biz.GetSettingOrDefault("mtproto_auto_install", "true") == "true"
		if autoInstall {
			needing, err := biz.ListExitsNeedingMTProto()
			if err != nil {
				log.Printf("[ghost-reconciler] mtproto list: %v", err)
			} else if len(needing) > 0 {
				log.Printf("[ghost-reconciler] %d exit(s) need mtproto-proxy install; firing 1 (rate-limit)", len(needing))
				exit := needing[0]
				go installMTProtoForExit(exit.ID)
			}
		}
```

- [ ] **Step 3: Add installMTProtoForExit helper**

At the bottom of `server/backend/cmd/api/ghost_reconciler.go`, append:

```go
// installMTProtoForExit fetches the full server row from DB, then invokes
// the same SSH-based installer used by runSetupExitNode's Step 8.
// Runs asynchronously; errors are logged but don't crash the reconciler.
func installMTProtoForExit(serverID int) {
	var srv struct {
		IPAddress      string
		SshUser        string
		SshPort        int
		SshPasswordEnc []byte
	}
	err := db.DB.QueryRow(`
		SELECT ip_address, COALESCE(ssh_user,'root'), COALESCE(ssh_port,22),
		       COALESCE(ssh_password_enc, ''::bytea)
		  FROM servers WHERE id = $1
	`, serverID).Scan(&srv.IPAddress, &srv.SshUser, &srv.SshPort, &srv.SshPasswordEnc)
	if err != nil {
		log.Printf("[ghost-reconciler] load server %d: %v", serverID, err)
		return
	}

	sshPass := ""
	if len(srv.SshPasswordEnc) > 0 {
		dec, derr := biz.DecryptSSHPassword(srv.SshPasswordEnc)
		if derr == nil {
			sshPass = dec
		}
	}

	scriptBytes, sErr := os.ReadFile("/opt/heyearth/scripts/install-mtproxy.sh")
	if sErr != nil {
		scriptBytes, sErr = os.ReadFile("server/backend/cmd/deploy/install-mtproxy.sh")
	}
	if sErr != nil {
		log.Printf("[ghost-reconciler] script not found: %v", sErr)
		return
	}

	if err := biz.MarkMTProtoStatus(serverID, "installing"); err != nil {
		log.Printf("[ghost-reconciler] mark installing %d: %v", serverID, err)
	}
	if instErr := biz.InstallMTProtoOnExit(serverID, srv.IPAddress, srv.SshUser, sshPass, srv.SshPort, scriptBytes); instErr != nil {
		_ = biz.MarkMTProtoStatus(serverID, "unhealthy")
		log.Printf("[ghost-reconciler] install mtproxy on %d (%s): %v", serverID, srv.IPAddress, instErr)
		return
	}
	_ = biz.MarkMTProtoStatus(serverID, "healthy")
	biz.BumpSubscriptionVersion()
	log.Printf("[ghost-reconciler] install mtproxy on %d (%s): success", serverID, srv.IPAddress)
}
```

- [ ] **Step 4: Imports**

Ensure top of `ghost_reconciler.go` imports `"os"` and `"github.com/heyearth/backend/internal/db"` (likely already there).

- [ ] **Step 5: Build**

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 6: Deploy install-mtproxy.sh to /opt/heyearth/scripts/ on hub**

The Go code reads the script from a fixed path on hub. We must place it there during deploy. We'll do this in Task 12 deploy phase — for now just note that quick_deploy.go currently doesn't upload `cmd/deploy/install-mtproxy.sh` to /opt/heyearth/scripts/. Need a quick deploy-time copy. Add to deploy script:

```bash
# In Task 12 deploy phase we'll handle the script upload. For now skip.
```

- [ ] **Step 7: Commit**

```bash
git add server/backend/cmd/api/ghost_reconciler.go
git commit -m "feat(reconciler): mtproto backfill — auto-install on existing exits"
```

---

## Task 7: TCP probe in StartTunnelHealthMonitor — 3/2 hysteresis (25 min)

**Files:**
- Modify: `server/backend/cmd/api/xray_sync.go`

**Goal:** Per-exit TCP dial to `mtproto_port` (default 8443), update `mtproto_status` based on 3-fail-down / 2-success-up hysteresis. Piggyback on existing 30-second tunnel-health loop.

- [ ] **Step 1: Find StartTunnelHealthMonitor**

```bash
grep -n "func StartTunnelHealthMonitor\|tunnel.*30\|checkTunnel" server/backend/cmd/api/xray_sync.go | head -10
```
Find the main loop body.

- [ ] **Step 2: Add MTProto probe section**

Inside the loop body (after existing tunnel checks), add a block that iterates all installed exits and dials :8443. Use module-level maps to track consecutive fail/success counts.

At the top of `xray_sync.go` (or wherever module vars live), add:
```go
var (
	mtprotoFailCount    = map[int]int{}
	mtprotoSuccessCount = map[int]int{}
	mtprotoCountMu      sync.Mutex
)
```
(import `sync` if not already.)

Inside the StartTunnelHealthMonitor loop body, append:
```go
		// ── MTProto :8443 TCP probe ──
		mtprotoPort := biz.GetSettingOrDefault("mtproto_port", "8443")
		rows, mErr := db.DB.Query(`
			SELECT id, ip_address, mtproto_status
			  FROM servers
			 WHERE is_exit_node = TRUE
			   AND lifecycle_state = 'online'
			   AND mtproto_installed_at IS NOT NULL
		`)
		if mErr == nil {
			type probeRow struct {
				ID     int
				IP     string
				Status string
			}
			var probes []probeRow
			for rows.Next() {
				var p probeRow
				if err := rows.Scan(&p.ID, &p.IP, &p.Status); err == nil {
					probes = append(probes, p)
				}
			}
			rows.Close()

			for _, p := range probes {
				addr := fmt.Sprintf("%s:%s", p.IP, mtprotoPort)
				conn, dErr := net.DialTimeout("tcp", addr, 3*time.Second)
				ok := dErr == nil
				if conn != nil {
					_ = conn.Close()
				}

				mtprotoCountMu.Lock()
				if ok {
					mtprotoSuccessCount[p.ID]++
					mtprotoFailCount[p.ID] = 0
					if mtprotoSuccessCount[p.ID] >= 2 && p.Status != "healthy" {
						_ = biz.MarkMTProtoStatus(p.ID, "healthy")
						log.Printf("[mtproto-health] server %d (%s) → healthy after 2 successes", p.ID, p.IP)
					}
				} else {
					mtprotoFailCount[p.ID]++
					mtprotoSuccessCount[p.ID] = 0
					if mtprotoFailCount[p.ID] >= 3 && p.Status != "unhealthy" {
						_ = biz.MarkMTProtoStatus(p.ID, "unhealthy")
						log.Printf("[mtproto-health] server %d (%s) → unhealthy after 3 fails: %v", p.ID, p.IP, dErr)
					}
				}
				mtprotoCountMu.Unlock()

				// Always update last_check regardless of state-change.
				_, _ = db.DB.Exec(`UPDATE servers SET mtproto_last_check = NOW() WHERE id = $1`, p.ID)
			}
		}
```

- [ ] **Step 3: Imports**

Ensure `xray_sync.go` top has:
```go
"net"
"sync"
"time"
"fmt"
"github.com/heyearth/backend/internal/db"
```
Most are likely already present.

- [ ] **Step 4: Build**

```bash
cd server/backend && go build ./...
```
Expected: silent. If unused-var or import errors → trim/add.

- [ ] **Step 5: Commit**

```bash
git add server/backend/cmd/api/xray_sync.go
git commit -m "feat(health): per-exit MTProto TCP-probe in tunnel-health-monitor (3/2 hysteresis)"
```

---

## Task 8: Update /api/v1/user/mtproto to use PickMTProtoExit (15 min)

**Files:**
- Modify: `server/backend/cmd/api/main.go`

- [ ] **Step 1: Find handleUserMTProto**

```bash
grep -n "handleUserMTProto\|mtproto_host\|mtproto_enabled" server/backend/cmd/api/main.go | head -10
```

- [ ] **Step 2: Replace static host/port with balancer pick**

Current logic likely reads:
```go
enabled := biz.GetSettingOrDefault("mtproto_enabled", "false") == "true"
host := biz.GetSettingOrDefault("mtproto_host", "")
port := biz.GetSettingOrDefault("mtproto_port", "8443")
secret := biz.GetSettingOrDefault("mtproto_secret", "")

if !enabled || host == "" || secret == "" {
    writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
    return
}
```

Replace with:
```go
enabled := biz.GetSettingOrDefault("mtproto_enabled", "false") == "true"
secret := biz.GetSettingOrDefault("mtproto_secret", "")

if !enabled || secret == "" {
    writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
    return
}

host, port, ok := biz.PickMTProtoExit()
if !ok {
    writeJSON(w, http.StatusOK, map[string]interface{}{
        "enabled": false,
        "reason":  "Временно недоступно",
    })
    return
}
```

The rest of the handler (building `tg_deeplink` + `t_me_link` strings, returning JSON) stays the same.

- [ ] **Step 3: Build**

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/main.go
git commit -m "feat(api): /api/v1/user/mtproto uses PickMTProtoExit balancer"
```

---

## Task 9: Admin MTProtoPage — drop Host/Port, add auto_install (25 min)

**Files:**
- Modify: `admin_web/src/pages/MTProtoPage.jsx`

**Goal:** Remove Host and Port input fields (now per-exit-managed). Add `auto_install` toggle. Optionally add "Переустановить везде" button that fires reinstall for every exit.

- [ ] **Step 1: Read current page**

```bash
grep -n "host\|port\|enabled\|auto_install" admin_web/src/pages/MTProtoPage.jsx | head -20
```

Confirm state shape and form fields.

- [ ] **Step 2: Update form state initialization**

Find:
```jsx
const [form, setForm] = useState({
  enabled: false,
  host: '',
  port: '8443',
  secret: '',
  sponsor_channel: '',
})
```

Replace with:
```jsx
const [form, setForm] = useState({
  enabled: false,
  secret: '',
  sponsor_channel: '',
  auto_install: true,
})
```

- [ ] **Step 3: Update load() population**

Find inside `load()`:
```jsx
setForm({
  enabled: !!data.enabled,
  host: data.host || '',
  port: data.port || '8443',
  secret: data.secret || '',
  sponsor_channel: data.sponsor_channel || '',
})
```

Replace with:
```jsx
setForm({
  enabled: !!data.enabled,
  secret: data.secret || '',
  sponsor_channel: data.sponsor_channel || '',
  auto_install: data.auto_install !== false,
})
```

- [ ] **Step 4: Remove Host and Port JSX blocks**

Find and DELETE the two JSX blocks:
```jsx
<div>
  <label className="text-xs text-text-secondary block mb-1">Host (FQDN или IP exit-VPS)</label>
  <input ... value={form.host} ... />
</div>
```
and
```jsx
<div>
  <label className="text-xs text-text-secondary block mb-1">Port</label>
  <input ... value={form.port} ... />
</div>
```

- [ ] **Step 5: Add auto_install toggle**

Insert below the "Включить MTProto" checkbox label (or wherever fits the form layout):

```jsx
<label className="flex items-center gap-3 cursor-pointer">
  <input
    type="checkbox"
    checked={form.auto_install}
    onChange={(e) => setForm({ ...form, auto_install: e.target.checked })}
    className="w-4 h-4"
  />
  <span className="text-sm">Автоматически ставить mtproxy на новые/существующие exit-серверы</span>
</label>
```

- [ ] **Step 6: Add explanation paragraph at the top**

After the page title's existing description paragraph, add a short note:
```jsx
<p className="text-xs text-text-secondary mb-4">
  Host и Port теперь управляются автоматически — балансировщик выбирает
  здоровый exit-сервер при каждом запросе пользователя.
</p>
```

- [ ] **Step 7: Build**

```bash
cd admin_web && npm run build
```
Expected: silent.

- [ ] **Step 8: Commit**

```bash
git add admin_web/src/pages/MTProtoPage.jsx
git commit -m "feat(admin): MTProtoPage drops Host/Port, adds auto_install toggle"
```

---

## Task 10: Admin ServerDetailPage — MTProto badge + Reinstall button (25 min)

**Files:**
- Modify: `admin_web/src/pages/ServerDetailPage.jsx`

**Goal:** Show `mtproto_status` next to the existing TunnelBadge area; add "Переустановить mtproxy" button for exit-nodes.

- [ ] **Step 1: Find tunnel_status display block**

```bash
grep -n "tunnel_status\|TunnelBadge\|setup_exit_node\|Re-provision" admin_web/src/pages/ServerDetailPage.jsx | head -15
```

- [ ] **Step 2: Add MTProto status badge near tunnel_status**

Locate the existing tunnel-status badge display (probably similar to `<span ... server.tunnel_status === 'healthy' ? ...>`). Adjacent to it, add:

```jsx
{server.is_exit_node && (
  <div className="flex items-center gap-2 mt-2">
    <span className="text-xs text-text-secondary">MTProto:</span>
    <span className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md text-xs ${
      server.mtproto_status === 'healthy' ? 'bg-emerald-500/15 text-emerald-300' :
      server.mtproto_status === 'unhealthy' ? 'bg-red-500/15 text-red-300' :
      server.mtproto_status === 'installing' ? 'bg-blue-500/15 text-blue-300' :
      'bg-gray-500/15 text-gray-300'
    }`}>
      <span className="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
      {server.mtproto_status === 'healthy' ? 'Healthy' :
       server.mtproto_status === 'unhealthy' ? 'Unhealthy' :
       server.mtproto_status === 'installing' ? 'Installing…' :
       'Unknown'}
    </span>
  </div>
)}
```

- [ ] **Step 3: Add Reinstall button**

Find the action buttons row (where `setup_exit_node` / `reprovision` / `hub_provision` buttons live). Add a new button:

```jsx
{server.is_exit_node && (
  <button onClick={() => { if (confirm('Переустановить mtproto-proxy на этой ноде?')) doAction('reinstall_mtproxy') }}
    className="px-3 py-1.5 text-xs rounded-lg bg-cyan-500/20 text-cyan-400 hover:bg-cyan-500/30 transition">
    Reinstall MTProxy
  </button>
)}
```

Place it inside the existing exit-node-specific button group (where `Hub Provision` / `Disable (soft)` / `Delete (hard)` already are).

- [ ] **Step 4: Build**

```bash
cd admin_web && npm run build
```
Expected: silent.

- [ ] **Step 5: Commit**

```bash
git add admin_web/src/pages/ServerDetailPage.jsx
git commit -m "feat(admin): ServerDetailPage — MTProto status badge + Reinstall button"
```

---

## Task 11: Admin ServersPage — mtproto_status badge (15 min)

**Files:**
- Modify: `admin_web/src/pages/ServersPage.jsx`

**Goal:** In the list of servers, next to `TunnelBadge`, show a small `mtproto_status` indicator for exit-nodes.

- [ ] **Step 1: Find TunnelBadge usage in card AND table views**

```bash
grep -n "TunnelBadge\|tunnel_status" admin_web/src/pages/ServersPage.jsx | head -10
```

Both card and table views render `<TunnelBadge status={srv.tunnel_status} />`. We add a sibling.

- [ ] **Step 2: Add MTProto badge in both views**

In **both** card view and table cell, find:
```jsx
<TunnelBadge status={srv.tunnel_status} />
```

Immediately AFTER it, add:
```jsx
{srv.is_exit_node && srv.mtproto_status && srv.mtproto_status !== 'unknown' && (
  <span className={`inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] ${
    srv.mtproto_status === 'healthy' ? 'bg-emerald-500/15 text-emerald-300' :
    srv.mtproto_status === 'unhealthy' ? 'bg-red-500/15 text-red-300' :
    'bg-blue-500/15 text-blue-300'
  }`} title={`MTProto: ${srv.mtproto_status}`}>
    MTP
  </span>
)}
```

- [ ] **Step 3: Build**

```bash
cd admin_web && npm run build
```
Expected: silent.

- [ ] **Step 4: Commit**

```bash
git add admin_web/src/pages/ServersPage.jsx
git commit -m "feat(admin): ServersPage — MTP status badge next to TunnelBadge"
```

---

## Task 12: Deploy + end-to-end verification (30 min)

- [ ] **Step 1: Stop API + deploy backend**

```bash
cd server/backend
go run ./cmd/deploy --ssh "systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api && echo OK"
go run quick_deploy.go
```
Expected: `heyearth-api: ACTIVE ✓` + `Health: {"status":"ok"}`.

- [ ] **Step 2: Verify migration 000042 applied**

```bash
go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c \"\\d servers\" | grep mtproto; sudo -u postgres psql heyearth -c \"SELECT key, value FROM app_settings WHERE key IN ('mtproto_auto_install', 'mtproto_host');\""
```
Expected: three mtproto columns listed; `mtproto_auto_install=true`; `mtproto_host` MISSING from settings (was deleted).

- [ ] **Step 3: Upload install-mtproxy.sh to hub static path**

The reconciler + handlers read the script from `/opt/heyearth/scripts/install-mtproxy.sh`. Upload it via base64-over-SSH:

```bash
cd server/backend
SCRIPT_B64=$(base64 -w0 cmd/deploy/install-mtproxy.sh)
go run ./cmd/deploy --ssh "mkdir -p /opt/heyearth/scripts && echo '$SCRIPT_B64' | base64 -d > /opt/heyearth/scripts/install-mtproxy.sh && chmod +x /opt/heyearth/scripts/install-mtproxy.sh && head -3 /opt/heyearth/scripts/install-mtproxy.sh && wc -l /opt/heyearth/scripts/install-mtproxy.sh"
```
Expected: shebang printed; ~85 lines.

- [ ] **Step 4: Deploy admin_web**

```bash
go run ./cmd/deploy --admin-web
```
Expected: nginx reloaded, no errors.

- [ ] **Step 5: Wait for Ghost Reconciler tick**

The reconciler runs every 30 min on a fixed cadence. To accelerate first run, restart `heyearth-api` (already done by quick_deploy.go) — first scan fires ~2 min after start.

Wait 3-4 minutes, then check:
```bash
go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '5 minutes ago' --no-pager | grep -E 'ghost-reconciler|mtproto'"
```
Expected: line like `[ghost-reconciler] N exit(s) need mtproto-proxy install; firing 1 (rate-limit)` followed by `[ghost-reconciler] install mtproxy on 2 (89.124.89.126): success` after a few minutes (build on Amsterdam takes 2-5 min).

If you see compile errors or SSH errors → investigate the specific failure (it'll be in the same log).

- [ ] **Step 6: Verify Amsterdam has daemon running**

```bash
go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c \"SELECT id, name, mtproto_status, mtproto_installed_at FROM servers WHERE is_exit_node;\""
```
Expected: row id=2, `mtproto_status='healthy'`, `mtproto_installed_at` recent timestamp.

- [ ] **Step 7: Verify tunnel-health-monitor logs MTProto probe**

After another minute:
```bash
go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '2 minutes ago' --no-pager | grep mtproto-health"
```
Expected: (no log entry if status is stable healthy — only state transitions are logged). OR a `[mtproto-health] server 2 → healthy after 2 successes` if this is the first healthy observation. Either is acceptable.

- [ ] **Step 8: Verify endpoint returns dynamic host**

Use any active-subscription user token:
```bash
USER_TOKEN="<paste valid user JWT here>"
curl -s -H "Authorization: Bearer $USER_TOKEN" https://hiearth.ru/api/v1/user/mtproto | python3 -m json.tool
```
Expected:
```json
{
  "enabled": true,
  "host": "89.124.89.126",
  "port": "8443",
  "secret": "26c834d4b1b097dfde254de8614047d7",
  "tg_deeplink": "tg://proxy?server=89.124.89.126&port=8443&secret=...",
  "t_me_link": "https://t.me/proxy?server=..."
}
```

- [ ] **Step 9: Telegram smoke (manual, optional)**

On a phone:
1. Open `https://hiearth.ru/connections` while logged in
2. Tap "Открыть в Telegram"
3. Confirm proxy in Telegram
4. Check Telegram Settings → Data and Storage → Proxy → should list `89.124.89.126:8443`
5. Send a message to anyone — verify Telegram works through proxy
6. If sponsor channel was set in admin, Telegram UI shows the sponsor banner

- [ ] **Step 10: Push + close Beads epic**

```bash
git push origin main
bd close <EPIC_ID from Task 0> --reason "B.2 deployed: per-exit MTProto + balancer + auto-install live"
```

---

## Risk register (from spec, summarized)

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | Reconciler hammers exit-VPS | 1 install per 30-min tick (rate-limit in Task 6 Step 2) |
| R-02 | install-mtproxy.sh not idempotent on rerun | Script handles `[[ -f $BIN ]]` and now ALWAYS overwrites secret if arg provided (Task 2) |
| R-03 | daemon crashes silently | systemd Restart=on-failure + Task 7 TCP probe catches sustained outage |
| R-04 | Shared secret leak via admin UI | Existing RBAC settings.read gate |
| R-05 | Telegram blocks shared secret across many exits | Admin regenerates secret → reconciler propagates within 30 min via reinstall path |
| R-06 | exit-node firewall not opening :8443 | install-mtproxy.sh runs `ufw allow 8443/tcp \|\| true` |
| R-07 | One unhealthy exit drags balancer | PickMTProtoExit filters `mtproto_status='healthy'` |
| R-08 | Port change requires re-running on each exit | Reconciler doesn't re-trigger on port change — admin clicks "Reinstall MTProxy" per exit (or in MTProtoPage future "Reinstall all" button) |

## Rollback

* **Backend revert**: `git revert <merge>` + `go run quick_deploy.go`. Migration 000042 columns can stay (NULL-safe). Manual down: psql with `000042_mtproto_per_exit.down.sql`.
* **Disable user-facing**: admin sets `mtproto_enabled=false` → endpoint returns `enabled:false` → site hides button.
* **Disable auto-install only**: admin unchecks "Автоматически" toggle (`mtproto_auto_install=false`). Existing daemons keep running, reconciler stops auto-adding to new/missing exits.
* **Per-exit removal**: SSH manual `systemctl stop mtproxy && systemctl disable mtproxy` (admin UI uninstall is out of scope).

## Out of scope reminders

- Sub-project D (per-plan rate-limit) — separate cycle
- Per-server secret rotation
- Geo-aware exit selection (use existing weighted random)
- mtproto-proxy stats ingestion
- Flutter app changes
- "Reinstall all" admin button (per-exit Reinstall button covers it for now)
