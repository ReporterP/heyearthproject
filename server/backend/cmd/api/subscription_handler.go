package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
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

// writeJSONUnavailable is writeSubscriptionUnavailable for the JSON API
// (/api/v1/user/connections): 503 + Retry-After + {"error": msg}. Clients
// must read it as "try again later", never as "no access / no profile".
func writeJSONUnavailable(w http.ResponseWriter, msg string) {
	w.Header().Set("Retry-After", "300")
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": msg})
}

// sessionLookupFailure reports whether a token check failed only because its
// session could not be read (biz.SessionLookupError) and returns the database
// error for the log.
func sessionLookupFailure(err error) (error, bool) {
	var sle *biz.SessionLookupError
	if errors.As(err, &sle) {
		return sle.Err, true
	}
	return nil, false
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

	// Every read of the profile happens BEFORE any header is written, and any
	// failed read answers 503: the client keeps its cached profile. A 200 built
	// from a failed read (no VLESS/Bamboo entries, a default SNI or host, a
	// routing profile without the plan's services) would replace a good cached
	// profile until the next refresh.
	in, err := loadHubConnectionInputs(userID)
	if err != nil {
		// 503 keeps the client's cached profiles; never substitute another UUID.
		log.Printf("[subscribe] user %s: profile inputs: %v", userID, err)
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
	}
	options := hubConnectionOptions(in)
	if len(options) == 0 {
		writeSubscriptionUnavailable(w, "no connections available")
		return
	}

	format := r.URL.Query().Get("format")

	var clashConfig string
	if format == "clash" {
		clashConfig = clashProfile(in)
	}

	configVersion, err := subscriptionSetting("config_version", "1")
	if err != nil {
		log.Printf("[subscribe] user %s: %v", userID, err)
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
	}
	userinfo, err := buildSubscriptionUserinfo(userID)
	if err != nil {
		log.Printf("[subscribe] user %s: %v", userID, err)
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
	}
	// The subscription domain (go.<domain>, through the reserve entries): links in
	// the profile follow the host the client fetched it from, so a client on that
	// name opens the cabinet on it too. «Авто» (JSON) is the owner's switch.
	var subDomain, autoMode, autoTestUser string
	for _, s := range []struct {
		dst      *string
		key, def string
	}{{&subDomain, "sub_domain", ""}, {&autoMode, "sub_auto_mode", autoModeOff}, {&autoTestUser, "sub_auto_test_user", ""}} {
		if *s.dst, err = subscriptionSetting(s.key, s.def); err != nil {
			log.Printf("[subscribe] user %s: %v", userID, err)
			writeSubscriptionUnavailable(w, "temporarily unavailable")
			return
		}
	}
	linkBase, webPage := publicBaseURL(), "https://hiearth.ru/dashboard"
	if d := strings.TrimSpace(subDomain); d != "" && requestHost(r.Host) == d {
		linkBase = "https://" + d
		webPage = linkBase + "/dashboard"
	}
	announceText, announceURL, err := buildSubscriptionAnnounceAt(userID, linkBase)
	if err != nil {
		log.Printf("[subscribe] user %s: %v", userID, err)
		writeSubscriptionUnavailable(w, "temporarily unavailable")
		return
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
	// The JSON subscription («⚡ Авто»): on request (?format=json), or for a Happ
	// client while the owner serves it (all, or the test user only).
	wantsJSON := format == "json" || (format == "" && wantsHappRouting && autoModeServes(autoMode, autoTestUser, userID))

	var uris []string
	var jsonBody []byte
	if wantsJSON {
		// The same strictness as the routing profile: a failed read is a 503.
		mtprotoIPs, err := biz.ListMTProtoExitIPs()
		if err == nil {
			var planID int
			if planID, err = biz.EffectivePlanID(userID); err == nil {
				var d directRouting
				if d.sites, d.ips, err = biz.CompactDirectRoutingStrict(planID, mtprotoIPs); err == nil {
					jsonBody = jsonSubscription(in, d)
				}
			}
		}
		if err != nil {
			log.Printf("[subscribe] user %s: json profile: %v", userID, err)
			writeSubscriptionUnavailable(w, "temporarily unavailable")
			return
		}
		if jsonBody != nil {
			format = "json"
		} else if format == "json" {
			format = "" // no VLESS way in (e.g. a plan without vless): the plain URIs below, never an empty 200
		}
	}
	if wantsHappRouting && jsonBody == nil {
		// Happ caches the routing profile too: any failed read is a 503, not a
		// profile without the MTProto IPs or the plan's direct-route services.
		mtprotoIPs, err := biz.ListMTProtoExitIPs()
		if err != nil {
			log.Printf("[subscribe] user %s: %v", userID, err)
			writeSubscriptionUnavailable(w, "temporarily unavailable")
			return
		}
		// Plan of the user's effective subscription: the plan the Hub routes the
		// user by (best_plan_id of ListActiveVlessClients), so the deeplink's
		// direct-route services match the Hub's per-plan rules. 0 = no plan.
		planID, err := biz.EffectivePlanID(userID)
		if err != nil {
			log.Printf("[subscribe] user %s: %v", userID, err)
			writeSubscriptionUnavailable(w, "temporarily unavailable")
			return
		}
		deeplink, err := biz.BuildHappRoutingDeeplinkForPlanStrict(planID, mtprotoIPs)
		if err != nil {
			log.Printf("[subscribe] user %s: happ routing profile: %v", userID, err)
			writeSubscriptionUnavailable(w, "temporarily unavailable")
			return
		}
		if deeplink != "" {
			uris = append(uris, deeplink)
		}
	}
	for _, opt := range options {
		if opt.Uri != "" {
			uris = append(uris, opt.Uri)
		}
	}

	// Common headers for all formats — see spec
	// docs/superpowers/specs/2026-05-23-happ-premium-integration-design.md
	w.Header().Set("X-Config-Version", configVersion)
	w.Header().Set("Subscription-Userinfo", userinfo)
	// Auto-update every subscription_update_interval_hours (default 4, matches
	// the auto-tuner); 1 h during an entry failover (phase 3).
	w.Header().Set("Profile-Update-Interval", strconv.Itoa(max(in.entries.interval, 1)))
	w.Header().Set("Profile-Title", "HiEarth")
	w.Header().Set("Profile-Web-Page-Url", webPage)
	w.Header().Set("Support-Url", webPage)
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
	if announceText != "" {
		w.Header().Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(announceText)))
		w.Header().Set("Announce-Url", announceURL)
	}

	switch format {
	case "json":
		// Happ: the local proxy ports of the configs get Happ's own login.
		w.Header().Set("Socks-Auth-Mode", "auto")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(jsonBody)

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

// clashProfile builds the Clash/ClashMeta YAML from in. Pure.
func clashProfile(in hubConnectionInputs) string {
	host := in.entries.main // the failover target, or the Hub
	if host == "" {
		host = in.mainServerHost
	}
	if host == "" {
		host = "hiearth.ru"
	}
	vlessUUID := in.vlessUUID

	// Protocol permissions of this user's plan
	canVLESS := in.canVLESS
	canHysteria2 := in.canHysteria2

	var proxies []string
	var proxyNames []string

	// 1. VLESS Reality (Clash.Meta / mihomo)
	realityPubKey := in.realityPubKey
	realityShortID := in.realityShortID
	realitySNI := in.realitySNI
	flowLine := ""
	if flow := in.realityFlow; flow != "" {
		flowLine = fmt.Sprintf("\n    flow: %s", flow)
	}
	if realityPubKey != "" && vlessUUID != "" && canVLESS {
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
			proxies = append(proxies, clashRealityProxy(p.name, host, vlessUUID, flowLine, realityPubKey, realityShortID, p.fp, realitySNI))
		}
	}

	// 1b. VLESS Reality + XHTTP (port 2443)
	if realityPubKey != "" && vlessUUID != "" && canVLESS {
		name := "HeYearth-XHTTP"
		proxyNames = append(proxyNames, name)
		proxies = append(proxies, clashXHTTPProxy(name, host, vlessUUID, realityPubKey, realityShortID, realitySNI, in.mainServerHost))
	}

	// 1c. Hysteria2 Express (premium plans only — auto-routed by Happ for UDP-heavy traffic).
	// Happ chooses Hysteria2 when QUIC is available; falls back to VLESS Reality when UDP
	// is blocked (corporate WiFi, mobile carriers throttling QUIC). The auth-backend
	// enforces plan membership server-side, so even if someone copies the URI to a base-tier
	// account it will be rejected.
	//
	// Per-plan up/down; safe defaults without an Express plan (a failed
	// plan read never gets here: loadHubConnectionInputs returns it).
	up, down := 50, 100
	if plan := in.plan; plan != nil && plan.ExpressEnabled {
		if plan.ExpressUpMbps > 0 {
			up = plan.ExpressUpMbps
		}
		if plan.ExpressDownMbps > 0 {
			down = plan.ExpressDownMbps
		}
	}
	if canHysteria2 {
		hy2Password, obfsPassword := in.hy2Password, in.obfsPassword
		if hy2Password != "" && obfsPassword != "" {
			name := "HiEarth-Express"
			proxyNames = append(proxyNames, name)
			// sni on the Hub stays the connect host, as it always was (pinned
			// by the subscription_clash golden). During a failover the server is
			// a relay IP that no certificate names (skip-cert-verify: false):
			// the cert domain then, like the URIs and the reserve proxies.
			// The main Express follows the target only when Бамбук answers
			// through it; otherwise it stays on the Hub.
			server, sni := host, host
			if in.entries.failover && !in.entries.mainUDP {
				server, sni = in.mainServerHost, in.mainServerHost
			} else if in.entries.failover {
				sni = in.hysteria2SNI
			}
			proxies = append(proxies, clashHy2Proxy(name, server, hy2Password, obfsPassword, sni, up, down))
		}
	}

	// While everyone is pinned to a relay: Home and XHTTP straight to the Hub
	// (the way out on Wi-Fi if the relay dies), like directOptions.
	if in.entries.pinned && in.mainServerHost != "" && realityPubKey != "" && vlessUUID != "" && canVLESS {
		proxyNames = append(proxyNames, "HeYearth-Home"+directLabel, "HeYearth-XHTTP"+directLabel)
		proxies = append(proxies,
			clashRealityProxy("HeYearth-Home"+directLabel, in.mainServerHost, vlessUUID, flowLine, realityPubKey, realityShortID, "chrome", realitySNI),
			clashXHTTPProxy("HeYearth-XHTTP"+directLabel, in.mainServerHost, vlessUUID, realityPubKey, realityShortID, realitySNI, in.mainServerHost))
	}

	// Reserve entries (phase 3): the same proxies through each healthy relay,
	// after the main ones; Express only through a relay whose UDP is healthy.
	reserves := shownReserves(in.entries)
	if in.mainServerHost == "" { // same gate as reserveOptions: no Hub address, no reserve lines
		reserves = nil
	}
	for k, r := range reserves {
		sfx := reserveSuffix(k)
		if realityPubKey != "" && vlessUUID != "" && canVLESS {
			proxyNames = append(proxyNames, "HeYearth-Home"+sfx, "HeYearth-XHTTP"+sfx)
			proxies = append(proxies,
				clashRealityProxy("HeYearth-Home"+sfx, r.address, vlessUUID, flowLine, realityPubKey, realityShortID, "chrome", realitySNI),
				clashXHTTPProxy("HeYearth-XHTTP"+sfx, r.address, vlessUUID, realityPubKey, realityShortID, realitySNI, in.mainServerHost))
		}
		if canHysteria2 && in.hy2Password != "" && in.obfsPassword != "" && r.udp {
			proxyNames = append(proxyNames, "HiEarth-Express"+sfx)
			proxies = append(proxies, clashHy2Proxy("HiEarth-Express"+sfx, r.address, in.hy2Password, in.obfsPassword, in.hysteria2SNI, up, down))
		}
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
`, strings.Join(proxies, "\n"), namesList)
}

// clashRealityProxy is one VLESS Reality TCP proxy (port 443). flowLine is
// "" or "\n    flow: <flow>".
func clashRealityProxy(name, host, uuid, flowLine, pbk, sid, fp, sni string) string {
	return fmt.Sprintf(
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
    servername: %s`, name, host, uuid, flowLine, pbk, sid, fp, sni)
}

// clashXHTTPProxy is the VLESS Reality + XHTTP proxy (port 2443). hubHost is
// main_server_host: the Hub's vless-xhttp inbound checks Host = hubHost and
// path "/" (xray_sync.go, the URI's host=/path=). A proxy whose server is
// another address (a relay, the main proxy during a failover) sets both in
// xhttp-opts — what mihomo sends without them is undocumented. On the Hub
// itself the proxy stays as it always was (pinned by subscription_clash).
func clashXHTTPProxy(name, host, uuid, pbk, sid, sni, hubHost string) string {
	opts := ""
	if hubHost != "" && host != hubHost {
		opts = fmt.Sprintf("\n    xhttp-opts:\n      path: \"/\"\n      host: %s", hubHost)
	}
	return fmt.Sprintf(
		`  - name: "%s"
    type: vless
    server: %s
    port: 2443
    uuid: "%s"
    network: xhttp%s
    tls: true
    reality-opts:
      public-key: "%s"
      short-id: "%s"
    client-fingerprint: chrome
    servername: %s`, name, host, uuid, opts, pbk, sid, sni)
}

// clashHy2Proxy is a Clash.Meta hysteria2 proxy. Ports range matches the
// iptables redirect configured by cmd/deploy --setup-hysteria2-hub (a relay
// forwards the same range to the Hub). Happ picks one randomly and rotates per
// its port-hop schedule.
func clashHy2Proxy(name, host, pw, obfs, sni string, up, down int) string {
	return fmt.Sprintf(
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
    down: "%d Mbps"`, name, host, pw, obfs, sni, up, down)
}

// buildSubscriptionUserinfo формирует заголовок Subscription-Userinfo.
// Format per v2ray/Happ de-facto standard: "upload=N; download=N; total=N; expire=UNIX".
// `total` is 0 if traffic_limit is NULL (unlimited plan).
// `expire` always present if there's an active subscription (Happ uses it for the
// "Истекает: DATE" display below the progress bar). "" without an active
// subscription; a failed read is an error (the handler answers 503 — Happ would
// otherwise show 0 used / no expiry).
func buildSubscriptionUserinfo(userID string) (string, error) {
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
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("subscription userinfo: %w", err)
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
	cycleUsed, err := biz.PeriodUsage(userID, cycleStart, cycleEnd)
	if err != nil {
		return "", fmt.Errorf("subscription userinfo: %w", err)
	}

	// expire = end of current cycle so happ shows the meaningful date (when
	// quota resets) for monthly-cycle users. If the subscription ends before
	// cycle end (e.g. user's annual plan ends mid-cycle), prefer valid_until.
	expireAt := cycleEnd
	if validUntil.Before(expireAt) {
		expireAt = validUntil
	}

	return fmt.Sprintf("upload=0; download=%d; total=%d; expire=%d",
		cycleUsed, effLimit, expireAt.Unix()), nil
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
		writeAuthError(w, err)
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

	baseURL := subscriptionBaseURL()
	subscribeURL := fmt.Sprintf("%s/api/v1/user/subscribe/%s", baseURL, token)

	// One-tap import into Happ: the plain happ://add/<url> deep link, built here. The
	// cabinet no longer waits for crypto.happ.su (up to 10 s) nor hands the subscription
	// URL — a bearer secret — to a third party (cleanup plan A6).
	happLink := happAddLink(subscribeURL)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscribe_token": token,
		"subscribe_url":   subscribeURL,
		"clash_url":       subscribeURL + "?format=clash",
		"happ_link":       happLink,
	})
}

// handleGetHappLink — public endpoint for ConnectPage to fetch the Happ
// import deeplink by subscribe token (no auth needed; token IS the bearer).
//
// GET /api/v1/user/happ-link/<token>
// → 200 OK {"happ_link": "happ://add/<subscribe url>"}
// → 404 if token unknown
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

	baseURL := subscriptionBaseURL()
	subscribeURL := fmt.Sprintf("%s/api/v1/user/subscribe/%s", baseURL, token)

	_ = userID
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"happ_link":     happAddLink(subscribeURL),
		"subscribe_url": subscribeURL,
	})
}

// happAddLink is Happ's import deep link for a subscription URL.
func happAddLink(subscribeURL string) string { return "happ://add/" + subscribeURL }

// buildSubscriptionAnnounce builds the dynamic Announce + Announce-Url
// headers shown in Happ as a single clickable line below the progress bar.
//
// Priority order:
//  1. Expired                  → "⚠️ Подписка истекла. Продлите →"   + /payment
//     1a. auto-renew from the wallet in ≤ 3 days and the wallet is short
//     → "💳 Пополните кошелёк: подписка закончится DD.MM"            + /wallet
//     (a covered renewal → "✅ Продлим с кошелька DD.MM"             + /wallet)
//  2. ≤ 1 day to expiry        → "⏰ Подписка истекает завтра!"        + /payment
//  3. ≤ 7 days, free-trial     → "⌛ Пробная подписка, осталось Nд"  + /payment
//  4. ≤ 7 days, paid           → "⌛ Истекает через Nд"               + /payment
//  5. traffic_used ≥ 80%       → "📊 Использовано N% трафика"         + /dashboard
//     5a. a news post for Happ  → "📣 <заголовок>"                   + /news (happ_days)
//  6. healthy (default)        → "Личный кабинет ↗"                   + /dashboard
//
// The dashboard / checkout URL roots are taken from publicBaseURL().
// Returns empty strings if the user has no subscription record at all
// (handler should not emit headers in that case); a failed read is an error.
func buildSubscriptionAnnounce(userID string) (text, url string, err error) {
	return buildSubscriptionAnnounceAt(userID, publicBaseURL())
}

// buildSubscriptionAnnounceAt is buildSubscriptionAnnounce with the links on base.
func buildSubscriptionAnnounceAt(userID, base string) (text, url string, err error) {
	var (
		validUntil   time.Time
		trafficUsed  int64
		trafficLimit *int64
		planSlug     sql.NullString
		autoRenew    bool
		planPrice    int
		pricePaid    int
		promo        string
		userDBID     int
	)
	err = db.DB.QueryRow(`
		SELECT us.valid_until,
		       COALESCE(us.traffic_used, 0),
		       us.traffic_limit,
		       COALESCE(p.slug, ''),
		       COALESCE(us.auto_renew, FALSE), COALESCE(p.price_rub, 0), COALESCE(us.price_paid, 0),
		       COALESCE(us.promocode_used, ''), u.id
		  FROM user_subscriptions us
		  JOIN users u  ON u.id = us.user_id
		  LEFT JOIN plans p ON p.id = us.plan_id
		 WHERE u.user_id = $1
		 ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&validUntil, &trafficUsed, &trafficLimit, &planSlug, &autoRenew, &planPrice, &pricePaid, &promo, &userDBID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("subscription announce: %w", err)
	}

	checkout := base + "/payment"
	dashboard := base + "/dashboard"
	now := time.Now()

	// 1. expired
	if validUntil.Before(now) {
		return "⚠️ Подписка истекла. Продлите →", checkout, nil
	}

	daysLeft := int(validUntil.Sub(now).Hours() / 24)
	isTrial := planSlug.Valid && planSlug.String == "free-trial"

	// 1a. Renewal from the wallet (W4/W7): the client must top up before the end.
	if autoRenew && planPrice > 0 && validUntil.Sub(now) <= 3*24*time.Hour {
		free := false
		if pricePaid == 0 {
			if free, err = biz.IsFreeAccessPromocode(context.Background(), db.DB, promo); err != nil {
				return "", "", fmt.Errorf("subscription announce: %w", err)
			}
		}
		if !free {
			balance, err := biz.WalletBalance(context.Background(), userDBID)
			if err != nil {
				return "", "", fmt.Errorf("subscription announce: %w", err)
			}
			end := validUntil.In(moscowTZ).Format("02.01")
			if balance < int64(planPrice)*100 {
				return "💳 Пополните кошелёк: подписка закончится " + end, base + "/wallet", nil
			}
			return "✅ Продлим с кошелька " + end, base + "/wallet", nil
		}
	}

	// 2. ≤ 1 day
	if daysLeft <= 1 {
		return "⏰ Подписка истекает завтра!", checkout, nil
	}

	// 3 / 4. ≤ 7 days
	if daysLeft <= 7 {
		if isTrial {
			return fmt.Sprintf("⌛ Пробная подписка, осталось %dд", daysLeft), checkout, nil
		}
		return fmt.Sprintf("⌛ Истекает через %dд", daysLeft), checkout, nil
	}

	// 5. traffic ≥ 80%
	if trafficLimit != nil && *trafficLimit > 0 {
		pct := int(float64(trafficUsed) * 100.0 / float64(*trafficLimit))
		if pct >= 80 {
			return fmt.Sprintf("📊 Использовано %d%% трафика", pct), dashboard, nil
		}
	}

	// 5a. an active news post for Happ (personal lines above always win)
	if text := happNewsAnnounce(); text != "" {
		return text, base + "/news", nil
	}

	// 6. healthy default — always include link to cabinet (per spec)
	return "Личный кабинет ↗", dashboard, nil
}

// writeAuthError answers a failed extractUserFromToken: 503 when only the session
// lookup failed (a database outage must not log the client out — the site drops its
// token on 401), 401 otherwise. Messages are Russian; the site tells them apart by the
// status, not by the text.
func writeAuthError(w http.ResponseWriter, err error) {
	if dbErr, ok := sessionLookupFailure(err); ok {
		log.Printf("[auth] session lookup failed: %v", dbErr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Сервис временно недоступен, попробуйте через минуту"})
		return
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Сессия истекла — войдите снова"})
}

// moscowTZ is the zone dates in Happ announces are shown in.
var moscowTZ = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Moscow"); err == nil {
		return l
	}
	return time.FixedZone("MSK", 3*3600)
}()
