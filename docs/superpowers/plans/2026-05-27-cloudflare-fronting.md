# Cloudflare CDN Fronting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add ☁️ Облако (VLESS-over-WebSocket through Cloudflare) as an additive subscription profile that bypasses per-IP Russian mobile operator blocks while preserving existing Reality direct profiles.

**Architecture:** Cloudflare proxies `cdn.hiearth.ru` (orange cloud) to our Hub origin nginx, which terminates TLS via CF Origin Cert, upgrades to WebSocket, and forwards to a new loopback Xray VLESS-WS inbound on :11443. Same exit-2 routing as Reality. Toggle-gated via `app_settings.cdn_enabled` for zero-risk rollback.

**Tech Stack:** Cloudflare DNS+CDN, nginx (existing :8443 + stream :443), Xray-core 26.3.27 (existing), PostgreSQL migrations, Go backend.

**Spec reference:** `docs/superpowers/specs/2026-05-27-cloudflare-fronting-design.md`

---

## File Structure

**New files (in repo):**
- `server/backend/migrations/000052_cdn_fronting.up.sql` — three app_settings rows
- `server/backend/migrations/000052_cdn_fronting.down.sql` — rollback

**Modified files (in repo):**
- `server/backend/cmd/api/xray_sync.go` — append `vless-ws-cdn` inbound when `cdn_enabled=true`
- `server/backend/cmd/api/client_api.go` — emit Облако URI when `cdn_enabled=true`

**New files (on Hub via SSH):**
- `/etc/nginx/cdn-origin/cert.pem` — CF Origin Cert (15-year)
- `/etc/nginx/cdn-origin/key.pem` — matching key
- `/etc/nginx/conf.d/cdn.conf` — new vhost for cdn.hiearth.ru

**Modified files (on Hub via SSH):**
- `/etc/nginx/stream.d/heyearth.conf` — add `cdn.hiearth.ru` to SNI router

---

## Task 0: Pre-flight (USER work in Cloudflare panel)

**Files:** N/A — user actions in Cloudflare dashboard + domain registrar panel.

This task gates everything. User completes these steps in CF web UI; provides Origin Cert files to implementer before Task 5.

- [ ] **Step 1: Create Cloudflare account**

Go to https://dash.cloudflare.com → Sign up (or login). Free plan is sufficient.

- [ ] **Step 2: Add hiearth.ru site to CF**

In CF dashboard → "Add a Site" → enter `hiearth.ru` → choose **Free plan**. CF will scan existing DNS, will detect A records `hiearth.ru → 45.150.39.236` and `admin.hiearth.ru → 45.150.39.236`. Accept the import.

- [ ] **Step 3: Set proxy=OFF on existing records (CRITICAL)**

For BOTH `hiearth.ru` and `admin.hiearth.ru`: ensure proxy status is **DNS only (grey cloud)**. If CF imported them as Proxied (orange) — toggle off. **This is required to keep Reality direct working** — orange cloud terminates TLS at CF and breaks Reality.

- [ ] **Step 4: Add new DNS A record for cdn.hiearth.ru**

In DNS panel → Add record:
- Type: `A`
- Name: `cdn`
- IPv4: `45.150.39.236`
- Proxy status: **Proxied (orange cloud)** ← CRITICAL

- [ ] **Step 5: Get CF nameservers**

CF will show 2 nameservers like `xxx.ns.cloudflare.com` and `yyy.ns.cloudflare.com` (specific names assigned to your account). Copy both.

- [ ] **Step 6: Update NS records at domain registrar**

Login to whoever sells you `hiearth.ru` (reg.ru / namecheap / godaddy / etc). Find DNS / nameserver settings for the domain. Replace existing NS records with the 2 CF nameservers from Step 5. Save.

- [ ] **Step 7: Wait for CF Active status**

Back in CF dashboard, refresh — domain status will show "Pending Nameserver Update" then within 5-60 minutes change to **"Active"**. CF will email confirmation.

- [ ] **Step 8: Configure SSL/TLS settings**

In CF dashboard → SSL/TLS → Overview:
- Encryption mode: **Full (strict)** ← CF↔origin cert validation, prevents MITM
- SSL/TLS → Edge Certificates → Minimum TLS Version: **1.2**
- SSL/TLS → Edge Certificates → Always Use HTTPS: **ON**

- [ ] **Step 9: Enable WebSockets**

CF dashboard → Network tab → **WebSockets: ON**. Critical for VLESS+WS to function.

- [ ] **Step 10: Generate Origin Certificate**

CF dashboard → SSL/TLS → Origin Server → "Create Certificate".
- Key type: **RSA (2048)** (works universally with nginx)
- Hostnames: `*.hiearth.ru, hiearth.ru` (wildcard covers cdn.hiearth.ru)
- Validity: **15 years**
- Click Create.

CF shows the cert (PEM format) and key (PEM format). Copy each to a separate text file:
- `cert.pem` — the certificate block (`-----BEGIN CERTIFICATE-----` … `-----END CERTIFICATE-----`)
- `key.pem` — the private key block (`-----BEGIN PRIVATE KEY-----` … `-----END PRIVATE KEY-----`)

- [ ] **Step 11: Verify cdn.hiearth.ru routes through CF**

After NS propagation (Step 7), from local machine (or any non-Hub network):

```bash
dig cdn.hiearth.ru +short
```

Expected: 2-3 CF edge IPs (typically `104.21.x.x` or `172.67.x.x`), **NOT** `45.150.39.236`. If you see Hub IP — wait longer for propagation or check proxy=ON in CF DNS settings.

```bash
curl -I https://cdn.hiearth.ru
```

Expected: response includes `server: cloudflare` header. You'll get an SSL/520 error from CF because origin doesn't yet have the vhost — that's expected at this stage. The point is to verify CF is in the path.

- [ ] **Step 12: Hand off cert.pem + key.pem to implementer**

Securely transfer the two PEM files. Implementer will install them on Hub in Task 5.

---

## Task 1: Database migration 000052 — CDN settings

**Files:**
- Create: `server/backend/migrations/000052_cdn_fronting.up.sql`
- Create: `server/backend/migrations/000052_cdn_fronting.down.sql`

- [ ] **Step 1: Write the up migration**

Create `server/backend/migrations/000052_cdn_fronting.up.sql`:

```sql
-- Three settings rows controlling the Cloudflare CDN fronting profile.
-- cdn_enabled is the master toggle (default false, off until E2E verified).
-- cdn_host and cdn_ws_path are configurable for future rotation without redeploy.

INSERT INTO app_settings (key, value, updated_at)
VALUES
    ('cdn_enabled',  'false',           NOW()),
    ('cdn_host',     'cdn.hiearth.ru',  NOW()),
    ('cdn_ws_path',  '/cdn-ws',         NOW())
ON CONFLICT (key) DO NOTHING;
```

The `ON CONFLICT DO NOTHING` makes the migration idempotent — re-running on an already-migrated DB doesn't error.

- [ ] **Step 2: Write the down migration**

Create `server/backend/migrations/000052_cdn_fronting.down.sql`:

```sql
DELETE FROM app_settings WHERE key IN ('cdn_enabled', 'cdn_host', 'cdn_ws_path');
```

- [ ] **Step 3: Verify migration parses (no apply yet)**

Build the backend — migrations run on startup, so build alone won't execute the SQL, but a syntax error in the SQL file would surface at runtime. Eyeball both files for typos.

```bash
cd server/backend && go build ./...
```

Expected: clean build, no errors.

- [ ] **Step 4: Commit**

```bash
git add server/backend/migrations/000052_cdn_fronting.up.sql server/backend/migrations/000052_cdn_fronting.down.sql
git commit -m "feat(migration): 000052 — app_settings rows for CDN fronting"
```

---

## Task 2: xray_sync.go — vless-ws-cdn inbound

**Files:**
- Modify: `server/backend/cmd/api/xray_sync.go` (around the `inbounds` slice declaration near line 213)

- [ ] **Step 1: Locate the inbounds declaration**

Open `server/backend/cmd/api/xray_sync.go`. Find the line containing:

```go
inbounds := []map[string]interface{}{
```

This is the slice declaring all Xray inbounds (currently: vless-reality :10443, vless-xhttp :2443, shadowsocks). We'll append a new entry to it conditionally.

- [ ] **Step 2: Add the conditional CDN inbound append**

After the closing `}` of the `inbounds := []map[string]interface{}{ ... }` declaration (after the shadowsocks entry, before the line that starts using `inbounds` elsewhere), insert this block:

```go
// ☁️ vless-ws-cdn — VLESS over WebSocket inbound on loopback :11443 для
// CDN-fronted path. Cloudflare terminates user-side TLS, our nginx vhost
// для cdn.hiearth.ru upgrades to WebSocket and proxies here. No security
// layer at xray level — TLS already terminated by CF, WS plaintext arrives
// from nginx via loopback. Toggle-gated via app_settings.cdn_enabled —
// inbound only generated when feature is enabled. Re-uses vlessClientsXHTTP
// (same per-user UUIDs, no flow because WebSocket is incompatible with
// XTLS Vision, identical rationale to xhttp transport).
if biz.GetSettingOrDefault("cdn_enabled", "false") == "true" {
	cdnWSPath := biz.GetSettingOrDefault("cdn_ws_path", "/cdn-ws")
	inbounds = append(inbounds, map[string]interface{}{
		"tag":      "vless-ws-cdn",
		"listen":   "127.0.0.1",
		"port":     11443,
		"protocol": "vless",
		"settings": map[string]interface{}{
			"clients":    vlessClientsXHTTP,
			"decryption": "none",
		},
		"streamSettings": map[string]interface{}{
			"network": "ws",
			"wsSettings": map[string]interface{}{
				"path":    cdnWSPath,
				"headers": map[string]interface{}{},
			},
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
		},
	})
}
```

- [ ] **Step 3: Build to verify syntax**

```bash
cd server/backend && go build ./...
```

Expected: clean build. If error mentions `vlessClientsXHTTP undefined`, the append block was placed before that variable was declared — move it down.

- [ ] **Step 4: Evidence test (RED) — inbound NOT in config when toggle off**

Locally generate the Xray config emission logic by running a quick check via the deployed Hub later. For now, this is verified by the integration test in Task 7. Skip empirical verification here, proceed.

- [ ] **Step 5: Commit**

```bash
git add server/backend/cmd/api/xray_sync.go
git commit -m "feat(xray): conditional vless-ws-cdn :11443 inbound (toggle-gated)"
```

---

## Task 3: client_api.go — ☁️ Облако profile emission

**Files:**
- Modify: `server/backend/cmd/api/client_api.go` (inside `buildHubConnectionOptions`, after the Дуб emission loop)

- [ ] **Step 1: Locate the Дуб emission loop**

Open `server/backend/cmd/api/client_api.go`. Find the block ending with the Дуб XHTTP emission (look for the `for i, fp := range xhttpFPs` loop and its closing `}`). The CDN block goes immediately after.

- [ ] **Step 2: Add the CDN profile emission**

Insert immediately after the Дуб for-loop's closing `}`, inside the same outer `if mainServerHost != "" && userVlessUUID != ""` block:

```go
// ☁️ Облако (CDN) — VLESS over WebSocket through Cloudflare. Bypasses
// per-IP operator blocks (Tele2 flapping, MTS throttling) by routing
// user traffic through CF edge IP pool (millions of IPs). Trades Reality
// stealth for CF's blanket-blocking immunity. Use when Berëza/Дуб are
// blocked at the operator level. Toggle-gated via app_settings.cdn_enabled
// for zero-deploy rollback. Priority 10 — displayed last; happ users try
// faster Reality profiles first, fall back to Облако if blocked.
if biz.GetSettingOrDefault("cdn_enabled", "false") == "true" {
	cdnHost := biz.GetSettingOrDefault("cdn_host", "cdn.hiearth.ru")
	cdnPath := biz.GetSettingOrDefault("cdn_ws_path", "/cdn-ws")
	if cdnHost != "" {
		cdnLabel := "☁️ Облако (CDN)"
		cdnURI := fmt.Sprintf(
			"vless://%s@%s:443?encryption=none&security=tls&sni=%s&type=ws&host=%s&path=%s&fp=chrome&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
			userVlessUUID, cdnHost, cdnHost, cdnHost,
			url.QueryEscape(cdnPath),
			url.PathEscape(cdnLabel))
		opts = append(opts, &heyearth_v1.ConnectionOption{
			Uri:                 cdnURI,
			Protocol:            "vless",
			Label:               cdnLabel,
			RecommendedNetworks: []string{"cellular", "wifi", "ethernet", "any"},
			Priority:            10, // displayed last — fallback when Reality direct fails
		})
	}
}
```

- [ ] **Step 3: Build to verify**

```bash
cd server/backend && go build ./...
```

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add server/backend/cmd/api/client_api.go
git commit -m "feat(subscription): emit ☁️ Облако VLESS+WS CDN profile (toggle-gated)"
```

---

## Task 4: Install CF Origin Cert on Hub

**Files:**
- Create on Hub: `/etc/nginx/cdn-origin/cert.pem`
- Create on Hub: `/etc/nginx/cdn-origin/key.pem`

**Prerequisite:** Task 0 step 12 — user has handed off cert.pem and key.pem to implementer.

- [ ] **Step 1: Save cert files to local /tmp**

Take the two PEM contents from user (Task 0.10). Save to local temp files (Windows):

```
C:\tmp\cdn-cert.pem
C:\tmp\cdn-key.pem
```

Ensure the cert file starts with `-----BEGIN CERTIFICATE-----` and key file with `-----BEGIN PRIVATE KEY-----` (or `-----BEGIN RSA PRIVATE KEY-----`).

- [ ] **Step 2: Create dir on Hub**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'mkdir -p /etc/nginx/cdn-origin && chmod 700 /etc/nginx/cdn-origin'
```

Expected: command exits 0.

- [ ] **Step 3: Upload cert file**

Using SSH pipe (Hub deploy uses SSH+stdin for file transfers because SFTP is unreliable per CLAUDE.md):

```bash
cd server/backend
type C:\tmp\cdn-cert.pem | go run ./cmd/deploy --ssh 'cat > /etc/nginx/cdn-origin/cert.pem && chmod 644 /etc/nginx/cdn-origin/cert.pem && wc -l /etc/nginx/cdn-origin/cert.pem'
```

Expected: outputs line count > 20 (cert is multi-line PEM).

- [ ] **Step 4: Upload key file**

```bash
cd server/backend
type C:\tmp\cdn-key.pem | go run ./cmd/deploy --ssh 'cat > /etc/nginx/cdn-origin/key.pem && chmod 600 /etc/nginx/cdn-origin/key.pem && wc -l /etc/nginx/cdn-origin/key.pem'
```

Expected: line count > 20.

- [ ] **Step 5: Verify cert validity**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'openssl x509 -in /etc/nginx/cdn-origin/cert.pem -noout -subject -dates'
```

Expected: subject mentions `*.hiearth.ru` or `hiearth.ru`; `notAfter=` shows a date ~15 years in future.

- [ ] **Step 6: Verify key matches cert**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'cert_md5=$(openssl x509 -noout -modulus -in /etc/nginx/cdn-origin/cert.pem | openssl md5); key_md5=$(openssl rsa -noout -modulus -in /etc/nginx/cdn-origin/key.pem | openssl md5); echo cert=$cert_md5 key=$key_md5; [ "$cert_md5" = "$key_md5" ] && echo MATCH || echo MISMATCH'
```

Expected: both MD5 digests identical, prints `MATCH`. If MISMATCH — user uploaded mismatched files; redo Task 0.10 → 4.3 → 4.4.

- [ ] **Step 7: Clean local /tmp**

```bash
del C:\tmp\cdn-cert.pem C:\tmp\cdn-key.pem
```

Removes private key from local disk.

- [ ] **Step 8: No commit (no files in repo touched)**

This task only changed Hub filesystem state. Nothing to git.

---

## Task 5: nginx vhost for cdn.hiearth.ru

**Files:**
- Create on Hub: `/etc/nginx/conf.d/cdn.conf`

- [ ] **Step 1: Write vhost config to Hub**

```bash
cd server/backend && go run ./cmd/deploy --ssh "cat > /etc/nginx/conf.d/cdn.conf <<'EOF'
# Cloudflare CDN fronting vhost for cdn.hiearth.ru.
# Receives WebSocket traffic from Cloudflare edge (CF terminates user TLS
# and re-encrypts to us via the CF Origin Cert). Upgrades WS and proxies
# plaintext WebSocket frames to Xray VLESS-WS inbound on 127.0.0.1:11443.
# Shares the :8443 listener already used by hiearth.ru/admin.hiearth.ru
# vhosts — nginx routes via Host-header (server_name) on TLS ALPN.

server {
    listen 8443 ssl http2;
    listen [::]:8443 ssl http2;
    server_name cdn.hiearth.ru;

    ssl_certificate     /etc/nginx/cdn-origin/cert.pem;
    ssl_certificate_key /etc/nginx/cdn-origin/key.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;

    location /cdn-ws {
        if (\$http_upgrade != \"websocket\") { return 404; }
        proxy_pass http://127.0.0.1:11443;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \"upgrade\";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }

    location / { return 444; }
}
EOF
chmod 644 /etc/nginx/conf.d/cdn.conf
echo wrote $(wc -l /etc/nginx/conf.d/cdn.conf)"
```

Expected: outputs `wrote NN /etc/nginx/conf.d/cdn.conf` where NN is around 25-30.

- [ ] **Step 2: Test nginx config (does NOT reload yet)**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'nginx -t'
```

Expected: `nginx: configuration file /etc/nginx/nginx.conf test is successful`.

If error mentions `cannot load certificate` — Task 4 cert files have permission/path issue. If `duplicate server name` — there's already a `cdn.hiearth.ru` server block somewhere; investigate before reload.

- [ ] **Step 3: No reload yet**

We'll reload nginx in Task 6 after also updating the stream SNI map. Skipping reload here ensures both changes land atomically.

- [ ] **Step 4: No commit**

(No repo files changed.)

---

## Task 6: nginx stream SNI router — add cdn.hiearth.ru

**Files:**
- Modify on Hub: `/etc/nginx/stream.d/heyearth.conf`

- [ ] **Step 1: Inspect current stream config**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'cat /etc/nginx/stream.d/heyearth.conf'
```

Expected output contains a `map $ssl_preread_server_name $backend { ... }` block with entries for hiearth.ru, www.hiearth.ru, admin.hiearth.ru, site.hiearth.ru, and a `default xray_vless;` line.

- [ ] **Step 2: Add cdn.hiearth.ru to the map**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sed -i.bak '/^\s*site.hiearth.ru/a\    cdn.hiearth.ru      nginx_https;' /etc/nginx/stream.d/heyearth.conf && grep -E 'cdn|site' /etc/nginx/stream.d/heyearth.conf"
```

Expected: grep output shows both `site.hiearth.ru` and `cdn.hiearth.ru` lines, both pointing to `nginx_https`.

If grep returns only the site line (no cdn) — sed pattern didn't match (different whitespace). Fall back to manual edit:

```bash
go run ./cmd/deploy --ssh 'nano /etc/nginx/stream.d/heyearth.conf'
```

Add the line manually inside the map block: `    cdn.hiearth.ru      nginx_https;`

- [ ] **Step 3: Test nginx full config**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'nginx -t'
```

Expected: `configuration file test is successful`.

- [ ] **Step 4: Reload nginx (atomic apply of Task 5 + Task 6)**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl reload nginx && systemctl is-active nginx'
```

Expected: outputs `active`.

- [ ] **Step 5: Verify nginx now listens cdn.hiearth.ru**

```bash
cd server/backend && go run ./cmd/deploy --ssh "curl -s -o /dev/null -w '%{http_code}\n' --resolve cdn.hiearth.ru:8443:127.0.0.1 https://cdn.hiearth.ru:8443/"
```

Expected: returns `444` (our deliberate non-WS-path silent-close) or `404` (location /cdn-ws non-WS request). NOT `502` (would indicate xray inbound not yet up — that's expected, we haven't deployed heyearth-api with new code yet; we'll do that in Task 7).

If returns `502` from hitting `/cdn-ws` directly — proceed. The vhost itself works; only the upstream xray inbound is missing, which gets fixed in Task 7.

- [ ] **Step 6: No commit**

---

## Task 7: Deploy heyearth-api + restart Xray

**Files:** (no edits, just deploy already-committed code from Tasks 1-3)

- [ ] **Step 1: Stop heyearth-api on Hub (clear old binary)**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
```

Expected: command exits 0.

- [ ] **Step 2: Deploy new binary via SSH pipe**

```bash
cd server/backend && go run quick_deploy.go
```

Expected output ends with:
```
heyearth-api: ACTIVE ✓
Health: {"status":"ok"}
Xray: active
```

Migration 000052 runs automatically on startup — three new rows inserted in app_settings.

- [ ] **Step 3: Verify migration applied**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -tA -c \"SELECT key, value FROM app_settings WHERE key LIKE 'cdn_%' ORDER BY key;\""
```

Expected output:
```
cdn_enabled|false
cdn_host|cdn.hiearth.ru
cdn_ws_path|/cdn-ws
```

- [ ] **Step 4: Verify Xray config does NOT yet contain vless-ws-cdn inbound (toggle off)**

```bash
cd server/backend && go run ./cmd/deploy --ssh "python3 -c \"import json; c=json.load(open('/opt/heyearth/xray/config.json')); print([i.get('tag') for i in c['inbounds']])\""
```

Expected output: `['vless-reality', 'vless-xhttp', 'shadowsocks', 'api-in']` — **NO `vless-ws-cdn` yet** because toggle is off. Correct.

- [ ] **Step 5: Verify subscription does NOT yet emit Облако (toggle off)**

```bash
cd server/backend && go run ./cmd/deploy --ssh "TOKEN=\$(sudo -u postgres psql -d heyearth -tA -c \"SELECT u.subscribe_token FROM users u JOIN user_subscriptions s ON s.user_id=u.id WHERE s.status='active' AND s.valid_until > NOW() LIMIT 1;\"); curl -s -A 'Happ/2.0.0' https://hiearth.ru/api/v1/user/subscribe/\$TOKEN | grep -oE '#[^[:space:]]+' | head -10"
```

Expected: 6 profiles (Берёза 1+2, Дуб 1+2, Бамбук 1+2) — NO ☁️ Облако. Correct.

- [ ] **Step 6: Verify Xray service stable**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl is-active xray && journalctl -u xray --since "30 sec ago" --no-pager | grep -iE "error|warning" | grep -v deprecat | head -5 || echo NO_ERRORS'
```

Expected: `active` followed by `NO_ERRORS` (or only the standard deprecation warnings filtered out).

- [ ] **Step 7: No commit** (code already committed in Tasks 1-3)

---

## Task 8: End-to-end test before enabling for users

**Files:** (no file changes, only DB toggle flip + manual user test)

This is the smoke test. We briefly enable Облако for ONLY admin testing, verify it works through CF, then disable until ready for production rollout.

- [ ] **Step 1: Enable CDN toggle**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE app_settings SET value='true', updated_at=NOW() WHERE key='cdn_enabled';\""
```

Expected: `UPDATE 1`.

- [ ] **Step 2: Restart heyearth-api to regenerate Xray config**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'systemctl restart heyearth-api && sleep 5 && systemctl restart xray && sleep 3 && systemctl is-active xray'
```

Expected: outputs `active`.

- [ ] **Step 3: Verify Xray config NOW contains vless-ws-cdn**

```bash
cd server/backend && go run ./cmd/deploy --ssh "python3 -c \"import json; c=json.load(open('/opt/heyearth/xray/config.json')); print([i.get('tag') for i in c['inbounds']])\""
```

Expected output includes `vless-ws-cdn` in the list.

- [ ] **Step 4: Verify Xray listens on :11443 loopback**

```bash
cd server/backend && go run ./cmd/deploy --ssh 'ss -tlnp | grep 11443'
```

Expected: line showing `127.0.0.1:11443` LISTEN, users xray.

- [ ] **Step 5: Verify subscription emits Облако**

```bash
cd server/backend && go run ./cmd/deploy --ssh "TOKEN=\$(sudo -u postgres psql -d heyearth -tA -c \"SELECT u.subscribe_token FROM users u JOIN user_subscriptions s ON s.user_id=u.id WHERE s.status='active' AND s.valid_until > NOW() LIMIT 1;\"); curl -s -A 'Happ/2.0.0' https://hiearth.ru/api/v1/user/subscribe/\$TOKEN | grep -oE '#[^[:space:]]+'"
```

Expected: 7 profiles, the new one being `#☁️%20Облако%20%28CDN%29` (URL-encoded).

- [ ] **Step 6: Extract the Облако URI and send to user**

```bash
cd server/backend && go run ./cmd/deploy --ssh "TOKEN=\$(sudo -u postgres psql -d heyearth -tA -c \"SELECT u.subscribe_token FROM users u JOIN user_subscriptions s ON s.user_id=u.id WHERE s.status='active' AND s.valid_until > NOW() LIMIT 1;\"); curl -s -A 'Happ/2.0.0' https://hiearth.ru/api/v1/user/subscribe/\$TOKEN | grep -E 'Облако|cdn.hiearth.ru'"
```

Expected: full `vless://...@cdn.hiearth.ru:443?...#☁️ Облако (CDN)` URI.

- [ ] **Step 7: USER ACTION — test in Happ**

User does:
1. Pull-to-refresh subscription in Happ (gets the 7-entry list).
2. Tap **☁️ Облако (CDN)** profile to connect.
3. Wait for "connected" indicator.
4. Open https://www.youtube.com and https://www.github.com in browser.

Expected: pages load smoothly via the CDN path. ping in Happ should show a value (any value < 2000ms indicates path is alive).

- [ ] **Step 8: If E2E test FAILS → diagnostic**

Common failures and where to look:
- **Profile shows "n/a" in Happ**: TCP-to-CF or TLS-to-CF fails. Check `dig cdn.hiearth.ru +short` from user's network — should resolve to CF IPs. Check user's network doesn't block CF entirely.
- **Connects but no internet**: WS handshake succeeded but xray routing wrong. On Hub: `journalctl -u xray --since '1 min ago' | grep vless-ws-cdn`. Look for "accepted tcp:..." entries with this inbound tag — if absent, nginx not forwarding correctly. Verify nginx logs: `tail /var/log/nginx/error.log` for proxy errors to 127.0.0.1:11443.
- **Slow / intermittent**: CF Free WS idle timeout may be biting. Check user is on subscription URI with `tcpKeepAliveInterval=15` (was already set in Task 3).

If diagnosis required, do NOT proceed to Task 9. Fix issue first, re-test from Step 7.

- [ ] **Step 9: If E2E test PASSES → proceed to Task 9 immediately**

Облако is functional. Move to enabling-for-all in Task 9. No code changes needed between Task 8 and 9 — they're sequential cutover steps.

---

## Task 9: Production rollout — enable Облако for all users

**Files:** (DB UPDATE only)

This task simply leaves `cdn_enabled=true` (already set in Task 8) and confirms it's live. Functionally Task 8.1 already enabled it. This task formalizes "production state" and documents the cutover.

- [ ] **Step 1: Confirm toggle is still ON**

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -tA -c \"SELECT key, value FROM app_settings WHERE key='cdn_enabled';\""
```

Expected: `cdn_enabled|true`. If somehow it got toggled off — UPDATE it back on:

```bash
cd server/backend && go run ./cmd/deploy --ssh "sudo -u postgres psql -d heyearth -c \"UPDATE app_settings SET value='true' WHERE key='cdn_enabled';\" && systemctl restart heyearth-api"
```

- [ ] **Step 2: Verify production subscription still emits Облако**

Same evidence command as Task 8 Step 5. Expected: 7 entries including ☁️ Облако.

- [ ] **Step 3: Commit a rollout marker file**

Create `docs/operations/cdn-rollout-2026-05-27.md` documenting the cutover for posterity:

```markdown
# CDN Fronting Production Rollout — 2026-05-27

**Status:** Enabled (cdn_enabled=true).
**Profiles in subscription:** Берёза 1+2, Дуб 1+2, Бамбук 1+2, ☁️ Облако.
**Verification:** E2E test from Task 8 passed — Облако reachable via CF, traffic flows.

**Rollback procedure** (single SQL):
```sql
UPDATE app_settings SET value='false' WHERE key='cdn_enabled';
```
Then `systemctl restart heyearth-api`. Облако disappears from new subscriptions on next user pull-to-refresh. Existing connections continue until user reconnects.

**Code refs:**
- Migration: server/backend/migrations/000052_cdn_fronting.up.sql
- xray_sync.go: vless-ws-cdn inbound (toggle-gated)
- client_api.go: Облако emission (toggle-gated)
- Hub nginx: /etc/nginx/conf.d/cdn.conf, /etc/nginx/stream.d/heyearth.conf
- Hub cert: /etc/nginx/cdn-origin/{cert,key}.pem (15-year CF Origin Cert)
```

- [ ] **Step 4: Commit**

```bash
git add docs/operations/cdn-rollout-2026-05-27.md
git commit -m "docs(ops): CDN fronting production rollout marker"
git push origin main
```

---

## Task 10: Post-deploy verification

**Files:** (verification only)

- [ ] **Step 1: User asks 1-2 testers to pull subscription and try Облако**

Including the original Tele2 user (the one with persistent Hub IP blocks). Expected: Облако works for them even when Берёза/Дуб timeout.

- [ ] **Step 2: Monitor Hub xray logs for vless-ws-cdn traffic**

```bash
cd server/backend && go run ./cmd/deploy --ssh "journalctl -u xray --since '5 min ago' --no-pager | grep vless-ws-cdn | head -20"
```

Expected: lines like `from 127.0.0.1:NNNNN accepted tcp:<dest> [vless-ws-cdn -> exit-2]`. Empty = no users have connected yet (give it time and circulate URIs).

- [ ] **Step 3: Monitor for nginx errors on cdn.hiearth.ru vhost**

```bash
cd server/backend && go run ./cmd/deploy --ssh "tail -50 /var/log/nginx/error.log | grep -iE 'cdn|11443' | head -10"
```

Expected: empty or only periodic info-level entries. No persistent error patterns.

- [ ] **Step 4: Spot-check CF analytics**

Optionally in CF dashboard → Analytics tab → check requests to cdn.hiearth.ru. After 1+ hour of traffic, should show measurable WebSocket connections.

- [ ] **Step 5: Close out**

If all checks pass — feature is live and stable. Update Beads:

```bash
bd create --title "CDN fronting live in production" --description "☁️ Облако profile emitted to all users. Toggle: cdn_enabled=true. Rollback procedure: see docs/operations/cdn-rollout-2026-05-27.md. Spec: docs/superpowers/specs/2026-05-27-cloudflare-fronting-design.md." --type=feature --priority=2
```

If issues observed → DO NOT close. Rollback per Task 9 step 3 rollback procedure. Open Beads bug ticket with observed symptoms.

---

## Self-Review

**Spec coverage check:**
- ✅ Section A (Architecture two-path additive): Tasks 1-3 implement the additive code paths
- ✅ Section B (CF account + DNS + Origin cert): Task 0 user pre-flight covers all 12 steps verbatim from spec
- ✅ Section C.1 (Xray new inbound): Task 2 implements with all fields from spec
- ✅ Section C.2 (nginx vhost): Task 5 implements verbatim from spec
- ✅ Section C.3 (stream SNI map): Task 6 implements
- ✅ Section C.4 (cert storage): Task 4 covers cert install
- ✅ Section D.1 (app_settings keys): Task 1 migration
- ✅ Section D.2 (Облако URI emission): Task 3
- ✅ Section E (cutover stages): Tasks 7 (Stage 1) → 8 (Stage 2) → 9 (Stage 3) → 10 (post-deploy)
- ✅ Rollback (E.5): documented in Task 9 step 3 ops doc

No gaps.

**Placeholder scan:** Grepped this document for TBD, TODO, "implement later" — none found. All code blocks complete. All evidence commands have expected outputs.

**Type consistency:**
- `vlessClientsXHTTP` referenced in Task 2 — exists in xray_sync.go (verified during brainstorming)
- `app_settings` keys `cdn_enabled`, `cdn_host`, `cdn_ws_path` consistent across Task 1 (migration), Task 2 (xray_sync read), Task 3 (client_api read)
- `cdnHost`, `cdnPath`, `cdnURI` variable naming consistent in Task 3
- `/etc/nginx/cdn-origin/cert.pem` and `/etc/nginx/cdn-origin/key.pem` paths consistent across Tasks 4, 5
- Port 11443 (xray vless-ws-cdn inbound), 8443 (nginx HTTPS), 443 (CF edge — user-facing) — distinct, no conflicts

All consistent.
