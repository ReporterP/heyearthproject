---
title: Premium Happ integration — proper subscription headers + landing page + dashboard redesign
status: design
created: 2026-05-23
beads_epic: TBD (created at writing-plans step)
prior_subprojects: B+C (i9fv), B.2 (gny5), A (3q3i)
---

# Premium Happ Integration

## Problem

User compared HeYearth subscription as shown in Happ vs competitor "Kurut VPN":

* HeYearth: just 3 VLESS profile lines (Home / Mobile / Strict). No progress bar, no expiry, no auto-update label, no link to dashboard, no "продлите" announce, refresh button broken.
* Competitor: progress bar `1GB / ∞`, expiry "Истекает: 24.05.2026", clickable announce "Пробная подписка. Продлите на сайте ↗", auto-update interval visible, refresh works, multi-country profile grouping.

Verified via `curl -i -A "Happ/1.0" /api/v1/user/subscribe/<token>` that our endpoint sends:

```
Content-Disposition: attachment; filename="heyearth-..."        ← KILLS subscription parsing
Subscription-Userinfo: upload=0; download=N                     ← missing total= and expire=
Profile-Title: HiEarth
Profile-Update-Interval: 4
Support-Url: https://hiearth.ru/dashboard
```

The `Content-Disposition: attachment` is the **root cause** — Happ treats the response as a file download instead of a subscription, ignoring all metadata headers. Plus we lack `total=`, `expire=`, `Profile-Web-Page-Url`, `Announce`, `Announce-Url`.

User also wants Dashboard redesign: large action buttons at top (Happ install + Telegram MTProto x2), subscription URL below with small copy button, "Обновить подписку" button removed (Happ refresh will work once headers fixed).

## Scope

### In scope
* Backend `cmd/api/subscription_handler.go`:
  * **Remove** `Content-Disposition: attachment` header (main fix)
  * Extend `buildSubscriptionUserinfo`: add `total=<bytes>; expire=<unix>` to existing `upload=0; download=N`
  * Add headers: `Profile-Web-Page-Url`, `Announce`, `Announce-Url`
  * Add `buildSubscriptionAnnounce(userID) (text, url string)` — dynamic, state-aware
* New frontend page `site/src/pages/ConnectPage.jsx` — `/connect?sub=<token>` landing:
  * Detects platform (iOS / Android / Desktop)
  * Triggers `happ://add/<base64(subscribe_url)>` deeplink
  * Falls back to App Store / Play Store / happ.su after 1.5s timeout (Android uses native Intent URI fallback)
  * Visible "Открываем Happ..." UI with manual fallback links + Copy-URL
* New `site/src/main.jsx` route: `/connect`
* Modify `site/src/pages/DashboardPage.jsx`:
  * New top `ConnectionActions` component — 3 big buttons in one row:
    1. "Добавить в приложение" → `/connect?sub=<token>`
    2. "Открыть в Telegram" → `tg://proxy?...` (existing MTProto)
    3. "Открыть через t.me" → `https://t.me/proxy?...` (existing MTProto)
  * Subscription URL block below with small "Копировать" button
  * Remove "Обновить подписку" button entirely
  * Inline existing `TelegramProxyButton` logic OR refactor to share with new `ConnectionActions`

### Out of scope (separate cycles)
* Admin `announce_override` mechanism (manual admin message)
* Multi-country / multi-server profile grouping (waits until ≥2 exit-nodes)
* Sub-project D (per-plan rate-limit)
* Flutter app changes (subscription headers will be picked up next Flutter build)
* `Profile-Title` base64 encoding for unicode (not needed for ASCII "HiEarth")

## Architecture

### Bugfix matrix (verified via curl 2026-05-23 01:27 MSK)

| Header / behavior | Current | Target |
|---|---|---|
| **`Content-Disposition`** | `attachment; filename=...` | **Removed** — Happ parses as subscription, not file |
| `Subscription-Userinfo` | `upload=0; download=N` | `upload=0; download=N; total=L; expire=U` |
| `Profile-Title` | `HiEarth` | Same (ASCII safe, no base64 needed) |
| `Profile-Update-Interval` | `4` | Same (hours) |
| `Support-Url` | `https://hiearth.ru/dashboard` | Same |
| `Profile-Web-Page-Url` | absent | `https://hiearth.ru/dashboard` |
| `Announce` | absent | dynamic by state (see below) |
| `Announce-Url` | absent | `/checkout` or `/dashboard` (state-dependent) |

### Dynamic Announce — state machine

`buildSubscriptionAnnounce(userID) → (text, url string)`:

```
if valid_until < NOW:                           → "⚠️ Подписка истекла. Продлите →"   + /checkout
else if days_left ≤ 1:                          → "⏰ Подписка истекает завтра!"        + /checkout
else if days_left ≤ 7 AND plan = free-trial:    → "⌛ Пробная подписка, осталось Nд"   + /checkout
else if days_left ≤ 7 AND plan = paid:          → "⌛ Истекает через Nд"                + /checkout
else if traffic_used / traffic_limit ≥ 0.8:     → "📊 Использовано N% трафика"          + /dashboard
else (healthy):                                 → "Личный кабинет ↗"                    + /dashboard
```

Never empty — even healthy state shows "Личный кабинет ↗" link for casual cabinet access.

### ConnectPage flow

```
User taps "Добавить в приложение" on /dashboard
   ▼
Navigate to https://hiearth.ru/connect?sub=<token>
   ▼
ConnectPage mounts:
   • Read ?sub=<token>
   • Compute subscribeUrl = window.location.origin + "/api/v1/user/subscribe/" + token
   • Compute happDeeplink = "happ://add/" + base64(subscribeUrl)
   • Detect platform via navigator.userAgent + navigator.platform:
       - iOS:     direct happDeeplink + 1500ms fallback → App Store
       - Android: intent://add/<b64>#Intent;scheme=happ;package=com.happproxy;
                  S.browser_fallback_url=https%3A//play.google.com/store/apps/details%3Fid%3Dcom.happproxy;end
                  (Intent URI handles fallback natively — no JS timeout needed)
       - Desktop: direct happDeeplink + 1500ms fallback → https://www.happ.su/main/ru
   ▼ visible UI during wait
   "Открываем Happ…" spinner + manual fallback buttons:
     [iOS App Store] [Google Play] [Скачать Happ для ПК]
     [Скопировать ссылку подписки]   ← для power users / Hiddify-PC / V2RayNG / Streisand / Clash
```

### Dashboard top redesign

```
Before:                                          After:
┌──────────────────────────────────┐            ┌────────────────────────────────────────────────┐
│ 🔗 Ваша ссылка для подключения  │            │  ✨ Кнопки подключения (вверху, крупно)         │
│ Скопируйте...                    │            │  ┌──────────┐ ┌───────────┐ ┌───────────────┐ │
│ https://hiearth.ru/...           │            │  │ Добавить │ │ Открыть   │ │ Открыть       │ │
│ [Добавить] [Копировать] [Refresh]│            │  │ в прил.  │ │ Telegram  │ │ через t.me    │ │
└──────────────────────────────────┘            │  └──────────┘ └───────────┘ └───────────────┘ │
                                                ├────────────────────────────────────────────────┤
                                                │ 🔗 Ваша ссылка для подключения                 │
                                                │ https://hiearth.ru/api/v1/user/subscribe/...   │
                                                │ [📋 Копировать]                                │
                                                │ (✕ "Обновить подписку" удалён)                 │
                                                └────────────────────────────────────────────────┘
```

## Components

### Backend (modify only — no new files)
* `cmd/api/subscription_handler.go`:
  * `handleSubscriptionLink`: drop `Content-Disposition: attachment`; add `Profile-Web-Page-Url`, `Announce`, `Announce-Url`
  * `buildSubscriptionUserinfo(userID)`: extend with `total=` (from `traffic_limit`) and `expire=` (from `valid_until` as Unix)
  * Add new function `buildSubscriptionAnnounce(userID) (text, url string)` — state-machine described above

### Frontend (`site/`)
* **Create** `site/src/pages/ConnectPage.jsx` (~150 lines):
  * Reads `?sub=<token>` from `useSearchParams`
  * `useEffect` on mount: detect platform → set `window.location.href` to deeplink or Intent URI
  * `setTimeout 1500ms` for iOS/Desktop fallback (Android handled by Intent URI itself)
  * UI: spinner + heading "Открываем Happ…" + sub-text + manual buttons
* **Modify** `site/src/main.jsx` (router): add `<Route path="/connect" element={<ConnectPage />} />`
* **Modify** `site/src/pages/DashboardPage.jsx`:
  * New small inline component `ConnectionActions` (or merge logic from `TelegramProxyButton`)
  * 3 big buttons styled consistently (primary brand for Happ, Telegram blue for tg, glass for t.me)
  * Subscription URL block below with `[Копировать]` only
  * Delete the "Обновить подписку" button JSX and its handler

### Constants (in code, not config)
* iOS App Store: `https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108`
* Android Play Store: `https://play.google.com/store/apps/details?id=com.happproxy`
* Desktop fallback: `https://www.happ.su/main/ru`
* Profile-Web-Page-Url default: `https://hiearth.ru/dashboard`
* Announce-Url for renewal: `https://hiearth.ru/checkout` (or `/dashboard?plans=1` — confirm with frontend convention; default to `/checkout`)

## Data flow

### Happ subscription fetch
```
Happ app → GET /api/v1/user/subscribe/<token>
   ▼
handleSubscriptionLink:
  1. resolve user by subscribe_token
  2. GetUserAccessState(userID) — 403 if not allowed
  3. buildHubConnectionOptions(userID) — 4 VLESS URIs (Home/Mobile/Strict/XHTTP)
  4. headers:
     Subscription-Userinfo: upload=0; download=<used>; total=<limit>; expire=<unix>
     Profile-Update-Interval: 4
     Profile-Title: HiEarth
     Profile-Web-Page-Url: https://hiearth.ru/dashboard
     Support-Url: https://hiearth.ru/dashboard
     Announce: <state-machine output>
     Announce-Url: <state-machine output>
     X-Config-Version: <int>
  5. body: plain URIs (Happ UA) or base64 (others)
   ▼
Happ:
  • Progress bar from upload+download / total
  • Expiry from `expire` unix
  • Announce row with clickable Announce-Url
  • "Открыть в браузере" button → Profile-Web-Page-Url
  • Refresh button works (no Content-Disposition confusion)
  • Auto-refresh every 4 hours
```

### Dashboard → ConnectPage
```
DashboardPage useEffect → getSubscribeLink() returns subscribe_url
DashboardPage useEffect → getMtproto() returns tg_deeplink + t_me_link
   ▼
Render ConnectionActions:
  Button 1 "Добавить в приложение"  → href="/connect?sub=<token-from-subscribe-url>"
  Button 2 "Открыть в Telegram"      → href=tg_deeplink
  Button 3 "Открыть через t.me"      → href=t_me_link
  
  (if mtproto.enabled === false, hide buttons 2 + 3 — show only button 1 centered)
```

### ConnectPage runtime
```
On mount:
  token = useSearchParams().get('sub')
  subscribeUrl = window.location.origin + '/api/v1/user/subscribe/' + token
  happDeeplink = 'happ://add/' + btoa(subscribeUrl).replace(/=/g, '')
  
  ua = navigator.userAgent
  isIOS     = /iPad|iPhone|iPod/.test(ua) && !window.MSStream
  isAndroid = /Android/.test(ua)
  isMobile  = isIOS || isAndroid
  
  if (isAndroid):
    window.location.href = `intent://add/${b64}#Intent;scheme=happ;package=com.happproxy;S.browser_fallback_url=${PLAY_STORE_URL_ENCODED};end`
  else:
    window.location.href = happDeeplink
    setTimeout(() => {
      if (!document.hidden) {
        window.location.href = isIOS ? APP_STORE_URL : DESKTOP_HAPP_URL
      }
    }, 1500)
```

## Error handling

| Scenario | Behavior |
|---|---|
| `?sub=<token>` missing | ConnectPage shows error + Back to Dashboard link |
| Subscription endpoint 403 | Last good Happ config persists; Announce explains "истекла" |
| `valid_until` past | `expire=<past_unix>` → Happ shows red expiry; Announce = "истекла" |
| `traffic_limit IS NULL` | Omit `total=` (Happ shows infinity progress) |
| iOS / Desktop no Happ | 1500ms timeout → App Store / happ.su redirect |
| Android no Happ | Intent URI native fallback to Play Store |
| Happ auto-refresh hit | Same endpoint hit every 4 hours; headers regen; UI updates |
| `mtproto.enabled` false | ConnectionActions hides MTProto buttons; only Happ button shown |
| Sponsor-channel not configured | MTProto still works; no banner shown in Telegram |

## Verification (evidence-driven, no automated tests)

### Backend
```bash
# Re-curl after deploy — compare to baseline (2026-05-23 01:27 MSK)
curl -i -A "Happ/1.0" "https://hiearth.ru/api/v1/user/subscribe/<token>" 2>&1 | head -25

# Expected presence:
#   Subscription-Userinfo: upload=0; download=<N>; total=<L>; expire=<unix>
#   Profile-Web-Page-Url: https://hiearth.ru/dashboard
#   Announce: <state-machine output>
#   Announce-Url: <state-machine output>

# Expected ABSENT:
#   Content-Disposition: attachment ← removed
```

### Frontend
```bash
# 1. ConnectPage builds + routes
curl -s "https://hiearth.ru/connect?sub=<token>" | grep -iE "happ|app store|play|happ\.su"

# 2. DashboardPage rendered without "Обновить подписку"
curl -s -H "Authorization: Bearer <jwt>" "https://hiearth.ru/dashboard" | grep -i "Обновить подписку"
# Expected: no matches
```

### Real Happ smoke (manual)
1. iOS device with Happ installed → open https://hiearth.ru/connect?sub=<token> in Safari → Happ opens with subscription import dialog
2. iOS device, uninstall Happ → repeat → App Store opens to Happ Proxy Utility Plus
3. Android device with Happ → same as iOS step 1
4. Android device, uninstall → Play Store opens
5. Desktop (Windows/macOS) with Happ → deeplink fires
6. Desktop, no Happ → 1.5s → happ.su/main/ru opens
7. **Happ app**: open subscription menu → progress bar visible? Expiry date visible? Announce text + clickable link visible? Refresh button works?

### State-machine variations
Test announce for each subscription state via different test users:
- Active healthy → "Личный кабинет ↗" + /dashboard
- Trial 5 days left → "⌛ Пробная подписка, осталось 5д" + /checkout
- Paid 3 days left → "⌛ Истекает через 3д" + /checkout
- Traffic 85% used → "📊 Использовано 85% трафика" + /dashboard
- Expired → "⚠️ Подписка истекла. Продлите →" + /checkout

## Rollback
* Single revert of subscription_handler.go restores broken-but-known state.
* ConnectPage + DashboardPage changes revertable independently.
* No DB migration required.

## Risk register

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | Happ doesn't honor non-standard `Announce` header | This is a de-facto standard supported by Happ, V2RayNG (some forks), and competitor screenshots — confirmed by user as feature in target client. |
| R-02 | Removing Content-Disposition breaks file-download flow for some other client | We tested via curl that Happ branch already returns plain URIs; other-client branch returns base64. Removing attachment-style filename doesn't break content download — clients fetch by URL, not by suggested filename. |
| R-03 | Intent URI typo on Android breaks fallback | Use widely-documented pattern; verify on emulator/device. |
| R-04 | 1500ms timeout fires before slow phone opens app | Tunable constant — start at 1500, can extend. Even on miss, user sees fallback UI + manual buttons. |
| R-05 | App Store / Play Store IDs change | Constants in code; admin can override via settings later if needed. |
| R-06 | User has unusual mobile browser (Firefox Mobile) | Falls into iOS/Android UA branch; deeplink + timeout fallback still works. |
| R-07 | Announce overwrites future admin override mechanism | Designed in: announce is built by Go function; admin override (app_settings.announce_override) can be added later by inserting a check at top of `buildSubscriptionAnnounce`. |

## Out of scope reminders
* Sub-project D (per-plan rate-limit) — separate cycle
* Admin announce override — defer to next iteration if needed
* Multi-country profile grouping in subscription body — waits until 2nd exit-node
* Flutter app changes — picks up backend changes automatically next build
* QR-code on ConnectPage Desktop — could be added for cross-device flow, defer

## Deliverable

This design feeds `superpowers:writing-plans`. Expected output: 6-8 task plan (~3 hours work). Beads epic + sub-tasks created at planning step. Implementation via subagent-driven loop preferred.
