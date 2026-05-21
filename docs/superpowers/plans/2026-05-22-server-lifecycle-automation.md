# Server Lifecycle Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make exit-server CRUD a real FSM with guaranteed hub-side cleanup, a safety-net Ghost Reconciler, and live propagation to user-side via `subscription_version` — so the "Latvia ghost" class of incidents becomes structurally impossible.

**Architecture:** New `lifecycle_state` column drives a 6-state FSM. New `biz/server_lifecycle.go` orchestrates `Provision()` and `Deprovision()` through hub-side `os/exec` operations (heyearth-api runs on the hub) and best-effort SSH teardown on the exit. `cmd/api/ghost_reconciler.go` runs every 30 min as safety-net. Admin UI gets lifecycle badge + 4 buttons + type-to-confirm Delete. Site polls subscription_version and shows toast on change.

**Tech Stack:** Go 1.21+, PostgreSQL 14+, systemd, Hysteria2, Xray, React 19, Tailwind v4.

**Beads epic:** to be created in Task 0.

**Verification:** Evidence-driven (no automated tests). Every task has SSH + SQL commands proving the change took effect.

**Deploy:** `cd server/backend && go run quick_deploy.go` for binary+migrations, `go run ./cmd/deploy --ssh '...'` for one-off SSH commands.

**Spec:** `docs/superpowers/specs/2026-05-21-server-lifecycle-automation-design.md`

---

## File Structure

### Create (Go backend)
* `server/backend/migrations/000040_server_lifecycle.up.sql` — schema additions
* `server/backend/migrations/000040_server_lifecycle.down.sql` — rollback
* `server/backend/internal/biz/server_lifecycle.go` — FSM, ProvisionEvent type, TransitionState, BumpSubscriptionVersion, Provision, Deprovision
* `server/backend/internal/biz/hub_provisioner.go` — hub-side os/exec (write config, systemd install/enable/start/stop/remove, archive)
* `server/backend/internal/biz/exit_teardown.go` — best-effort SSH teardown with 10s timeout
* `server/backend/cmd/api/ghost_reconciler.go` — 30-min scan goroutine

### Create (frontend)
* `admin_web/src/components/ServerLifecycleBadge.tsx` — colored badge per state
* `admin_web/src/components/DeleteServerDialog.tsx` — type-to-confirm
* `site/src/components/AvailableLocations.jsx` — widget with polling

### Create (ops)
* `server/backend/cmd/deploy/migrate-hysteria-naming.sh` — one-time legacy rename

### Modify
* `server/backend/internal/biz/server.go` — `ListHealthyExitNodes` adds `AND lifecycle_state='online'`
* `server/backend/internal/admin/servers_handlers.go` — `DeleteServerHandler` now async-deprovisions before DELETE; `ServerActionHandler` gets `deprovision_soft` and `deprovision_hard` actions
* `server/backend/cmd/api/main.go` — register `StartGhostReconciler()`
* `server/backend/cmd/api/client_api.go` (or equivalent that serves `/api/v1/user/connections`) — add `subscription_version` field + `X-Subscription-Version` header
* `admin_web/src/pages/ServerDetailPage.tsx` — wire lifecycle badge + 4 buttons + dialog
* `admin_web/src/pages/ServersPage.tsx` — lifecycle column
* `site/src/api/user.js` — `getConnections` returns version, helper `getSubscriptionVersionFromHeaders`
* `site/src/pages/DashboardPage.jsx` — mount AvailableLocations widget + toast on version change

---

## Task 0: Create Beads epic + sub-tasks

- [ ] **Step 1: Create epic**

```bash
cd C:\Users\alex\Documents\heyearthproject
bd create --title="Server Lifecycle Automation (sub-project A)" \
  --description="DB lifecycle_state + FSM + Ghost Reconciler + admin UI + user-cab signal. Spec: docs/superpowers/specs/2026-05-21-server-lifecycle-automation-design.md. Plan: docs/superpowers/plans/2026-05-22-server-lifecycle-automation.md" \
  --type=epic --priority=1
```
Expected: prints new ID like `heyearthproject-XXXX`. Record it as `EPIC_ID`.

- [ ] **Step 2: Create per-task sub-issues**

For each task (1-15) below, run `bd create --title="Task N: ..." --type=task --priority=1 --description="See plan section Task N"`, then `bd dep add <task-id> <EPIC_ID> --type=parent-child`. Add sequential `--type=blocks` between tasks where indicated (1→2, 2→3, ..., 14→15).

- [ ] **Step 3: Commit nothing yet (Beads tracks itself in Dolt)**

---

## Task 1: Migration 000040 — lifecycle schema (15 min)

**Files:**
- Create: `server/backend/migrations/000040_server_lifecycle.up.sql`
- Create: `server/backend/migrations/000040_server_lifecycle.down.sql`

- [ ] **Step 1: Write up migration**

File: `server/backend/migrations/000040_server_lifecycle.up.sql`

```sql
-- Server Lifecycle Automation (sub-project A)
-- Adds an explicit FSM column to servers + a subscription_version setting bumped
-- on every lifecycle transition so user clients can detect server changes.

ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS lifecycle_state TEXT NOT NULL DEFAULT 'online'
        CHECK (lifecycle_state IN
            ('pending', 'provisioning', 'online', 'failed', 'deprovisioning', 'offline'));

ALTER TABLE servers
    ADD COLUMN IF NOT EXISTS lifecycle_updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS idx_servers_lifecycle
    ON servers(lifecycle_state) WHERE is_exit_node = true;

-- Backfill: existing exit-nodes with status='online' → lifecycle_state='online'.
-- Servers with status='offline' → lifecycle_state='offline' (preserve current state).
UPDATE servers
   SET lifecycle_state = CASE
       WHEN status = 'online' THEN 'online'
       WHEN status = 'offline' THEN 'offline'
       ELSE 'online'
   END
 WHERE is_exit_node = true;

-- Global counter bumped on every lifecycle change.
-- Clients (site/Flutter) compare local-stored value to this; mismatch → refetch subscription.
INSERT INTO app_settings (key, value) VALUES ('subscription_version', '1')
    ON CONFLICT (key) DO NOTHING;

-- Knobs for Ghost Reconciler (Task 9). Defaults are safe: alert always, auto-cleanup ON, auto-repair OFF.
INSERT INTO app_settings (key, value) VALUES ('ghost_auto_cleanup', 'true')
    ON CONFLICT (key) DO NOTHING;
INSERT INTO app_settings (key, value) VALUES ('ghost_auto_repair', 'false')
    ON CONFLICT (key) DO NOTHING;
-- Days to keep /opt/heyearth/state/server-archive/<id>-<ts>/ artifacts before manual cleanup.
INSERT INTO app_settings (key, value) VALUES ('server_archive_retention_days', '90')
    ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 2: Write down migration**

File: `server/backend/migrations/000040_server_lifecycle.down.sql`

```sql
DROP INDEX IF EXISTS idx_servers_lifecycle;
ALTER TABLE servers DROP COLUMN IF EXISTS lifecycle_updated_at;
ALTER TABLE servers DROP COLUMN IF EXISTS lifecycle_state;
DELETE FROM app_settings WHERE key IN
    ('subscription_version', 'ghost_auto_cleanup', 'ghost_auto_repair', 'server_archive_retention_days');
```

- [ ] **Step 3: Build verifies migrations load**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 4: Commit**

```bash
git add server/backend/migrations/000040_server_lifecycle.up.sql \
        server/backend/migrations/000040_server_lifecycle.down.sql
git commit -m "feat(migration): server lifecycle_state column + subscription_version"
```

---

## Task 2: biz/server_lifecycle.go — FSM core + version bump (30 min)

**Files:**
- Create: `server/backend/internal/biz/server_lifecycle.go`

- [ ] **Step 1: Create the file with core types and helpers**

File: `server/backend/internal/biz/server_lifecycle.go`

```go
// Package biz — server lifecycle FSM.
//
// States: pending → provisioning → online | failed → deprovisioning → offline | deleted.
// "deleted" is not a stored value — it means the row was removed.
//
// All transitions are atomic SQL UPDATEs that check the previous state, so
// concurrent operations cannot corrupt the FSM. On RowsAffected=0 we return
// ErrInvalidTransition.
package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// Lifecycle states (must match CHECK constraint in migration 000040).
const (
	LifecyclePending        = "pending"
	LifecycleProvisioning   = "provisioning"
	LifecycleOnline         = "online"
	LifecycleFailed         = "failed"
	LifecycleDeprovisioning = "deprovisioning"
	LifecycleOffline        = "offline"
)

// ErrInvalidTransition is returned when the current DB state does not match
// the expected `from` value — typically a concurrent operation already moved
// the row, or the row was deleted.
var ErrInvalidTransition = errors.New("invalid lifecycle transition")

// ProvisionEvent is one progress signal emitted during Provision/Deprovision.
type ProvisionEvent struct {
	Stage     string    `json:"stage"`             // short slug, e.g. "ssh_exit_setup"
	Status    string    `json:"status"`            // pending|running|done|error|skipped
	Message   string    `json:"message,omitempty"` // human-readable detail
	Timestamp time.Time `json:"timestamp"`
}

// emit is a non-blocking helper used by Provision/Deprovision to send events.
// The channel is buffered by the caller; if full we drop the event rather than block.
func emit(ch chan<- ProvisionEvent, stage, status, message string) {
	if ch == nil {
		return
	}
	ev := ProvisionEvent{
		Stage:     stage,
		Status:    status,
		Message:   message,
		Timestamp: time.Now(),
	}
	select {
	case ch <- ev:
	default:
		// channel full — drop event (callers may consume slowly)
	}
}

// TransitionState atomically moves a server from `from` to `to`.
// Returns ErrInvalidTransition if the row's current state ≠ `from`.
func TransitionState(serverID int, from, to string) error {
	res, err := db.DB.Exec(`
		UPDATE servers
		   SET lifecycle_state = $1,
		       lifecycle_updated_at = NOW(),
		       updated_at = CURRENT_TIMESTAMP
		 WHERE id = $2 AND lifecycle_state = $3
	`, to, serverID, from)
	if err != nil {
		return fmt.Errorf("transition state: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrInvalidTransition
	}
	return nil
}

// GetLifecycleState returns the current lifecycle state of a server.
func GetLifecycleState(serverID int) (string, error) {
	var state string
	err := db.DB.QueryRow(`SELECT lifecycle_state FROM servers WHERE id=$1`, serverID).Scan(&state)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("server %d not found", serverID)
	}
	return state, err
}

// ── Subscription version (signals user-side clients to refetch) ─────────────

var subscriptionVersionCache struct {
	sync.Mutex
	value int64
	at    time.Time
}

// GetSubscriptionVersion returns the cached version, refreshing every 5 sec.
// Cheap to call from API hot path.
func GetSubscriptionVersion() int64 {
	subscriptionVersionCache.Lock()
	defer subscriptionVersionCache.Unlock()
	if time.Since(subscriptionVersionCache.at) < 5*time.Second && subscriptionVersionCache.value > 0 {
		return subscriptionVersionCache.value
	}
	var raw string
	_ = db.DB.QueryRow(`SELECT value FROM app_settings WHERE key='subscription_version'`).Scan(&raw)
	v, _ := strconv.ParseInt(raw, 10, 64)
	if v <= 0 {
		v = 1
	}
	subscriptionVersionCache.value = v
	subscriptionVersionCache.at = time.Now()
	return v
}

// BumpSubscriptionVersion increments the version atomically and invalidates the cache.
// Called from every successful Provision/Deprovision.
func BumpSubscriptionVersion() {
	_, err := db.DB.Exec(`
		UPDATE app_settings
		   SET value = (COALESCE(NULLIF(value, '')::bigint, 0) + 1)::text
		 WHERE key = 'subscription_version'
	`)
	if err != nil {
		// non-fatal — clients will see stale version, next bump will sync
		return
	}
	subscriptionVersionCache.Lock()
	subscriptionVersionCache.at = time.Time{} // invalidate
	subscriptionVersionCache.Unlock()
}

// ── Provision/Deprovision are implemented in this file in Tasks 5 and 6 ─────
// They consume the FSM + ProvisionEvent + helpers from internal/biz/hub_provisioner.go.

// ProvisionTimeout is the upper bound for a single Provision run.
const ProvisionTimeout = 5 * time.Minute

// DeprovisionTimeout is the upper bound for a single Deprovision run.
const DeprovisionTimeout = 90 * time.Second

// withTimeout wraps the parent context with the given timeout.
func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, d)
}
```

- [ ] **Step 2: Build to verify**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/server_lifecycle.go
git commit -m "feat(biz): lifecycle FSM core + subscription_version cache"
```

---

## Task 3: biz/hub_provisioner.go — hub-side ops (45 min)

**Files:**
- Create: `server/backend/internal/biz/hub_provisioner.go`

**Context:** heyearth-api runs ON the hub as root, so it can `os/exec` systemd commands directly. The Hysteria2 client config template + systemd unit template mirror those in `cmd/deploy/main.go:2080`+. Each function is idempotent — it checks current state before acting.

- [ ] **Step 1: Create the file**

File: `server/backend/internal/biz/hub_provisioner.go`

```go
// Package biz — hub-side provisioner.
//
// heyearth-api runs ON the hub as root, so we use os/exec for systemd
// operations and ioutil for file writes. Every function is idempotent
// (checks current state before acting) so retrying an interrupted flow is safe.
//
// All artifacts (.service files, .yaml configs, archives) live under well-known
// paths so the Ghost Reconciler can scan them.
package biz

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	hubHysteriaConfigDir = "/opt/heyearth/hysteria"
	hubSystemdDir        = "/etc/systemd/system"
	hubArchiveBase       = "/opt/heyearth/state/server-archive"
)

// HysteriaClientConfig is the data needed to render the client.yaml template.
type HysteriaClientConfig struct {
	ServerID  int
	ExitIP    string
	ExitPort  int    // Hysteria2 server port range, usually "20000-50000" passed via ExitPortRange
	AuthKey   string // matches Hysteria2 server `auth.password`
	SocksPort int    // local SOCKS5 listener (1088 + ServerID)
}

// hysteriaClientYAML returns the rendered config for a given server.
// Pattern mirrors cmd/deploy/main.go:2080+; updated to also include the
// P1 ACL block (private + BT port rejects) so new servers inherit P1 hardening.
func hysteriaClientYAML(c HysteriaClientConfig) string {
	return fmt.Sprintf(`# Generated by heyearth-api hub_provisioner.go for server id=%d
server: %s:20000-50000

auth: %s

tls:
  insecure: true

transport:
  udp:
    hopInterval: 30s

quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520

bandwidth:
  up: 100 mbps
  down: 100 mbps

socks5:
  listen: 127.0.0.1:%d

# ─── ACL: P1-hardening block-list ───
# Reject private CIDRs + port 0 + BitTorrent ports at the client side.
acl:
  inline:
    - reject(10.0.0.0/8)
    - reject(172.16.0.0/12)
    - reject(192.168.0.0/16)
    - reject(127.0.0.0/8)
    - reject(all, tcp/0)
    - reject(all, udp/0)
    - reject(all, tcp/6881-6889)
    - reject(all, udp/6881-6889)
    - reject(all, tcp/51413)
    - reject(all, udp/51413)
    - direct(all)
`, c.ServerID, c.ExitIP, c.AuthKey, c.SocksPort)
}

// hysteriaServiceUnit returns the systemd unit for hysteria2-client-<id>.service.
func hysteriaServiceUnit(serverID int, exitIP string) string {
	return fmt.Sprintf(`[Unit]
Description=Hysteria2 Client for server id=%d (%s) — HeYearth
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/hysteria client -c %s/client-%d.yaml
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, serverID, exitIP, hubHysteriaConfigDir, serverID)
}

// WriteHysteriaClientConfig writes /opt/heyearth/hysteria/client-<id>.yaml.
// Idempotent — overwrites any existing file. Mode 0600 (contains auth secret).
func WriteHysteriaClientConfig(c HysteriaClientConfig) error {
	if err := os.MkdirAll(hubHysteriaConfigDir, 0o755); err != nil {
		return fmt.Errorf("mkdir hysteria dir: %w", err)
	}
	path := filepath.Join(hubHysteriaConfigDir, fmt.Sprintf("client-%d.yaml", c.ServerID))
	if err := os.WriteFile(path, []byte(hysteriaClientYAML(c)), 0o600); err != nil {
		return fmt.Errorf("write client-%d.yaml: %w", c.ServerID, err)
	}
	return nil
}

// InstallSystemdUnit writes /etc/systemd/system/hysteria2-client-<id>.service
// and runs `systemctl daemon-reload`. Idempotent.
func InstallSystemdUnit(serverID int, exitIP string) error {
	unitName := fmt.Sprintf("hysteria2-client-%d.service", serverID)
	path := filepath.Join(hubSystemdDir, unitName)
	if err := os.WriteFile(path, []byte(hysteriaServiceUnit(serverID, exitIP)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", unitName, err)
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	return nil
}

// EnableAndStartUnit enables + starts hysteria2-client-<id>. Idempotent.
func EnableAndStartUnit(serverID int) error {
	unitName := fmt.Sprintf("hysteria2-client-%d", serverID)
	// enable (silently ok if already enabled)
	if err := exec.Command("systemctl", "enable", unitName).Run(); err != nil {
		return fmt.Errorf("enable %s: %w", unitName, err)
	}
	// start (silently ok if already running)
	if err := exec.Command("systemctl", "restart", unitName).Run(); err != nil {
		return fmt.Errorf("restart %s: %w", unitName, err)
	}
	// short readiness wait
	time.Sleep(2 * time.Second)
	if err := exec.Command("systemctl", "is-active", "--quiet", unitName).Run(); err != nil {
		return fmt.Errorf("%s did not become active", unitName)
	}
	return nil
}

// StopAndDisableUnit stops + disables hysteria2-client-<id>. Idempotent (no error
// if already stopped/disabled or if the unit does not exist).
func StopAndDisableUnit(serverID int) error {
	unitName := fmt.Sprintf("hysteria2-client-%d", serverID)
	// stop if active
	if exec.Command("systemctl", "is-active", "--quiet", unitName).Run() == nil {
		_ = exec.Command("systemctl", "stop", unitName).Run()
	}
	// disable if enabled
	if exec.Command("systemctl", "is-enabled", "--quiet", unitName).Run() == nil {
		_ = exec.Command("systemctl", "disable", unitName).Run()
	}
	return nil
}

// ArchiveServerArtefacts moves the .service + .yaml + last 100 journal lines
// to /opt/heyearth/state/server-archive/<id>-<unix-ts>/.
// Returns the archive directory path. Idempotent — if files are already gone,
// it just creates the directory and writes the journal snapshot.
func ArchiveServerArtefacts(serverID int) (string, error) {
	ts := time.Now().Unix()
	archiveDir := filepath.Join(hubArchiveBase, fmt.Sprintf("%d-%d", serverID, ts))
	if err := os.MkdirAll(archiveDir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir archive: %w", err)
	}
	// move .yaml if exists
	yamlSrc := filepath.Join(hubHysteriaConfigDir, fmt.Sprintf("client-%d.yaml", serverID))
	yamlDst := filepath.Join(archiveDir, fmt.Sprintf("client-%d.yaml.bak", serverID))
	if _, err := os.Stat(yamlSrc); err == nil {
		_ = os.Rename(yamlSrc, yamlDst)
	}
	// move .service if exists
	svcSrc := filepath.Join(hubSystemdDir, fmt.Sprintf("hysteria2-client-%d.service", serverID))
	svcDst := filepath.Join(archiveDir, fmt.Sprintf("hysteria2-client-%d.service.bak", serverID))
	if _, err := os.Stat(svcSrc); err == nil {
		_ = os.Rename(svcSrc, svcDst)
	}
	// snapshot last 100 journal lines (best-effort)
	jLog := filepath.Join(archiveDir, "journal-snapshot.log")
	cmd := exec.Command("journalctl", "-u", fmt.Sprintf("hysteria2-client-%d", serverID), "-n", "100", "--no-pager")
	out, _ := cmd.CombinedOutput()
	_ = os.WriteFile(jLog, out, 0o644)
	return archiveDir, nil
}

// RemoveSystemdUnitFile removes the .service file (after stop+disable+archive)
// and runs daemon-reload. Idempotent.
func RemoveSystemdUnitFile(serverID int) error {
	unitPath := filepath.Join(hubSystemdDir, fmt.Sprintf("hysteria2-client-%d.service", serverID))
	if _, err := os.Stat(unitPath); err == nil {
		if err := os.Remove(unitPath); err != nil {
			return fmt.Errorf("remove %s: %w", unitPath, err)
		}
	}
	return exec.Command("systemctl", "daemon-reload").Run()
}

// KillStaleProcess sends SIGKILL to any hysteria process whose -c argument
// points to client-<id>.yaml. Last-resort fallback if systemctl stop failed.
func KillStaleProcess(serverID int) {
	// pgrep -f returns matching PIDs
	pattern := fmt.Sprintf("client-%d.yaml", serverID)
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	if err != nil {
		return
	}
	for _, pidStr := range splitLines(string(out)) {
		_ = exec.Command("kill", "-9", pidStr).Run()
	}
}

// ListHubHysteriaUnits returns the server IDs whose hysteria2-client-<id>.service
// file currently exists on the hub. Used by the Ghost Reconciler.
func ListHubHysteriaUnits() ([]int, error) {
	pattern := filepath.Join(hubSystemdDir, "hysteria2-client-*.service")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, m := range matches {
		var id int
		_, err := fmt.Sscanf(filepath.Base(m), "hysteria2-client-%d.service", &id)
		if err != nil || id <= 0 {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
```

- [ ] **Step 2: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/hub_provisioner.go
git commit -m "feat(biz): hub_provisioner — idempotent hub-side ops for hysteria client units"
```

---

## Task 4: biz/exit_teardown.go — best-effort SSH (20 min)

**Files:**
- Create: `server/backend/internal/biz/exit_teardown.go`

**Context:** When we deprovision a server, we *try* to SSH and stop services on the exit-node, but if SSH fails (VPS already decommissioned, network blip, key revoked) we log a warning and proceed with hub-side cleanup. Total budget: 10 sec.

- [ ] **Step 1: Create the file**

File: `server/backend/internal/biz/exit_teardown.go`

```go
// Package biz — best-effort SSH teardown on exit-nodes.
//
// We never block hub-side cleanup on exit-node reachability. If the SSH
// connection fails within ~10 seconds we log a warning and the caller
// continues. This protects us from VPS-already-decommissioned scenarios.
package biz

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

const exitTeardownTimeout = 10 * time.Second

// TeardownExitNode SSH-es into the exit-node and stops xray, hysteria2-server,
// node-agent. Returns nil only on full success; on any partial failure or
// timeout, returns a wrapped error which the caller is expected to log as a
// warning (NOT abort hub-side cleanup).
func TeardownExitNode(serverID int) error {
	ctx, cancel := context.WithTimeout(context.Background(), exitTeardownTimeout)
	defer cancel()

	cfg, err := loadServerSSHConfig(serverID)
	if err != nil {
		return fmt.Errorf("load ssh config: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", cfg.ip, cfg.port)
	dialer := net.Dialer{Timeout: exitTeardownTimeout}
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, cfg.sshConfig)
	if err != nil {
		_ = tcpConn.Close()
		return fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	script := "systemctl stop xray hysteria2-server node-agent 2>&1; " +
		"echo TEARDOWN_OK"
	out, runErr := runRemoteCommand(client, script, exitTeardownTimeout)
	if runErr != nil {
		return fmt.Errorf("remote stop: %w (output=%s)", runErr, out)
	}
	if !bytes.Contains(out, []byte("TEARDOWN_OK")) {
		return fmt.Errorf("teardown did not complete cleanly: %s", out)
	}
	log.Printf("[exit-teardown] server %d (%s): services stopped OK", serverID, cfg.ip)
	return nil
}

type exitSSHConfig struct {
	ip        string
	port      int
	sshConfig *ssh.ClientConfig
}

// loadServerSSHConfig reads ssh credentials from the servers row and decrypts the password.
// Mirrors sshDialForServer() in internal/admin but lives in biz so deprovision can use it
// without depending on the admin package.
func loadServerSSHConfig(serverID int) (exitSSHConfig, error) {
	var (
		ip       string
		port     int
		user     string
		passEnc  []byte
		hostKey  string
	)
	err := db.DB.QueryRow(`
		SELECT ip_address, COALESCE(ssh_port, 22), COALESCE(ssh_user, 'root'),
		       COALESCE(ssh_password_enc, ''::bytea), COALESCE(ssh_host_key, '')
		  FROM servers WHERE id=$1
	`, serverID).Scan(&ip, &port, &user, &passEnc, &hostKey)
	if err != nil {
		return exitSSHConfig{}, err
	}
	password, err := crypto.DecryptString(passEnc)
	if err != nil {
		return exitSSHConfig{}, fmt.Errorf("decrypt ssh password: %w", err)
	}
	hostKeyCb := ssh.InsecureIgnoreHostKey()
	if hostKey != "" {
		pub, _, _, _, perr := ssh.ParseAuthorizedKey([]byte(hostKey))
		if perr == nil {
			hostKeyCb = ssh.FixedHostKey(pub)
		}
	}
	return exitSSHConfig{
		ip:   ip,
		port: port,
		sshConfig: &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: hostKeyCb,
			Timeout:         exitTeardownTimeout,
		},
	}, nil
}

// runRemoteCommand runs a single command and returns combined stdout+stderr.
func runRemoteCommand(client *ssh.Client, cmd string, timeout time.Duration) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case err := <-done:
		return buf.Bytes(), err
	case <-time.After(timeout):
		_ = session.Signal(ssh.SIGKILL)
		return buf.Bytes(), fmt.Errorf("ssh command timeout after %s", timeout)
	}
}
```

> **Note on `crypto.DecryptString`**: this is the existing helper used by `sshDialForServer` in the admin package — symmetric AES-256-GCM with key from env. If the exact signature differs (e.g. returns `([]byte, error)`), adjust the type conversion. Verify with `grep -n "func DecryptString" server/backend/internal/crypto/`.

- [ ] **Step 2: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success. If `crypto.DecryptString` signature differs, fix the call.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/exit_teardown.go
git commit -m "feat(biz): best-effort SSH teardown for exit-nodes (10s timeout)"
```

---

## Task 5: biz/server_lifecycle.go — Provision() (30 min)

**Files:**
- Modify: `server/backend/internal/biz/server_lifecycle.go` (append)

**Context:** Wires SSH-exit-setup (reuses existing `runSetupExitNode` flow lives in admin pkg — so we'll model Provision as **hub-side only**: it assumes the exit-node has been set up via the existing admin "setup_exit_node" action and we just need to install/start the hub-side tunnel). For now Provision is the **hub-only** half — admin still triggers exit-side via the existing `runSetupExitNode` flow which already does this. We separate hub-only Provision so it can be called from Ghost Reconciler auto-repair and from Deprovision-then-Reprovision flows.

- [ ] **Step 1: Append Provision function**

Append to `server/backend/internal/biz/server_lifecycle.go`:

```go
// HubProvisionExisting installs hub-side tunnel artefacts for a server whose
// exit-side is already set up (i.e. tunnel_uuid + tunnel_ss_password are set
// in the DB). This is the path used by:
//   - Admin "Provision" button when exit-side was set up via cmd/deploy first
//   - Ghost Reconciler auto-repair when hub-side files are missing
//
// Sets lifecycle_state online → no other transitions; idempotent.
//
// progressCh is optional (may be nil).
func HubProvisionExisting(serverID int, progressCh chan<- ProvisionEvent) error {
	emit(progressCh, "load_server", "running", "Загружаем данные сервера")

	var (
		exitIP     string
		ssPassword string
	)
	err := db.DB.QueryRow(`
		SELECT ip_address, COALESCE(tunnel_ss_password, '')
		  FROM servers WHERE id=$1 AND is_exit_node = TRUE
	`, serverID).Scan(&exitIP, &ssPassword)
	if err != nil {
		emit(progressCh, "load_server", "error", err.Error())
		return fmt.Errorf("load server %d: %w", serverID, err)
	}
	if ssPassword == "" {
		emit(progressCh, "load_server", "error", "tunnel_ss_password пуст — сначала запустите setup_exit_node")
		return fmt.Errorf("server %d has no tunnel credentials yet", serverID)
	}

	// We don't strictly enforce the from-state here because this function is
	// called by admin's existing "Provision" action which today completes outside
	// of our FSM. We *do* bump to provisioning if currently pending/offline/failed,
	// and bump to online at the end.
	current, err := GetLifecycleState(serverID)
	if err != nil {
		return err
	}
	switch current {
	case LifecyclePending, LifecycleOffline, LifecycleFailed:
		_ = TransitionState(serverID, current, LifecycleProvisioning)
	case LifecycleProvisioning, LifecycleOnline:
		// proceed (idempotent re-run)
	default:
		emit(progressCh, "transition", "error", fmt.Sprintf("invalid current state %q", current))
		return ErrInvalidTransition
	}

	emit(progressCh, "write_config", "running", "Создаём client-N.yaml")
	if err := WriteHysteriaClientConfig(HysteriaClientConfig{
		ServerID:  serverID,
		ExitIP:    exitIP,
		AuthKey:   ssPassword,
		SocksPort: 1088 + serverID,
	}); err != nil {
		emit(progressCh, "write_config", "error", err.Error())
		_ = TransitionState(serverID, LifecycleProvisioning, LifecycleFailed)
		return err
	}
	emit(progressCh, "write_config", "done", "")

	emit(progressCh, "install_unit", "running", "Устанавливаем systemd-юнит")
	if err := InstallSystemdUnit(serverID, exitIP); err != nil {
		emit(progressCh, "install_unit", "error", err.Error())
		_ = TransitionState(serverID, LifecycleProvisioning, LifecycleFailed)
		return err
	}
	emit(progressCh, "install_unit", "done", "")

	emit(progressCh, "start_unit", "running", "Запускаем туннель")
	if err := EnableAndStartUnit(serverID); err != nil {
		emit(progressCh, "start_unit", "error", err.Error())
		_ = TransitionState(serverID, LifecycleProvisioning, LifecycleFailed)
		return err
	}
	emit(progressCh, "start_unit", "done", "")

	BumpSubscriptionVersion()
	emit(progressCh, "subscription_bump", "done", "")

	// best-effort xray re-sync via external trigger written to file watcher /
	// or signal — we'll just call sync hook in Task 7 (after we modify server.go
	// filter); for now leave it to admin restart pattern.
	if err := TransitionState(serverID, LifecycleProvisioning, LifecycleOnline); err != nil {
		// possibly already online (idempotent); read current and accept online
		s, _ := GetLifecycleState(serverID)
		if s != LifecycleOnline {
			emit(progressCh, "transition_online", "error", err.Error())
			return err
		}
	}
	emit(progressCh, "done", "done", "Сервер онлайн")
	return nil
}
```

- [ ] **Step 2: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/server_lifecycle.go
git commit -m "feat(biz): HubProvisionExisting — hub-side tunnel provisioner"
```

---

## Task 6: biz/server_lifecycle.go — Deprovision() (30 min)

**Files:**
- Modify: `server/backend/internal/biz/server_lifecycle.go` (append)

**Context:** Hard mode deletes the row at the end; Soft mode leaves the row at `offline` so it can be re-provisioned later. Both archive artefacts.

- [ ] **Step 1: Append Deprovision function and helpers**

Append to `server/backend/internal/biz/server_lifecycle.go`:

```go
// DeprovisionMode controls whether the row is deleted (hard) or kept (soft).
type DeprovisionMode string

const (
	DeprovisionSoft DeprovisionMode = "soft"
	DeprovisionHard DeprovisionMode = "hard"
)

// Deprovision tears down a server's hub-side artefacts, attempts a best-effort
// exit-side teardown, and either marks the row offline (soft) or deletes it (hard).
//
// Order (matters):
//   1. transition online → deprovisioning
//   2. bump subscription_version + force xray re-sync (callers re-route away)
//   3. wait 3 sec for in-flight tunneling to drain
//   4. systemctl stop hysteria2-client-N (with kill -9 fallback)
//   5. archive .yaml + .service + journal snapshot
//   6. systemctl disable + remove .service + daemon-reload
//   7. best-effort SSH teardown of exit-node services
//   8. if hard: DELETE FROM servers; if soft: TransitionState → offline
//
// progressCh is optional (may be nil).
func Deprovision(serverID int, mode DeprovisionMode, progressCh chan<- ProvisionEvent) error {
	current, err := GetLifecycleState(serverID)
	if err != nil {
		return err
	}
	// Allow deprovision from online / failed / provisioning / pending (defensive).
	// Forbid from already-deprovisioning (concurrent op) or offline (no-op).
	if current == LifecycleDeprovisioning {
		return fmt.Errorf("server %d already deprovisioning", serverID)
	}
	if current == LifecycleOffline && mode == DeprovisionSoft {
		emit(progressCh, "noop", "done", "уже offline, нечего делать")
		return nil
	}

	if err := TransitionState(serverID, current, LifecycleDeprovisioning); err != nil {
		return fmt.Errorf("enter deprovisioning: %w", err)
	}
	emit(progressCh, "transition_deprovisioning", "done", "")

	// 2 + 3: bump version so clients re-fetch, then drain.
	BumpSubscriptionVersion()
	emit(progressCh, "subscription_bump", "done", "")
	emit(progressCh, "drain", "running", "Ждём 3 сек, чтобы балансировщик увёл клиентов")
	time.Sleep(3 * time.Second)
	emit(progressCh, "drain", "done", "")

	// 4: stop hub-side service.
	emit(progressCh, "hub_stop", "running", "Останавливаем hysteria2-client-N")
	if err := StopAndDisableUnit(serverID); err != nil {
		emit(progressCh, "hub_stop", "error", err.Error())
		KillStaleProcess(serverID) // last resort
	}
	// Verify process really gone — kill anything left over.
	KillStaleProcess(serverID)
	emit(progressCh, "hub_stop", "done", "")

	// 5: archive.
	emit(progressCh, "archive", "running", "Архивируем конфиг и юнит")
	archivePath, archErr := ArchiveServerArtefacts(serverID)
	if archErr != nil {
		emit(progressCh, "archive", "error", archErr.Error())
		// Defense: if we couldn't archive, do NOT remove originals.
		_ = TransitionState(serverID, LifecycleDeprovisioning, LifecycleFailed)
		return archErr
	}
	emit(progressCh, "archive", "done", archivePath)

	// 6: remove unit file + daemon-reload.
	emit(progressCh, "remove_unit", "running", "Удаляем .service файл")
	if err := RemoveSystemdUnitFile(serverID); err != nil {
		emit(progressCh, "remove_unit", "error", err.Error())
		// archive succeeded — file is moved; log and proceed.
	} else {
		emit(progressCh, "remove_unit", "done", "")
	}

	// 7: best-effort exit-side teardown (must not block).
	emit(progressCh, "exit_teardown", "running", "Лучшая попытка остановить сервисы на exit")
	if err := TeardownExitNode(serverID); err != nil {
		log.Printf("[deprovision] server %d: exit teardown failed (best-effort): %v", serverID, err)
		emit(progressCh, "exit_teardown", "skipped", err.Error())
	} else {
		emit(progressCh, "exit_teardown", "done", "")
	}

	// 8: finalize.
	if mode == DeprovisionHard {
		emit(progressCh, "delete_row", "running", "Удаляем запись из БД")
		if _, err := db.DB.Exec(`DELETE FROM servers WHERE id=$1`, serverID); err != nil {
			emit(progressCh, "delete_row", "error", err.Error())
			_ = TransitionState(serverID, LifecycleDeprovisioning, LifecycleFailed)
			return err
		}
		emit(progressCh, "delete_row", "done", "")
		BumpSubscriptionVersion() // bump again now that row is gone
		emit(progressCh, "done", "done", "Сервер удалён")
		return nil
	}

	// soft → offline
	if err := TransitionState(serverID, LifecycleDeprovisioning, LifecycleOffline); err != nil {
		// rare: row was deleted concurrently — accept
		s, _ := GetLifecycleState(serverID)
		if s != "" {
			emit(progressCh, "transition_offline", "error", err.Error())
			return err
		}
	}
	BumpSubscriptionVersion()
	emit(progressCh, "done", "done", "Сервер отключён (offline)")
	return nil
}
```

> **Note:** add `"log"` to imports at the top of `server_lifecycle.go` if not already present.

- [ ] **Step 2: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/server_lifecycle.go
git commit -m "feat(biz): Deprovision — hub teardown + best-effort exit + archive"
```

---

## Task 7: biz/server.go — filter lifecycle in ListHealthyExitNodes (15 min)

**Files:**
- Modify: `server/backend/internal/biz/server.go:198-215` (the `listExitNodesByStatus` function)

- [ ] **Step 1: Add lifecycle filter**

Edit `server/backend/internal/biz/server.go`. Replace lines around 198-215 (the function body of `listExitNodesByStatus`):

Find:
```go
func listExitNodesByStatus(statusFilter string) ([]ExitNodeTunnel, error) {
	query := `
		SELECT id, COALESCE(name, ''), ip_address,
		       tunnel_protocol, tunnel_port,
		       COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''),
		       COALESCE(tunnel_short_id, ''), COALESCE(tunnel_sni, ''),
		       COALESCE(tunnel_ss_method, ''), COALESCE(tunnel_ss_password, ''),
		       tunnel_status, COALESCE(load_weight, 1)
		FROM servers
		WHERE is_exit_node = TRUE
	`
```

Replace with:
```go
func listExitNodesByStatus(statusFilter string) ([]ExitNodeTunnel, error) {
	// Defense-in-depth: only return servers whose lifecycle is 'online'.
	// tunnel_status alone is not enough — a server marked offline in the FSM
	// must never appear in xray routing (otherwise we get ghosts).
	query := `
		SELECT id, COALESCE(name, ''), ip_address,
		       tunnel_protocol, tunnel_port,
		       COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''),
		       COALESCE(tunnel_short_id, ''), COALESCE(tunnel_sni, ''),
		       COALESCE(tunnel_ss_method, ''), COALESCE(tunnel_ss_password, ''),
		       tunnel_status, COALESCE(load_weight, 1)
		FROM servers
		WHERE is_exit_node = TRUE
		  AND lifecycle_state = 'online'
	`
```

- [ ] **Step 2: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 3: Commit**

```bash
git add server/backend/internal/biz/server.go
git commit -m "fix(biz): ListHealthyExitNodes filters lifecycle_state='online' (defense-in-depth)"
```

---

## Task 8: admin handlers — DeleteServer + new actions (30 min)

**Files:**
- Modify: `server/backend/internal/admin/servers_handlers.go:335-347` (DeleteServerHandler)
- Modify: `server/backend/internal/admin/servers_handlers.go:377-414` (ServerActionHandler switch)

- [ ] **Step 1: Replace DeleteServerHandler with async deprovision**

In `server/backend/internal/admin/servers_handlers.go`, find:

```go
func DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM servers WHERE id = $1`, serverID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete server")
		return
	}
	WriteAuditLog(r, "delete_server", "server", fmt.Sprintf("%d", serverID), nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Server deleted"})
}
```

Replace with:

```go
func DeleteServerHandler(w http.ResponseWriter, r *http.Request) {
	serverID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid server id")
		return
	}
	// Reject if another provision/deprovision is already in flight.
	if v, ok := provisionProgress.Load(serverID); ok {
		pp := v.(*ProvisionProgress)
		if !pp.Finished {
			writeAdminError(w, http.StatusConflict, "Operation already in progress")
			return
		}
	}
	WriteAuditLog(r, "delete_server", "server", fmt.Sprintf("%d", serverID), nil)
	go runDeprovision(serverID, biz.DeprovisionHard)
	writeAdminJSON(w, http.StatusAccepted, map[string]string{
		"message":   "Deprovisioning started",
		"server_id": fmt.Sprintf("%d", serverID),
	})
}

// runDeprovision wraps biz.Deprovision with the same ProvisionProgress reporting
// pattern used by runSetupExitNode, so the existing /actions/progress endpoint
// keeps working unchanged for the frontend.
func runDeprovision(serverID int, mode biz.DeprovisionMode) {
	steps := []ProvisionStep{
		{Name: "transition_deprovisioning", Description: "Перевод в deprovisioning", Status: "pending"},
		{Name: "subscription_bump", Description: "Bump subscription_version", Status: "pending"},
		{Name: "drain", Description: "Drain 3 sec", Status: "pending"},
		{Name: "hub_stop", Description: "Остановка hysteria2-client-N", Status: "pending"},
		{Name: "archive", Description: "Архивирование .yaml + .service", Status: "pending"},
		{Name: "remove_unit", Description: "Удаление systemd-юнита", Status: "pending"},
		{Name: "exit_teardown", Description: "Лучшая попытка остановки на exit", Status: "pending"},
		{Name: "delete_row", Description: "Удаление записи из БД (hard)", Status: "pending"},
		{Name: "done", Description: "Готово", Status: "pending"},
	}
	progress := &ProvisionProgress{
		ServerID: serverID,
		Action:   fmt.Sprintf("deprovision_%s", string(mode)),
		Steps:    steps,
		Started:  time.Now(),
	}
	provisionProgress.Store(serverID, progress)

	stepIdx := map[string]int{}
	for i, s := range steps {
		stepIdx[s.Name] = i
	}
	ch := make(chan biz.ProvisionEvent, 32)

	// Consumer goroutine maps ProvisionEvent → ProvisionStep updates.
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for ev := range ch {
			idx, ok := stepIdx[ev.Stage]
			if !ok {
				continue
			}
			progress.Steps[idx].Status = ev.Status
			if ev.Message != "" {
				progress.Steps[idx].Error = ev.Message
			}
			provisionProgress.Store(serverID, progress)
		}
	}()

	err := biz.Deprovision(serverID, mode, ch)
	close(ch)
	<-doneCh

	if err != nil {
		log.Printf("[deprovision] server %d (%s): error: %v", serverID, mode, err)
	} else {
		log.Printf("[deprovision] server %d (%s): completed", serverID, mode)
	}
	progress.Finished = true
	provisionProgress.Store(serverID, progress)
}
```

> **Imports check:** ensure `"github.com/heyearth/backend/internal/biz"` is imported at the top of the file (it already is — used by other handlers).

- [ ] **Step 2: Add new ServerActionHandler cases**

In `server/backend/internal/admin/servers_handlers.go`, find the switch block in `ServerActionHandler` (around line 377) and add new cases just before `default:`:

Find:
```go
	case "fetch_tunnel_creds":
		if err := fetchTunnelCreds(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeAdminError(w, http.StatusBadRequest, "Unsupported server action")
		return
	}
```

Replace with:
```go
	case "fetch_tunnel_creds":
		if err := fetchTunnelCreds(server); err != nil {
			writeAdminError(w, http.StatusBadGateway, err.Error())
			return
		}
	case "deprovision_soft":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Operation already in progress")
				return
			}
		}
		go runDeprovision(serverID, biz.DeprovisionSoft)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Deprovisioning (soft) started"})
		return
	case "deprovision_hard":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Operation already in progress")
				return
			}
		}
		go runDeprovision(serverID, biz.DeprovisionHard)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Deprovisioning (hard) started"})
		return
	case "hub_provision":
		if v, ok := provisionProgress.Load(serverID); ok {
			pp := v.(*ProvisionProgress)
			if !pp.Finished {
				writeAdminError(w, http.StatusConflict, "Operation already in progress")
				return
			}
		}
		go runHubProvisionOnly(serverID)
		writeAdminJSON(w, http.StatusAccepted, map[string]string{"message": "Hub provisioning started"})
		return
	default:
		writeAdminError(w, http.StatusBadRequest, "Unsupported server action")
		return
	}
```

- [ ] **Step 3: Add runHubProvisionOnly helper**

Append to `server/backend/internal/admin/servers_handlers.go` (anywhere — recommended near `runSetupExitNode`):

```go
// runHubProvisionOnly is the async wrapper for biz.HubProvisionExisting.
// Used by the admin "Provision" button when the exit-side has already been set up
// (e.g. server row was created with credentials but hub-side wasn't synced, OR
// after a soft deprovision when re-enabling the same server).
func runHubProvisionOnly(serverID int) {
	steps := []ProvisionStep{
		{Name: "load_server", Description: "Загрузка данных сервера", Status: "pending"},
		{Name: "write_config", Description: "Создание client-N.yaml", Status: "pending"},
		{Name: "install_unit", Description: "Установка systemd-юнита", Status: "pending"},
		{Name: "start_unit", Description: "Запуск туннеля", Status: "pending"},
		{Name: "subscription_bump", Description: "Bump subscription_version", Status: "pending"},
		{Name: "done", Description: "Готово", Status: "pending"},
	}
	progress := &ProvisionProgress{
		ServerID: serverID,
		Action:   "hub_provision",
		Steps:    steps,
		Started:  time.Now(),
	}
	provisionProgress.Store(serverID, progress)

	stepIdx := map[string]int{}
	for i, s := range steps {
		stepIdx[s.Name] = i
	}
	ch := make(chan biz.ProvisionEvent, 32)
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		for ev := range ch {
			idx, ok := stepIdx[ev.Stage]
			if !ok {
				continue
			}
			progress.Steps[idx].Status = ev.Status
			if ev.Message != "" {
				progress.Steps[idx].Error = ev.Message
			}
			provisionProgress.Store(serverID, progress)
		}
	}()

	err := biz.HubProvisionExisting(serverID, ch)
	close(ch)
	<-doneCh

	if err != nil {
		log.Printf("[hub-provision] server %d: %v", serverID, err)
	} else {
		log.Printf("[hub-provision] server %d: ok", serverID)
	}
	progress.Finished = true
	provisionProgress.Store(serverID, progress)
}
```

- [ ] **Step 4: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 5: Commit**

```bash
git add server/backend/internal/admin/servers_handlers.go
git commit -m "feat(admin): DeleteServerHandler async deprovision + new lifecycle actions"
```

---

## Task 9: cmd/api/ghost_reconciler.go (30 min)

**Files:**
- Create: `server/backend/cmd/api/ghost_reconciler.go`

- [ ] **Step 1: Create the file**

File: `server/backend/cmd/api/ghost_reconciler.go`

```go
package main

import (
	"log"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Ghost Reconciler ───────────────────────
//
// Каждые 30 минут сканирует hub на предмет рассогласований между
// systemd-юнитами hysteria2-client-N.service и записями servers table.
//
// Два типа аномалий:
//   1. orphan: на диске есть .service файл, но row отсутствует или
//      lifecycle_state ∈ {offline}. Действие: WARN + (если app_settings.ghost_auto_cleanup=true)
//      → биз.Deprovision(soft). Soft, не hard, потому что мы не хотим удалить
//      запись если она вообще существует.
//   2. missing: row говорит lifecycle_state='online', а .service файла нет.
//      Действие: WARN + (если app_settings.ghost_auto_repair=true)
//      → биз.HubProvisionExisting (восстановит config + юнит).
//
// Цель: даже если кто-то напишет код с регрессией lifecycle, reconciler
// найдёт расхождение и в крайнем случае автоматически починит.

const ghostReconcilerInterval = 30 * time.Minute

func StartGhostReconciler() {
	go func() {
		time.Sleep(2 * time.Minute) // дать API и Xray устаканиться
		log.Println("[ghost-reconciler] запущен (каждые 30 минут)")
		runGhostReconciler() // первый сразу
		ticker := time.NewTicker(ghostReconcilerInterval)
		defer ticker.Stop()
		for range ticker.C {
			runGhostReconciler()
		}
	}()
}

func runGhostReconciler() {
	hubUnits, err := biz.ListHubHysteriaUnits()
	if err != nil {
		log.Printf("[ghost-reconciler] error listing hub units: %v", err)
		return
	}
	dbStates, err := loadServerLifecycleMap()
	if err != nil {
		log.Printf("[ghost-reconciler] error loading DB: %v", err)
		return
	}

	hubSet := make(map[int]bool, len(hubUnits))
	for _, id := range hubUnits {
		hubSet[id] = true
	}

	autoCleanup := strings.EqualFold(biz.GetSettingOrDefault("ghost_auto_cleanup", "true"), "true")
	autoRepair := strings.EqualFold(biz.GetSettingOrDefault("ghost_auto_repair", "false"), "true")

	var orphans, missing int

	// orphan: file exists but row missing or row says offline.
	for _, id := range hubUnits {
		state, exists := dbStates[id]
		if !exists {
			orphans++
			log.Printf("[ghost-reconciler] ORPHAN: hysteria2-client-%d.service exists, row missing", id)
			if autoCleanup {
				go runAutoCleanup(id)
			}
			continue
		}
		if state == biz.LifecycleOffline {
			orphans++
			log.Printf("[ghost-reconciler] ORPHAN: hysteria2-client-%d.service exists, lifecycle=offline", id)
			if autoCleanup {
				go runAutoCleanup(id)
			}
		}
	}

	// missing: row online but no file.
	for id, state := range dbStates {
		if state != biz.LifecycleOnline {
			continue
		}
		if !hubSet[id] {
			missing++
			log.Printf("[ghost-reconciler] MISSING: server %d lifecycle=online, no .service file", id)
			if autoRepair {
				go runAutoRepair(id)
			}
		}
	}

	log.Printf("[ghost-reconciler] scan: %d units, %d rows, %d orphans, %d missing (auto_cleanup=%v auto_repair=%v)",
		len(hubUnits), len(dbStates), orphans, missing, autoCleanup, autoRepair)
}

func loadServerLifecycleMap() (map[int]string, error) {
	rows, err := db.DB.Query(`SELECT id, lifecycle_state FROM servers WHERE is_exit_node = TRUE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int]string)
	for rows.Next() {
		var id int
		var state string
		if err := rows.Scan(&id, &state); err != nil {
			continue
		}
		out[id] = state
	}
	return out, rows.Err()
}

// runAutoCleanup is invoked as goroutine — uses soft mode to be safe.
// If the server row is gone entirely, we can't transition lifecycle, but we can
// still clean up hub-side artefacts directly.
func runAutoCleanup(serverID int) {
	log.Printf("[ghost-reconciler] auto-cleanup server %d", serverID)
	// Try the FSM path first if the row exists.
	if _, err := biz.GetLifecycleState(serverID); err == nil {
		if err := biz.Deprovision(serverID, biz.DeprovisionSoft, nil); err != nil {
			log.Printf("[ghost-reconciler] auto-cleanup server %d via FSM failed: %v", serverID, err)
		}
		return
	}
	// Row is gone → direct hub-side cleanup.
	_ = biz.StopAndDisableUnit(serverID)
	biz.KillStaleProcess(serverID)
	if path, err := biz.ArchiveServerArtefacts(serverID); err == nil {
		log.Printf("[ghost-reconciler] auto-cleanup server %d: archive at %s", serverID, path)
	}
	_ = biz.RemoveSystemdUnitFile(serverID)
}

func runAutoRepair(serverID int) {
	log.Printf("[ghost-reconciler] auto-repair server %d", serverID)
	if err := biz.HubProvisionExisting(serverID, nil); err != nil {
		log.Printf("[ghost-reconciler] auto-repair server %d failed: %v", serverID, err)
	}
}
```

- [ ] **Step 2: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 3: Commit**

```bash
git add server/backend/cmd/api/ghost_reconciler.go
git commit -m "feat(api): Ghost Reconciler — 30-min safety net against server-state drift"
```

---

## Task 10: Register Ghost Reconciler in main.go (10 min)

**Files:**
- Modify: `server/backend/cmd/api/main.go` (around line 845-863, where other Start* loops are registered)

- [ ] **Step 1: Locate insertion point**

```bash
cd server/backend && grep -n "StartSubscriptionCron\|StartTrafficStatsCollector\|StartAutoTuner" cmd/api/main.go
```
Expected: prints lines around 845-863.

- [ ] **Step 2: Add registration**

Find the existing line (number from grep above, e.g.):
```go
	StartXraySyncLoop()
```

Right after the block of `Start*` calls (after the last one — likely `StartDunningCron()`), add a blank line and:

```go

	// Ghost Reconciler: scan hub every 30 min for drift between systemd units
	// and DB lifecycle state. Optional auto-cleanup / auto-repair via app_settings.
	StartGhostReconciler()
```

- [ ] **Step 3: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/main.go
git commit -m "feat(api): register Ghost Reconciler in startup"
```

---

## Task 11: Public API — subscription_version in /api/v1/user/connections (15 min)

**Files:**
- Modify: handler serving `GET /api/v1/user/connections` (locate first)

- [ ] **Step 1: Locate handler**

```bash
cd server/backend && grep -rn '/api/v1/user/connections\|UserConnectionsHandler\|getConnections' cmd/api internal/ | head -10
```
Expected: gives the file:function. Likely `cmd/api/client_api.go` or similar.

- [ ] **Step 2: Add subscription_version to JSON response + header**

In the response struct, add a new field:
```go
type connectionsResponse struct {
	// ... existing fields ...
	SubscriptionVersion int64 `json:"subscription_version"`
}
```

In the handler body, before `json.NewEncoder(w).Encode(resp)`:
```go
ver := biz.GetSubscriptionVersion()
resp.SubscriptionVersion = ver
w.Header().Set("X-Subscription-Version", fmt.Sprintf("%d", ver))
```

> **Note:** the existing response struct name may differ. Adapt the field name to match. If the response is a `map[string]interface{}` rather than a struct, just add `m["subscription_version"] = ver`.

- [ ] **Step 3: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/
git commit -m "feat(api): expose subscription_version on /api/v1/user/connections"
```

---

## Task 12: Deploy-time script — migrate legacy hysteria2-client.service (20 min)

**Files:**
- Create: `server/backend/cmd/deploy/migrate-hysteria-naming.sh`

**Context:** The legacy `hysteria2-client.service` (no -N suffix) currently points to Amsterdam (server id=2). We rename it to `hysteria2-client-2.service` so the naming convention is uniform and Ghost Reconciler treats it consistently.

- [ ] **Step 1: Create the script**

File: `server/backend/cmd/deploy/migrate-hysteria-naming.sh`

```bash
#!/usr/bin/env bash
# One-time migration: rename legacy hysteria2-client.service → hysteria2-client-<id>.service.
# Idempotent: if no legacy unit exists, exits 0.
# Run ON the hub.

set -euo pipefail

LEGACY_UNIT="/etc/systemd/system/hysteria2-client.service"
HYSTERIA_DIR="/opt/heyearth/hysteria"
BACKUP_DIR="/opt/heyearth/state/legacy-hysteria-migration-$(date +%Y%m%d-%H%M%S)"

if [[ ! -f "$LEGACY_UNIT" ]]; then
    echo "[migrate-hysteria-naming] no legacy unit at $LEGACY_UNIT — nothing to do"
    exit 0
fi

# Identify which exit-node the legacy unit points to via the config it runs.
# The ExecStart line refers to /opt/heyearth/hysteria/client.yaml.
LEGACY_CONFIG="$HYSTERIA_DIR/client.yaml"
if [[ ! -f "$LEGACY_CONFIG" ]]; then
    echo "[migrate-hysteria-naming] legacy unit found but $LEGACY_CONFIG missing — manual fix required" >&2
    exit 1
fi

EXIT_IP=$(grep -E '^server:' "$LEGACY_CONFIG" | awk '{print $2}' | cut -d: -f1)
if [[ -z "$EXIT_IP" ]]; then
    echo "[migrate-hysteria-naming] could not parse server IP from $LEGACY_CONFIG" >&2
    exit 1
fi
echo "[migrate-hysteria-naming] legacy unit points to $EXIT_IP"

# Look up server id from DB.
SERVER_ID=$(sudo -u postgres psql -tA heyearth -c "SELECT id FROM servers WHERE ip_address='$EXIT_IP' AND is_exit_node=true LIMIT 1;")
SERVER_ID=$(echo "$SERVER_ID" | tr -d ' \n')
if [[ -z "$SERVER_ID" || ! "$SERVER_ID" =~ ^[0-9]+$ ]]; then
    echo "[migrate-hysteria-naming] could not find server id for $EXIT_IP" >&2
    exit 1
fi
echo "[migrate-hysteria-naming] mapped to server id=$SERVER_ID"

NEW_UNIT="/etc/systemd/system/hysteria2-client-${SERVER_ID}.service"
NEW_CONFIG="$HYSTERIA_DIR/client-${SERVER_ID}.yaml"

if [[ -f "$NEW_UNIT" ]]; then
    echo "[migrate-hysteria-naming] $NEW_UNIT already exists — assuming migration done; stopping legacy + cleaning up"
    systemctl stop hysteria2-client 2>/dev/null || true
    systemctl disable hysteria2-client 2>/dev/null || true
    mkdir -p "$BACKUP_DIR"
    mv "$LEGACY_UNIT" "$BACKUP_DIR/" 2>/dev/null || true
    mv "$LEGACY_CONFIG" "$BACKUP_DIR/" 2>/dev/null || true
    systemctl daemon-reload
    echo "[migrate-hysteria-naming] legacy artefacts archived to $BACKUP_DIR"
    exit 0
fi

mkdir -p "$BACKUP_DIR"
cp "$LEGACY_UNIT"   "$BACKUP_DIR/hysteria2-client.service.before-migration"
cp "$LEGACY_CONFIG" "$BACKUP_DIR/client.yaml.before-migration"
echo "[migrate-hysteria-naming] backups in $BACKUP_DIR"

# Stop legacy + disable
systemctl stop hysteria2-client
systemctl disable hysteria2-client

# Rename files. The unit's ExecStart references client.yaml; we need to update
# the file path inside the new unit too.
sed "s|client\\.yaml|client-${SERVER_ID}.yaml|g; s|Hysteria2 VPN Client.*|Hysteria2 Client for server id=${SERVER_ID} (${EXIT_IP}) — HeYearth|" \
    "$LEGACY_UNIT" > "$NEW_UNIT"
mv "$LEGACY_CONFIG" "$NEW_CONFIG"
rm "$LEGACY_UNIT"

systemctl daemon-reload
systemctl enable "hysteria2-client-${SERVER_ID}"
systemctl restart "hysteria2-client-${SERVER_ID}"
sleep 2
systemctl is-active "hysteria2-client-${SERVER_ID}" >/dev/null && \
    echo "[migrate-hysteria-naming] hysteria2-client-${SERVER_ID} is ACTIVE ✓" || {
        echo "[migrate-hysteria-naming] new unit did not start; restoring legacy"
        cp "$BACKUP_DIR/hysteria2-client.service.before-migration" "$LEGACY_UNIT"
        cp "$BACKUP_DIR/client.yaml.before-migration"             "$LEGACY_CONFIG"
        systemctl daemon-reload
        systemctl enable hysteria2-client
        systemctl restart hysteria2-client
        rm -f "$NEW_UNIT" "$NEW_CONFIG"
        exit 1
    }

echo "[migrate-hysteria-naming] DONE. Legacy at $BACKUP_DIR"
```

- [ ] **Step 2: Verify locally that bash parses (no run yet)**

```bash
cd server/backend && bash -n cmd/deploy/migrate-hysteria-naming.sh && echo OK
```
Expected: `OK` (means no syntax error).

- [ ] **Step 3: Commit**

```bash
chmod +x server/backend/cmd/deploy/migrate-hysteria-naming.sh
git add server/backend/cmd/deploy/migrate-hysteria-naming.sh
git commit -m "ops(deploy): one-time script migrate-hysteria-naming.sh for legacy unit"
```

> **NOTE: do NOT run this script yet — it goes after the new code is deployed (Task 14 below).**

---

## Task 13: Frontend admin — lifecycle badge + buttons + dialog (45 min)

**Files:**
- Create: `admin_web/src/components/ServerLifecycleBadge.tsx`
- Create: `admin_web/src/components/DeleteServerDialog.tsx`
- Modify: `admin_web/src/pages/ServerDetailPage.tsx`
- Modify: `admin_web/src/pages/ServersPage.tsx`

- [ ] **Step 1: ServerLifecycleBadge component**

File: `admin_web/src/components/ServerLifecycleBadge.tsx`

```tsx
import React from 'react'

type Props = { state: string }

const palette: Record<string, { bg: string; fg: string; label: string }> = {
  pending:        { bg: 'bg-gray-500/15',   fg: 'text-gray-300',   label: 'Pending'        },
  provisioning:   { bg: 'bg-blue-500/15',   fg: 'text-blue-300',   label: 'Provisioning…'  },
  online:         { bg: 'bg-emerald-500/15',fg: 'text-emerald-300',label: 'Online'         },
  failed:         { bg: 'bg-red-500/15',    fg: 'text-red-300',    label: 'Failed'         },
  deprovisioning: { bg: 'bg-amber-500/15',  fg: 'text-amber-300',  label: 'Deprovisioning…'},
  offline:        { bg: 'bg-slate-500/15',  fg: 'text-slate-300',  label: 'Offline'        },
}

export default function ServerLifecycleBadge({ state }: Props) {
  const p = palette[state] || palette.pending
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-xs font-medium ${p.bg} ${p.fg}`}>
      <span className="w-1.5 h-1.5 rounded-full bg-current opacity-70" />
      {p.label}
    </span>
  )
}
```

- [ ] **Step 2: DeleteServerDialog component**

File: `admin_web/src/components/DeleteServerDialog.tsx`

```tsx
import React, { useState } from 'react'

type Props = {
  serverId: number
  serverName: string
  onConfirm: () => void
  onCancel: () => void
}

export default function DeleteServerDialog({ serverId, serverName, onConfirm, onCancel }: Props) {
  const [typed, setTyped] = useState('')
  const expected = `delete-${serverId}`
  const ok = typed.trim() === expected
  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50">
      <div className="bg-bg-surface border border-border rounded-xl p-6 max-w-md w-full">
        <h3 className="text-lg font-semibold mb-2">Удалить сервер #{serverId}?</h3>
        <p className="text-sm text-text-secondary mb-4">
          Сервер «{serverName}» будет полностью удалён: остановлен hysteria2-клиент на хабе,
          выполнена попытка остановки сервисов на самой ноде, артефакты сохранены в архив,
          запись в БД удалена. Это действие необратимо.
        </p>
        <p className="text-sm mb-2">
          Для подтверждения введите <code className="text-text-primary bg-bg-app px-1 rounded">{expected}</code>:
        </p>
        <input
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          className="w-full px-3 py-2 bg-bg-app border border-border rounded-md text-sm mb-4"
          placeholder={expected}
          autoFocus
        />
        <div className="flex justify-end gap-2">
          <button onClick={onCancel} className="px-4 py-2 text-sm rounded-md border border-border hover:bg-bg-app">
            Отмена
          </button>
          <button
            onClick={onConfirm}
            disabled={!ok}
            className="px-4 py-2 text-sm rounded-md bg-red-600 text-white disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Удалить навсегда
          </button>
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Wire badge + buttons + dialog into ServerDetailPage**

In `admin_web/src/pages/ServerDetailPage.tsx`:

a) Add imports near other component imports:
```tsx
import ServerLifecycleBadge from '../components/ServerLifecycleBadge'
import DeleteServerDialog from '../components/DeleteServerDialog'
```

b) Add state and handlers (find a place near other action handlers):
```tsx
const [confirmDelete, setConfirmDelete] = useState(false)

async function doDeprovisionSoft() {
  if (!confirm('Перевести сервер в offline (hub-side cleanup, запись сохранится)?')) return
  await fetch(`/api/admin/servers/${server.id}/actions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ action: 'deprovision_soft' }),
  })
  // existing progress modal will start polling automatically
}

async function doDeleteHard() {
  setConfirmDelete(false)
  await fetch(`/api/admin/servers/${server.id}`, {
    method: 'DELETE',
    headers: { Authorization: `Bearer ${token}` },
  })
}

async function doHubProvision() {
  await fetch(`/api/admin/servers/${server.id}/actions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` },
    body: JSON.stringify({ action: 'hub_provision' }),
  })
}
```

c) In the header area where status is rendered (look for the `status` display), add the badge:
```tsx
<ServerLifecycleBadge state={server.lifecycle_state || 'online'} />
```

d) In the actions section, add the new buttons (the existing `setup_exit_node` / `reprovision` button stays):
```tsx
<button onClick={doHubProvision} className="...primary button styling...">
  Provision (hub-only)
</button>
<button onClick={doDeprovisionSoft} className="...amber button styling...">
  Disable (soft)
</button>
<button onClick={() => setConfirmDelete(true)} className="...red button styling...">
  Delete (hard)
</button>
{confirmDelete && (
  <DeleteServerDialog
    serverId={server.id}
    serverName={server.name}
    onConfirm={doDeleteHard}
    onCancel={() => setConfirmDelete(false)}
  />
)}
```

> **NOTE on button styling:** use the same Tailwind classes already in this page for primary/danger buttons. Don't introduce new design tokens.

- [ ] **Step 4: Add lifecycle column to ServersPage**

In `admin_web/src/pages/ServersPage.tsx`:

a) Add import: `import ServerLifecycleBadge from '../components/ServerLifecycleBadge'`

b) In the table header row, add a new `<th>` for "Lifecycle" (between Country and Status or wherever fits the existing layout).

c) In the table body row, add a `<td>`:
```tsx
<td className="px-3 py-2">
  <ServerLifecycleBadge state={s.lifecycle_state || 'online'} />
</td>
```

> **NOTE:** the `GetServersHandler` already returns the full row (uses `SELECT *` patterns in loadServerByID); it will include `lifecycle_state` automatically after migration 000040.

- [ ] **Step 5: Build admin_web**

```bash
cd admin_web && npm run build
```
Expected: `dist/` populated, no TypeScript errors.

- [ ] **Step 6: Commit**

```bash
git add admin_web/src/components/ServerLifecycleBadge.tsx \
        admin_web/src/components/DeleteServerDialog.tsx \
        admin_web/src/pages/ServerDetailPage.tsx \
        admin_web/src/pages/ServersPage.tsx
git commit -m "feat(admin-ui): lifecycle badge + Disable/Delete buttons + confirm dialog"
```

---

## Task 14: Frontend site — Available Locations widget + toast (30 min)

**Files:**
- Modify: `site/src/api/user.js`
- Create: `site/src/components/AvailableLocations.jsx`
- Modify: `site/src/pages/DashboardPage.jsx`

- [ ] **Step 1: Extend getConnections to return version**

In `site/src/api/user.js`, replace the existing `getConnections`:

Find:
```js
export async function getConnections(token, platform) {
  const url = new URL(`${API_BASE}/api/v1/user/connections`, window.location.origin)
  if (platform) url.searchParams.set('platform', platform)
  const res = await fetch(url.toString(), {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load connections')
}
```

Replace with:
```js
export async function getConnections(token, platform) {
  const url = new URL(`${API_BASE}/api/v1/user/connections`, window.location.origin)
  if (platform) url.searchParams.set('platform', platform)
  const res = await fetch(url.toString(), {
    headers: { Authorization: `Bearer ${token}` },
  })
  const data = await jsonOrThrow(res, 'Failed to load connections')
  // Expose the header version too — body has it as a field but header is cheaper for ETag-like checks.
  data.__subscriptionVersionHeader = parseInt(res.headers.get('X-Subscription-Version') || '0', 10)
  return data
}
```

- [ ] **Step 2: AvailableLocations component**

File: `site/src/components/AvailableLocations.jsx`

```jsx
import React, { useEffect, useRef, useState } from 'react'
import { getConnections } from '../api/user'

const POLL_INTERVAL = 30_000 // 30 sec
const LAST_VERSION_KEY = 'subscription_version_last_seen'

export default function AvailableLocations({ token }) {
  const [conns, setConns] = useState(null)
  const [error, setError] = useState(null)
  const [toast, setToast] = useState(null)
  const lastVersionRef = useRef(
    parseInt(localStorage.getItem(LAST_VERSION_KEY) || '0', 10)
  )

  useEffect(() => {
    if (!token) return
    let cancelled = false

    async function tick() {
      try {
        const data = await getConnections(token)
        if (cancelled) return
        const ver = data.subscription_version || data.__subscriptionVersionHeader || 0
        if (lastVersionRef.current && ver > lastVersionRef.current) {
          setToast('Подписка обновлена администратором. Список локаций синхронизирован.')
          // hide toast after 6 sec
          setTimeout(() => setToast(null), 6000)
        }
        if (ver > 0) {
          lastVersionRef.current = ver
          localStorage.setItem(LAST_VERSION_KEY, String(ver))
        }
        setConns(data)
        setError(null)
      } catch (e) {
        if (!cancelled) setError(e.message)
      }
    }

    tick()
    const handle = setInterval(tick, POLL_INTERVAL)
    return () => {
      cancelled = true
      clearInterval(handle)
    }
  }, [token])

  if (!conns && !error) return null

  // Locations come from server.tunnel_sni / server.name; the backend already exposes them
  // as part of the connection bundle. Different backends shape this differently — adapt:
  const locations = Array.isArray(conns?.locations) ? conns.locations : []

  return (
    <div className="rounded-xl border border-border bg-bg-surface p-4 mb-4">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold text-text-primary">Доступные локации</h3>
        <span className="text-xs text-text-secondary">обновление каждые 30 сек</span>
      </div>
      {error && <p className="text-xs text-red-400">{error}</p>}
      {locations.length === 0 && !error && (
        <p className="text-sm text-text-secondary">Список локаций недоступен.</p>
      )}
      {locations.length > 0 && (
        <ul className="flex flex-wrap gap-2">
          {locations.map((loc) => (
            <li
              key={loc.id || loc.name}
              className="px-2 py-1 rounded-md bg-emerald-500/10 text-emerald-300 text-xs"
            >
              {loc.country_code ? `${loc.country_code} · ` : ''}{loc.name}
            </li>
          ))}
        </ul>
      )}
      {toast && (
        <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-40 px-4 py-2 rounded-lg bg-blue-500/90 text-white text-sm shadow-lg">
          {toast}
        </div>
      )}
    </div>
  )
}
```

> **Note:** the existing `getConnections` response shape may not yet expose a `locations[]` array — if not, the widget shows the empty message. That's acceptable as a placeholder; the backend response can be enriched separately. For now the user-side change focuses on the version-change toast which works regardless of the locations array.

- [ ] **Step 3: Mount widget in DashboardPage**

In `site/src/pages/DashboardPage.jsx`:

a) Add import near the top:
```jsx
import AvailableLocations from '../components/AvailableLocations'
```

b) Inside the main render, near the top of the dashboard layout (above other widgets):
```jsx
<AvailableLocations token={token} />
```

- [ ] **Step 4: Build site**

```bash
cd site && npm run build
```
Expected: `dist/` populated, no errors.

- [ ] **Step 5: Commit**

```bash
git add site/src/api/user.js \
        site/src/components/AvailableLocations.jsx \
        site/src/pages/DashboardPage.jsx
git commit -m "feat(site): AvailableLocations widget with 30s polling + version-change toast"
```

---

## Task 15: Deploy + verification (end-to-end) (30 min)

- [ ] **Step 1: Deploy backend**

```bash
cd server/backend && go run ./cmd/deploy --ssh "systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api && echo OK"
cd server/backend && go run quick_deploy.go
```
Expected: `heyearth-api: ACTIVE ✓` + `Health: {"status":"ok"}`.

- [ ] **Step 2: Deploy admin_web + site**

```bash
cd server/backend && go run ./cmd/deploy --site --admin-web
```
Expected: assets uploaded, nginx reload OK.

- [ ] **Step 3: Run one-time hysteria naming migration**

```bash
cd server/backend && go run ./cmd/deploy --ssh "bash /tmp/migrate-hysteria-naming.sh" 2>&1 || true
# If the deploy doesn't copy this script, transfer it explicitly:
scp cmd/deploy/migrate-hysteria-naming.sh root@45.150.39.236:/tmp/
ssh root@45.150.39.236 'bash /tmp/migrate-hysteria-naming.sh'
```
Expected (typical):
```
[migrate-hysteria-naming] legacy unit points to 89.124.89.126
[migrate-hysteria-naming] mapped to server id=2
[migrate-hysteria-naming] hysteria2-client-2 is ACTIVE ✓
[migrate-hysteria-naming] DONE. Legacy at /opt/heyearth/state/legacy-hysteria-migration-...
```

- [ ] **Step 4: Verify migration on hub**

```bash
cd server/backend && go run ./cmd/deploy --ssh "systemctl is-active hysteria2-client-2; ls /etc/systemd/system/hysteria2-client*; ls /opt/heyearth/hysteria/"
```
Expected: `active`, only `hysteria2-client-2.service` listed, only `client-2.yaml` in config dir.

- [ ] **Step 5: Verify Ghost Reconciler ran**

```bash
cd server/backend && go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '5 minutes ago' --no-pager | grep ghost-reconciler"
```
Expected: at least one line like
```
[ghost-reconciler] scan: 1 units, 1 rows, 0 orphans, 0 missing (auto_cleanup=true auto_repair=false)
```

- [ ] **Step 6: Verify lifecycle column populated**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c 'SELECT id, name, lifecycle_state, status, tunnel_status FROM servers;'"
```
Expected: row id=2 (Amsterdam) shows `lifecycle_state=online`.

- [ ] **Step 7: Verify subscription_version exposed**

```bash
# fetch any user token (or use a known test user)
USER_TOKEN="<paste valid user JWT here>"
curl -s -D - -H "Authorization: Bearer $USER_TOKEN" https://hiearth.ru/api/v1/user/connections -o /tmp/conn.json | grep -i 'X-Subscription-Version'
cat /tmp/conn.json | python3 -c 'import sys, json; d=json.load(sys.stdin); print("body version:", d.get("subscription_version"))'
```
Expected: `X-Subscription-Version: 1` header + matching body field.

- [ ] **Step 8: Smoke-test deprovision-then-reprovision on a non-prod server**

> Skip if Amsterdam is the only exit. To test safely, add a dummy server row in the admin (no hub-side state) and run actions on it.

```bash
# In admin UI: click "Disable" on the dummy → verify lifecycle goes to offline
# In admin UI: click "Provision" on the dummy → verify lifecycle returns to online (assuming exit-side is reachable)
# In admin UI: click "Delete" + type "delete-<N>" → verify row gone and archive present
```

- [ ] **Step 9: Verify Ghost Reconciler catches a synthetic orphan**

```bash
# Create a fake .service to simulate a future regression:
cd server/backend && go run ./cmd/deploy --ssh "echo '[Unit]
Description=fake' > /etc/systemd/system/hysteria2-client-9999.service && systemctl daemon-reload"

# Wait for the next reconciler tick (or restart heyearth-api to fire it immediately on startup+2min):
sleep 130  # crude wait — better is to set ghost_reconciler_interval to 1 min via app_settings if we want to speed iteration

go run ./cmd/deploy --ssh "journalctl -u heyearth-api --since '5 minutes ago' --no-pager | grep -E 'ORPHAN|MISSING'"
```
Expected: a line like `ORPHAN: hysteria2-client-9999.service exists, row missing`.

Cleanup:
```bash
cd server/backend && go run ./cmd/deploy --ssh "rm -f /etc/systemd/system/hysteria2-client-9999.service && systemctl daemon-reload"
```

- [ ] **Step 10: Push + close Beads epic**

```bash
git push origin main
bd close <list of task IDs from Task 0> --reason "Sub-project A: server lifecycle automation deployed and verified"
```

---

## Risk Register

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | heyearth-api lacks systemd permissions on hub | API already runs as root for similar ops (see `exec.Command("systemctl", "restart", "heyearth-api")` in `runSetupExitNode`). Verify by deploying Task 3 and watching for permission errors in the first ghost-reconciler scan. |
| R-02 | Migration script breaks Amsterdam tunnel mid-rename | Script keeps backups, verifies new unit is `active` after rename, and on failure restores the legacy unit before exiting non-zero. |
| R-03 | DELETE FROM servers leaves FK debris in plan_servers/traffic_stats | `plan_servers` has ON DELETE CASCADE for server_id. `traffic_stats` has no FK constraint to servers (data lives forever, fine for history). Verified in migrations 000003-000037. |
| R-04 | subscription_version bump under DB load | Single-row UPDATE, no contention worth caring about. Cache invalidation is per-instance (we run one heyearth-api). |
| R-05 | Ghost Reconciler auto-cleanup deletes the wrong unit | Gate is `lifecycle_state ∈ {offline}` OR row missing entirely. If a unit's server still has `lifecycle_state=online` we never touch it. Default `auto_repair=false` means we never auto-create — admin is alerted first. |
| R-06 | Concurrent admin ops on same server | `provisionProgress.Load` check at handler entry returns 409. FSM atomic transitions guard the backend layer. |
| R-07 | Long-running tunnels are interrupted by 3-sec drain | 3 sec is much shorter than typical session; the next packet hits the new balancer. Xray re-balance via `RequestXraySync` already in place. |

## Rollback

- **Whole sub-project**: `git revert <merge commit>` + `go run quick_deploy.go`. Migration `000040` can stay (column is nullable-default and ignored if code is reverted). To fully drop it: `psql -c "$(cat migrations/000040_server_lifecycle.down.sql)"`.
- **Single task** that broke deploy: `git revert` that specific commit, redeploy. Each commit is intentionally small and reversible.
- **Hysteria naming migration** that broke Amsterdam: the script's own restore branch handles it, but manual recovery: copy `/opt/heyearth/state/legacy-hysteria-migration-*/hysteria2-client.service.before-migration` back to `/etc/systemd/system/hysteria2-client.service`, copy the .yaml back to `/opt/heyearth/hysteria/client.yaml`, run `systemctl daemon-reload && systemctl enable hysteria2-client && systemctl restart hysteria2-client`.

## Out of scope (separate cycles)

- Flutter app changes (separate cycle, polling pattern same as site)
- Sub-projects B (SOCKS5 removal), D (per-plan rate-limit), C (MTProto perk)
- Multi-region admin sharding
- Web UI for browsing `/opt/heyearth/state/server-archive/` (CLI / `ServerFileBrowserHandler` is enough for now)
- Automated tests (project has none; verification is evidence-driven per CLAUDE.md)
