# Premium Happ Integration — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make HeYearth subscription render premium-quality in Happ (progress bar, expiry, dynamic announce, working refresh) and replace dashboard subscription-block with 3 large action buttons.

**Architecture:** Backend `subscription_handler.go` gets header bugfixes (drop `Content-Disposition: attachment`, extend `Subscription-Userinfo` with `total=`+`expire=`, add `Profile-Web-Page-Url`+`Announce`+`Announce-Url`). New helper `buildSubscriptionAnnounce` is a state-machine over `user_subscriptions`. Frontend gets new `ConnectPage.jsx` at `/connect?sub=<token>` for platform-aware Happ deeplink with App Store / Play Store / happ.su fallback. `DashboardPage.jsx` restructured: 3 large buttons on top (Happ + Telegram x2), URL block below with copy-only.

**Tech Stack:** Go 1.21+, PostgreSQL 14+, React 19 (JSX), react-router-dom, Tailwind v4.

**Spec:** `docs/superpowers/specs/2026-05-23-happ-premium-integration-design.md`

**Beads epic:** to be created in Task 0.

**Production:** hub 198.51.100.236. No DB migration. Deploy via `cd server/backend && go run quick_deploy.go` then `go run ./cmd/deploy --site` for frontend.

**Verification baseline (2026-05-23 01:27 MSK curl):**
```
HTTP/1.1 200 OK
Content-Disposition: attachment; filename="heyearth-216629e8"   ← TO REMOVE
Profile-Title: HiEarth
Profile-Update-Interval: 4
Subscription-Userinfo: upload=0; download=304273861083          ← TO EXTEND
Support-Url: https://hiearth.ru/dashboard
(absent) Profile-Web-Page-Url                                    ← TO ADD
(absent) Announce + Announce-Url                                 ← TO ADD
```

---

## File Structure

### Modify (backend)
* `server/backend/cmd/api/subscription_handler.go` (290 lines)
  * `handleSubscriptionLink` — drop default-branch `Content-Disposition: attachment` (line ~96 and ~102), keep the Clash YAML one (line ~86) since Clash IS a file download
  * `buildSubscriptionUserinfo` — extend with `total=` and `expire=`
  * New `buildSubscriptionAnnounce(userID) (text, url string)` — state machine

### Modify (frontend)
* `site/src/main.jsx` — register `/connect` route
* `site/src/pages/DashboardPage.jsx` (1071 lines) — create top `ConnectionActions` block; remove "Обновить подписку"

### Create (frontend)
* `site/src/pages/ConnectPage.jsx` — landing with platform detection + deeplink fallback
* (Optional) `site/src/components/ConnectionActions.jsx` — extracted 3-button block (or inline)

---

## Task 0: Create Beads epic (5 min)

- [ ] **Step 1:** Create epic

```bash
cd C:\Users\alex\Documents\heyearthproject
bd create --title="Premium Happ integration — subscription headers + landing + dashboard redesign" \
  --description="Fix Content-Disposition bug + add Subscription-Userinfo total/expire + Profile-Web-Page-Url + dynamic Announce. New /connect landing for Happ deeplink with App Store/Play Store/happ.su fallback. DashboardPage redesign: 3 big buttons top + URL below + remove Обновить подписку. Spec: docs/superpowers/specs/2026-05-23-happ-premium-integration-design.md. Plan: docs/superpowers/plans/2026-05-23-happ-premium-integration.md" \
  --type=epic --priority=1
```
Expected: prints new ID like `heyearthproject-XXXX`. Record as `EPIC_ID`.

---

## Task 1: Extend buildSubscriptionUserinfo with total= and expire= (15 min)

**Files:**
- Modify: `server/backend/cmd/api/subscription_handler.go` (function `buildSubscriptionUserinfo` at line ~231)

- [ ] **Step 1:** Replace the function with extended version

Find the current function:
```go
// buildSubscriptionUserinfo формирует заголовок Subscription-Userinfo.
func buildSubscriptionUserinfo(userID string) string {
	var trafficUsed int64
	var trafficLimit *int64
	err := db.DB.QueryRow(`
		SELECT COALESCE(us.traffic_used, 0), us.traffic_limit
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&trafficUsed, &trafficLimit)
	if err != nil {
		return ""
	}

	info := fmt.Sprintf("upload=0; download=%d", trafficUsed)
	if trafficLimit != nil && *trafficLimit > 0 {
		info += fmt.Sprintf("; total=%d", *trafficLimit)
	}
	return info
}
```

Replace with:
```go
// buildSubscriptionUserinfo формирует заголовок Subscription-Userinfo.
// Format per v2ray/Happ de-facto standard: "upload=N; download=N; total=N; expire=UNIX".
// `total` omitted if traffic_limit is NULL (unlimited plan).
// `expire` always present if there's an active subscription (Happ uses it for the
// "Истекает: DATE" display below the progress bar).
func buildSubscriptionUserinfo(userID string) string {
	var trafficUsed int64
	var trafficLimit *int64
	var validUntil time.Time
	err := db.DB.QueryRow(`
		SELECT COALESCE(us.traffic_used, 0), us.traffic_limit, us.valid_until
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&trafficUsed, &trafficLimit, &validUntil)
	if err != nil {
		return ""
	}

	info := fmt.Sprintf("upload=0; download=%d", trafficUsed)
	if trafficLimit != nil && *trafficLimit > 0 {
		info += fmt.Sprintf("; total=%d", *trafficLimit)
	}
	info += fmt.Sprintf("; expire=%d", validUntil.Unix())
	return info
}
```

- [ ] **Step 2:** Verify `time` is imported

Top of file should have `"time"` in the imports block. Run:
```bash
grep -n '"time"' server/backend/cmd/api/subscription_handler.go
```
Expected: returns a line. If empty — add `"time"` to the import block.

- [ ] **Step 3:** Build

```bash
cd server/backend && go build ./...
```
Expected: silent success.

- [ ] **Step 4:** Commit

```bash
git add server/backend/cmd/api/subscription_handler.go
git commit -m "fix(subscribe): add total= and expire= to Subscription-Userinfo

Happ requires both fields to show the progress bar and the expiry
date. Without total=, no progress bar renders. Without expire=, no
'Истекает: DATE' line appears under the bar."
```

---

## Task 2: Add buildSubscriptionAnnounce helper — state machine (25 min)

**Files:**
- Modify: `server/backend/cmd/api/subscription_handler.go` — add new function at bottom

- [ ] **Step 1:** Append new function

At the bottom of `server/backend/cmd/api/subscription_handler.go` (after `handleGetSubscribeToken`), append:

```go
// buildSubscriptionAnnounce builds the dynamic Announce + Announce-Url
// headers shown in Happ as a single clickable line below the progress bar.
//
// Priority order:
//   1. Expired                  → "⚠️ Подписка истекла. Продлите →"   + /checkout
//   2. ≤ 1 day to expiry        → "⏰ Подписка истекает завтра!"        + /checkout
//   3. ≤ 7 days, free-trial     → "⌛ Пробная подписка, осталось Nд"  + /checkout
//   4. ≤ 7 days, paid           → "⌛ Истекает через Nд"               + /checkout
//   5. traffic_used ≥ 80%       → "📊 Использовано N% трафика"         + /dashboard
//   6. healthy (default)        → "Личный кабинет ↗"                   + /dashboard
//
// The dashboard / checkout URL roots are taken from publicBaseURL().
// Returns empty strings if the user has no subscription record at all
// (handler should not emit headers in that case).
func buildSubscriptionAnnounce(userID string) (text, url string) {
	var (
		validUntil   time.Time
		trafficUsed  int64
		trafficLimit *int64
		planSlug     sql.NullString
	)
	err := db.DB.QueryRow(`
		SELECT us.valid_until,
		       COALESCE(us.traffic_used, 0),
		       us.traffic_limit,
		       COALESCE(p.slug, '')
		  FROM user_subscriptions us
		  JOIN users u  ON u.id = us.user_id
		  LEFT JOIN plans p ON p.id = us.plan_id
		 WHERE u.user_id = $1
		 ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&validUntil, &trafficUsed, &trafficLimit, &planSlug)
	if err != nil {
		return "", ""
	}

	base := publicBaseURL()
	checkout := base + "/checkout"
	dashboard := base + "/dashboard"
	now := time.Now()

	// 1. expired
	if validUntil.Before(now) {
		return "⚠️ Подписка истекла. Продлите →", checkout
	}

	daysLeft := int(validUntil.Sub(now).Hours() / 24)
	isTrial := planSlug.Valid && planSlug.String == "free-trial"

	// 2. ≤ 1 day
	if daysLeft <= 1 {
		return "⏰ Подписка истекает завтра!", checkout
	}

	// 3 / 4. ≤ 7 days
	if daysLeft <= 7 {
		if isTrial {
			return fmt.Sprintf("⌛ Пробная подписка, осталось %dд", daysLeft), checkout
		}
		return fmt.Sprintf("⌛ Истекает через %dд", daysLeft), checkout
	}

	// 5. traffic ≥ 80%
	if trafficLimit != nil && *trafficLimit > 0 {
		pct := int(float64(trafficUsed) * 100.0 / float64(*trafficLimit))
		if pct >= 80 {
			return fmt.Sprintf("📊 Использовано %d%% трафика", pct), dashboard
		}
	}

	// 6. healthy default — always include link to cabinet (per spec)
	return "Личный кабинет ↗", dashboard
}
```

- [ ] **Step 2:** Verify `database/sql` (for `sql.NullString`) is imported

Top of file should already have `"database/sql"` from existing code. Run:
```bash
grep -n '"database/sql"' server/backend/cmd/api/subscription_handler.go
```
If empty, add `"database/sql"` to imports.

- [ ] **Step 3:** Build

```bash
cd server/backend && go build ./...
```
Expected: silent. If unused `daysLeft` warnings appear — already used in expression, ignore. If `publicBaseURL` undefined — check it's exposed; should be (it's used elsewhere in `handleGetSubscribeToken`).

- [ ] **Step 4:** Commit

```bash
git add server/backend/cmd/api/subscription_handler.go
git commit -m "feat(subscribe): buildSubscriptionAnnounce — dynamic Announce state machine

Returns (text, url) for the Happ Announce + Announce-Url headers:
- expired                → 'Подписка истекла. Продлите →' + /checkout
- ≤ 1 day                → 'Подписка истекает завтра!'
- ≤ 7 days trial         → 'Пробная подписка, осталось Nд'
- ≤ 7 days paid          → 'Истекает через Nд'
- traffic ≥ 80%          → 'Использовано N% трафика'
- healthy (default)      → 'Личный кабинет ↗' + /dashboard

Always non-empty for active subscriptions per spec."
```

---

## Task 3: Wire new headers into handleSubscriptionLink + remove Content-Disposition (20 min)

**Files:**
- Modify: `server/backend/cmd/api/subscription_handler.go` — inside `handleSubscriptionLink`

- [ ] **Step 1:** Add the new headers near the existing common-headers block

Find the existing common-headers block in `handleSubscriptionLink` (around line ~67):
```go
	// Common headers for all formats
	configVersion := biz.GetSettingOrDefault("config_version", "1")
	w.Header().Set("X-Config-Version", configVersion)
	w.Header().Set("Subscription-Userinfo", buildSubscriptionUserinfo(userID))
	w.Header().Set("Profile-Update-Interval", "4") // auto-update every 4 hours (matches auto-tuner)
	w.Header().Set("Profile-Title", "HiEarth")
	w.Header().Set("Support-Url", "https://hiearth.ru/dashboard")
```

Replace with (adds Profile-Web-Page-Url + Announce + Announce-Url):
```go
	// Common headers for all formats — see spec
	// docs/superpowers/specs/2026-05-23-happ-premium-integration-design.md
	configVersion := biz.GetSettingOrDefault("config_version", "1")
	w.Header().Set("X-Config-Version", configVersion)
	w.Header().Set("Subscription-Userinfo", buildSubscriptionUserinfo(userID))
	w.Header().Set("Profile-Update-Interval", "4") // auto-update every 4 hours (matches auto-tuner)
	w.Header().Set("Profile-Title", "HiEarth")
	w.Header().Set("Profile-Web-Page-Url", "https://hiearth.ru/dashboard")
	w.Header().Set("Support-Url", "https://hiearth.ru/dashboard")

	// Dynamic announce shown in Happ as a clickable row below the progress bar.
	if announceText, announceURL := buildSubscriptionAnnounce(userID); announceText != "" {
		w.Header().Set("Announce", announceText)
		w.Header().Set("Announce-Url", announceURL)
	}
```

- [ ] **Step 2:** Remove `Content-Disposition: attachment` from the DEFAULT (non-Clash) branch

Find at line ~95-103:
```go
		if strings.Contains(ua, "happ") || r.URL.Query().Get("raw") == "1" {
			// Happ and raw mode: plain text URIs without base64
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"heyearth-%s\"", token[:8]))
			w.Write([]byte(plainURIs))
		} else {
			// V2Ray/SingBox: standard base64 encoded
			encoded := base64.StdEncoding.EncodeToString([]byte(plainURIs))
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"heyearth-%s\"", token[:8]))
			w.Write([]byte(encoded))
		}
```

Replace with (Content-Disposition removed from BOTH branches — this is the main fix):
```go
		if strings.Contains(ua, "happ") || r.URL.Query().Get("raw") == "1" {
			// Happ and raw mode: plain text URIs, no base64.
			// NO Content-Disposition: attachment — Happ treats attachment as file
			// download and skips parsing metadata headers (bug verified 2026-05-23).
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(plainURIs))
		} else {
			// V2Ray/SingBox/Nekoray: standard base64-encoded list.
			// NO Content-Disposition: attachment — same reason as Happ branch.
			encoded := base64.StdEncoding.EncodeToString([]byte(plainURIs))
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(encoded))
		}
```

KEEP the Clash YAML branch's Content-Disposition (around line ~86):
```go
	case "clash":
		clashConfig := buildClashConfig(userID)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=heyearth.yaml")
		w.Write([]byte(clashConfig))
```
Clash users DO want a YAML file download.

- [ ] **Step 3:** Build

```bash
cd server/backend && go build ./...
```
Expected: silent.

- [ ] **Step 4:** Commit

```bash
git add server/backend/cmd/api/subscription_handler.go
git commit -m "fix(subscribe): remove Content-Disposition attachment + add Announce/Profile-Web-Page-Url

Happ treats Content-Disposition: attachment as 'this is a file
download' and skips parsing metadata headers entirely. Removing it
unlocks: progress bar, expiry display, refresh button, Announce row.

Also wires new dynamic Announce + Announce-Url headers from
buildSubscriptionAnnounce, and adds Profile-Web-Page-Url for the
'open in browser' link inside Happ.

Clash YAML branch keeps Content-Disposition (it IS a file download)."
```

---

## Task 4: ConnectPage component (45 min)

**Files:**
- Create: `site/src/pages/ConnectPage.jsx`

- [ ] **Step 1:** Create the file with platform-aware deeplink + fallback logic

File: `site/src/pages/ConnectPage.jsx`

```jsx
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

/* ── Platform constants ── */
const APP_STORE_URL = 'https://apps.apple.com/us/app/happ-proxy-utility-plus/id6517741108'
const PLAY_STORE_URL = 'https://play.google.com/store/apps/details?id=com.happproxy'
const DESKTOP_HAPP_URL = 'https://www.happ.su/main/ru'
const DEEPLINK_TIMEOUT_MS = 1500

/* ── Helpers ── */
function detectPlatform() {
  if (typeof navigator === 'undefined') return 'desktop'
  const ua = navigator.userAgent || ''
  if (/iPad|iPhone|iPod/.test(ua) && !window.MSStream) return 'ios'
  if (/Android/.test(ua)) return 'android'
  return 'desktop'
}

function buildHappDeeplink(subscribeUrl) {
  // base64-encode the URL, strip trailing '=' padding (Happ accepts either)
  const b64 = btoa(subscribeUrl).replace(/=+$/, '')
  return `happ://add/${b64}`
}

function buildAndroidIntent(subscribeUrl) {
  const b64 = btoa(subscribeUrl).replace(/=+$/, '')
  const fallback = encodeURIComponent(PLAY_STORE_URL)
  return `intent://add/${b64}#Intent;scheme=happ;package=com.happproxy;S.browser_fallback_url=${fallback};end`
}

async function copyToClipboard(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/* ── Component ── */
export default function ConnectPage() {
  const [searchParams] = useSearchParams()
  const token = (searchParams.get('sub') || '').trim()
  const platform = detectPlatform()
  const [copied, setCopied] = useState(false)
  const [triggered, setTriggered] = useState(false)

  if (!token) {
    return (
      <section className="min-h-[60vh] flex items-center justify-center px-4">
        <div className="glass-card rounded-2xl p-8 max-w-md text-center">
          <h1 className="text-2xl font-bold mb-2">Ссылка некорректна</h1>
          <p className="text-sm text-text-secondary mb-6">
            Параметр <code>sub</code> отсутствует. Откройте кнопку «Добавить в приложение» из личного кабинета.
          </p>
          <Link to="/dashboard" className="inline-flex items-center gap-2 px-4 py-2 rounded-xl bg-brand/20 text-brand hover:bg-brand/30 transition">
            ← В личный кабинет
          </Link>
        </div>
      </section>
    )
  }

  const subscribeUrl = `${window.location.origin}/api/v1/user/subscribe/${token}`
  const happDeeplink = buildHappDeeplink(subscribeUrl)

  useEffect(() => {
    if (typeof window === 'undefined') return

    // Android: Intent URI handles fallback natively, no JS timeout needed.
    if (platform === 'android') {
      const androidIntent = buildAndroidIntent(subscribeUrl)
      window.location.href = androidIntent
      setTriggered(true)
      return
    }

    // iOS / Desktop: try deeplink, fall back to download URL after timeout.
    const fallback = platform === 'ios' ? APP_STORE_URL : DESKTOP_HAPP_URL
    window.location.href = happDeeplink
    setTriggered(true)

    const handle = setTimeout(() => {
      // If the app opened, the tab is hidden (iOS) or unloaded. If still visible,
      // redirect to the store/site for install.
      if (!document.hidden) {
        window.location.href = fallback
      }
    }, DEEPLINK_TIMEOUT_MS)

    return () => clearTimeout(handle)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  async function doCopy() {
    const ok = await copyToClipboard(subscribeUrl)
    if (ok) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1800)
    }
  }

  return (
    <section className="min-h-[80vh] flex items-center justify-center px-4 py-10">
      <div className="glass-card rounded-2xl p-6 md:p-8 max-w-lg w-full">
        <div className="flex items-center gap-3 mb-3">
          <div className="w-10 h-10 rounded-xl bg-brand/20 flex items-center justify-center">
            <svg className="w-6 h-6 text-brand" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
              <path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" />
            </svg>
          </div>
          <h1 className="text-xl md:text-2xl font-bold">Открываем Happ…</h1>
        </div>
        {triggered && (
          <p className="text-sm text-text-secondary mb-5">
            Если Happ не открылся автоматически, выберите ваше устройство ниже.
          </p>
        )}

        <div className="space-y-2 mb-6">
          <a href={APP_STORE_URL} target="_blank" rel="noopener noreferrer"
             className="block px-4 py-3 rounded-xl bg-bg-surface/50 hover:bg-bg-surface/70 border border-border text-sm text-text-primary transition">
            📱 Скачать Happ для iPhone (App Store)
          </a>
          <a href={PLAY_STORE_URL} target="_blank" rel="noopener noreferrer"
             className="block px-4 py-3 rounded-xl bg-bg-surface/50 hover:bg-bg-surface/70 border border-border text-sm text-text-primary transition">
            🤖 Скачать Happ для Android (Google Play)
          </a>
          <a href={DESKTOP_HAPP_URL} target="_blank" rel="noopener noreferrer"
             className="block px-4 py-3 rounded-xl bg-bg-surface/50 hover:bg-bg-surface/70 border border-border text-sm text-text-primary transition">
            💻 Скачать Happ для ПК (Windows / macOS / Linux)
          </a>
        </div>

        <div className="border-t border-border pt-5">
          <p className="text-xs text-text-secondary mb-2">
            Или используйте другой клиент (v2rayNG, Streisand, Hiddify):
          </p>
          <div className="flex items-stretch gap-2">
            <code className="flex-1 px-3 py-2 rounded-lg bg-bg-app text-xs font-mono break-all overflow-hidden text-ellipsis whitespace-nowrap">
              {subscribeUrl}
            </code>
            <button onClick={doCopy}
                    className="px-3 py-2 rounded-lg bg-brand/20 text-brand hover:bg-brand/30 text-sm transition shrink-0">
              {copied ? '✓ OK' : 'Копировать'}
            </button>
          </div>
        </div>

        <div className="mt-6 text-center">
          <Link to="/dashboard" className="text-xs text-text-secondary hover:text-text-primary transition">
            ← Назад в личный кабинет
          </Link>
        </div>
      </div>
    </section>
  )
}
```

- [ ] **Step 2:** Verify build

```bash
cd site && npm run build
```
Expected: vite reports `built in N`, no errors. (We can't test the route yet — Task 5 wires it.)

- [ ] **Step 3:** Commit (with route registration in Task 5)

Defer commit until Task 5; this file alone is dead code until the route is registered. We'll add+commit together.

---

## Task 5: Register /connect route (10 min)

**Files:**
- Modify: `site/src/main.jsx`

- [ ] **Step 1:** Add import + route

In `site/src/main.jsx`, find the imports near the top (other page imports like `LandingPage`, `LoginPage`, etc.). Add:
```jsx
import ConnectPage from './pages/ConnectPage.jsx'
```

In the `<Routes>` block, find the existing `<Route path="/dashboard" ... />` line. Add a sibling immediately above or below:
```jsx
<Route path="/connect" element={<ConnectPage />} />
```

Note: `/connect` does NOT require authentication — user might be logged out when opening the deeplink. ConnectPage works only with the `?sub=<token>` parameter; the token is itself an auth bearer for the subscription endpoint.

- [ ] **Step 2:** Build

```bash
cd site && npm run build
```
Expected: silent success.

- [ ] **Step 3:** Commit (ConnectPage + main.jsx together)

```bash
git add site/src/pages/ConnectPage.jsx site/src/main.jsx
git commit -m "feat(site): ConnectPage at /connect with platform-aware Happ deeplink

- iOS: happ://add/<b64> + 1.5s App Store fallback
- Android: intent URI with native Play Store fallback
- Desktop: happ://add/<b64> + 1.5s happ.su fallback
- Manual links for App Store / Play Store / happ.su shown immediately
- Subscription URL copyable for other clients (Hiddify/v2rayNG/Streisand)
- Public route (no auth) — token in ?sub= is itself the bearer"
```

---

## Task 6: DashboardPage redesign — 3 big buttons top + remove "Обновить подписку" (45 min)

**Files:**
- Modify: `site/src/pages/DashboardPage.jsx` (~1071 lines)

- [ ] **Step 1:** Inspect current layout

```bash
grep -n "Ваша ссылка\|Обновить подписку\|TelegramProxyButton\|Добавить в приложение\|Копировать ссылку\|subscribe-url\|subscribeLink\?\.subscribe_url" site/src/pages/DashboardPage.jsx | head -20
```
Note the line numbers of:
* The "Ваша ссылка для подключения" block
* The "Обновить подписку" button + handler
* The current "Добавить в приложение" / "Копировать ссылку" buttons
* Where `<TelegramProxyButton token={token} />` is mounted

- [ ] **Step 2:** Remove "Обновить подписку" button + its handler

Find the JSX block around line ~413-434 (per grep result). It looks roughly like:
```jsx
{/* Обновить подписку: cache-buster query чтобы клиент перечитал */}
<button
  onClick={...}
  className="..."
>
  ...
  Обновить подписку
</button>
```

Delete the entire `<button>` (including the comment). Also find and delete any handler function it references (search for the onClick target — likely something like `refreshSubscription`, `setRefreshing`, or inline `() => fetch(...)`).

If the handler references state like `const [refreshing, setRefreshing] = useState(false)` that is ONLY used by this button — delete that state too.

- [ ] **Step 3:** Locate "Добавить в приложение" button

Search for the current "Добавить в приложение" link/button. It's likely a `<a href=...>` or `<button onClick=...>` near the subscription URL display. Record its current target (it probably opens the subscription URL directly).

- [ ] **Step 4:** Create ConnectionActions block — 3 large buttons grid

Find where `<TelegramProxyButton token={token} />` is currently mounted in DashboardPage's main render. It's at the top of the page (per earlier work). Replace that single line with a new inline block:

```jsx
{/* ═══ Top action buttons: Happ + Telegram x2 ═══ */}
<ConnectionActions
  token={token}
  subscribeUrl={subscribeLink?.subscribe_url}
/>
```

Then add the `ConnectionActions` definition either:
* (Option A — inline) right above the export default function in DashboardPage.jsx
* (Option B — file) as `site/src/components/ConnectionActions.jsx`

Choose **Option A** for minimal file churn. Add at the top of DashboardPage.jsx (after imports, before main component):

```jsx
import { getMtproto } from '../api/user.js'

function ConnectionActions({ token, subscribeUrl }) {
  const [mtproto, setMtproto] = React.useState(null)

  React.useEffect(() => {
    if (!token) return
    let cancelled = false
    getMtproto(token).then((data) => {
      if (!cancelled) setMtproto(data)
    }).catch(() => {/* hide MTProto buttons on error */})
    return () => { cancelled = true }
  }, [token])

  const happHref = subscribeUrl
    ? `/connect?sub=${encodeURIComponent(extractTokenFromUrl(subscribeUrl))}`
    : '#'

  const mtprotoEnabled = mtproto && mtproto.enabled
  const cols = mtprotoEnabled ? 'sm:grid-cols-3' : 'sm:grid-cols-1'

  return (
    <div className={`grid grid-cols-1 ${cols} gap-3 mb-6 animate-fade-in-up`}>
      {/* 1. Happ install */}
      <a
        href={happHref}
        className="group flex items-center justify-center gap-3 px-5 py-5 rounded-2xl bg-gradient-to-br from-brand to-brand-dark text-white font-semibold shadow-lg shadow-brand/20 hover:shadow-brand/30 hover:scale-[1.02] transition-all duration-200 min-h-[64px]"
      >
        <svg className="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={2}>
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
        </svg>
        <span>Добавить в приложение</span>
      </a>

      {/* 2. Telegram tg:// */}
      {mtprotoEnabled && (
        <a
          href={mtproto.tg_deeplink}
          className="group flex items-center justify-center gap-3 px-5 py-5 rounded-2xl bg-blue-500 hover:bg-blue-400 text-white font-semibold shadow-lg shadow-blue-500/20 hover:shadow-blue-500/30 hover:scale-[1.02] transition-all duration-200 min-h-[64px]"
        >
          <svg className="w-6 h-6" viewBox="0 0 24 24" fill="currentColor">
            <path d="M21.05 3.05 2.95 9.13c-.93.32-.92 1.6 0 1.92l4.62 1.55 1.55 4.62c.32.93 1.6.92 1.92 0l1.05-3.13 3.65 2.74c.7.52 1.7.13 1.86-.72L21.6 4.07c.17-.84-.6-1.55-1.55-1.02Z" />
          </svg>
          <span>Открыть в Telegram</span>
        </a>
      )}

      {/* 3. Telegram via t.me web */}
      {mtprotoEnabled && (
        <a
          href={mtproto.t_me_link}
          target="_blank"
          rel="noopener noreferrer"
          className="group flex items-center justify-center gap-3 px-5 py-5 rounded-2xl glass hover:bg-white/10 text-text-primary font-semibold transition-all duration-200 min-h-[64px]"
        >
          <svg className="w-6 h-6 text-text-secondary" fill="none" viewBox="0 0 24 24" stroke="currentColor" strokeWidth={1.5}>
            <path strokeLinecap="round" strokeLinejoin="round" d="M13.19 8.688a4.5 4.5 0 011.242 7.244l-4.5 4.5a4.5 4.5 0 01-6.364-6.364l1.757-1.757m13.35-.622l1.757-1.757a4.5 4.5 0 00-6.364-6.364l-4.5 4.5a4.5 4.5 0 001.242 7.244" />
          </svg>
          <span>Открыть через t.me</span>
        </a>
      )}
    </div>
  )
}

// Extracts the trailing token segment from a subscribe URL like
// https://hiearth.ru/api/v1/user/subscribe/<token>
function extractTokenFromUrl(url) {
  if (!url) return ''
  const parts = url.split('/')
  return parts[parts.length - 1] || ''
}
```

Make sure `React` is imported (the file may use `import { useState, useEffect } from 'react'` — adjust accordingly: replace `React.useState` with `useState` and `React.useEffect` with `useEffect` if hooks are already destructured at top).

- [ ] **Step 5:** Remove the old `<TelegramProxyButton token={token} />` mounts in DashboardPage

If the file imports and uses `TelegramProxyButton`, remove those (the component logic is now inside `ConnectionActions`). Search:
```bash
grep -n "TelegramProxyButton" site/src/pages/DashboardPage.jsx
```
Delete the import line and any `<TelegramProxyButton ... />` JSX (the new `<ConnectionActions />` replaces them).

Do NOT delete `site/src/components/TelegramProxyButton.jsx` itself — `ConnectionsPage.jsx` (the existing one) may still use it.

- [ ] **Step 6:** Find and clean up the "Ваша ссылка для подключения" block — remove "Добавить в приложение" button (now in ConnectionActions); keep only URL display + small Copy button

Find the block. The existing layout has the URL display followed by buttons. Replace the buttons row with:

```jsx
<div className="flex items-stretch gap-2 mt-3">
  <code className="flex-1 px-3 py-2 rounded-lg bg-bg-app text-xs font-mono break-all overflow-hidden text-ellipsis whitespace-nowrap">
    {subscribeLink.subscribe_url}
  </code>
  <button
    onClick={() => copyText(subscribeLink.subscribe_url, 'sub-url')}
    className="px-3 py-2 rounded-lg bg-brand/20 text-brand hover:bg-brand/30 text-sm transition shrink-0"
  >
    {copiedField === 'sub-url' ? '✓ OK' : 'Копировать'}
  </button>
</div>
```

(`copyText` and `copiedField` may already exist in DashboardPage — use existing names. If `copyText` is named differently, match.)

- [ ] **Step 7:** Build site

```bash
cd site && npm run build
```
Expected: silent. If errors:
* "React is not defined" — change `React.useState` to `useState` (add `useState` to existing hooks import)
* "copyText is not defined" — find the existing copy helper name, replace
* JSX syntax errors — likely unbalanced div from removing the old button block

- [ ] **Step 8:** Commit

```bash
git add site/src/pages/DashboardPage.jsx
git commit -m "feat(site): DashboardPage top redesign — 3 big action buttons + removed 'Обновить'

New ConnectionActions block at the very top of the dashboard:
  1. Добавить в приложение → /connect?sub=<token> (Happ landing)
  2. Открыть в Telegram → tg://proxy?... (MTProto, only if enabled)
  3. Открыть через t.me → web fallback (MTProto, only if enabled)

Subscription URL block below now shows only the URL + small Copy
button. 'Обновить подписку' button removed — Happ refresh works
natively after the Content-Disposition fix.

ConnectionActions is inline in DashboardPage.jsx (option A) to avoid
spreading auth/data-fetching across files."
```

---

## Task 7: Deploy backend + frontend + verification on prod (30 min)

- [ ] **Step 1:** Deploy backend

```bash
cd server/backend
go run ./cmd/deploy --ssh "systemctl stop heyearth-api && rm -f /opt/heyearth/mainserver/heyearth-api && echo OK"
go run quick_deploy.go
```
Expected: `heyearth-api: ACTIVE ✓` + `Health: {"status":"ok"}`.

- [ ] **Step 2:** Deploy site

```bash
go run ./cmd/deploy --site
```
Expected: vite build success, assets uploaded, nginx reload OK.

- [ ] **Step 3:** Verify subscription headers (THE main fix)

```bash
# Replace <TOKEN> with a real subscribe token from DB
curl -i -A "Happ/1.0" "https://hiearth.ru/api/v1/user/subscribe/<redacted>" 2>&1 | head -25
```
Expected (compare to baseline):
* ✅ `Content-Disposition: attachment` **GONE**
* ✅ `Subscription-Userinfo: upload=0; download=N; total=L; expire=U`
* ✅ `Profile-Web-Page-Url: https://hiearth.ru/dashboard`
* ✅ `Announce: <state-machine output>` (varies by user — for active healthy: `Личный кабинет ↗`)
* ✅ `Announce-Url: https://hiearth.ru/dashboard` (for healthy) or `/checkout` (for expiring)
* ✅ Existing `Profile-Title`, `Profile-Update-Interval`, `Support-Url` still present

- [ ] **Step 4:** Verify ConnectPage routes

```bash
curl -s -o /dev/null -w "HTTP %{http_code}\n" "https://hiearth.ru/connect?sub=<redacted>"
```
Expected: `HTTP 200`. The page renders client-side; the HTTP response is the SPA shell.

```bash
curl -s "https://hiearth.ru/connect?sub=test" | grep -E "Happ|App Store|happ\.su"
```
Expected: matches — manual fallback link text present in static HTML or bundle.

- [ ] **Step 5:** Verify DashboardPage no longer ships "Обновить подписку"

```bash
SITE_JS=$(curl -s https://hiearth.ru/dashboard | grep -oE 'assets/[^"]+\.js' | head -1)
curl -s "https://hiearth.ru/${SITE_JS}" | grep -c "Обновить подписку"
```
Expected: `0` (string not in bundle).

- [ ] **Step 6:** Manual Happ smoke (on a real device)

For at least ONE phone, do:
1. Open https://hiearth.ru/dashboard while logged in
2. Tap "Добавить в приложение" — should redirect to `/connect?sub=...`
3. ConnectPage triggers deeplink
4. **If Happ installed**: app opens with "Add subscription" prompt — confirm
5. **If Happ NOT installed**: after 1.5s, App Store / Play Store opens to Happ
6. After install, return to Happ — subscription should be visible with:
   * ✅ Progress bar (`X / Y`)
   * ✅ Expiry date `Истекает: <date>`
   * ✅ Announce row with clickable link
   * ✅ Refresh button working — tap → no error, metadata refreshes

- [ ] **Step 7:** Push all commits

```bash
git push origin main
```

- [ ] **Step 8:** Close Beads epic

```bash
bd close <EPIC_ID from Task 0> --reason "Premium Happ integration deployed: headers fixed, ConnectPage live, DashboardPage redesigned. Verified via curl + manual Happ smoke."
```

---

## Risk register (from spec)

| ID | Risk | Mitigation |
|---|---|---|
| R-01 | Happ ignores Announce header | de-facto standard supported by Happ (visible in competitor screenshot) |
| R-02 | Removing Content-Disposition breaks other clients | curl shows we already serve plain URIs for Happ UA and base64 for V2RayNG — clients fetch by URL not filename |
| R-03 | Intent URI typo blocks Play Store fallback | use widely-documented pattern, verify on device |
| R-04 | 1500ms too short for slow phones | tunable constant; manual fallback links are also visible immediately |
| R-05 | App Store ID changes | constant in code; can be moved to settings later |
| R-06 | Firefox / unusual mobile browser | UA pattern catches iOS+Android safely, others fall into desktop branch (still works) |
| R-07 | Announce conflicts with future admin override | designed in: announce is built by Go function; admin override can wrap at function top |

## Rollback

* `git revert` — all changes isolated to subscription_handler.go, DashboardPage.jsx, main.jsx, new ConnectPage.jsx. No DB schema changes.
* If specific Happ regression and need to immediately re-enable old behavior: re-add `Content-Disposition` line via one-liner SSH on hub editing the deployed binary — NO, that's not how Go works. Revert via git is the only path. Fast enough (5-10 min).

## Out of scope reminders
* Sub-project D (per-plan rate-limit) — separate cycle
* Admin announce override — defer
* Multi-country profile grouping — waits for 2nd exit-node
* Flutter app changes — picks up backend changes automatically next build
* QR code on ConnectPage for cross-device flow — defer
* `Profile-Title` base64 encoding — not needed for ASCII "HiEarth"
