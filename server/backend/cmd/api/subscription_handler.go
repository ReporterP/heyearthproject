package main

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Subscription Links ───────────────────────
//
// Динамические подписки (sub-links) — совместимы с V2RayNG, Nekoray, Throne, SingBox, Clash.
// Пользователь вставляет ссылку в клиент, при изменении конфига на сервере — автообновление.
//
// GET /api/v1/user/subscribe/{token} — base64 список URI (V2Ray/SingBox формат)
// GET /api/v1/user/subscribe/{token}?format=clash — YAML для Clash/ClashMeta

// writeSubscriptionUnavailable answers a temporary failure with 503 and
// Retry-After. Subscription clients (Happ and friends) keep their cached
// profiles on 5xx, whereas a 404/403 makes them treat the link as invalid.
func writeSubscriptionUnavailable(w http.ResponseWriter, msg string) {
	w.Header().Set("Retry-After", "300")
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte(msg))
}

func handleSubscriptionLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Извлекаем subscribe_token из URL: /api/v1/user/subscribe/{token}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/user/subscribe/")
	token := strings.TrimSpace(path)
	if token == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("missing token"))
		return
	}

	// Ищем пользователя по subscribe_token
	var userID string
	err := db.DB.QueryRow(`
		SELECT user_id FROM users WHERE subscribe_token::text = $1
	`, token).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("invalid token"))
			return
		}
		// A DB failure is not an invalid link: answer 503 so the client keeps
		// its cached profiles. Never log the token itself.
		log.Printf("[subscribe] subscribe_token lookup failed: %v", err)
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
	}

	// Проверяем доступ
	access, err := biz.GetUserAccessState(userID, "")
	if err != nil {
		log.Printf("[subscribe] user %s: access state lookup failed: %v", userID, err)
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
	}
	if !access.CanConnect {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("no active subscription"))
		return
	}

	// Получаем connection options (per-user)
	options, err := buildHubConnectionOptions(userID)
	if err != nil {
		// 503 keeps the client's cached profiles; never substitute another UUID.
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
	}
	if len(options) == 0 {
		writeSubscriptionUnavailable(w, "no connections available")
		return
	}

	format := r.URL.Query().Get("format")

	// Build the Clash profile before any header is written: if the per-user
	// UUID cannot be resolved the client must get a clean 503 (and keep its
	// cached profile), not a 200 with an empty "# error" body.
	var clashConfig string
	if format == "clash" {
		clashConfig, err = buildClashConfig(userID)
		if err != nil {
			writeSubscriptionUnavailable(w, "temporarily unavailable")
			return
		}
	}

	// Common headers for all formats — see spec
	// docs/superpowers/specs/2026-05-23-happ-premium-integration-design.md
	configVersion := biz.GetSettingOrDefault("config_version", "1")
	w.Header().Set("X-Config-Version", configVersion)
	w.Header().Set("Subscription-Userinfo", buildSubscriptionUserinfo(userID))
	w.Header().Set("Profile-Update-Interval", "4") // auto-update every 4 hours (matches auto-tuner)
	w.Header().Set("Profile-Title", "HiEarth")
	w.Header().Set("Profile-Web-Page-Url", "https://hiearth.ru/dashboard")
	w.Header().Set("Support-Url", "https://hiearth.ru/dashboard")
	// Routing-Enable: 1 — we DO want Happ's routing rules to apply, because
	// the subscription body ships a `happ://routing/onadd/<b64>` profile with
	// DirectIp exclusions for our MTProto-proxy IPs. Without these exclusions,
	// when a user enables both VPN and Telegram's MTProto-proxy, the Telegram
	// → MTProto-proxy connection loops through the VPN tunnel and breaks.
	w.Header().Set("Routing-Enable", "1")

	// Dynamic announce shown in Happ as a clickable row below the progress bar.
	// Happ requires `base64:` prefix for non-ASCII content per their docs
	// https://www.happ.su/main/dev-docs/app-management — Cyrillic + emoji break
	// the URL parser otherwise (manifesting as "URL подписки не валидна" on iOS).
	if announceText, announceURL := buildSubscriptionAnnounce(userID); announceText != "" {
		w.Header().Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(announceText)))
		w.Header().Set("Announce-Url", announceURL)
	}

	// Build URI list — prepend a Happ routing profile deeplink if we have
	// MTProto-proxy IPs to exclude from VPN routing, OR if the user's plan has
	// direct-route services attached (e.g. RU banks/госуслуги bypass).
	// The deeplink format `happ://routing/onadd/<b64-json>` is documented at
	// https://www.happ.su/main/dev-docs/routing.md.
	//
	// IMPORTANT: emit happ://routing ONLY for Happ User-Agent. Non-Happ clients
	// (Hiddify, v2rayNG, sing-box, NekoBox) misparse the huge base64 blob
	// (~300KB) embedded as URI #1 — Hiddify Android user reported TLS handshake
	// errors + total connectivity failure tracing to the happ://routing line
	// corrupting the subscription parser state. The previous "non-Happ clients
	// ignore unknown URI schemes safely" assumption proved wrong in practice.
	uaForRouting := strings.ToLower(r.Header.Get("User-Agent"))
	wantsHappRouting := strings.Contains(uaForRouting, "happ")

	var uris []string
	if wantsHappRouting {
		mtprotoIPs, mtErr := biz.ListMTProtoExitIPs()
		if mtErr != nil {
			log.Printf("[subscribe] ListMTProtoExitIPs error: %v (continuing with no MTProto direct-IPs)", mtErr)
			mtprotoIPs = nil
		}
		// Determine plan_id of the user's current active subscription. Errors are
		// non-fatal — planID stays 0, BuildHappRoutingDeeplinkForPlan handles that.
		planID := 0
		_ = db.DB.QueryRow(`
			SELECT COALESCE(plan_id, 0) FROM user_subscriptions
			 WHERE user_id = (SELECT id FROM users WHERE user_id = $1)
			   AND valid_until > CURRENT_TIMESTAMP
			   AND plan_id IS NOT NULL
			 ORDER BY valid_until DESC LIMIT 1
		`, userID).Scan(&planID)
		if deeplink := biz.BuildHappRoutingDeeplinkForPlan(planID, mtprotoIPs); deeplink != "" {
			uris = append(uris, deeplink)
		}
	}
	for _, opt := range options {
		if opt.Uri != "" {
			uris = append(uris, opt.Uri)
		}
	}

	switch format {
	case "clash":
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=heyearth.yaml")
		w.Write([]byte(clashConfig))

	default:
		ua := strings.ToLower(r.Header.Get("User-Agent"))
		plainURIs := strings.Join(uris, "\n")

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
	}
}

// buildClashConfig генерирует Clash/ClashMeta YAML конфиг.
// Returns an error when the per-user UUID cannot be resolved; the caller must
// answer 503 rather than ship a placeholder profile that would overwrite the
// client's cached one.
func buildClashConfig(userID string) (string, error) {
	host := biz.GetSettingOrDefault("main_server_host", "hiearth.ru")
	vlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		log.Printf("[subscribe] user %s: vless uuid error: %v", userID, err)
		return "", fmt.Errorf("vless uuid: %w", err)
	}

	// Check protocol permissions for this user's plan
	canVLESS := biz.IsProtocolAllowed(userID, "vless")
	canSOCKS := biz.IsProtocolAllowed(userID, "socks")
	canHTTP := biz.IsProtocolAllowed(userID, "http")
	canHysteria2 := biz.IsProtocolAllowed(userID, "hysteria2")

	var proxies []string
	var proxyNames []string

	// 1. VLESS Reality (Clash.Meta / mihomo)
	realityPubKey := biz.GetSettingOrDefault("reality_public_key", "")
	realityShortID := biz.GetSettingOrDefault("reality_short_id", "")
	realitySNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
	if realityPubKey != "" && vlessUUID != "" && canVLESS {
		flow := biz.GetSettingOrDefault("reality_flow", "xtls-rprx-vision")
		flowLine := ""
		if flow != "" {
			flowLine = fmt.Sprintf("\n    flow: %s", flow)
		}

		// Multi-profile: 3 VLESS configs for different ISP aggressiveness.
		// fp diversification: chrome (most-tested), firefox (second-most-tested),
		// edge (Chromium-based but distinct JA3). Avoid ios/safari — XTLS #5918,
		// #4852 regressions cause flapping handshake on RU mobile DPI
		// (research: docs/research/2026-05-27-happ-tele2-tls-handshake.md).
		profiles := []struct{ name, fp string }{
			{"HeYearth-Home", "chrome"},
			{"HeYearth-Mobile", "firefox"},
			{"HeYearth-Strict", "edge"},
		}
		for _, p := range profiles {
			proxyNames = append(proxyNames, p.name)
			proxies = append(proxies, fmt.Sprintf(
				`  - name: "%s"
    type: vless
    server: %s
    port: 443
    uuid: "%s"%s
    network: tcp
    tls: true
    reality-opts:
      public-key: "%s"
      short-id: "%s"
    client-fingerprint: %s
    servername: %s`, p.name, host, vlessUUID, flowLine, realityPubKey, realityShortID, p.fp, realitySNI))
		}
	}

	// 1b. VLESS Reality + XHTTP (port 2443)
	if realityPubKey != "" && vlessUUID != "" && canVLESS {
		name := "HeYearth-XHTTP"
		proxyNames = append(proxyNames, name)
		proxies = append(proxies, fmt.Sprintf(
			`  - name: "%s"
    type: vless
    server: %s
    port: 2443
    uuid: "%s"
    network: xhttp
    tls: true
    reality-opts:
      public-key: "%s"
      short-id: "%s"
    client-fingerprint: chrome
    servername: %s`, name, host, vlessUUID, realityPubKey, realityShortID, realitySNI))
	}

	// 1c. Hysteria2 Express (premium plans only — auto-routed by Happ for UDP-heavy traffic).
	// Happ chooses Hysteria2 when QUIC is available; falls back to VLESS Reality when UDP
	// is blocked (corporate WiFi, mobile carriers throttling QUIC). The auth-backend
	// enforces plan membership server-side, so even if someone copies the URI to a base-tier
	// account it will be rejected.
	if canHysteria2 {
		hy2Password, hy2Err := biz.GetUserHysteria2Password(userID)
		obfsPassword := biz.GetHysteria2ObfsPassword()
		// Per-plan up/down. Fall back to safe defaults if plan lookup fails.
		up, down := 50, 100
		if plan, perr := biz.GetUserPlan(userID); perr == nil && plan != nil && plan.ExpressEnabled {
			if plan.ExpressUpMbps > 0 {
				up = plan.ExpressUpMbps
			}
			if plan.ExpressDownMbps > 0 {
				down = plan.ExpressDownMbps
			}
		}
		if hy2Err == nil && hy2Password != "" && obfsPassword != "" {
			name := "HiEarth-Express"
			proxyNames = append(proxyNames, name)
			// Clash.Meta hysteria2 profile. Ports range matches the iptables redirect
			// configured by cmd/deploy --setup-hysteria2-hub. Happ picks one randomly
			// and rotates per its port-hop schedule.
			proxies = append(proxies, fmt.Sprintf(
				`  - name: "%s"
    type: hysteria2
    server: %s
    ports: 30000-65000
    password: "%s"
    obfs: salamander
    obfs-password: "%s"
    sni: %s
    skip-cert-verify: false
    up: "%d Mbps"
    down: "%d Mbps"`, name, host, hy2Password, obfsPassword, host, up, down))
		}
	}

	// 2. SOCKS
	if vlessUUID != "" && canSOCKS {
		name := "HeYearth-SOCKS"
		proxyNames = append(proxyNames, name)
		proxies = append(proxies, fmt.Sprintf(
			`  - name: "%s"
    type: socks5
    server: %s
    port: 1080
    username: "%s"
    password: "%s"`, name, host, userID, vlessUUID))
	}

	// 3. HTTP proxy
	if vlessUUID != "" && canHTTP {
		name := "HeYearth-HTTP"
		proxyNames = append(proxyNames, name)
		proxies = append(proxies, fmt.Sprintf(
			`  - name: "%s"
    type: http
    server: %s
    port: 8080
    username: "%s"
    password: "%s"`, name, host, userID, vlessUUID))
	}

	namesList := ""
	for _, n := range proxyNames {
		namesList += fmt.Sprintf("\n      - \"%s\"", n)
	}

	return fmt.Sprintf(`# HeYearth — Clash/ClashMeta Config
mixed-port: 7890
allow-lan: false
mode: rule
log-level: info

proxies:
%s

proxy-groups:
  - name: "HeYearth"
    type: select
    proxies:%s
      - DIRECT

rules:
  - MATCH,HeYearth
`, strings.Join(proxies, "\n"), namesList), nil
}

// buildSubscriptionUserinfo формирует заголовок Subscription-Userinfo.
// buildSubscriptionUserinfo формирует заголовок Subscription-Userinfo.
// Format per v2ray/Happ de-facto standard: "upload=N; download=N; total=N; expire=UNIX".
// `total` omitted if traffic_limit is NULL (unlimited plan).
// `expire` always present if there's an active subscription (Happ uses it for the
// "Истекает: DATE" display below the progress bar).
func buildSubscriptionUserinfo(userID string) string {
	// Resolve active subscription: started_at (for cycle math), valid_until,
	// and the effective per-cycle limit (user override > plan default > 0/unlimited).
	var startedAt sql.NullTime
	var validUntil time.Time
	var subLimit sql.NullInt64
	var planLimit sql.NullInt64
	var userDBID int
	err := db.DB.QueryRow(`
		SELECT u.id, us.started_at, us.valid_until,
		       us.traffic_limit, p.traffic_limit_bytes
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY us.valid_until DESC
		LIMIT 1
	`, userID).Scan(&userDBID, &startedAt, &validUntil, &subLimit, &planLimit)
	if err != nil {
		return ""
	}
	_ = userDBID // reserved for future per-user override path

	// Effective limit (0 = unlimited; total=0 is the happ standard for ∞)
	var effLimit int64
	if subLimit.Valid && subLimit.Int64 > 0 {
		effLimit = subLimit.Int64
	} else if planLimit.Valid && planLimit.Int64 > 0 {
		effLimit = planLimit.Int64
	}

	// Cycle window — usage we report is *this cycle*, not subscription lifetime.
	cycleStart, cycleEnd := biz.CycleWindow(startedAt, time.Now())
	cycleUsed, _ := biz.PeriodUsage(userID, cycleStart, cycleEnd)

	// expire = end of current cycle so happ shows the meaningful date (when
	// quota resets) for monthly-cycle users. If the subscription ends before
	// cycle end (e.g. user's annual plan ends mid-cycle), prefer valid_until.
	expireAt := cycleEnd
	if validUntil.Before(expireAt) {
		expireAt = validUntil
	}

	return fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d",
		cycleUsed, effLimit, expireAt.Unix())
}

// handleGetSubscribeToken — REST endpoint для получения subscribe token текущего пользователя.
func handleGetSubscribeToken(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	var token string
	err = db.DB.QueryRow(`
		SELECT COALESCE(subscribe_token::text, '') FROM users WHERE user_id = $1
	`, userID).Scan(&token)
	if err != nil || token == "" {
		// Генерируем если нет
		err = db.DB.QueryRow(`
			UPDATE users SET subscribe_token = gen_random_uuid()
			WHERE user_id = $1 RETURNING subscribe_token::text
		`, userID).Scan(&token)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate token: " + err.Error()})
			return
		}
	}

	baseURL := publicBaseURL()
	subscribeURL := fmt.Sprintf("%s/api/v1/user/subscribe/%s", baseURL, token)

	// Happ one-tap import requires `happ://crypt5/<encrypted>` per docs.
	// We fetch from crypto.happ.su lazily and cache in users.happ_crypt_link.
	// On failure we still return the plain subscribe_url — frontend can copy.
	happLink, _ := biz.GetOrFetchHappCryptLink(userID, subscribeURL)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscribe_token": token,
		"subscribe_url":   subscribeURL,
		"clash_url":       subscribeURL + "?format=clash",
		"happ_link":       happLink,
	})
}

// handleGetHappLink — public endpoint for ConnectPage to fetch the Happ
// crypt5 deeplink by subscribe token (no auth needed; token IS the bearer).
//
// GET /api/v1/user/happ-link/<token>
// → 200 OK {"happ_link": "happ://crypt5/..."}
// → 404 if token unknown
// → 502 if crypto.happ.su unreachable on first call (subsequent calls hit cache)
func handleGetHappLink(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/v1/user/happ-link/")
	token := strings.TrimSpace(path)
	if token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token"})
		return
	}

	var userID string
	err := db.DB.QueryRow(`
		SELECT user_id FROM users WHERE subscribe_token::text = $1
	`, token).Scan(&userID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invalid token"})
		return
	}

	baseURL := publicBaseURL()
	subscribeURL := fmt.Sprintf("%s/api/v1/user/subscribe/%s", baseURL, token)

	happLink, err := biz.GetOrFetchHappCryptLink(userID, subscribeURL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error":         "crypto.happ.su unavailable",
			"subscribe_url": subscribeURL,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"happ_link":     happLink,
		"subscribe_url": subscribeURL,
	})
}

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
