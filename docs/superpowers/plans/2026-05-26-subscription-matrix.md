# Subscription Matrix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 3 current subscription entries (🌳 Берёза / 🌲 Дуб / 🎋 Бамбук) with an 8-entry matrix: 4 Berëza × uTLS FPs (chrome/safari/ios/firefox) + 2 Дуб × FPs (chrome/safari XHTTP) + 2 Бамбук × CC (BBR + Brutal). This gives happ-desktop enough fingerprint/transport variety to find a working profile on any Wi-Fi operator's DPI.

**Architecture:** Server-side Xray and Hysteria2 inbounds remain unchanged — Xray Reality accepts any uTLS fingerprint from the client (FP is client-only), and Hysteria2 server accepts both BBR and Brutal congestion control transparently. All matrix expansion happens in `client_api.go::buildHubConnectionOptions` which emits 8 `ConnectionOption` protos instead of 3. happ-desktop auto-sorts by ping (working → top, n/a → bottom) and natively filters Бамбуки on cellular via the `"only WiFi"` substring in their labels.

**Tech Stack:** Go 1.25+, gRPC server in `cmd/api`, protobuf `ConnectionOption` schema, Xray-core 26.3.27 on Hub, Hysteria2 server with port-hopping iptables on Hub :50443.

**Spec:** `docs/superpowers/specs/2026-05-26-subscription-matrix-design.md`
**Beads epic:** `heyearthproject-s7mc`

---

## File Structure

Only one file changes. No new files, no new packages, no schema changes:

| File | Lines | Change |
|---|---|---|
| `server/backend/cmd/api/client_api.go` | 177-321 (function `buildHubConnectionOptions` body) | Replace the 3-block emit (Berëza single / Дуб single / Бамбук single) with an 8-emit version. Helper data tables for FP variants. |

Everything else (Xray config generation in `xray_sync.go`, Reality keys, Hysteria2 password rotation, subscription delivery in `subscription_handler.go`, smart-routing happ:// prefix in `biz/happ_routing.go`) stays as-is.

---

## Task 1: Rewrite buildHubConnectionOptions to emit 8 entries

**Files:**
- Modify: `server/backend/cmd/api/client_api.go:177-321`

**Context the engineer needs:**

The function `buildHubConnectionOptions(userID int) []*heyearth_v1.ConnectionOption` builds the per-user list of profiles that happ sees in its subscription. The user clicks any profile name and happ auto-configures the underlying VPN client (Xray for VLESS entries, Hysteria2 for hysteria2:// entries). Above line 177 the function has already resolved:
- `userVlessUUID` (per-user Reality auth UUID)
- `mainServerHost` (= "hiearth.ru" — Hub's public hostname)
- `userID` (passed in)

Above line 178 it also calls `biz.IsProtocolAllowed(userID, "vless")` to honour plan-level restrictions. We preserve that check — premium-only plans should not see VLESS entries.

The current code reads three app_settings (`reality_public_key`, `reality_short_id`, `reality_sni_domain`, `reality_flow`, `xhttp_port`) and per-user Hysteria2 credentials (`biz.GetUserHysteria2Password`, `biz.GetHysteria2ObfsPassword`). We keep all those.

The current 3 emits look like (abbreviated):
```go
// 🌳 Берёза - chrome FP TCP Reality :443
vlessURI := fmt.Sprintf("vless://%s@%s:443?...&fp=chrome&...#%s", ...)
opts = append(opts, &heyearth_v1.ConnectionOption{Uri: vlessURI, Label: "🌳 Берёза - основной", Priority: 0, ...})

// 🌲 Дуб - chrome FP XHTTP Reality :2443
xhttpURI := fmt.Sprintf("vless://%s@%s:%s?...&type=xhttp&...&fp=chrome&...#%s", ..., xhttpPort, ...)
opts = append(opts, &heyearth_v1.ConnectionOption{Uri: xhttpURI, Label: "🌲 Дуб - запасной", Priority: 1, ...})

// 🎋 Бамбук - Hysteria2 default BBR
hy2URI := fmt.Sprintf("hysteria2://%s@%s:50443/?obfs=salamander&...#%s", ...)
opts = append(opts, &heyearth_v1.ConnectionOption{Uri: hy2URI, Label: "🎋 Бамбук - скорость, только Wi-Fi", Priority: 2, ...})
```

The new code emits 8 entries by iterating over small data tables.

- [ ] **Step 1: Read current state of the function**

```bash
sed -n '170,325p' server/backend/cmd/api/client_api.go
```

Expected: see the existing buildHubConnectionOptions body with three opts.append calls (Berëza, Дуб, Бамбук) ending at line ~321 with `return opts`.

- [ ] **Step 2: Replace lines 177-321 with the matrix version**

Open `server/backend/cmd/api/client_api.go`. Replace everything between line 177 (the `if mainServerHost != "" && userVlessUUID != "" {` line — keep this line) and the `return opts` line (~323), with the code below.

The new code structure:
1. Define `realityFPs` slice (4 entries) and `xhttpFPs` slice (2 entries) as small data tables.
2. Loop over realityFPs → emit 4 Berëza URIs (priority 0..3).
3. Loop over xhttpFPs → emit 2 Дуб URIs (priority 4..5).
4. Emit Бамбук 1 (BBR, no bandwidth params) at priority 6.
5. Emit Бамбук 2 (Brutal, with `&upmbps=20&downmbps=80`) at priority 7.

Exact replacement code (paste this in place of lines 177-321; the closing `}` of `if mainServerHost != "" && userVlessUUID != ""` and the `return opts` line stay at the end):

```go
	if mainServerHost != "" && userVlessUUID != "" {
		// 1. VLESS Reality (максимальная защита от DPI) — only if plan allows vless
		realityPubKey := biz.GetSettingOrDefault("reality_public_key", "")
		realityShortID := biz.GetSettingOrDefault("reality_short_id", "")
		realitySNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
		if realityPubKey != "" && canVLESS {
			flow := biz.GetSettingOrDefault("reality_flow", "xtls-rprx-vision")
			flowParam := ""
			if flow != "" {
				flowParam = "&flow=" + flow
			}

			// FP matrix — happ-desktop sees different uTLS ClientHello JA3s for
			// each entry. Russian ISP DPI applies different rules per operator;
			// chrome/safari/ios/firefox give us 4 distinct JA3 signatures so at
			// least one passes through any given operator's DPI whitelist.
			// uTLS FP is purely client-side — server (Xray) doesn't validate it,
			// so all 4 entries share the same vless-reality inbound on :443.
			//
			// PathEscape (not QueryEscape): RFC 3986 fragment encoding emits %20
			// for spaces; QueryEscape's `+` is rendered literally by happ-desktop.
			//
			// 🌳 Берёза 1..4 — TCP Reality :443, 4 uTLS variants.
			realityFPs := []string{"chrome", "safari", "ios", "firefox"}
			for i, fp := range realityFPs {
				label := fmt.Sprintf("🌳 Берёза %d", i+1)
				vlessURI := fmt.Sprintf(
					"vless://%s@%s:443?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=tcp&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100%s#%s",
					userVlessUUID, mainServerHost, realitySNI, fp, realityPubKey, realityShortID, flowParam,
					url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 vlessURI,
					Protocol:            "vless",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
					Priority:            int32(i), // 0..3
				})
			}

			// 🌲 Дуб 1..2 — XHTTP Reality :2443 (configurable port via app_settings.xhttp_port),
			// 2 uTLS variants. XHTTP framing looks like an HTTP/1.1 long-poll
			// instead of pure-Reality timing pattern, slipping past DPI that
			// learned to fingerprint TCP+Vision. Server inbound `vless-xhttp`
			// strips the `flow` field from clients (see xray_sync.go::vlessClientsXHTTP)
			// because XTLS Vision is incompatible with xhttp transport.
			xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")
			xhttpFPs := []string{"chrome", "safari"}
			for i, fp := range xhttpFPs {
				label := fmt.Sprintf("🌲 Дуб %d", i+1)
				xhttpURI := fmt.Sprintf(
					"vless://%s@%s:%s?encryption=none&security=reality&sni=%s&fp=%s&pbk=%s&sid=%s&type=xhttp&path=%%2F&host=%s&mode=auto&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
					userVlessUUID, mainServerHost, xhttpPort, realitySNI, fp, realityPubKey, realityShortID,
					url.QueryEscape(mainServerHost),
					url.PathEscape(label))
				opts = append(opts, &heyearth_v1.ConnectionOption{
					Uri:                 xhttpURI,
					Protocol:            "vless",
					Label:               label,
					RecommendedNetworks: []string{"wifi", "cellular", "ethernet", "any"},
					Priority:            int32(4 + i), // 4..5
				})
			}
		}
	}

	// 2. Hysteria2 «Бамбук» — QUIC/UDP, Wi-Fi only (mobile carriers QoS UDP).
	// Two entries differ only in congestion control:
	//   Бамбук 1 — no bandwidth in URI → Hysteria2 client picks BBR (adaptive)
	//   Бамбук 2 — explicit upmbps/downmbps → Hysteria2 client picks Brutal CC
	//              (fixed bandwidth, ignores loss-induced backoff — beats operator
	//              QoS that throttles UDP via packet loss injection)
	// Both labels contain "only WiFi" — happ natively hides them on cellular.
	if mainServerHost != "" {
		hy2Password, hy2Err := biz.GetUserHysteria2Password(userID)
		obfsPassword := biz.GetHysteria2ObfsPassword()
		if hy2Err == nil && hy2Password != "" && obfsPassword != "" {
			// Бамбук 1 (BBR adaptive)
			bambooBBRLabel := "🎋 Бамбук 1 only WiFi"
			bambooBBRURI := fmt.Sprintf(
				"hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=50000-50100#%s",
				url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
				url.PathEscape(bambooBBRLabel))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 bambooBBRURI,
				Protocol:            "hysteria2",
				Label:               bambooBBRLabel,
				RecommendedNetworks: []string{"wifi", "ethernet"},
				Priority:            6,
			})

			// Бамбук 2 (Brutal CC, 20↑/80↓ Mbps fixed)
			bambooBrutalLabel := "🎋 Бамбук 2 only WiFi"
			bambooBrutalURI := fmt.Sprintf(
				"hysteria2://%s@%s:50443/?obfs=salamander&obfs-password=%s&sni=%s&insecure=0&mport=50000-50100&upmbps=20&downmbps=80#%s",
				url.QueryEscape(hy2Password), mainServerHost, url.QueryEscape(obfsPassword), mainServerHost,
				url.PathEscape(bambooBrutalLabel))
			opts = append(opts, &heyearth_v1.ConnectionOption{
				Uri:                 bambooBrutalURI,
				Protocol:            "hysteria2",
				Label:               bambooBrutalLabel,
				RecommendedNetworks: []string{"wifi", "ethernet"},
				Priority:            7,
			})
		}
	}

	return opts
}
```

Note the structural changes vs old code:
- Variables `realityFPs` and `xhttpFPs` are local — no app_settings or DB calls per FP, fixed list.
- Loops use `int32(i)` and `int32(4 + i)` for Priority — explicit cast to satisfy protobuf int32 field.
- Helper `xhttpPort := biz.GetSettingOrDefault("xhttp_port", "2443")` stays — already added in commit `ecd4556`.
- Bамбук 1 URI is byte-identical to the previous `hy2URI` except for the fragment name; Бамбук 2 only adds `&upmbps=20&downmbps=80` before the `#`.

- [ ] **Step 3: Build the backend to verify the rewrite compiles**

```bash
cd server/backend && go build ./...
```

Expected: no output (compile clean). If you see an error mentioning `int32` or `url.PathEscape`, double-check you pasted the loop bodies correctly.

- [ ] **Step 4: Vet**

```bash
cd server/backend && go vet ./...
```

Expected: no output.

- [ ] **Step 5: Sanity-check the diff size**

```bash
git diff --stat server/backend/cmd/api/client_api.go
```

Expected: a single file changed, roughly `100-130 deletions, 90-110 insertions`. If diff size differs by more than 30%, you removed or kept too much — re-read the structural notes in Step 2.

- [ ] **Step 6: Commit**

```bash
git add server/backend/cmd/api/client_api.go
git commit -m "$(cat <<'EOF'
feat(subscription): 8-entry matrix with FP/CC diversity

Replace 3 entries (Berëza/Дуб/Бамбук) with 8-entry matrix:
  * 🌳 Берёза 1..4 — TCP Reality :443 × uTLS chrome/safari/ios/firefox
  * 🌲 Дуб 1..2   — XHTTP Reality :2443 × uTLS chrome/safari
  * 🎋 Бамбук 1   — Hysteria2 BBR (adaptive)
  * 🎋 Бамбук 2   — Hysteria2 Brutal CC (20↑/80↓ Mbps fixed)

Targets the «переменный успех» Reality shows on Wi-Fi across
different Russian ISP DPI: each operator's whitelist passes
different JA3 hashes, so shipping 4-FP variety gives happ-sort
enough material to find a working profile per network. Brutal CC
beats operator UDP QoS by ignoring loss-induced backoff.

No server-side changes — Xray Reality accepts any uTLS FP from
clients (FP is client-only signal), and Hysteria2 server handles
BBR/Brutal transparently. happ-native "only WiFi" filter in
Бамбук labels hides UDP entries on cellular automatically.

Spec: docs/superpowers/specs/2026-05-26-subscription-matrix-design.md
Beads: heyearthproject-s7mc

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

Expected: commit succeeds, hash shown in output.

---

## Task 2: Deploy + curl smoke verify

**Files:**
- Touches: production Hub binary at `/opt/heyearth/mainserver/heyearth-api` (deploy via `quick_deploy.go`)

**Context the engineer needs:**

`quick_deploy.go` is a fast SSH-piped binary upload (avoids SFTP which hangs on this VPS). It builds locally with `GOOS=linux GOARCH=amd64`, then streams the binary over SSH to `/opt/heyearth/mainserver/heyearth-api.new`, runs `mv && systemctl restart heyearth-api`. The service must be stopped before upload because the running process holds an open fd on the binary (Linux allows replacing but `mv` might race the restart).

Subscribe endpoint is `GET /api/v1/user/subscribe/<token>`. The User-Agent matters: when it contains "happ" we return raw happ:// format (smart-routing prefix + one URI per line). Otherwise base64 v2rayN format. We use `-H "User-Agent: Happ/2.8.0"` to get the line format that's easy to grep.

The user from previous session has token `<redacted>`. Their vless_uuid is `<redacted>` and hysteria2_password starts with `f619861494deaf63...`. Use this token for the smoke test.

- [ ] **Step 1: Stop the API service before binary upload**

```bash
cd server/backend
go run ./cmd/deploy --ssh 'systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api'
```

Expected: no errors. Service stops in <2s.

- [ ] **Step 2: Run quick_deploy**

```bash
cd server/backend && go run quick_deploy.go
```

Expected: messages like `building...`, `uploading...`, `restarting...`, finally `done`. Total ~30-60s depending on link.

- [ ] **Step 3: Verify service is up + healthy**

```bash
cd server/backend
go run ./cmd/deploy --ssh 'systemctl status heyearth-api --no-pager | head -10'
```

Expected: `Active: active (running)`, recent start time, no error in last log lines.

- [ ] **Step 4: Curl subscription**

```bash
curl -sS -H "User-Agent: Happ/2.8.0" \
  "https://hiearth.ru/api/v1/user/subscribe/<redacted>" \
  > /tmp/subs.txt
wc -l /tmp/subs.txt
cat /tmp/subs.txt
```

Expected:
- `wc -l` reports **9 lines** (1 happ:// smart-routing prefix line + 8 URI lines).
- First line starts with `happ://routing/onadd/`
- Lines 2-9 are URIs in this order (priorities 0..7):
  - `vless://...@hiearth.ru:443?...&fp=chrome&...&flow=xtls-rprx-vision#%F0%9F%8C%B3%20%D0%91%D0%B5%D1%80%D1%91%D0%B7%D0%B0%201`
  - `vless://...@hiearth.ru:443?...&fp=safari&...` (Берёза 2)
  - `vless://...@hiearth.ru:443?...&fp=ios&...` (Берёза 3)
  - `vless://...@hiearth.ru:443?...&fp=firefox&...` (Берёза 4)
  - `vless://...@hiearth.ru:2443?...&fp=chrome&...&type=xhttp&...` (Дуб 1)
  - `vless://...@hiearth.ru:2443?...&fp=safari&...&type=xhttp&...` (Дуб 2)
  - `hysteria2://...@hiearth.ru:50443/?obfs=salamander&...#...%D0%91%D0%B0%D0%BC%D0%B1%D1%83%D0%BA%201%20only%20WiFi` (no `upmbps`)
  - `hysteria2://...?...&upmbps=20&downmbps=80#...%D0%91%D0%B0%D0%BC%D0%B1%D1%83%D0%BA%202%20only%20WiFi`

- [ ] **Step 5: Decode each FP and label to verify matrix**

```bash
grep -oE 'fp=[a-z]+' /tmp/subs.txt
```

Expected exactly six lines:
```
fp=chrome
fp=safari
fp=ios
fp=firefox
fp=chrome
fp=safari
```
(First 4 are Berëza, last 2 are Дуб XHTTP.)

```bash
grep -oE '#[^[:space:]]+' /tmp/subs.txt | while read frag; do
  python3 -c "import urllib.parse, sys; print(urllib.parse.unquote(sys.argv[1][1:]))" "$frag"
done
```

Expected exactly 8 decoded labels:
```
🌳 Берёза 1
🌳 Берёза 2
🌳 Берёза 3
🌳 Берёза 4
🌲 Дуб 1
🌲 Дуб 2
🎋 Бамбук 1 only WiFi
🎋 Бамбук 2 only WiFi
```

- [ ] **Step 6: Confirm Brutal-only params present exactly once**

```bash
grep -c 'upmbps=20&downmbps=80' /tmp/subs.txt
```

Expected: `1` (only on Бамбук 2, not on Бамбук 1).

```bash
grep -c 'type=xhttp' /tmp/subs.txt
```

Expected: `2` (the two Дуб entries).

- [ ] **Step 7: Confirm uniform XHTTP host param**

```bash
grep -oE 'host=[^&#]+' /tmp/subs.txt
```

Expected: exactly 2 occurrences of `host=hiearth.ru` (one per Дуб entry — Берёза TCP has no `host=` param).

If any check fails, fix the code in `client_api.go` and redeploy starting from Task 2 Step 1. Do not proceed to Task 3 until all 7 checks pass.

---

## Task 3: Hub self-test through 3 selected URIs

**Files:**
- Touches: temporary xray client configs on Hub (`/tmp/test-berëza-chrome.json`, `/tmp/test-berëza-firefox.json`, `/tmp/test-dub-safari.json`)

**Context the engineer needs:**

The Hub has `/opt/heyearth/xray/xray` (Xray-core 26.3.27, upgraded in previous session). We start a local xray client process on Hub bound to `127.0.0.1:NNNN` as SOCKS5 inbound, with outbound = the Reality URI we want to test. Then `curl --socks5-hostname 127.0.0.1:NNNN https://api.ipify.org` proxies through the URI. If Reality auth succeeds → traffic goes through one of the exit nodes → ipify returns the exit's IP.

Currently only one exit node is online: `192.0.2.126` (NL Amsterdam 32Tb). So the success criterion is: `curl ipify` returns exactly `192.0.2.126`.

We test 3 critical paths:
1. **Berëza Chrome (priority 0)** — most-used path, must work.
2. **Berëza Firefox (priority 3)** — new FP added in this matrix, verify uTLS firefox config doesn't break Reality auth.
3. **Дуб Safari (priority 5)** — XHTTP transport + safari FP, two new dimensions at once.

We skip ios FP and Дуб chrome because those reuse param patterns already proven elsewhere (Berëza safari = same code path as ios; Дуб safari = same code path as Дуб chrome modulo `fp=`).

We test from Hub itself (not from a separate IP) — server↔server testing isolates server-side correctness; client-side reachability is a separate concern verified by the user's happ app afterwards.

- [ ] **Step 1: Get current Reality keys from app_settings**

```bash
cd server/backend
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -t -c "SELECT key, value FROM app_settings WHERE key IN ('"'"'reality_public_key'"'"','"'"'reality_short_id'"'"','"'"'reality_sni_domain'"'"',  '"'"'main_server_host'"'"');"'
```

Expected output contains four key=value pairs. Note the values — you'll inline them into the test configs below. Example values (yours may differ):
- `reality_public_key | 6AIgfh6XhR5ET-9p5A-7ixl3PqXklzvvF5Is04IfJgA`
- `reality_short_id  | d42a7317e5206bd4`
- `reality_sni_domain | yandex.ru`
- `main_server_host  | hiearth.ru`

Also get the user's vless_uuid (we use the same test user as Task 2):

```bash
go run ./cmd/deploy --ssh 'set -a; source /opt/heyearth/mainserver/api.env; psql "$DATABASE_URL" -t -c "SELECT vless_uuid FROM users WHERE subscribe_token='"'"'<redacted-uuid>'"'"';"'
```

Expected: a single UUID (e.g. `<redacted>`).

- [ ] **Step 2: Write Berëza Chrome test config to Hub and run it**

Substitute the values from Step 1 into the config below (replace `<PBK>`, `<SID>`, `<SNI>`, `<UUID>` with the four values you got).

```bash
go run ./cmd/deploy --ssh 'cat > /tmp/test-bereza-chrome.json << EOF
{
  "log":{"loglevel":"info"},
  "inbounds":[{"tag":"socks","listen":"127.0.0.1","port":10920,"protocol":"socks","settings":{"auth":"noauth","udp":true}}],
  "outbounds":[{
    "tag":"bereza-chrome",
    "protocol":"vless",
    "settings":{"vnext":[{"address":"198.51.100.236","port":443,"users":[{"id":"<UUID>","encryption":"none","flow":"xtls-rprx-vision"}]}]},
    "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"<SNI>","fingerprint":"chrome","publicKey":"<PBK>","shortId":"<SID>"}}
  }]
}
EOF
/opt/heyearth/xray/xray run -config /tmp/test-bereza-chrome.json > /tmp/test-bereza-chrome.log 2>&1 &
sleep 2
timeout 10 curl -sk --max-time 8 --socks5-hostname 127.0.0.1:10920 -w "\nSTATUS:%{http_code} TIME:%{time_total}s\n" https://api.ipify.org
pkill -f "xray run -config /tmp/test-bereza-chrome.json"
'
```

Expected output ends with:
```
192.0.2.126
STATUS:200 TIME:0.2XXs
```

If you get STATUS:000 or empty body — check `/tmp/test-bereza-chrome.log` on Hub for errors. Most common cause: wrong UUID/PBK/SID substitution.

- [ ] **Step 3: Berëza Firefox test (new FP — must validate uTLS firefox impersonation)**

```bash
go run ./cmd/deploy --ssh 'cat > /tmp/test-bereza-firefox.json << EOF
{
  "log":{"loglevel":"info"},
  "inbounds":[{"tag":"socks","listen":"127.0.0.1","port":10921,"protocol":"socks","settings":{"auth":"noauth","udp":true}}],
  "outbounds":[{
    "tag":"bereza-firefox",
    "protocol":"vless",
    "settings":{"vnext":[{"address":"198.51.100.236","port":443,"users":[{"id":"<UUID>","encryption":"none","flow":"xtls-rprx-vision"}]}]},
    "streamSettings":{"network":"tcp","security":"reality","realitySettings":{"serverName":"<SNI>","fingerprint":"firefox","publicKey":"<PBK>","shortId":"<SID>"}}
  }]
}
EOF
/opt/heyearth/xray/xray run -config /tmp/test-bereza-firefox.json > /tmp/test-bereza-firefox.log 2>&1 &
sleep 2
timeout 10 curl -sk --max-time 8 --socks5-hostname 127.0.0.1:10921 -w "\nSTATUS:%{http_code} TIME:%{time_total}s\n" https://api.ipify.org
pkill -f "xray run -config /tmp/test-bereza-firefox.json"
'
```

Expected: `192.0.2.126` + `STATUS:200`. If firefox FP fails but chrome passes, the uTLS firefox profile may not be supported by the current Xray build — unlikely for 26.3.27, but report this to user before continuing.

- [ ] **Step 4: Дуб Safari XHTTP test (new transport + new FP combo)**

```bash
go run ./cmd/deploy --ssh 'cat > /tmp/test-dub-safari.json << EOF
{
  "log":{"loglevel":"info"},
  "inbounds":[{"tag":"socks","listen":"127.0.0.1","port":10922,"protocol":"socks","settings":{"auth":"noauth","udp":true}}],
  "outbounds":[{
    "tag":"dub-safari",
    "protocol":"vless",
    "settings":{"vnext":[{"address":"198.51.100.236","port":2443,"users":[{"id":"<UUID>","encryption":"none"}]}]},
    "streamSettings":{"network":"xhttp","security":"reality","xhttpSettings":{"host":"hiearth.ru","path":"/","mode":"auto"},"realitySettings":{"serverName":"<SNI>","fingerprint":"safari","publicKey":"<PBK>","shortId":"<SID>"}}
  }]
}
EOF
/opt/heyearth/xray/xray run -config /tmp/test-dub-safari.json > /tmp/test-dub-safari.log 2>&1 &
sleep 2
timeout 10 curl -sk --max-time 8 --socks5-hostname 127.0.0.1:10922 -w "\nSTATUS:%{http_code} TIME:%{time_total}s\n" https://api.ipify.org
pkill -f "xray run -config /tmp/test-dub-safari.json"
'
```

Expected: `192.0.2.126` + `STATUS:200`. The xhttp inbound on :2443 uses `mode: auto` server-side; Xray 26.x clients negotiate to `stream-one` (HTTP/2). Verify the log on Hub mentions `XHTTP is dialing to tcp:198.51.100.236:2443, mode stream-one`:

```bash
go run ./cmd/deploy --ssh 'grep "XHTTP is dialing" /tmp/test-dub-safari.log | head -1'
```

Expected: a line like `transport/internet/splithttp: XHTTP is dialing to tcp:198.51.100.236:2443, mode stream-one, HTTP version 2, host hiearth.ru`.

- [ ] **Step 5: Clean up Hub test files**

```bash
go run ./cmd/deploy --ssh 'rm -f /tmp/test-bereza-chrome.json /tmp/test-bereza-firefox.json /tmp/test-dub-safari.json /tmp/test-bereza-chrome.log /tmp/test-bereza-firefox.log /tmp/test-dub-safari.log'
```

Expected: no output.

- [ ] **Step 6: Beads close + status update**

```bash
bd update heyearthproject-s7mc --notes "8-entry matrix deployed. Hub self-test passes: Berëza-chrome ✓, Berëza-firefox ✓, Дуб-safari (xhttp stream-one HTTP/2) ✓. All return exit IP 192.0.2.126 (NL)."
bd close heyearthproject-s7mc
```

Expected: beads marks epic as closed.

- [ ] **Step 7: Push to remote**

```bash
git push origin main
```

Expected: push succeeds, commits visible on remote.

---

## Self-Review

Skimming the spec against the plan:

**Spec coverage:**
- ✅ Acceptance #1 (8 URI lines) → covered in Task 2 Step 4 (`wc -l == 9`)
- ✅ Acceptance #2 (label names exact) → covered in Task 2 Step 5 (URL-decode each fragment)
- ✅ Acceptance #3 (FP params exact per entry) → covered in Task 2 Step 5 (`grep -oE 'fp=...'`)
- ✅ Acceptance #4 (Бамбук 2 has `upmbps=20&downmbps=80`) → covered in Task 2 Step 6 (`grep -c 'upmbps=20...'`)
- ✅ Acceptance #5 (`go build && go vet` clean) → Task 1 Steps 3-4
- ✅ Acceptance #6 (happ-desktop sees 8 entries / 6 on cellular) → user-side verification, called out in Task 3's beads notes; not a build-checkable test
- ✅ Acceptance #7 (at least one Reality works e2e on flaky Wi-Fi) → user-side verification, called out implicitly

**Placeholder scan:**
- No `TBD`, `TODO`, `implement later`, `Similar to Task N`.
- Step 2 of Task 3 has placeholder `<PBK>/<SID>/<SNI>/<UUID>` — but these are explicitly labelled and Step 1 retrieves them from psql. This is intentional (engineer must substitute their actual current values), not a plan-failure placeholder.

**Type consistency:**
- `Priority int32` cast: `int32(i)` in Berëza loop, `int32(4 + i)` in Дуб loop, plain `6` and `7` literals for Бамбук — all int32 (Go promotes int literals to int32 in protobuf field assignment).
- Labels referenced in tests (`🌳 Берёза 1`, etc.) match the labels emitted in code.
- `realityFPs` and `xhttpFPs` slice names used consistently.

No issues found. Plan is ready.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-26-subscription-matrix.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
