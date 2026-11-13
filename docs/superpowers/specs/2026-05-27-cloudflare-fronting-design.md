# Cloudflare CDN Fronting — Design Spec

**Date:** 2026-05-27
**Author:** Claude + Alex
**Status:** Brainstorming complete, awaiting user spec review
**Beads:** TBD on plan creation

---

## 1. Problem

Russian mobile operators (Tele2, MTS) periodically apply per-IP QoS rules or
outright TCP blocks on our Hub IP `198.51.100.236` (vdsina Moscow,
AS48282). Symptoms observed in production:

- **MTS:** `hiearth.ru` reachable from operator's network, but VPN sessions to
  the Hub experience severe bandwidth throttling (5–30 sec page loads).
  ICMP RTT user→Hub stays at 3ms — network path itself is fast; throttling
  is application-layer at the operator.
- **Tele2:** worse — Hub IP intermittently unreachable entirely. TCP timeout
  to `hiearth.ru:443` without RKN redirect (i.e., not a Roskomnadzor
  registry block, but a Tele2 internal flapping rule that engages and
  disengages on hour-or-day cycles).
- Behavior varies per-subscriber on the same operator in the same region —
  consistent with per-SIM QoS profiles, not a uniform block.

The Hub IP is single-point: any operator block to it kills all profiles
(Берёза, Дуб, Бамбук) at once for affected users.

No amount of Reality/uTLS/fingerprint tuning fixes this — the block is
below the protocol layer. Server-side has been verified healthy:
end-to-end user→Hub→Exit→Google = 190ms in synthetic test from Hub.

## 2. Goal

Add a **fallback connection path** that bypasses per-IP operator
blocking by routing user traffic through Cloudflare's edge network
(millions of IPs in pool). Operators cannot block Cloudflare without
blocking half the legitimate internet, so the CF-fronted path stays
reachable even when our Hub IP is blocked/throttled.

## 3. Non-goals

- **Not replacing Reality.** Reality remains the primary stealth
  transport for users whose operators don't block our IP. Reality offers
  superior DPI obfuscation (TLS handshake mimicry) that no CDN-fronted
  protocol can match. We're adding an alternative, not migrating.
- **Not solving Roskomnadzor registry blocks.** If the federal registry
  ever adds our domains/IPs (currently it has not), users see a Roskomnadzor
  stub page — that requires a different mitigation (domain rotation).
- **Not replacing Hysteria2 (Бамбук).** UDP/QUIC path stays for users on
  good WiFi where it offers the best latency.

## 4. Architecture

### 4.1 High-level

Two coexisting paths for VLESS subscription profiles:

```
Path 1 (existing, primary): Reality direct
  User → Hub IP 198.51.100.236 (hiearth.ru, DNS-only in CF)
       → nginx :443 (SNI router by ssl_preread_server_name)
       → on SNI=realitySNI → Xray vless-reality :10443
       → on SNI=cdn.hiearth.ru → nginx_https :8443 (NEW vhost)
       → routing → exit-2 → internet

Path 2 (new, fallback): VLESS over WebSocket through Cloudflare
  User → Cloudflare edge IP (pool of millions)
       → CF terminates TLS for cdn.hiearth.ru (proxy=on, orange cloud)
       → CF forwards WebSocket to origin 198.51.100.236
       → nginx :443 → SNI=cdn.hiearth.ru → nginx_https :8443
       → new vhost cdn.conf → location /cdn-ws → ws upgrade
       → Xray vless-ws-cdn inbound :11443 (loopback)
       → routing → SAME exit-2 → internet
```

Both paths terminate at the same exit-side routing, share the same
balancer rules, identical user UUIDs.

### 4.2 Why Reality cannot pass through CF

Reality's stealth depends on direct TLS handshake to our server while
mimicking a third-party legit site's cert. Cloudflare's CDN/proxy
architecture **terminates TLS at edge** — CF's certificate is presented,
the user's TLS handshake ends at CF, then CF makes its own TLS to our
origin. The middle-box-style termination breaks Reality's camouflage
invariants. CF Spectrum (paid L4 passthrough) would preserve Reality
but costs Enterprise-tier $$$ and adds complexity for no immediate
benefit over WS-through-CF.

### 4.3 Why WebSocket over gRPC/XHTTP

VLESS over WebSocket is the most battle-tested protocol for CDN
fronting. CF supports it natively (WebSockets must be toggled ON in
Network tab — Free tier supported). gRPC works but adds HTTP/2
complexity in nginx upstream config and has happ-desktop interop
quirks. XHTTP is newest and less proven through CF Free.

## 5. Components

### 5.1 Cloudflare account & DNS (user action)

User performs these via CF dashboard. No code involved.

| Step | Action |
|------|--------|
| 1 | Create CF account at dash.cloudflare.com |
| 2 | Add Site `hiearth.ru` on Free plan |
| 3 | Let CF import existing DNS records via auto-scan |
| 4 | Add new DNS A record: `cdn.hiearth.ru → 198.51.100.236`, **Proxied (orange cloud)** |
| 5 | Verify existing records `hiearth.ru` and `admin.hiearth.ru` remain **DNS only (grey cloud)** — Reality must stay direct |
| 6 | Copy 2 CF-assigned nameservers (e.g. `xxx.ns.cloudflare.com`) |
| 7 | Update NS records at the domain registrar to CF's nameservers |
| 8 | Wait for CF Active status (5–60 min DNS propagation) |
| 9 | SSL/TLS → Full (strict): CF↔origin TLS validation |
| 10 | SSL/TLS → Edge Certificates → Min TLS 1.2 |
| 11 | Network → **WebSockets ON** (critical for VLESS+WS) |
| 12 | SSL/TLS → Origin Server → Create Origin Certificate (15-year valid, signed by CF). Download cert.pem + key.pem. Hand off to implementer. |

### 5.2 Origin (Hub) nginx changes

**New vhost** as a new `server { ... }` block added to the existing nginx
that already listens on `:8443 ssl` (used by hiearth.ru/admin.hiearth.ru
web). nginx routes by Host header — new server block matches
`server_name cdn.hiearth.ru` without needing a separate listen port.

File `/etc/nginx/conf.d/cdn.conf`:

```nginx
server {
    listen 8443 ssl http2;       # SAME port as existing nginx_https
    listen [::]:8443 ssl http2;
    server_name cdn.hiearth.ru;  # Host-header routed by nginx

    ssl_certificate     /etc/nginx/cdn-origin/cert.pem;
    ssl_certificate_key /etc/nginx/cdn-origin/key.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;

    location /cdn-ws {
        if ($http_upgrade != "websocket") { return 404; }
        proxy_pass http://127.0.0.1:11443;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }

    location / { return 444; }  # silently close non-WS paths
}
```

Confirmed at implementation time: nginx already listens `:8443 ssl` for
`hiearth.ru` / `admin.hiearth.ru` web vhosts. Adding a third
server_name on the same listen port is the standard nginx pattern, no
conflict.

**Stream SNI map update** in `/etc/nginx/stream.d/heyearth.conf`:

```nginx
map $ssl_preread_server_name $backend {
    hiearth.ru          nginx_https;
    www.hiearth.ru      nginx_https;
    admin.hiearth.ru    nginx_https;
    site.hiearth.ru     nginx_https;
    cdn.hiearth.ru      nginx_https;   # NEW
    default             xray_vless;
}
```

**Cert storage**: `/etc/nginx/cdn-origin/{cert,key}.pem` mode 0600 owned by root.

### 5.3 Origin (Hub) Xray changes

**New inbound** generated by `xray_sync.go`:

```go
{
    "tag": "vless-ws-cdn",
    "listen": "127.0.0.1",
    "port": 11443,
    "protocol": "vless",
    "settings": {
        "clients": vlessClientsXHTTP,   // re-use — same UUIDs, no flow
        "decryption": "none"
    },
    "streamSettings": {
        "network": "ws",
        "wsSettings": {
            "path": cdnWSPath,   // from biz.GetSettingOrDefault("cdn_ws_path", "/cdn-ws")
            "headers": {}
        }
        // no security — CF terminates TLS, plain WS arrives at xray
    },
    "sniffing": {
        "enabled": true,
        "destOverride": ["http", "tls", "quic"]
    }
}
```

Re-uses `vlessClientsXHTTP` (clients-without-flow) — WebSocket is
incompatible with XTLS Vision, same rationale as XHTTP.

### 5.4 Subscription emission

**New profile** in `client_api.go::buildHubConnectionOptions`, after
existing Berёza/Дуб emissions:

```go
if biz.GetSettingOrDefault("cdn_enabled", "false") == "true" {
    cdnHost := biz.GetSettingOrDefault("cdn_host", "cdn.hiearth.ru")
    cdnPath := biz.GetSettingOrDefault("cdn_ws_path", "/cdn-ws")
    if cdnHost != "" {
        label := "☁️ Облако (CDN)"
        cdnURI := fmt.Sprintf(
            "vless://%s@%s:443?encryption=none&security=tls&sni=%s&type=ws&host=%s&path=%s&fp=chrome&tcpKeepAliveInterval=15&tcpKeepAliveIdle=100#%s",
            userVlessUUID, cdnHost, cdnHost, cdnHost,
            url.QueryEscape(cdnPath),
            url.PathEscape(label))
        opts = append(opts, &heyearth_v1.ConnectionOption{
            Uri:                 cdnURI,
            Protocol:            "vless",
            Label:               label,
            RecommendedNetworks: []string{"cellular", "wifi", "ethernet", "any"},
            Priority:            10, // displayed last
        })
    }
}
```

URI param breakdown:
- `security=tls` — Happ does TLS handshake to CF edge
- `sni=cdn.hiearth.ru` — required for CF SNI routing
- `type=ws` — WebSocket transport
- `host=cdn.hiearth.ru` — WS Host header (matches nginx server_name)
- `path=/cdn-ws` — matches nginx location and xray wsSettings.path
- `fp=chrome` — uTLS Chrome fingerprint, legitimate CF traffic shape
- `tcpKeepAliveInterval=15` — counters CF Free 100s idle timeout

### 5.5 New `app_settings` keys

| Key | Default | Type | Purpose |
|-----|---------|------|---------|
| `cdn_enabled` | `false` | bool | Master toggle. Subscription emits Облако only when true. |
| `cdn_host` | `cdn.hiearth.ru` | string | CF-fronted hostname. Allows future rotation without code change. |
| `cdn_ws_path` | `/cdn-ws` | string | WebSocket path. Must match nginx location AND xray wsSettings.path. |

Single source of truth in DB. Operator can flip toggle via SQL without
deploy:

```sql
UPDATE app_settings SET value='true' WHERE key='cdn_enabled';
```

## 6. Migration plan (cutover)

### 6.1 Pre-flight (user does in CF panel)

User completes 5.1 steps 1–12. Hand off cert.pem + key.pem to implementer.

### 6.2 Stage 1 — Origin setup (zero user impact)

1. Install CF Origin cert at `/etc/nginx/cdn-origin/` (mode 0600).
2. Write new `/etc/nginx/conf.d/cdn.conf` (vhost from 5.2).
3. Update `/etc/nginx/stream.d/heyearth.conf` to route `cdn.hiearth.ru` SNI.
4. `nginx -t && systemctl reload nginx`.
5. Code change `xray_sync.go` (vless-ws-cdn inbound from 5.3).
6. Code change `client_api.go` (Облако emission from 5.4, **toggle-gated, default off**).
7. Build & deploy heyearth-api via `quick_deploy.go`.
8. Force `systemctl restart xray` to pick up new inbound.
9. Verify:
   - `ss -tlnp | grep 11443` shows xray listening
   - `nginx -T | grep cdn` shows vhost loaded
   - Loopback test: `curl -k --resolve cdn.hiearth.ru:8443:127.0.0.1 https://cdn.hiearth.ru:8443/cdn-ws` returns 404 (correct — non-WS request)
   - From CF edge: `curl -I https://cdn.hiearth.ru` returns CF + nginx headers

### 6.3 Stage 2 — End-to-end test (CF→Hub→exit through CF)

Implementer creates a one-off VLESS+WS test profile (single URI, **not** in
subscription endpoint), hands it to user. User imports into Happ, connects,
opens youtube.com — expected: works smoothly through Облако path.

If test passes → proceed to Stage 3.
If test fails → diagnose at Hub via `xray --log debug`. Rollback is
trivial (Облако is not emitted to anyone yet).

### 6.4 Stage 3 — Enable for users

```sql
UPDATE app_settings SET value='true' WHERE key='cdn_enabled';
```

Then `systemctl restart heyearth-api` (forces fresh subscription generation).
Subscription endpoint now emits Облако to all users. Users pull-to-refresh
in Happ → see new profile → can try it.

### 6.5 Rollback

If anything breaks at any point:

```sql
UPDATE app_settings SET value='false' WHERE key='cdn_enabled';
```

`systemctl restart heyearth-api`. Облако disappears from subscriptions on
next user refresh. Берёза/Дуб/Бамбук unaffected (separate code paths).

nginx vhost and xray inbound remain in place (idle, no traffic until next
enable). Zero risk to existing profiles.

## 7. Failure modes & mitigations

| Failure | Behavior | Mitigation |
|---------|----------|------------|
| CF edge IP blocked in RU (extremely unlikely) | Облако unreachable | Reality direct profiles still work — operational fallback for users |
| CF Origin cert expired (15-year validity) | nginx HTTPS error on cdn.hiearth.ru | User regenerates cert in CF panel, hands off, implementer replaces files + nginx reload |
| CF Free tier WS idle timeout (100s default) | Long-idle connections drop | `tcpKeepAliveInterval=15` in URI keeps NAT and CF connection warm |
| nginx :8443 port conflict | n/a — by design, we share :8443 listener via Host-header routing | New server block matches `server_name cdn.hiearth.ru` on same :8443 — standard nginx pattern, no conflict |
| User's Happ doesn't support VLESS+WS | Profile import fails | Happ-desktop has VLESS+WS support since v1.x. Unlikely. Reality remains accessible. |
| CF blocks free tier WS traffic for VPN-shaped patterns | Облако disconnects after handshake | Upgrade to CF Pro $20/mo. Or migrate to gRPC/XHTTP transport. |

## 8. Success criteria

1. Tele2 user (whose direct Hub access is blocked) pulls subscription,
   selects Облако, opens youtube.com — page loads in <5 seconds.
2. MTS user reports faster page loads on Облако vs Берёза (throttle
   bypass observed).
3. Existing Reality users (where direct works) experience zero
   observable change — Берёза/Дуб/Бамбук continue working as before.

## 9. Open questions (post-deploy iteration)

- Should Облако be promoted to higher priority (try-first) for users on
  Tele2 specifically? Would require operator detection — out of scope
  for v1.
- Should we surface Облако only when other profiles fail? User-side
  feature — for now, Happ shows all profiles, user picks.
- CF Pro upgrade trigger condition? If Облако CDN bandwidth grows
  large, CF Free egress fees may kick in. Monitor and plan upgrade.

## 10. Cross-references

- Pre-Hysteria2 baseline restored: commit `9e97501`.
- Mux disabled on SS exit outbound: commit `b7fe50d`.
- iOS uTLS fingerprint for Берёза 1 / Дуб 1: commit `a3d69dd`.
- MTS throttling root cause analysis: this session's research.
