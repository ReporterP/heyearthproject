# SOCKS5 Removal + MTProto Telegram-Button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete every user-facing trace of SOCKS5/HTTP-proxy from HiEarth and add a single-shared-secret MTProto proxy on Amsterdam exit with one-click "Add to Telegram" deeplink button.

**Architecture:** Backend stops generating user-facing :1080/:8080 Xray inbounds and SOCKS/HTTP URI entries in the connection bundle. New `mtproto-proxy` daemon (Telegram official binary) runs on Amsterdam exit at :8443 with sponsor-channel support. New `/api/v1/user/mtproto` endpoint serves `tg://proxy?...` deeplink for any active subscriber. Site ConnectionsPage drops ~250 lines of SOCKS5 UI; gains a small Telegram block. Admin gets a new MTProtoPage for settings.

**Tech Stack:** Go 1.21+, PostgreSQL, systemd, Xray, Telegram `mtproto-proxy` binary (TelegramMessenger/MTProxy), React 19 + Tailwind v4.

**Beads epic:** to be created in Task 0.

**Verification:** Evidence-driven (no automated tests). SSH + SQL commands per task.

**Deploy:** `cd server/backend && go run quick_deploy.go` (binary + migrations + restart). `go run ./cmd/deploy --site --admin-web` for frontend. `go run ./cmd/deploy --ssh '...'` for one-off SSH.

**Spec:** `docs/superpowers/specs/2026-05-22-socks5-removal-mtproto-perk-design.md`

---

## File Structure

### Create (Go backend)
* `server/backend/migrations/000041_socks_to_mtproto.up.sql`
* `server/backend/migrations/000041_socks_to_mtproto.down.sql`
* `server/backend/internal/admin/mtproto_handlers.go` — admin GET/PUT settings

### Create (ops)
* `server/backend/cmd/deploy/install-mtproxy.sh` — one-time install on Amsterdam

### Create (frontend admin)
* `admin_web/src/pages/MTProtoPage.jsx`

### Create (frontend site)
* `site/src/components/TelegramProxyButton.jsx`

### Modify (Go backend)
* `server/backend/cmd/api/client_api.go` — drop SOCKS5/HTTP from `buildHubConnectionOptions`; add `handleUserMTProto`; register route
* `server/backend/cmd/api/xray_sync.go` — remove user-facing `socks5-proxy`/`http-proxy` inbounds + `buildProxyAccounts` call + routing rule references
* `server/backend/internal/admin/routes.go` — register mtproto admin handlers

### Modify (frontend admin)
* `admin_web/src/pages/PlansPage.jsx` — placeholder text "socks-basic" → "vless-reality"; struck-through inactive plans
* `admin_web/src/App.jsx` (or router file) — add `/mtproto` route + sidebar item

### Modify (frontend site)
* `site/src/pages/ConnectionsPage.jsx` — gut SOCKS5 sections, add Telegram block
* `site/src/pages/DashboardPage.jsx` — verify zero SOCKS5 refs
* `site/src/pages/PrivacyPage.jsx`, `OfferPage.jsx`, `RefundPage.jsx`, `LandingPage.jsx` — copy scrub (SOCKS → VLESS Reality)
* `site/src/api/user.js` — add `getMtproto(token)` helper

---

## Task 0: Beads epic + sub-tasks (10 min)

- [ ] **Step 1: Create epic**

```bash
cd C:\Users\alex\Documents\heyearthproject
bd create --title="SOCKS5 removal + MTProto Telegram button (B+C merged)" \
  --description="Remove user-facing SOCKS5/HTTP. Add Telegram MTProto deeplink button. Spec: docs/superpowers/specs/2026-05-22-socks5-removal-mtproto-perk-design.md. Plan: docs/superpowers/plans/2026-05-22-socks5-removal-mtproto-perk.md" \
  --type=epic --priority=1
```
Expected: prints new ID like `heyearthproject-XXXX`. Record as `EPIC_ID`.

- [ ] **Step 2: Create sub-tasks for each plan task (1-15)**

For each task below, run:
```bash
bd create --title="Task N: <one-line summary>" --type=task --priority=1 \
  --description="See docs/superpowers/plans/2026-05-22-socks5-removal-mtproto-perk.md Task N"
bd dep add <new-id> <EPIC_ID> --type=parent-child
```
For sequential dependencies (where stated below), add `--type=blocks` between consecutive task IDs.

---

## Task 1: Migration 000041 — deactivate SOCKS plans + add mtproto settings (15 min)

**Files:**
- Create: `server/backend/migrations/000041_socks_to_mtproto.up.sql`
- Create: `server/backend/migrations/000041_socks_to_mtproto.down.sql`

- [ ] **Step 1: Write up migration**

File: `server/backend/migrations/000041_socks_to_mtproto.up.sql`

```sql
-- SOCKS5 removal + MTProto perk (sub-projects B+C merged).
-- Deactivates legacy SOCKS-only plans (zero active subscribers verified live).
-- Seeds default mtproto_* settings.

UPDATE plans
   SET is_active = false,
       updated_at = NOW()
 WHERE slug IN ('socks-basic', 'socks-select')
   AND is_active = true;

INSERT INTO app_settings (key, value) VALUES
  ('mtproto_enabled',         'false'),
  ('mtproto_host',            ''),
  ('mtproto_port',            '8443'),
  ('mtproto_secret',          ''),
  ('mtproto_sponsor_channel', '')
ON CONFLICT (key) DO NOTHING;
```

- [ ] **Step 2: Write down migration**

File: `server/backend/migrations/000041_socks_to_mtproto.down.sql`

```sql
DELETE FROM app_settings WHERE key IN
  ('mtproto_enabled', 'mtproto_host', 'mtproto_port', 'mtproto_secret', 'mtproto_sponsor_channel');

UPDATE plans
   SET is_active = true, updated_at = NOW()
 WHERE slug IN ('socks-basic', 'socks-select');
```

- [ ] **Step 3: Build verifies migrations parse**

Run: `cd server/backend && go build ./...`
Expected: silent success.

- [ ] **Step 4: Commit**

```bash
git add server/backend/migrations/000041_socks_to_mtproto.up.sql \
        server/backend/migrations/000041_socks_to_mtproto.down.sql
git commit -m "feat(migration): deactivate SOCKS plans + seed mtproto settings"
```

---

## Task 2: Backend — drop SOCKS5/HTTP from connection bundle (20 min)

**Files:**
- Modify: `server/backend/cmd/api/client_api.go` (in `buildHubConnectionOptions`, around lines 162-262)

- [ ] **Step 1: Edit `buildHubConnectionOptions`**

In `server/backend/cmd/api/client_api.go`, locate the function `buildHubConnectionOptions(userID string)`. Replace its body to drop SOCKS/HTTP fully:

Find (lines ~162-262):
```go
func buildHubConnectionOptions(userID string) []*heyearth_v1.ConnectionOption {
	mainServerHost := biz.GetSettingOrDefault("main_server_host", "")
	socks5Port := biz.GetSettingOrDefault("socks5_port", "1080")
	httpProxyPort := biz.GetSettingOrDefault("http_proxy_port", "8080")

	// Per-user UUID используется как пароль для прокси-авторизации
	userVlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "")
	}

	// Check user's plan for protocol restrictions
	canVLESS := biz.IsProtocolAllowed(userID, "vless")
	canSOCKS := biz.IsProtocolAllowed(userID, "socks")
	canHTTP := biz.IsProtocolAllowed(userID, "http")

	var opts []*heyearth_v1.ConnectionOption

	if mainServerHost != "" && userVlessUUID != "" {
```

Replace with:
```go
func buildHubConnectionOptions(userID string) []*heyearth_v1.ConnectionOption {
	mainServerHost := biz.GetSettingOrDefault("main_server_host", "")

	// Per-user UUID используется в URI VLESS Reality для authentication
	userVlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		userVlessUUID = biz.GetSettingOrDefault("vless_uuid", "")
	}

	// Only VLESS is exposed to users now. SOCKS/HTTP user-facing inbounds removed
	// (см. spec 2026-05-22-socks5-removal-mtproto-perk-design.md).
	canVLESS := biz.IsProtocolAllowed(userID, "vless")

	var opts []*heyearth_v1.ConnectionOption

	if mainServerHost != "" && userVlessUUID != "" {
```

- [ ] **Step 2: Remove the canSOCKS and canHTTP blocks**

In the same function, find and delete the two blocks (lines ~235-258):
```go
		// 2. SOCKS proxy (per-user авторизация) — only if plan allows.
		// The #HeYearth-SOCKS5 fragment becomes the entry name in clients that
		// import the subscription (V2RayNG, Happ, Clash, etc.) — without it the
		// SOCKS entry is nameless and indistinguishable from untagged proxies.
		if canSOCKS {
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 fmt.Sprintf("socks://%s:%s@%s:%s#HeYearth-SOCKS5", userID, userVlessUUID, mainServerHost, socks5Port),
				Protocol:            "socks",
				Label:               "SOCKS прокси",
				RecommendedNetworks: []string{"cellular", "wifi", "ethernet", "any"},
				Priority:            1,
			})
		}

		// 3. HTTP proxy (per-user авторизация) — only if plan allows.
		if canHTTP {
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 fmt.Sprintf("http://%s:%s@%s:%s#HeYearth-HTTP", userID, userVlessUUID, mainServerHost, httpProxyPort),
				Protocol:            "http",
				Label:               "HTTP прокси",
				RecommendedNetworks: []string{"wifi", "ethernet", "any"},
				Priority:            2,
			})
		}
	}

	return opts
}
```

Replace the entire block from `// 2. SOCKS proxy` through the next `}` (the closing `}` of `if mainServerHost != ""`) — but keep the outer closing `}` of the function. The replacement is:
```go
	}

	return opts
}
```

(meaning: drop the two `if canSOCKS` / `if canHTTP` blocks but keep `return opts`).

- [ ] **Step 3: Build verifies no compile error from unused variables**

Run: `cd server/backend && go build ./...`
Expected: silent. (Removed `socks5Port`, `httpProxyPort`, `canSOCKS`, `canHTTP` declarations match the dropped usage.)

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/client_api.go
git commit -m "feat(client_api): drop SOCKS5/HTTP URIs from user connection bundle"
```

---

## Task 3: Backend — remove user-facing SOCKS5/HTTP Xray inbounds (25 min)

**Files:**
- Modify: `server/backend/cmd/api/xray_sync.go`

- [ ] **Step 1: Remove buildProxyAccounts call**

In `cmd/api/xray_sync.go`, find line ~169:
```go
	socksAccounts, httpAccounts := buildProxyAccounts()
```

Delete this line entirely.

Find and delete the following block that uses these arrays — there is a `httpAccounts = append(...)` line at ~172. Remove that line too. Trace to find any other reference to `socksAccounts` and `httpAccounts` in the file (`grep -n socksAccounts xray_sync.go`) and remove each occurrence.

- [ ] **Step 2: Remove the two inbound definitions**

Find the SOCKS5 inbound block (around line 226-235):
```go
		{
			"tag": "socks5-proxy", "listen": "0.0.0.0", "port": 1080, "protocol": "socks",
			"settings": map[string]interface{}{
				"auth":   "password",
				"udp":    true,
				"accounts": socksAccounts,
			},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls"}},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
					"tcpKeepAliveInterval": 30,
				},
			},
		},
```
Delete it entirely (the whole map literal element).

Find the HTTP proxy inbound (around line 238-249):
```go
		{
			"tag": "http-proxy", "listen": "0.0.0.0", "port": 8080, "protocol": "http",
			"settings": map[string]interface{}{"allowTransparent": false, "accounts": httpAccounts},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls"}},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
					"tcpKeepAliveInterval": 30,
				},
			},
		},
```
Delete it entirely.

- [ ] **Step 3: Fix the "no exit-nodes" routing rule that mentions those inboundTags**

Find at line ~423:
```go
			"inboundTag":  []string{"socks5-proxy", "http-proxy", "vless-reality", "vless-xhttp"},
```
Replace with:
```go
			"inboundTag":  []string{"vless-reality", "vless-xhttp"},
```

- [ ] **Step 4: Delete the buildProxyAccounts helper function (around line 747)**

Find the function definition (search `func buildProxyAccounts`):
```go
func buildProxyAccounts() (socksAccounts []map[string]string, httpAccounts []map[string]string) {
	... // body
}
```
Delete the entire function. If the build complains about other call-sites, follow the chain (a single helper used only by SOCKS/HTTP inbounds — should be deleted cleanly).

- [ ] **Step 5: Build verifies**

Run: `cd server/backend && go build ./...`
Expected: silent. Resolve any leftover dangling reference if the compiler complains; the message will name the file + line.

- [ ] **Step 6: Bump subscription_version on next sync**

The xray_sync code path already calls `biz.BumpSubscriptionVersion()` for config changes? Verify by grep:
```bash
grep -n BumpSubscriptionVersion server/backend/cmd/api/xray_sync.go server/backend/internal/biz/server_lifecycle.go
```
Expected: `server_lifecycle.go` has it. If `xray_sync.go` does NOT call it when config changes, add at the END of `SyncXrayConfig` (after the atomic write succeeds, before `RestartXray`):
```go
	// Notify clients via subscription_version bump — they will re-fetch
	// the connection bundle on next 30-sec poll.
	if err := biz.BumpSubscriptionVersion(); err != nil {
		log.Printf("[xray-sync] BumpSubscriptionVersion: %v", err)
	}
```

- [ ] **Step 7: Commit**

```bash
git add server/backend/cmd/api/xray_sync.go
git commit -m "feat(xray): remove user-facing SOCKS5 :1080 and HTTP :8080 inbounds"
```

---

## Task 4: Backend — new `/api/v1/user/mtproto` endpoint (25 min)

**Files:**
- Modify: `server/backend/cmd/api/client_api.go` — add handler + register route

- [ ] **Step 1: Add handler function**

In `cmd/api/client_api.go`, append after `handleUserConnections` (search for it):

```go
// handleUserMTProto — GET /api/v1/user/mtproto
// Returns the Telegram MTProto proxy deeplink if MTProto is enabled and
// the user has an active subscription. Result is the same for everyone
// (shared secret) — this is a convenience perk, not a privacy feature.
func handleUserMTProto(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	// Active subscription gate — any plan (free-trial counts).
	hasActive, err := biz.UserHasActiveSubscription(userID)
	if err != nil {
		log.Printf("[mtproto] UserHasActiveSubscription(%s): %v", userID, err)
		http.Error(w, "subscription check failed", http.StatusInternalServerError)
		return
	}
	if !hasActive {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"enabled": false,
			"reason":  "Доступно для активных подписчиков",
		})
		return
	}

	enabled := biz.GetSettingOrDefault("mtproto_enabled", "false") == "true"
	host := biz.GetSettingOrDefault("mtproto_host", "")
	port := biz.GetSettingOrDefault("mtproto_port", "8443")
	secret := biz.GetSettingOrDefault("mtproto_secret", "")

	// Degraded response if admin enabled but didn't fill secret/host yet.
	if !enabled || host == "" || secret == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": false})
		return
	}

	tgDeeplink := fmt.Sprintf("tg://proxy?server=%s&port=%s&secret=%s", host, port, secret)
	tMeLink := fmt.Sprintf("https://t.me/proxy?server=%s&port=%s&secret=%s", host, port, secret)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"enabled":     true,
		"host":        host,
		"port":        port,
		"secret":      secret,
		"tg_deeplink": tgDeeplink,
		"t_me_link":   tMeLink,
	})
}
```

If `requireUser` doesn't exist in this codebase, locate how other user-handlers extract userID (e.g. `handleUserConnections`) and follow the same pattern (likely `biz.UserFromRequest(r)` or similar). Adapt accordingly — DO NOT invent function names.

- [ ] **Step 2: Add `UserHasActiveSubscription` helper if missing**

```bash
grep -n "func UserHasActiveSubscription" server/backend/internal/biz/*.go
```

If not found, add to `server/backend/internal/biz/user.go` (append):
```go
// UserHasActiveSubscription returns true if user has at least one
// non-expired subscription, regardless of plan.
func UserHasActiveSubscription(userID string) (bool, error) {
	var count int
	err := db.DB.QueryRow(`
		SELECT COUNT(*) FROM user_subscriptions us
		 JOIN users u ON u.id = us.user_id
		 WHERE u.user_id = $1 AND us.valid_until > NOW()
	`, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
```

- [ ] **Step 3: Register the route**

In `cmd/api/client_api.go`, find the route registration block (line ~917):
```go
	http.HandleFunc("/api/v1/user/connections", apiLimiter.Middleware(handleUserConnections))
```

Add directly after:
```go
	http.HandleFunc("/api/v1/user/mtproto", apiLimiter.Middleware(handleUserMTProto))
```

- [ ] **Step 4: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 5: Commit**

```bash
git add server/backend/cmd/api/client_api.go server/backend/internal/biz/user.go
git commit -m "feat(api): GET /api/v1/user/mtproto returns Telegram proxy deeplink"
```

---

## Task 5: Backend — admin handlers for MTProto settings (25 min)

**Files:**
- Create: `server/backend/internal/admin/mtproto_handlers.go`
- Modify: `server/backend/internal/admin/routes.go` — register routes

- [ ] **Step 1: Create handler file**

File: `server/backend/internal/admin/mtproto_handlers.go`

```go
package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/heyearth/backend/internal/biz"
)

// MTProtoSettings — admin-facing representation of all mtproto_* settings.
type MTProtoSettings struct {
	Enabled         bool   `json:"enabled"`
	Host            string `json:"host"`
	Port            string `json:"port"`
	Secret          string `json:"secret"`
	SponsorChannel  string `json:"sponsor_channel"`
}

// GetMTProtoSettings returns the current mtproto_* settings to admin.
func GetMTProtoSettings(w http.ResponseWriter, r *http.Request) {
	s := MTProtoSettings{
		Enabled:        biz.GetSettingOrDefault("mtproto_enabled", "false") == "true",
		Host:           biz.GetSettingOrDefault("mtproto_host", ""),
		Port:           biz.GetSettingOrDefault("mtproto_port", "8443"),
		Secret:         biz.GetSettingOrDefault("mtproto_secret", ""),
		SponsorChannel: biz.GetSettingOrDefault("mtproto_sponsor_channel", ""),
	}
	writeAdminJSON(w, http.StatusOK, s)
}

// UpdateMTProtoSettings writes a full settings payload.
func UpdateMTProtoSettings(w http.ResponseWriter, r *http.Request) {
	var s MTProtoSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	enabled := "false"
	if s.Enabled {
		enabled = "true"
	}
	for k, v := range map[string]string{
		"mtproto_enabled":         enabled,
		"mtproto_host":            s.Host,
		"mtproto_port":            s.Port,
		"mtproto_secret":          s.Secret,
		"mtproto_sponsor_channel": s.SponsorChannel,
	} {
		if err := biz.SetSetting(k, v); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to save: "+k)
			return
		}
	}

	// Bump subscription_version so clients re-fetch the deeplink.
	_ = biz.BumpSubscriptionVersion()

	WriteAuditLog(r, "update_mtproto_settings", "settings", "mtproto", nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "MTProto settings updated"})
}

// GenerateMTProtoSecret produces a fresh random 32-hex-char secret and saves it.
// Telegram requires exactly 32 hex characters for v1 secrets (also supports
// dd-prefix and v2 formats but plain 32 hex is the simplest and works).
func GenerateMTProtoSecret(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to generate random")
		return
	}
	secret := hex.EncodeToString(buf)
	if err := biz.SetSetting("mtproto_secret", secret); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save secret")
		return
	}
	_ = biz.BumpSubscriptionVersion()
	WriteAuditLog(r, "generate_mtproto_secret", "settings", "mtproto", nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"secret": secret})
}
```

- [ ] **Step 2: Register routes in `internal/admin/routes.go`**

Find the file `server/backend/internal/admin/routes.go`. Look for a function that registers admin routes (likely `RegisterRoutes` or similar — `grep -n "HandleFunc" server/backend/internal/admin/routes.go`).

Add three new lines following the existing pattern:
```go
	mux.HandleFunc("GET /api/admin/mtproto", requirePermission("settings.view", GetMTProtoSettings))
	mux.HandleFunc("PUT /api/admin/mtproto", requirePermission("settings.edit", UpdateMTProtoSettings))
	mux.HandleFunc("POST /api/admin/mtproto/generate-secret", requirePermission("settings.edit", GenerateMTProtoSecret))
```

If the existing pattern doesn't use `mux.HandleFunc("METHOD /path", ...)` but plain `mux.HandleFunc("/path", ...)`, switch to the existing style and route on `r.Method` inside the handlers (or use a wrapper — match existing code).

- [ ] **Step 3: Build verifies**

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 4: Commit**

```bash
git add server/backend/internal/admin/mtproto_handlers.go \
        server/backend/internal/admin/routes.go
git commit -m "feat(admin): MTProto settings CRUD + secret generator"
```

---

## Task 6: Install mtproto-proxy daemon on Amsterdam exit (35 min)

**Files:**
- Create: `server/backend/cmd/deploy/install-mtproxy.sh`

- [ ] **Step 1: Write install script**

File: `server/backend/cmd/deploy/install-mtproxy.sh`

```bash
#!/usr/bin/env bash
# One-time install of Telegram official mtproto-proxy on this server.
# Run as root. Usage: bash install-mtproxy.sh [PORT]
#
# After install:
#   - binary at /usr/local/bin/mtproto-proxy
#   - working dir /opt/mtproxy
#   - systemd unit mtproxy.service
#   - listens on TCP <PORT> (default 8443)
#   - prints the generated secret to stdout
#
# To rotate sponsor channel or secret, edit /etc/default/mtproxy and restart.

set -euo pipefail

PORT="${1:-8443}"
WORKDIR=/opt/mtproxy
BIN=/usr/local/bin/mtproto-proxy

if [[ $EUID -ne 0 ]]; then
  echo "ERR: run as root" >&2
  exit 1
fi

echo "[install-mtproxy] dependencies"
apt-get update -qq
apt-get install -y -qq build-essential libssl-dev zlib1g-dev git curl

mkdir -p "$WORKDIR"
cd "$WORKDIR"

if [[ ! -f "$BIN" ]]; then
  echo "[install-mtproxy] cloning + building"
  rm -rf MTProxy
  git clone https://github.com/TelegramMessenger/MTProxy.git
  cd MTProxy
  make -j"$(nproc)"
  install -m 0755 objs/bin/mtproto-proxy "$BIN"
  cd ..
fi

echo "[install-mtproxy] fetching Telegram proxy config"
curl -s https://core.telegram.org/getProxySecret -o "$WORKDIR/proxy-secret"
curl -s https://core.telegram.org/getProxyConfig -o "$WORKDIR/proxy-multi.conf"

if [[ ! -f "$WORKDIR/secret" ]]; then
  echo "[install-mtproxy] generating user secret"
  head -c 16 /dev/urandom | xxd -ps > "$WORKDIR/secret"
fi
SECRET="$(cat "$WORKDIR/secret")"

echo "[install-mtproxy] /etc/default/mtproxy (env)"
cat > /etc/default/mtproxy <<EOF
MTPROXY_PORT=$PORT
MTPROXY_SECRET=$SECRET
# Set MTPROXY_TAG=<sponsor-channel-tag-from-mtproxybot> to enable sponsored channel banner.
# Leave empty for no sponsor.
MTPROXY_TAG=
EOF

echo "[install-mtproxy] systemd unit"
cat > /etc/systemd/system/mtproxy.service <<'SVC'
[Unit]
Description=Telegram MTProto Proxy (HeYearth)
After=network.target

[Service]
Type=simple
EnvironmentFile=/etc/default/mtproxy
WorkingDirectory=/opt/mtproxy
ExecStart=/bin/bash -c '\
  ARGS="-u nobody -p 2398 -H ${MTPROXY_PORT} -S ${MTPROXY_SECRET} \
        --aes-pwd /opt/mtproxy/proxy-secret /opt/mtproxy/proxy-multi.conf \
        -M $(nproc)"; \
  if [ -n "${MTPROXY_TAG:-}" ]; then ARGS="$ARGS -P ${MTPROXY_TAG}"; fi; \
  exec /usr/local/bin/mtproto-proxy $ARGS'
Restart=on-failure
RestartSec=5
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
SVC

systemctl daemon-reload
systemctl enable mtproxy
systemctl restart mtproxy
sleep 2
systemctl is-active mtproxy

# Open firewall (best-effort — depends on what's installed)
if command -v ufw >/dev/null 2>&1; then
  ufw allow "$PORT"/tcp || true
fi

PUBLIC_IP="$(curl -fsSL https://api.ipify.org || hostname -I | awk '{print $1}')"
echo
echo "[install-mtproxy] DONE"
echo "  host:   $PUBLIC_IP"
echo "  port:   $PORT"
echo "  secret: $SECRET"
echo
echo "Set these via admin UI → MTProto page → Save."
```

- [ ] **Step 2: Transfer + run on Amsterdam (89.124.89.126)**

Use the base64 trick (deploy tool already configured for hub; transfer to exit-VPS requires the same pattern):

```bash
cd "C:\Users\alex\Documents\heyearthproject"
B64=$(base64 -w0 server/backend/cmd/deploy/install-mtproxy.sh)
# scp via ssh-from-hub or directly if you have SSH access to Amsterdam:
ssh root@89.124.89.126 "echo '$B64' | base64 -d > /tmp/install-mtproxy.sh && chmod +x /tmp/install-mtproxy.sh && bash /tmp/install-mtproxy.sh 8443"
```

If direct SSH from your workstation to the exit is set up differently (likely via the existing servers.ssh_password_enc credentials), use the admin Server Actions UI to run the script — or via deploy tool's `--ssh` after first chaining through hub.

Expected output (final lines):
```
[install-mtproxy] DONE
  host:   89.124.89.126
  port:   8443
  secret: <32 hex chars>
```

Record the secret — you'll paste it into admin UI later.

- [ ] **Step 3: Verify listening**

```bash
ssh root@89.124.89.126 "ss -tln | grep :8443; systemctl is-active mtproxy"
```
Expected: `LISTEN ... :8443 ...` + `active`.

- [ ] **Step 4: Commit script**

```bash
git add server/backend/cmd/deploy/install-mtproxy.sh
git commit -m "ops: install-mtproxy.sh — Telegram MTProto on exit-node"
```

---

## Task 7: Site — strip SOCKS5 UI from ConnectionsPage.jsx (40 min)

**Files:**
- Modify: `site/src/pages/ConnectionsPage.jsx`

- [ ] **Step 1: Locate and remove SOCKS5-related state, derivations, and links**

Open `site/src/pages/ConnectionsPage.jsx`. Search for and delete:
* The `socks5Addr`, `socks5Parts`, `tgSocksLink` variable declarations
* All JSX blocks rendering "SOCKS прокси" card, the credentials table (host/port/login/password), the QR code for SOCKS5, the "Telegram via SOCKS" link, and the IMS/iOS/Android setup instructions referencing SOCKS5

Use grep first to enumerate occurrences:
```bash
grep -n "socks5\|SOCKS\|socks\|http://.*proxy\|tgSocks" site/src/pages/ConnectionsPage.jsx
```

Remove every match (and the surrounding JSX it belongs to). The page should keep:
* VLESS Reality URI + QR
* Subscription URL block
* Any platform-agnostic copy buttons

- [ ] **Step 2: Verify build after delete**

```bash
cd site && npm run build
```
Expected: silent success.

- [ ] **Step 3: Quick visual check of the dev server (optional)**

```bash
cd site && npm run dev
# Open http://localhost:5173/connections and confirm no SOCKS references on screen
```

- [ ] **Step 4: Commit**

```bash
git add site/src/pages/ConnectionsPage.jsx
git commit -m "feat(site): strip user-facing SOCKS5/HTTP UI from ConnectionsPage"
```

---

## Task 8: Site — Telegram MTProto block + button (25 min)

**Files:**
- Create: `site/src/components/TelegramProxyButton.jsx`
- Modify: `site/src/api/user.js` — add `getMtproto` helper
- Modify: `site/src/pages/ConnectionsPage.jsx` — mount Telegram block

- [ ] **Step 1: Add API helper**

In `site/src/api/user.js`, append:

```js
export async function getMtproto(token) {
  const res = await fetch(`${API_BASE}/api/v1/user/mtproto`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  return jsonOrThrow(res, 'Failed to load MTProto info')
}
```

- [ ] **Step 2: Create TelegramProxyButton component**

File: `site/src/components/TelegramProxyButton.jsx`

```jsx
import { useEffect, useState } from 'react'
import { getMtproto } from '../api/user.js'

/**
 * TelegramProxyButton — fetches /api/v1/user/mtproto on mount and renders
 * a small block with two buttons: "Открыть в Telegram" (tg:// deeplink)
 * and "Открыть через t.me" (web fallback). Renders nothing if MTProto
 * is not enabled or user has no active subscription.
 */
export default function TelegramProxyButton({ token }) {
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    if (!token) {
      setLoading(false)
      return
    }
    getMtproto(token)
      .then((d) => {
        if (!cancelled) setData(d)
      })
      .catch(() => {
        // 4xx/5xx → just hide the block
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [token])

  if (loading) return null
  if (!data || !data.enabled) return null

  return (
    <div className="glass-card rounded-2xl p-5 md:p-6 mb-6 border border-brand/20 animate-fade-in-up">
      <div className="flex items-center gap-3 mb-3">
        <div className="w-10 h-10 rounded-xl bg-gradient-to-br from-blue-500 to-blue-400 flex items-center justify-center">
          <svg className="w-6 h-6 text-white" viewBox="0 0 24 24" fill="currentColor">
            <path d="M21.05 3.05 2.95 9.13c-.93.32-.92 1.6 0 1.92l4.62 1.55 1.55 4.62c.32.93 1.6.92 1.92 0l1.05-3.13 3.65 2.74c.7.52 1.7.13 1.86-.72L21.6 4.07c.17-.84-.6-1.55-1.55-1.02Z" />
          </svg>
        </div>
        <div>
          <h3 className="text-base font-semibold text-text-primary">Прокси для Telegram</h3>
          <p className="text-xs text-text-secondary">Бонус — быстрая настройка в один клик</p>
        </div>
      </div>
      <p className="text-sm text-text-secondary mb-3">
        Только для трафика Telegram. Не заменяет VPN, но помогает когда Telegram блокируется отдельно.
      </p>
      <div className="flex flex-wrap gap-2">
        <a
          href={data.tg_deeplink}
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-blue-500 hover:bg-blue-400 text-white text-sm font-medium transition min-h-[44px]"
        >
          Открыть в Telegram
        </a>
        <a
          href={data.t_me_link}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-2 px-4 py-2 rounded-xl glass hover:bg-white/10 text-sm text-text-secondary hover:text-text-primary transition min-h-[44px]"
        >
          Открыть через t.me
        </a>
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Mount the button in ConnectionsPage**

Add import at the top of `site/src/pages/ConnectionsPage.jsx`:
```jsx
import TelegramProxyButton from '../components/TelegramProxyButton.jsx'
```

Inside the main JSX (somewhere in the visible page body, after the subscription URL block but before the platform-specific instructions), add:
```jsx
<TelegramProxyButton token={token} />
```

(The page already pulls `token` from `useAuth()` — verify the variable name matches what's in scope at the insertion point.)

- [ ] **Step 4: Build**

```bash
cd site && npm run build
```
Expected: silent success.

- [ ] **Step 5: Commit**

```bash
git add site/src/components/TelegramProxyButton.jsx \
        site/src/api/user.js \
        site/src/pages/ConnectionsPage.jsx
git commit -m "feat(site): TelegramProxyButton component + ConnectionsPage integration"
```

---

## Task 9: Site — copy scrub across remaining pages (20 min)

**Files:**
- Modify: `site/src/pages/DashboardPage.jsx`, `PrivacyPage.jsx`, `OfferPage.jsx`, `RefundPage.jsx`, `LandingPage.jsx`

- [ ] **Step 1: Grep for remaining SOCKS5 references**

```bash
cd site/src && grep -rn -i "socks" .
```

- [ ] **Step 2: For each hit, replace user-facing copy**

Apply these substitutions (case-preserving — be careful in JSX strings):
* "SOCKS5 прокси" → "VLESS Reality"
* "SOCKS прокси" → "VLESS Reality"
* "шифрованный SOCKS-канал" → "VLESS Reality канал"
* "включите SOCKS-прокси" → "импортируйте подписку"
* Where SOCKS was listed as one of several supported protocols, just remove the SOCKS bullet.

Do NOT touch:
* JSON keys or code identifiers (unlikely in JSX pages but possible in comments)
* Any reference to "Hub→Exit tunnel uses SOCKS internally" comments (these are dev-facing, leave alone)

- [ ] **Step 3: Build**

```bash
cd site && npm run build
```
Expected: silent success.

- [ ] **Step 4: Final grep — should be empty for user-facing**

```bash
cd site && grep -rn -i "socks" src/pages/ src/components/ | grep -v "// "
```
Expected: empty (only code comments remain, if any).

- [ ] **Step 5: Commit**

```bash
git add site/src/
git commit -m "chore(site): scrub SOCKS5 mentions from user-facing copy"
```

---

## Task 10: Admin — MTProtoPage + routing + sidebar (30 min)

**Files:**
- Create: `admin_web/src/pages/MTProtoPage.jsx`
- Modify: `admin_web/src/App.jsx` (or router file) — add route
- Modify: sidebar component (find via `grep -rn "ServersPage\|Серверы" admin_web/src`) — add entry

- [ ] **Step 1: Create MTProtoPage**

File: `admin_web/src/pages/MTProtoPage.jsx`

```jsx
import { useState, useEffect } from 'react'
import { useToast } from '../components/Toast.jsx'

const API = '/api/admin/mtproto'

async function authedFetch(method, path, body) {
  const token = localStorage.getItem('admin_token') || ''
  const init = {
    method,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  }
  if (body !== undefined) init.body = JSON.stringify(body)
  const res = await fetch(path, init)
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

export default function MTProtoPage() {
  const toast = useToast()
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState({
    enabled: false,
    host: '',
    port: '8443',
    secret: '',
    sponsor_channel: '',
  })

  async function load() {
    setLoading(true)
    try {
      const data = await authedFetch('GET', API)
      setForm({
        enabled: !!data.enabled,
        host: data.host || '',
        port: data.port || '8443',
        secret: data.secret || '',
        sponsor_channel: data.sponsor_channel || '',
      })
    } catch (e) {
      toast.error(e.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
  }, [])

  async function save() {
    setSaving(true)
    try {
      await authedFetch('PUT', API, form)
      toast.success('Сохранено. Клиенты увидят обновление через 30 сек.')
    } catch (e) {
      toast.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  async function regenerateSecret() {
    if (!confirm('Сгенерировать новый секрет? Все клиенты потеряют текущий deeplink.')) return
    setSaving(true)
    try {
      const { secret } = await authedFetch('POST', `${API}/generate-secret`)
      setForm((f) => ({ ...f, secret }))
      toast.success('Новый секрет сгенерирован. Не забудьте Сохранить чтобы обновить остальные поля если нужно.')
    } catch (e) {
      toast.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  if (loading) return <div className="p-6 text-text-secondary">Loading…</div>

  return (
    <div className="p-4 md:p-6 max-w-2xl">
      <h1 className="text-xl md:text-2xl font-bold mb-1">MTProto Telegram Proxy</h1>
      <p className="text-sm text-text-secondary mb-6">
        Кнопка для добавления HiEarth MTProto-прокси в Telegram. Бонус-фича для активных подписчиков.
      </p>

      <div className="space-y-4 bg-surface-card rounded-xl border border-white/10 p-5">
        <label className="flex items-center gap-3 cursor-pointer">
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm({ ...form, enabled: e.target.checked })}
            className="w-4 h-4"
          />
          <span className="text-sm">Включить MTProto (виден ли deeplink в личном кабинете)</span>
        </label>

        <div>
          <label className="text-xs text-text-secondary block mb-1">Host (FQDN или IP exit-VPS)</label>
          <input
            type="text"
            value={form.host}
            onChange={(e) => setForm({ ...form, host: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono"
            placeholder="89.124.89.126"
          />
        </div>

        <div>
          <label className="text-xs text-text-secondary block mb-1">Port</label>
          <input
            type="text"
            value={form.port}
            onChange={(e) => setForm({ ...form, port: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono"
            placeholder="8443"
          />
        </div>

        <div>
          <label className="text-xs text-text-secondary block mb-1">Secret (32 hex chars)</label>
          <div className="flex gap-2">
            <input
              type="text"
              value={form.secret}
              onChange={(e) => setForm({ ...form, secret: e.target.value })}
              className="flex-1 px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm font-mono"
              placeholder="d4...e1"
            />
            <button
              onClick={regenerateSecret}
              disabled={saving}
              className="px-3 py-2 rounded-lg bg-amber-500/20 text-amber-300 text-xs hover:bg-amber-500/30"
            >
              Regenerate
            </button>
          </div>
        </div>

        <div>
          <label className="text-xs text-text-secondary block mb-1">
            Sponsor channel (необязательно — добавит баннер канала в Telegram при использовании прокси)
          </label>
          <input
            type="text"
            value={form.sponsor_channel}
            onChange={(e) => setForm({ ...form, sponsor_channel: e.target.value })}
            className="w-full px-3 py-2 rounded-lg bg-surface-alt border border-white/10 text-sm"
            placeholder="@hiearth (получите тег у @mtproxybot)"
          />
        </div>

        <div className="pt-2">
          <button
            onClick={save}
            disabled={saving}
            className="px-5 py-2 rounded-lg bg-brand/30 text-brand text-sm font-medium hover:bg-brand/40 disabled:opacity-50"
          >
            {saving ? 'Сохраняем…' : 'Сохранить'}
          </button>
        </div>
      </div>

      <p className="text-xs text-text-secondary mt-4">
        Для получения тега у <code>@mtproxybot</code> в Telegram: отправьте боту команду
        <code className="text-text-primary"> /newproxy</code>, укажите хост + порт + секрет, бот вернёт тег.
      </p>
    </div>
  )
}
```

- [ ] **Step 2: Find router and add route**

```bash
grep -n "Route\|ServersPage\|PlansPage" admin_web/src/App.jsx 2>/dev/null | head -5
```

In the router file (likely `App.jsx`), import and add:
```jsx
import MTProtoPage from './pages/MTProtoPage'
```
And add a route entry in the existing routing block:
```jsx
<Route path="/mtproto" element={<MTProtoPage />} />
```

(Use existing pattern — if it's `<Routes>...</Routes>` add inside that.)

- [ ] **Step 3: Find sidebar and add menu item**

```bash
grep -rln "Серверы\|Servers" admin_web/src/ | head -3
```

Open the sidebar component file. Find an entry like `{ to: '/servers', label: 'Серверы' }` and add right after:
```jsx
{ to: '/mtproto', label: 'MTProto' },
```

If the entries are JSX (not array), follow the JSX pattern:
```jsx
<NavLink to="/mtproto" ...>MTProto</NavLink>
```

- [ ] **Step 4: Build**

```bash
cd admin_web && npm run build
```
Expected: silent success.

- [ ] **Step 5: Commit**

```bash
git add admin_web/src/pages/MTProtoPage.jsx admin_web/src/App.jsx admin_web/src/components/
git commit -m "feat(admin): MTProtoPage with settings form + secret generator"
```

---

## Task 11: Admin — PlansPage placeholder + inactive styling (15 min)

**Files:**
- Modify: `admin_web/src/pages/PlansPage.jsx`

- [ ] **Step 1: Update slug placeholder text**

In `admin_web/src/pages/PlansPage.jsx`, find:
```jsx
placeholder="socks-basic"
```

Replace with:
```jsx
placeholder="vless-reality"
```

- [ ] **Step 2: Visually mark inactive plans (struck-through name)**

Locate the table row rendering. Find the cell for `plan.name`. Wrap it conditionally:
```jsx
<span className={plan.is_active ? '' : 'line-through text-text-secondary'}>{plan.name}</span>
```

- [ ] **Step 3: Build**

```bash
cd admin_web && npm run build
```
Expected: silent success.

- [ ] **Step 4: Commit**

```bash
git add admin_web/src/pages/PlansPage.jsx
git commit -m "feat(admin): PlansPage placeholder update + struck-through inactive plans"
```

---

## Task 12: Deploy backend + verify migration + endpoints (20 min)

- [ ] **Step 1: Deploy backend**

```bash
cd server/backend && go run ./cmd/deploy --ssh "systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api && echo STOPPED"
cd server/backend && go run quick_deploy.go
```
Expected: `heyearth-api: ACTIVE ✓` + `Health: {"status":"ok"}`.

- [ ] **Step 2: Verify migration 000041 applied**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c \"SELECT slug, is_active FROM plans WHERE slug IN ('socks-basic','socks-select'); SELECT key, value FROM app_settings WHERE key LIKE 'mtproto_%';\""
```
Expected: SOCKS plans `is_active=false` (or zero rows if they never existed). 5 mtproto_* settings present.

- [ ] **Step 3: Verify SOCKS5/HTTP user-facing inbounds gone**

```bash
cd server/backend && go run ./cmd/deploy --ssh "grep -E '\"port\":\\s*(1080|8080)' /opt/heyearth/xray/config.json; echo ---; ss -tln | grep -E ':1080|:8080'"
```
Expected: both empty (port 1080 and 8080 no longer in Xray config or listening).

- [ ] **Step 4: Verify MTProto endpoint responds (degraded since not yet configured)**

Get a valid user JWT from your dashboard or run:
```bash
TOKEN="<paste-user-jwt>"
curl -s -H "Authorization: Bearer $TOKEN" https://hiearth.ru/api/v1/user/mtproto | python3 -m json.tool
```
Expected (since admin didn't enable yet): `{"enabled": false}` or `{"enabled": false, "reason": "..."}`.

- [ ] **Step 5: Verify connections endpoint no longer has socks/http URIs**

```bash
curl -s -H "Authorization: Bearer $TOKEN" https://hiearth.ru/api/v1/user/connections | python3 -c "import sys,json; d=json.load(sys.stdin); print([c.get('protocol') for c in d.get('options', d.get('connections', []))])"
```
Expected: list contains only `vless` entries — no `socks`, no `http`.

---

## Task 13: Deploy frontend + admin enables MTProto + smoke test (25 min)

- [ ] **Step 1: Deploy site + admin_web**

```bash
cd server/backend && go run ./cmd/deploy --site --admin-web
```
Expected: both built, uploaded to /var/www/heyearth-site and /var/www/heyearth-admin, nginx reload OK.

- [ ] **Step 2: Open admin UI → MTProto page**

In a browser go to https://admin.hiearth.ru/mtproto.

Fill in:
* **Host**: `89.124.89.126` (or `mtproxy.hiearth.ru` if DNS set up)
* **Port**: `8443`
* **Secret**: paste the secret printed by Task 6 Step 2 (from `/opt/mtproxy/secret` on Amsterdam)
* **Sponsor channel**: leave empty for now
* **Enabled**: tick the checkbox

Click **Сохранить**. Expect toast "Сохранено. Клиенты увидят обновление через 30 сек."

- [ ] **Step 3: Verify settings persisted**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql heyearth -c \"SELECT key, value FROM app_settings WHERE key LIKE 'mtproto_%';\""
```
Expected: enabled=true, host=89.124.89.126, port=8443, secret=<32 hex>, sponsor empty.

- [ ] **Step 4: Verify user endpoint now returns deeplink**

```bash
TOKEN="<user-jwt>"
curl -s -H "Authorization: Bearer $TOKEN" https://hiearth.ru/api/v1/user/mtproto | python3 -m json.tool
```
Expected:
```json
{
  "enabled": true,
  "host": "89.124.89.126",
  "port": "8443",
  "secret": "...",
  "tg_deeplink": "tg://proxy?server=89.124.89.126&port=8443&secret=...",
  "t_me_link": "https://t.me/proxy?server=89.124.89.126&port=8443&secret=..."
}
```

- [ ] **Step 5: Open user site → ConnectionsPage**

In a browser go to https://hiearth.ru/connections (logged in as a user with active subscription).
* Verify: NO SOCKS5 cards, NO credentials table, NO IMS/iOS/Android-via-SOCKS instructions
* Verify: Telegram proxy block visible with "Открыть в Telegram" button
* Click "Открыть в Telegram" on a phone — Telegram opens proxy-add dialog with hiearth.ru parameters
* Confirm dialog → check Telegram → Settings → Data and Storage → Proxy — the new proxy is listed and active

---

## Task 14: Smoke-test MTProto traffic through Amsterdam (15 min)

- [ ] **Step 1: From a phone, after adding the proxy in Telegram, send a message in any chat**

Expected: message delivers normally.

- [ ] **Step 2: Verify Amsterdam sees the proxy traffic**

```bash
ssh root@89.124.89.126 "ss -tn state established '( sport = :8443 )' | wc -l; journalctl -u mtproxy --since '5 minutes ago' --no-pager | tail -10"
```
Expected: at least 1 ESTABLISHED connection (your phone), recent log lines without errors.

- [ ] **Step 3: Disable + re-enable test**

In admin UI, toggle Enabled off → Save → reload user ConnectionsPage → Telegram block should disappear.
Toggle Enabled on → Save → reload → block reappears.

- [ ] **Step 4: Sponsor channel test (optional, can defer)**

When @hiearth channel is ready, in Telegram message `@mtproxybot` with `/newproxy`, give it host + port + secret, paste the returned tag into admin → MTProto → Sponsor channel → Save. SSH to Amsterdam and restart: `systemctl restart mtproxy` (the systemd unit reads `/etc/default/mtproxy` MTPROXY_TAG env). Note: admin save persists to DB only — the daemon's runtime tag is set via env. So either:
* Update `/etc/default/mtproxy` MTPROXY_TAG manually and `systemctl restart mtproxy`
* OR extend the install script to be re-runnable with new tag

For now, treat sponsor channel as "set in DB for documentation, set in daemon env manually". This is acceptable for MVP.

---

## Task 15: Push + close Beads + final verification (10 min)

- [ ] **Step 1: Git status clean**

```bash
git status
```
Expected: nothing to commit.

- [ ] **Step 2: Push**

```bash
git push origin main
```

- [ ] **Step 3: Close Beads tasks**

```bash
bd close <Task-1..15 IDs from Task 0> --reason "B+C deployed: SOCKS5 user-facing removed, MTProto Telegram button live."
```

- [ ] **Step 4: Final user smoke**

User reports: dashboard works, connections page shows VLESS + Telegram button, no SOCKS5 anywhere.

- [ ] **Step 5: Update Beads memory**

```bash
bd remember --key "mtproto-perk-deployed" "MTProto Telegram-button perk is live (sub-project B+C). Daemon: Telegram official mtproto-proxy on 89.124.89.126:8443 (systemd unit mtproxy.service). Shared secret in app_settings.mtproto_secret. Admin page /mtproto. User endpoint /api/v1/user/mtproto. User-facing SOCKS5 (was :1080) and HTTP (was :8080) Xray inbounds fully removed; hub-internal SOCKS to Hysteria2 outbound stays at 127.0.0.1:1090."
```

---

## Risk Register

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | An old client app still has SOCKS5 in cached subscription | `subscription_version` bump signals re-fetch; old SOCKS5 URI just won't connect (port closed) — no harm. |
| R-02 | mtproto-proxy fails to build on Amsterdam (libssl mismatch) | Install script pulls latest Ubuntu deps; if it fails, fall back to `9seconds/mtg` (Go implementation, prebuilt binary) — adjust install script. |
| R-03 | Telegram bans the shared secret (overuse) | Admin "Regenerate" button rotates in seconds; clients re-fetch deeplink on next poll. |
| R-04 | Sponsor channel tag wrong → mtproto-proxy refuses to start | Empty default; admin sets after channel is ready and tag received. |
| R-05 | Active user has SOCKS-based plan (verified zero but theoretical) | Plans deactivated, not deleted; FK from `user_subscriptions` still resolves. VLESS Reality works for them via subscription URL. |
| R-06 | ConnectionsPage gut-removal breaks unrelated layout | Build verifies; deploy verified by Task 13 user smoke. Pure delete + add Telegram block in same JSX position. |
| R-07 | `requireUser` or other helpers don't exist by that exact name | Task 4 says: locate how other user-handlers extract userID and follow the same pattern. Don't invent names. |
| R-08 | Direct SSH to Amsterdam not configured from workstation | Task 6 Step 2: use existing admin Server Actions or chain through hub. The script is the artifact; transport is flexible. |

## Rollback

* **Whole feature**: `git revert <merge>` + redeploy backend + frontend. Migration 000041 keys harmless to keep; or `.down.sql` rolls back.
* **mtproto-proxy daemon broken**: on Amsterdam, `systemctl stop mtproxy && systemctl disable mtproxy`. UFW: `ufw delete allow 8443/tcp`. Admin UI: disable Enabled and Save.
* **User-facing SOCKS5 missed somewhere**: standalone grep + remove later — not a release blocker since :1080/:8080 inbounds are gone, no traffic possible.

## Out of scope (separate cycles)

* Flutter app changes — picks up new bundle shape automatically
* Sub-project D (per-plan rate-limit)
* Per-user MTProto secrets (would require custom mtproto-proxy fork or middleware)
* MTProto load balancing across multiple exits
* Telegram traffic analytics
* Removing the internal `socks5` tunnel-protocol OPTION in admin ServersPage (it's a Hub→Exit choice, kept as future fallback)
