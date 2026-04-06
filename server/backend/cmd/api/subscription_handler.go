package main

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"

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
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("invalid token"))
		return
	}

	// Проверяем доступ
	access, err := biz.GetUserAccessState(userID, "")
	if err != nil || !access.CanConnect {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("no active subscription"))
		return
	}

	// Получаем connection options (per-user)
	options := buildHubConnectionOptions(userID)
	if len(options) == 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("no connections available"))
		return
	}

	format := r.URL.Query().Get("format")

	switch format {
	case "clash":
		clashConfig := buildClashConfig(userID)
		w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=heyearth.yaml")
		w.Header().Set("Subscription-Userinfo", buildSubscriptionUserinfo(userID))
		w.Write([]byte(clashConfig))

	default:
		// V2Ray/SingBox формат: base64 encoded list of URIs
		var uris []string
		for _, opt := range options {
			if opt.Uri != "" {
				uris = append(uris, opt.Uri)
			}
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(strings.Join(uris, "\n")))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Subscription-Userinfo", buildSubscriptionUserinfo(userID))
		w.Write([]byte(encoded))
	}
}

// buildClashConfig генерирует Clash/ClashMeta YAML конфиг.
func buildClashConfig(userID string) string {
	host := biz.GetSettingOrDefault("main_server_host", "hiearth.ru")
	vlessUUID, err := biz.GetUserVlessUUID(userID)
	if err != nil {
		log.Printf("[subscribe] vless uuid error: %v", err)
		return "# error: no vless uuid"
	}

	var proxies []string
	var proxyNames []string

	// 1. VLESS Reality (Clash.Meta / mihomo)
	realityPubKey := biz.GetSettingOrDefault("reality_public_key", "")
	realityShortID := biz.GetSettingOrDefault("reality_short_id", "")
	realitySNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
	if realityPubKey != "" && vlessUUID != "" {
		name := "HeYearth-VLESS"
		proxyNames = append(proxyNames, name)
		proxies = append(proxies, fmt.Sprintf(
			`  - name: "%s"
    type: vless
    server: %s
    port: 443
    uuid: "%s"
    flow: xtls-rprx-vision
    network: tcp
    tls: true
    reality-opts:
      public-key: "%s"
      short-id: "%s"
    client-fingerprint: chrome
    servername: %s`, name, host, vlessUUID, realityPubKey, realityShortID, realitySNI))
	}

	// 2. SOCKS
	if vlessUUID != "" {
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
	if vlessUUID != "" {
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
`, strings.Join(proxies, "\n"), namesList)
}

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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscribe_token": token,
		"subscribe_url":   subscribeURL,
		"clash_url":       subscribeURL + "?format=clash",
	})
}
