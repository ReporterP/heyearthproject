---
title: User-facing SOCKS5 removal + MTProto Telegram-button perk
status: design
created: 2026-05-22
parent_initiative: "Turn P1 lessons into permanent features (A → B+C → D)"
beads_epic: TBD (created at writing-plans step)
prior_sub_project: A (Server Lifecycle Automation) — deployed
combines: B (SOCKS5 removal) + C (MTProto perk) — merged into one cycle per user scope decision
---

# SOCKS5 Removal + MTProto Telegram Button

## Problem

HiEarth currently exposes user-facing SOCKS5 + HTTP-proxy inbounds on the hub (:1080, :8080). They are leftover from the early product when "VPN over SOCKS" was the primary offering. Today:
* The actual VPN value is delivered through VLESS Reality (modern, DPI-resistant, single-URI handoff to client apps).
* SOCKS5 surface is a maintenance burden — confusing UI (one user, two protocol cards, copy-paste-credentials-into-Telegram instructions), and a vector for the abuse patterns identified in P1 (e.g. SOCKS5 i/o timeout flood through Hub→Exit).
* Zero active subscribers on socks-only plans (`socks-basic`, `socks-select`) — verified live in DB.

User wants to **delete** SOCKS5 from every user-facing surface (URI bundle, ConnectionsPage, plans catalog, marketing copy) and **replace** the convenience-for-Telegram angle with a real Telegram-native solution: an "Add to Telegram" button that opens an MTProto proxy deeplink. MTProto is scoped down from full per-user protocol to a curated single shared proxy — pure convenience feature ("плюшка"), not a differentiator or paid tier.

## Scope

### In scope
* DB migration 000041: deactivate `socks-basic` + `socks-select` rows (if present); add new `mtproto_*` settings keys to `app_settings`.
* Backend `xray_sync.go`: stop generating user-facing `socks5-proxy` and `http-proxy` inbounds in Xray config; remove per-user SOCKS/HTTP account generation.
* Backend `client_api.go` (connection bundle handler): drop `socks5` + `http` URI fields from response; remove related env/setting reads.
* Backend new endpoint `GET /api/v1/user/mtproto` — returns `{ enabled, host, port, secret, tg_deeplink, t_me_link }` if the active subscription allows.
* Exit-node Amsterdam: install `mtproxy` daemon (Telegram official `mtproto-proxy` from TelegramMessenger/MTProxy) + systemd unit + UFW :8443.
* Admin new page `MTProtoPage.jsx` — settings: enable toggle, host, port, secret (generate-button), sponsor channel.
* Admin `PlansPage.jsx`: remove "socks-basic" placeholder hint, replace with "vless-reality"; show is_active=false plans struck-through.
* Site `ConnectionsPage.jsx` (410 lines → ~250 lines): delete all SOCKS5/HTTP UI blocks (cards, credentials table, IMS/iOS/Android setup instructions, Telegram-via-SOCKS deeplink). Add **Telegram MTProto block**: title, two buttons "Открыть в Telegram" (tg:// deeplink) + "Открыть через t.me" (web fallback), small badge "Бонус", info line "Только для Telegram-трафика, не VPN".
* Site `DashboardPage.jsx`: if `socks5`/`http` were rendered anywhere, remove. Confirm no other references.
* Privacy/Offer/Refund pages in `site/src/pages/`: grep for "SOCKS"/"шифрованный прокси" mentions and update copy to "VLESS Reality" / "защищённое соединение".
* Marketing landing page (if mentions SOCKS): update.
* Admin `ServersPage.jsx`: tunnel-protocol dropdown KEEPS `socks5` / `shadowsocks` options (these are **Hub→Exit tunnel choices**, not user-facing — different concept).

### Out of scope (separate cycles)
* Flutter app — defer to Flutter cycle (will pick up cleaned API shape on next rebuild anyway).
* Per-user MTProto secrets (user constrained scope to "single shared button").
* Sub-project D (per-tariff rate-limit) — separate cycle.
* SOCKS5 tunnel-protocol option for FUTURE exit-nodes — preserved as fallback in admin.
* Removing internal `127.0.0.1:1090` SOCKS5 used by Hysteria2 client — that is hub-internal, untouched.

## Architecture

### SOCKS5 surface map (what stays vs goes)

```
USER-FACING (removed):
  [Xray inbound :1080 socks5-proxy]   ❌ delete generation in xray_sync.go
  [Xray inbound :8080 http-proxy]     ❌ delete generation in xray_sync.go
  [client_api.go bundle.socks5 URI]   ❌ delete
  [client_api.go bundle.http URI]     ❌ delete
  [Site SOCKS5 UI cards/instructions] ❌ delete
  [Plans 'socks-basic' / 'socks-select'] ❌ is_active=false

INTERNAL (kept):
  [Hysteria2 client local SOCKS :1090] ✅ kept (hub→exit tunnel pipe)
  [servers.tunnel_protocol='socks5'] ✅ kept (option for future exit-nodes)
  [Xray outbound to Hysteria2 via SOCKS] ✅ kept (internal hop)
```

### MTProto deployment

```
                  TELEGRAM app on user device
                         │ tg://proxy?server=...
                         ▼
              ┌────────────────────────┐
              │  Amsterdam exit-VPS    │
              │  89.124.89.126:8443    │
              │  mtproto-proxy daemon  │
              │  (TelegramMessenger/   │
              │   MTProxy official)    │
              └────────────────────────┘
                         │
                         ▼
                  Telegram CDN endpoints
                  (proxy traffic to Telegram)
```

Key choices:
* **Standalone daemon**, not Xray inbound — `mtproto-proxy` is Telegram's purpose-built binary, lightweight (~3 MB), well-maintained, gives sponsor-channel support which Xray's mtproto inbound does not. Lives ON Amsterdam exit-VPS to leverage its existing bandwidth + reputation.
* **Single shared secret** for all HiEarth users — this is "convenient button", not privacy-isolation. Secret rotates on admin demand via "Generate new secret" button.
* **Port 8443** (TCP) on Amsterdam — needs UFW open. NOT routed through Xray hub, this is a direct connection from user's Telegram client to Amsterdam.
* **Sponsor channel**: `mtproto-proxy --sponsor-channel @hiearth` flag (configurable in admin, default empty = disabled; user will enable later when channel is ready).
* **Gating**: `/api/v1/user/mtproto` endpoint returns deeplink only for users with active subscription (any plan). Free-trial users included — it's a perk.

### Settings keys (in `app_settings`)

```
mtproto_enabled         boolean (default false; admin toggles when ready)
mtproto_host            text    (default empty; e.g. "mtproxy.hiearth.ru" or "89.124.89.126")
mtproto_port            integer (default 8443)
mtproto_secret          text    (32 hex chars, generated; stored as-is for serving)
mtproto_sponsor_channel text    (default empty; e.g. "@hiearth" later)
```

## Components

### Backend (Go)
* **Modify** `cmd/api/xray_sync.go` — remove `socks5-proxy` + `http-proxy` inbound generation and the associated `socks5Accounts` / `httpAccounts` arrays. Reduces ~80 lines.
* **Modify** `cmd/api/client_api.go` — drop `socks5` + `http` fields from connection bundle response. Keep `vless` (Reality). Possibly drop `socks5Accounts` lookup from `biz.ListSocksAccountsForActiveUsers` (if no other consumer).
* **Modify** `internal/biz/*` — find and remove `ListSocksAccountsForActiveUsers` / `GenerateSocksCredentials` (if unused elsewhere after the cleanup).
* **Create** `internal/admin/mtproto_handlers.go` — CRUD for mtproto settings (GET/PUT).
* **Create** handler in `cmd/api/client_api.go` for `GET /api/v1/user/mtproto`.
* **Create** `migrations/000041_socks_to_mtproto.up.sql` + `.down.sql`.

### Exit-node side (Amsterdam VPS)
* **Create** `cmd/deploy/install-mtproxy.sh` — one-time script: download binary, write systemd unit, write configs, generate secret, enable+start.
* Push secret + host + port back to hub `app_settings` via existing SSH-based path.

### Frontend admin (React 19, JSX)
* **Create** `admin_web/src/pages/MTProtoPage.jsx` — settings form: enable toggle, host, port, secret (with regenerate button), sponsor channel.
* **Modify** `admin_web/src/main.jsx` (or router) — add `/mtproto` route.
* **Modify** sidebar (search for the sidebar component) — add "MTProto" entry.
* **Modify** `pages/PlansPage.jsx` — change placeholder from "socks-basic" to "vless-reality"; deactivated plans render struck-through with disabled state.

### Frontend site (React 19, JSX)
* **Heavy modify** `pages/ConnectionsPage.jsx` — remove ~250 lines of SOCKS5 UI; add ~60 lines of MTProto Telegram block.
* **Verify** `pages/DashboardPage.jsx` — search/replace any `socks5` references.
* **Modify** copy in `pages/PrivacyPage.jsx`, `pages/OfferPage.jsx`, `pages/RefundPage.jsx` — replace "SOCKS5" → "VLESS Reality" / "защищённое соединение".
* **Modify** `pages/LandingPage.jsx` — if it mentions SOCKS, replace.
* **Create** small `components/TelegramProxyButton.jsx` (~30 lines) — reusable button for MTProto deeplink (used inside ConnectionsPage).

## Data flow — User clicks "Add to Telegram"

```
Site ConnectionsPage mount
  → GET /api/v1/user/mtproto with Bearer token
  → backend reads app_settings.mtproto_* + verifies active subscription
  → response: { enabled:true, host:"...", port:8443, secret:"...",
                tg_deeplink:"tg://proxy?server=...&port=...&secret=...",
                t_me_link:"https://t.me/proxy?server=...&port=...&secret=..." }
  ▼
Site renders two buttons in Telegram block
  ▼
User taps "Открыть в Telegram"
  → browser hands off tg:// to Telegram app
  → Telegram shows confirmation dialog "Connect to this proxy?"
  → user confirms → proxy added to Telegram Settings → traffic routes via Amsterdam:8443
  → if sponsor_channel configured: Telegram UI shows "Sponsored by @hiearth"
```

## Migration plan for plans table

```sql
-- 000041_socks_to_mtproto.up.sql
UPDATE plans SET is_active = false
 WHERE slug IN ('socks-basic', 'socks-select') AND is_active = true;

-- Defensive: leave rows in DB (don't DELETE), they're historical
-- and any FK from payments/user_subscriptions stays valid.

INSERT INTO app_settings (key, value) VALUES
  ('mtproto_enabled', 'false'),
  ('mtproto_host', ''),
  ('mtproto_port', '8443'),
  ('mtproto_secret', ''),
  ('mtproto_sponsor_channel', '')
ON CONFLICT (key) DO NOTHING;
```

### Down migration: re-activate plans + remove mtproto keys.

## Error handling

| Stage | Failure | Behavior |
|---|---|---|
| `/api/v1/user/mtproto` | `mtproto_enabled=false` | Return `{ enabled: false }`. Site hides Telegram block. |
| `/api/v1/user/mtproto` | No active subscription | Return 403. Site hides block, may show "Доступно для подписчиков" hint. |
| Xray sync after inbound removal | Existing user with old subscription URL connecting via SOCKS5 | Connection refused (port :1080 closed). User must refetch subscription URL — `subscription_version` bump triggers banner already in place. |
| mtproto-proxy daemon down | Telegram client times out connecting | Sad face. We monitor via systemd + alert; doesn't affect VPN. |
| Admin "Generate secret" | DB error | Show error toast, secret unchanged. |
| Admin "Apply" | DB error | Show error toast. |

## Subscription bump

`biz.BumpSubscriptionVersion()` (from sub-project A) is called when:
* SOCKS5 user-facing inbounds disappear from Xray config (triggered on first deploy after migration 000041).
* Admin updates `mtproto_*` settings.

This signals the site polling layer that connections have changed — UI banner shown.

## Verification (evidence-driven)

After deploy:
```bash
# 1. Plans deactivated
psql heyearth -c "SELECT slug, is_active FROM plans WHERE slug IN ('socks-basic','socks-select');"
# → both is_active=false

# 2. mtproto settings present
psql heyearth -c "SELECT key, value FROM app_settings WHERE key LIKE 'mtproto_%';"
# → 5 rows

# 3. Xray config no longer has user-facing SOCKS5 / HTTP inbounds
grep -E '"port"\s*:\s*(1080|8080)' /opt/heyearth/xray/config.json
# → empty (only :443/:2443/:10443 remain user-facing)

# 4. Hub port 1080/8080 not listening
ss -tln | grep -E ":(1080|8080)\b"
# → empty

# 5. User connections endpoint no longer returns socks5/http
curl -H "Authorization: Bearer <user-jwt>" https://hiearth.ru/api/v1/user/connections | python3 -m json.tool | grep -iE '"socks|"http"'
# → empty

# 6. MTProto endpoint serves deeplink (after admin enables + secret set)
curl -H "Authorization: Bearer <user-jwt>" https://hiearth.ru/api/v1/user/mtproto | python3 -m json.tool
# → { enabled: true, host, port, secret, tg_deeplink, t_me_link }

# 7. Amsterdam mtproto-proxy daemon running
ssh root@89.124.89.126 'systemctl is-active mtproxy && ss -tln | grep :8443'
# → active + LISTEN

# 8. Site builds without SOCKS5 references
cd site && npm run build && grep -ic socks dist/assets/*.js
# → 0 (or extremely small if "socks" is sub-string of something innocent)
```

User-facing smoke test (manual):
* Open https://hiearth.ru/dashboard logged in → no SOCKS5 block visible
* Open Connections page → only VLESS QR + Telegram MTProto button visible
* Click "Открыть в Telegram" on a phone → Telegram opens proxy-add dialog
* Confirm dialog → check Telegram Settings → proxy added with hiearth.ru parameters

## Risk register

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | An active user has SOCKS5 in their copy-pasted subscription | They retry via fresh subscription URL fetch; sub_version bump shows banner. No real damage — VLESS still works. |
| R-02 | mtproto-proxy port :8443 conflict with Hysteria2 SS+WS port :443 | Different ports, no conflict. |
| R-03 | Telegram blocks single shared MTProto secret (overuse) | Plan: monitor; admin can regenerate secret instantly; users see new secret on next page refresh (no version-bump needed for them, the deeplink is fetched fresh). |
| R-04 | mtproto-proxy binary install via curl from GitHub fails | Pin a release version; checksum verification in install script. |
| R-05 | Privacy / Offer / Refund mention SOCKS5 and we miss a spot | Grep-based audit in plan; mismatch in legal doc is annoying but not breaking. |
| R-06 | Admin enables MTProto without filling secret | Backend endpoint returns `enabled:false` if secret empty even when enabled flag is true — safe degraded response. |
| R-07 | Hub config rebuild after inbound removal restarts Xray briefly | Existing atomic Xray write pattern; ~1-2 sec interruption. |
| R-08 | Sponsor channel @hiearth doesn't exist yet | Default empty; admin enables when channel registered. |

## Rollback

* **Whole sub-project**: `git revert <merge commit>` + redeploy. Migration 000041 keys can stay (harmless). To fully undo: psql with `.down.sql`.
* **mtproto-proxy on Amsterdam**: `systemctl stop mtproxy && systemctl disable mtproxy && rm /etc/systemd/system/mtproxy.service && rm -rf /opt/mtproxy`. UFW: `ufw delete allow 8443/tcp`.
* **Plans accidentally deactivated**: `UPDATE plans SET is_active=true WHERE slug IN ('socks-basic','socks-select');` — but we verified zero active subscribers, so this is theoretical.
* **Site removed user's still-paste-functioning SOCKS5 instructions**: full git revert of the ConnectionsPage edit; old SOCKS5 inbounds gone, so this only restores broken instructions — better to keep the new UI.

## Out of scope reminders (separate cycles)

* Flutter app changes — picks up new bundle shape automatically on next rebuild
* Sub-project D (per-plan rate-limit)
* User-controlled MTProto secret (custom per-user)
* MTProto load balancing across multiple exit-nodes
* Detailed Telegram-traffic analytics

## Deliverable

This design feeds `superpowers:writing-plans` to produce ~12-15 task implementation plan. Beads epic + sub-tasks created at planning step.
