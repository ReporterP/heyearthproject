package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Xray Config Sync (Hub-and-Spoke) ───────────────────────
//
// Hub-and-Spoke архитектура:
//   - Hub (главный сервер) принимает подключения пользователей
//   - Spoke (exit-ноды) подключены к Hub через зашифрованные туннели
//   - Hub маршрутизирует трафик пользователей через здоровые exit-ноды
//   - Балансировка нагрузки: leastPing по всем доступным exit-нодам
//   - Если exit-нод нет — пользовательский трафик блокируется (кроме явных RU-direct правил)
//
// Безопасность:
//   - Per-user UUID для VLESS, per-user login/pass для SOCKS5/HTTP
//   - Блокировка приватных IP (geoip:private) — межсегментная изоляция
//   - Блокировка BitTorrent протокола
//   - Все политики фильтрации применяются только на Hub
//   - Spoke-ноды не имеют прямого доступа друг к другу (hair-pinning через Hub)

const (
	xrayConfigPath = "/opt/heyearth/xray/config.json"
	xrayKeysPath   = "/opt/heyearth/state/reality-keys.json"
)

var (
	xraySyncMu sync.Mutex

	// Debounce: timer-based coalescing of sync requests.
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
)

// xrayRealityKeys — ключи Reality из /opt/heyearth/state/reality-keys.json
type xrayRealityKeys struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	ShortID    string `json:"shortId"`
	UUID       string `json:"uuid"`
	SSPassword string `json:"ssPassword"`
}

// loadRealityKeys загружает Reality-ключи с диска
func loadRealityKeys() (*xrayRealityKeys, error) {
	data, err := os.ReadFile(xrayKeysPath)
	if err != nil {
		return nil, fmt.Errorf("read reality keys: %w", err)
	}
	var keys xrayRealityKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("parse reality keys: %w", err)
	}
	return &keys, nil
}

// ─────────────────────── Config Generation ───────────────────────

// SyncXrayConfig генерирует Xray конфиг со всеми активными пользователями,
// exit-нодами и политиками безопасности, и перезапускает Xray.
// RequestXraySync запрашивает синхронизацию с debounce (2 секунды тишины).
// Множественные вызовы за 2 сек сливаются в один SyncXrayConfig.
func RequestXraySync() {
	debounceMu.Lock()
	defer debounceMu.Unlock()
	if debounceTimer != nil {
		debounceTimer.Stop()
	}
	debounceTimer = time.AfterFunc(2*time.Second, func() {
		runXraySync("debounced sync error")
	})
}

// xraySyncAlertKey — ключ алерта «конфиг xray не применяется».
const xraySyncAlertKey = "xray-sync-failed"

// xraySyncAlertDetailsMax limits the error text in the alert: `xray run -test`
// output can be long and a Telegram message is capped at 4096 characters.
const xraySyncAlertDetailsMax = 800

// runXraySync is the single entry point of all background syncs (startup,
// periodic, debounced): it runs SyncXrayConfig, logs a failure and keeps the
// "xray-sync-failed" alert in step with the result.
func runXraySync(label string) {
	err := SyncXrayConfig()
	if err != nil {
		log.Printf("[xray-sync] %s: %v", label, err)
	}
	reportXraySync(alerts, err)
}

// reportXraySync raises a critical alert on any SyncXrayConfig error (leak
// guard, `xray run -test`, restart failure, DB errors) — until it is fixed the
// running config silently stays stale. A nil result, including "config
// unchanged" and "xray not installed", resolves the alert; resolving a key that
// was never raised is a no-op.
func reportXraySync(m *alertManager, err error) {
	if err == nil {
		m.Resolve(xraySyncAlertKey, "Конфиг xray снова применяется")
		return
	}
	details := []rune(err.Error())
	if len(details) > xraySyncAlertDetailsMax {
		details = append(details[:xraySyncAlertDetailsMax], '…')
	}
	m.Raise(xraySyncAlertKey, AlertCritical, "Конфиг xray не применяется", string(details))
}

// SyncXrayConfig немедленно синхронизирует конфиг (для startup/periodic/forced).
func SyncXrayConfig() error {
	xraySyncMu.Lock()
	defer xraySyncMu.Unlock()

	// Проверяем что Xray установлен
	if _, err := os.Stat(xrayConfigPath); os.IsNotExist(err) {
		return nil // Xray не установлен — пропускаем
	}

	keys, err := loadRealityKeys()
	if err != nil {
		return fmt.Errorf("load reality keys: %w", err)
	}

	// ── Per-user клиенты с premium-уровнями ──
	activeClients, err := biz.ListActiveVlessClients()
	if err != nil {
		return fmt.Errorf("list active clients: %w", err)
	}

	// Per-user quota cutoff: load the set ONCE per sync. Users over their
	// current billing cycle quota are excluded from VLESS clients arrays and
	// per-user routing rules. The set re-computes on every sync (debounced
	// 2s), so cycle-resets pick up automatically without a flag.
	quotaExceeded, qerr := biz.ListQuotaExceededUUIDs()
	if qerr != nil {
		log.Printf("[xray-sync] WARN failed to load quota-exceeded set: %v — continuing without cutoff", qerr)
		quotaExceeded = map[string]struct{}{}
	}

	// Серверный UUID — для обратной совместимости и админ-доступа
	serverUUID := keys.UUID
	if serverUUID != "" {
		found := false
		for _, c := range activeClients {
			if c.UUID == serverUUID {
				found = true
				break
			}
		}
		if !found {
			activeClients = append([]biz.ActiveVlessClient{{UUID: serverUUID, UserID: "server@heyearth", IsPremium: true}}, activeClients...)
		}
	}

	// Маппинг UUID → user_id для email (Xray Stats API)
	uuidToUserID := make(map[string]string)
	for _, c := range activeClients {
		uuidToUserID[c.UUID] = c.UserID
	}

	// Persist Reality public key info to app_settings for client consumption
	_ = biz.SetSetting("reality_public_key", keys.PublicKey)
	_ = biz.SetSetting("reality_short_id", keys.ShortID)

	// Policy levels: 0 = standard, 1 = premium
	vlessClients := make([]map[string]interface{}, 0, len(activeClients))
	skippedQuota := 0
	for _, c := range activeClients {
		if _, blocked := quotaExceeded[c.UUID]; blocked {
			skippedQuota++
			continue // over cycle quota — re-included when cycle resets
		}
		email := c.UserID
		if email == "" {
			email = "server@heyearth"
		}
		level := 0
		if c.IsPremium {
			level = 1
		}
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":    c.UUID,
			"flow":  "xtls-rprx-vision",
			"email": email,
			"level": level,
		})
	}
	if skippedQuota > 0 {
		log.Printf("[xray-sync] quota cutoff: %d user(s) excluded from VLESS clients this sync", skippedQuota)
	}

	// (activeClients уже использованы для заполнения vlessClients и uuidToUserID)
	if len(vlessClients) == 0 && serverUUID != "" {
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":    serverUUID,
			"flow":  "xtls-rprx-vision",
			"email": "server@heyearth",
		})
	}

	// vlessClientsXHTTP mirrors vlessClients minus the `flow` field. XTLS Vision
	// (`xtls-rprx-vision`) only works with `network=tcp`; on an xhttp/splithttp
	// transport the Xray server rejects the handshake when clients have flow
	// set. Until now both inbounds shared `vlessClients` — the xhttp inbound
	// has been accepting connections but failing them right after Reality
	// handshake (visible to happ as "н/д" / no latency).
	vlessClientsXHTTP := make([]map[string]interface{}, 0, len(vlessClients))
	for _, c := range vlessClients {
		cc := make(map[string]interface{}, len(c))
		for k, v := range c {
			if k == "flow" {
				continue
			}
			cc[k] = v
		}
		vlessClientsXHTTP = append(vlessClientsXHTTP, cc)
	}

	// ── Reality SNI (настраивается через app_settings, по умолчанию — apple.com) ──
	// apple.com — оптимальный SNI для обхода DPI в России (массовый трафик, нельзя заблокировать)
	realitySNI := "apple.com"
	if v, err := biz.GetSetting("reality_sni_domain"); err == nil && v != "" {
		realitySNI = v
	}

	// ── Inbounds ──
	inbounds := []map[string]interface{}{
		{
			"tag": "vless-reality", "listen": "127.0.0.1", "port": 10443, "protocol": "vless",
			"settings": map[string]interface{}{"clients": vlessClients, "decryption": "none"},
			"streamSettings": map[string]interface{}{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]interface{}{
					"show": false, "dest": realitySNI + ":443", "xver": 0,
					"serverNames":   []string{realitySNI, "www." + realitySNI},
					"privateKey":    keys.PrivateKey,
					"shortIds":      []string{keys.ShortID, ""},
					"xPaddingBytes": "100-1000", // Anti-DPI: normalize packet sizes
				},
			},
			"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
		{
			// VLESS Reality + XHTTP (splithttp) — anti-behavioral-DPI transport
			// XHTTP splits upload/download into separate HTTP transactions,
			// making traffic statistically indistinguishable from normal web browsing.
			// Port 2443 direct (no nginx stream needed).
			"tag": "vless-xhttp", "listen": "0.0.0.0", "port": 2443, "protocol": "vless",
			"settings": map[string]interface{}{"clients": vlessClientsXHTTP, "decryption": "none"},
			"streamSettings": map[string]interface{}{
				"network": "xhttp", "security": "reality",
				"xhttpSettings": map[string]interface{}{
					// path MUST match what clients send in their URI's `path=` param.
					// `/` is the minimal-friction default — looks like a generic web
					// site root request, no path-specific filtering hits.
					"path": "/",
					// host is the SNI that the xhttp transport uses for the HTTP
					// Host header. MUST be one of realitySettings.serverNames OR
					// listed here explicitly; without it Xray validates against
					// realitySettings.serverNames (yandex.ru) but happ sends
					// Host: hiearth.ru → silent handshake failure (Reality OK,
					// xhttp layer rejects). This was the «н/д» bug.
					"host":          biz.GetSettingOrDefault("main_server_host", "hiearth.ru"),
					"mode":          "auto",
					"xPaddingBytes": "100-1000",
					// R4: cap each POST chunk under 15 KB so a single TCP doesn't
					// accumulate enough data to trigger the RU "freezing" attack
					// on foreign-datacenter IPs (~15-20 KB threshold observed).
					"scMaxEachPostBytes": "10000-15000",
					// R4: spread response across many buffered POSTs so the
					// "freezing" attack can't latch onto a single long-lived TCP.
					// NB: Xray 26.x types this as int64 (NOT a "min-max" string range
					// like scMaxEachPostBytes). Verified via Xray source code +
					// runtime crash: `cannot unmarshal string into Go struct field
					// SplitHTTPConfig.scMaxBufferedPosts of type int64`. Default 30.
					// 40 chosen as conservative upper bound to absorb burst response
					// without hitting connection-drop threshold.
					"scMaxBufferedPosts": 40,
				},
				"realitySettings": map[string]interface{}{
					"show": false, "dest": realitySNI + ":443", "xver": 0,
					"serverNames":   []string{realitySNI, "www." + realitySNI},
					"privateKey":    keys.PrivateKey,
					"shortIds":      []string{keys.ShortID, ""},
					"xPaddingBytes": "100-1000",
				},
				// T1.2: bilateral TCP keepalive — same rationale as vless-reality
				// inbound above. XHTTP is HTTP/1.1 long-poll over TCP, so the
				// underlying TCP socket is just as vulnerable to mobile CGNAT
				// idle reap as plain Reality TCP.
				"sockopt": map[string]interface{}{
					"tcpKeepAliveInterval": 30,
					"tcpKeepAliveIdle":     120,
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
				},
			},
			"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
		{
			"tag": "shadowsocks", "listen": "0.0.0.0", "port": 8388, "protocol": "shadowsocks",
			"settings": map[string]interface{}{"method": "2022-blake3-aes-128-gcm", "password": keys.SSPassword, "network": "tcp,udp"},
			"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
	}

	// ☁️ vless-xhttp-cdn — VLESS over XHTTP inbound on loopback :11443 for
	// CDN-fronted path. Cloudflare terminates user-side TLS, our nginx vhost
	// for cdn.hiearth.ru proxies plain HTTP to here. No security layer at
	// xray level — TLS already terminated by CF.
	//
	// XHTTP chosen over WebSocket because xray 26.x deprecates WS ("Please
	// migrate to XHTTP H2 & H3"). XHTTP also splits up/down streams (packet-up
	// mode) which plays better with CF's HTTP/2 multiplexing quirks.
	//
	// Toggle-gated via app_settings.cdn_enabled — inbound only generated when
	// feature is enabled. Re-uses vlessClientsXHTTP (same per-user UUIDs, no
	// flow because XHTTP is incompatible with XTLS Vision).
	if biz.GetSettingOrDefault("cdn_enabled", "false") == "true" {
		cdnXHPath := biz.GetSettingOrDefault("cdn_xh_path", "/cdn-xh")
		cdnHost := biz.GetSettingOrDefault("cdn_host", "cdn.hiearth.ru")
		inbounds = append(inbounds, map[string]interface{}{
			"tag":      "vless-xhttp-cdn",
			"listen":   "127.0.0.1",
			"port":     11443,
			"protocol": "vless",
			"settings": map[string]interface{}{
				"clients":    vlessClientsXHTTP,
				"decryption": "none",
			},
			"streamSettings": map[string]interface{}{
				"network": "xhttp",
				"xhttpSettings": map[string]interface{}{
					"path": cdnXHPath,
					"host": cdnHost,
					"mode": "packet-up", // CDN-friendly: separate up/down HTTP requests
				},
			},
			"sniffing": map[string]interface{}{
				"enabled":      true,
				"destOverride": []string{"http", "tls", "quic"},
			},
		})
	}

	// ── Xray Stats API ──
	// dokodemo-door inbound для gRPC API доступа к статистике
	inbounds = append(inbounds, map[string]interface{}{
		"tag": "api-in", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door",
		"settings": map[string]interface{}{"address": "127.0.0.1"},
	})

	// SOCKS5 inbound для AmneziaWG (Palma) egress. AWG's tun2socks
	// (hev-socks5-tunnel) dials this on loopback; traffic then flows through
	// the SAME global routing rules as Reality (inboundTag: null) → RU domains
	// + RU geoip go to direct-freedom (Hub Moscow), everything else to the
	// exit-balancer (Amsterdam). sniffing recovers the domain from TLS SNI /
	// HTTP Host so geosite (domain) rules fire, matching Happ's split.
	// Loopback-only (127.0.0.1) — never exposed to the network.
	inbounds = append(inbounds, map[string]interface{}{
		"tag":      "socks-awg",
		"listen":   "127.0.0.1",
		"port":     10808,
		"protocol": "socks",
		"settings": map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
			"ip":   "127.0.0.1",
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
			"routeOnly":    true,
		},
	})

	// Every inbound except api-in carries user traffic; the no-exit block rule
	// must cover all of them (C2: shadowsocks, socks-awg and CDN used to leak).
	userInbounds := userInboundTags(inbounds)

	// ── Outbounds ──
	// outbounds[0] is xray's default handler: unmatched traffic and balancer
	// failures without fallbackTag land there. It MUST be block (C2).
	outbounds := []map[string]interface{}{
		newBlockOutbound(),
		{
			// Hub-local egress for xray's own DNS client only (dnsDirectRule).
			"protocol": "freedom", "tag": xrayDirectTag,
			"settings": map[string]interface{}{"domainStrategy": "UseIP"},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen": true,
					"tcpNoDelay":  true,
				},
			},
		},
	}

	// ── Exit-ноды (Spoke outbounds) ──
	exitNodes, err := biz.ListHealthyExitNodes()
	if err != nil {
		log.Printf("[xray-sync] Ошибка загрузки exit-нод: %v", err)
		exitNodes = nil
	}

	var exitTags []string
	for _, node := range exitNodes {
		outbound := buildExitNodeOutbound(node)
		if outbound != nil {
			outbounds = append(outbounds, outbound)
			tag := fmt.Sprintf("exit-%d", node.ID)
			// Weighted: duplicate tag by load_weight for proportional distribution.
			w := node.LoadWeight
			if w < 1 {
				w = 1
			}
			for i := 0; i < w; i++ {
				exitTags = append(exitTags, tag)
			}
		}
	}

	// ── Traffic Masking Rules (premium only) ──
	maskingRules, err := biz.ListActiveTrafficMaskingRules()
	if err != nil {
		log.Printf("[xray-sync] Ошибка загрузки правил маскировки: %v", err)
		maskingRules = nil
	}

	// Для каждого правила маскировки создаём freedom outbound с нужным SNI
	type maskRouteEntry struct {
		sourceDomain string
		outboundTag  string
	}
	var maskRouteEntries []maskRouteEntry
	for _, rule := range maskingRules {
		tag := fmt.Sprintf("mask-%d", rule.ID)
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "freedom",
			"settings": map[string]interface{}{
				"domainStrategy": "UseIP",
			},
		}
		outbounds = append(outbounds, outbound)
		maskRouteEntries = append(maskRouteEntries, maskRouteEntry{
			sourceDomain: rule.SourceDomain,
			outboundTag:  tag,
		})
	}

	// ── Direct-routing outbound ──
	// Unconditional: cheap, idempotent, avoids "tag not found" errors at Xray
	// startup if rule construction below emits outboundTag before outbound is
	// registered. UseIP so Xray resolves FQDN locally on the Hub (RU IP).
	outbounds = append(outbounds, map[string]interface{}{
		"tag":      "direct-freedom",
		"protocol": "freedom",
		"settings": map[string]interface{}{"domainStrategy": "UseIP"},
	})

	// ── Routing (политики безопасности) ──
	rules := []map[string]interface{}{
		// 0. Встроенный DNS xray → с Hub. Явно: default outbound теперь block.
		dnsDirectRule(),
		// 1. Блокировка приватных IP — inter-spoke изоляция
		// Spoke-ноды не могут обращаться к другим spoke или к внутренней сети Hub
		{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
		// 2. Блокировка BitTorrent
		{"type": "field", "protocol": []string{"bittorrent"}, "outboundTag": "block"},
		// 3. Блокировка XMPP (port 5222/5223) — китайские стоковые ROM (OPPO/Realme/Vivo)
		//    бесконечно retry'ят отключённый Google Talk endpoint. Generates
		//    SOCKS5 i/o timeout backlog в Hysteria2 client → замедляет всех юзеров.
		//    Подтверждено: 26 из 28 SOCKS5 ошибок на www.google.com:5222.
		{"type": "field", "port": "5222,5223", "outboundTag": "block"},
		// 4. DNS-over-HTTPS: НЕ блокируем, пусть идёт через exit-ноды
		// Chrome/Edge используют DoH (chrome.cloudflare-dns.com), блокировка ломает DNS резолвинг
	}

	// ── Per-plan direct-routing rules ──
	// Для каждого плана с прикреплёнными direct-сервисами добавляем правила
	// (user → direct-freedom) с фильтром по domain И/ИЛИ ip. Вставляются ДО
	// per-plan balancer rules и ДО no-exit blackhole — поэтому при падении
	// всех exit-нод пользователи с direct-сервисами продолжают достукиваться.
	directPlanIDs, drErr := biz.GetPlanIDsWithDirectRoutes()
	if drErr != nil {
		log.Printf("[xray-sync] GetPlanIDsWithDirectRoutes error: %v (skipping direct rules)", drErr)
		directPlanIDs = nil
	}
	// Accumulate the UNION of all RU direct-route domains/IPs across plans for
	// the socks-awg inbound (AmneziaWG). The per-plan rules below are user-bound
	// (VLESS UUID); socks-awg has no user, so it gets its own inbound-bound split
	// rules built from the same lists (added after the loop).
	awgDirectDomainsSet := map[string]struct{}{}
	awgDirectIPsSet := map[string]struct{}{}
	for _, planID := range directPlanIDs {
		services, sErr := biz.GetDirectRouteServicesForPlan(planID)
		if sErr != nil {
			log.Printf("[xray-sync] GetDirectRouteServicesForPlan(plan=%d) error: %v (skipping)", planID, sErr)
			continue
		}
		var domains []string
		var ips []string
		for _, svc := range services {
			if !svc.IsActive {
				continue
			}
			for _, d := range svc.Domains {
				if d != "" {
					domains = append(domains, "domain:"+d)
				}
			}
			ips = append(ips, svc.IP4...)
			ips = append(ips, svc.IP6...)
			ips = append(ips, svc.CIDR4...)
			ips = append(ips, svc.CIDR6...)
		}
		// Feed the inbound-bound socks-awg split (union across all plans, deduped).
		for _, d := range domains {
			awgDirectDomainsSet[d] = struct{}{}
		}
		for _, ip := range ips {
			awgDirectIPsSet[ip] = struct{}{}
		}
		var userIDs []string
		for _, client := range activeClients {
			if client.PlanID == planID && client.UserID != "" {
				userIDs = append(userIDs, client.UserID)
			}
		}
		if len(userIDs) == 0 {
			continue
		}
		if len(domains) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"user":        userIDs,
				"domain":      domains,
				"outboundTag": "direct-freedom",
			})
		}
		if len(ips) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"user":        userIDs,
				"ip":          ips,
				"outboundTag": "direct-freedom",
			})
		}
	}

	// ── socks-awg (AmneziaWG / Palma) split-routing ──
	// The per-plan rules above are user-bound (VLESS UUID); the socks-awg SOCKS5
	// inbound (fed by tun2socks) has no user, so it would fall straight to the
	// catch-all exit-balancer (Amsterdam) — ALL traffic incl. RU. To match Happ's
	// split we emit inbound-bound rules from the SAME union of RU direct domains/
	// IPs: RU → direct-freedom (Hub Moscow), everything else falls through to the
	// catch-all exit-balancer rule below. Sorted for deterministic config output.
	if len(awgDirectDomainsSet) > 0 {
		awgDomains := make([]string, 0, len(awgDirectDomainsSet))
		for d := range awgDirectDomainsSet {
			awgDomains = append(awgDomains, d)
		}
		sort.Strings(awgDomains)
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"inboundTag":  []string{"socks-awg"},
			"domain":      awgDomains,
			"outboundTag": "direct-freedom",
		})
	}
	if len(awgDirectIPsSet) > 0 {
		awgIPs := make([]string, 0, len(awgDirectIPsSet))
		for ip := range awgDirectIPsSet {
			awgIPs = append(awgIPs, ip)
		}
		sort.Strings(awgIPs)
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"inboundTag":  []string{"socks-awg"},
			"ip":          awgIPs,
			"outboundTag": "direct-freedom",
		})
	}

	// Traffic masking routing rules (только для premium пользователей)
	for _, entry := range maskRouteEntries {
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"domain":      []string{"domain:" + entry.sourceDomain},
			"outboundTag": entry.outboundTag,
		})
	}

	routing := map[string]interface{}{
		"domainStrategy": "IPIfNonMatch",
		"rules":          rules,
	}

	// Если есть здоровые exit-ноды — маршрутизируем через них (балансировка)
	if len(exitTags) > 0 {
		balancerStrategy := biz.GetSettingOrDefault("balancer_strategy", "random")

		// Default balancer with all exit nodes
		balancers := []map[string]interface{}{
			newBalancer("exit-balancer", exitTags, balancerStrategy),
		}

		// Per-plan balancers: create a balancer for each unique set of allowed servers
		planBalancers := map[int]string{} // planID → balancer tag
		for _, client := range activeClients {
			if client.PlanID == 0 || len(client.AllowedServerIDs) == 0 {
				continue // no plan or all servers → default balancer
			}
			if _, exists := planBalancers[client.PlanID]; exists {
				continue // already created for this plan
			}
			// Build tag list for this plan's servers
			var planTags []string
			for _, serverID := range client.AllowedServerIDs {
				tag := fmt.Sprintf("exit-%d", serverID)
				// Check if this exit node is actually in our healthy list
				for _, et := range exitTags {
					if et == tag {
						planTags = append(planTags, tag)
						break
					}
				}
			}
			if len(planTags) == 0 {
				continue // no healthy servers for this plan → use default
			}
			balancerTag := fmt.Sprintf("plan-%d-balancer", client.PlanID)
			planBalancers[client.PlanID] = balancerTag
			balancers = append(balancers, newBalancer(balancerTag, planTags, balancerStrategy))
		}
		routing["balancers"] = balancers

		// Per-plan routing rules: match user email → plan-specific balancer
		for _, client := range activeClients {
			if _, blocked := quotaExceeded[client.UUID]; blocked {
				continue // quota cutoff — user already missing from clients arrays
			}
			if balancerTag, ok := planBalancers[client.PlanID]; ok {
				rules = append(rules, map[string]interface{}{
					"type":        "field",
					"user":        []string{client.UserID},
					"balancerTag": balancerTag,
				})
			}
		}

		// Default rule: all remaining traffic → default balancer with all exit nodes
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"network":     "tcp,udp",
			"balancerTag": "exit-balancer",
		})
		routing["rules"] = rules

	} else {
		// Нет здоровых exit-нод: блокируем ВЕСЬ пользовательский трафик на всех
		// пользовательских inbound (включая shadowsocks, socks-awg, CDN).
		// Явные RU-direct правила выше продолжают работать. Исключение — DNS (порт 53):
		// клиенты AWG резолвят через 1.1.1.1/8.8.8.8 внутри туннеля, без ответа не
		// откроются даже RU-direct сайты. Запрос глушит outbound dns-hijack и отвечает
		// встроенным DNS xray (он уходит с Hub по dnsDirectRule); остальное блокируется.
		log.Println("[xray-sync] ВНИМАНИЕ: нет здоровых exit-нод, ВЕСЬ пользовательский трафик блокируется")
		outbounds = append(outbounds, dnsHijackOutbound())
		rules = append(rules, noExitDNSRule(userInbounds), noExitBlockRule(userInbounds))
		routing["rules"] = rules
	}

	// Observatory: мониторинг задержки до exit-нод для leastPing
	var observatory map[string]interface{}
	if len(exitTags) > 0 {
		observatory = map[string]interface{}{
			"subjectSelector": []string{"exit-"},
			"probeInterval":   "30s",
			"probeURL":        "https://www.google.com/generate_204",
		}
	}

	// API routing rule: перенаправляем трафик api-in → api handler
	apiRule := map[string]interface{}{
		"type": "field", "inboundTag": []string{"api-in"}, "outboundTag": "api",
	}
	// Вставляем api rule первым в routing rules
	existingRules := routing["rules"].([]map[string]interface{})
	routing["rules"] = append([]map[string]interface{}{apiRule}, existingRules...)

	// API outbound (handler для StatsService)
	outbounds = append(outbounds, map[string]interface{}{
		"protocol": "freedom", "tag": "api",
	})

	config := map[string]interface{}{
		"log": map[string]string{"loglevel": "warning"},
		// DNS: только IPv4 (IPv6 сломан у многих РФ-провайдеров — убираем задержку на AAAA)
		"dns": map[string]interface{}{
			"servers":       getDNSServers(),
			"queryStrategy": "UseIPv4",
			"disableCache":  false,
			"tag":           xrayDNSTag,
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   routing,
		// Stats API: включаем сбор статистики
		"stats": map[string]interface{}{},
		// API handler для доступа к статистике
		"api": map[string]interface{}{
			"tag": "api",
			"services": []string{
				"StatsService",
			},
		},
		// Policy: учёт трафика + буферы + таймауты
		// Level 0 = standard, Level 1 = premium
		"policy": map[string]interface{}{
			"levels": map[string]interface{}{
				"0": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
					"handshake":         4,    // Таймаут handshake (сек)
					"connIdle":          3600, // Idle timeout (сек) — 1 час, не рвём long-lived (AI streaming, WebSocket, SSE)
					"uplinkOnly":        2,    // Ждать 2 сек после закрытия download перед закрытием upload
					"downlinkOnly":      5,    // Ждать 5 сек после закрытия upload перед закрытием download
					"bufferSize":        256,  // 256KB буфер на соединение (для видеостриминга)
				},
				"1": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
					"handshake":         4,
					"connIdle":          3600, // Premium: тот же 1 час idle, что и standard (раньше было 600)
					"uplinkOnly":        2,
					"downlinkOnly":      5,
					"bufferSize":        512, // Premium: 512KB буфер (стриминг HD видео)
				},
			},
			"system": map[string]interface{}{
				"statsInboundUplink":    true,
				"statsInboundDownlink":  true,
				"statsOutboundUplink":   true,
				"statsOutboundDownlink": true,
			},
		},
	}

	// Observatory для leastPing балансировки exit-нод
	if observatory != nil {
		config["observatory"] = observatory
	}

	// Leak guard: never write a config whose default outbound or balancer
	// fallback could send user traffic out via the Hub's RU IP.
	if err := checkNoDirectLeak(inbounds, outbounds, routing); err != nil {
		return fmt.Errorf("xray config leak guard: %w", err)
	}

	// Сериализуем конфиг
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal xray config: %w", err)
	}

	// Atomic write: tmp file → rename (prevents partial writes on crash/power loss)
	// Xray requires .json extension for config format detection
	tmpPath := strings.TrimSuffix(xrayConfigPath, ".json") + ".tmp.json"
	if err := os.WriteFile(tmpPath, configJSON, 0600); err != nil {
		return fmt.Errorf("write xray config (tmp): %w", err)
	}

	// Validate config before applying
	xrayBin := "/opt/heyearth/xray/xray"
	if p, err := exec.LookPath("xray"); err == nil {
		xrayBin = p
	}
	validateCmd := exec.Command(xrayBin, "run", "-test", "-config", tmpPath)
	if output, err := validateCmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("xray config validation failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	// Skip restart if config hasn't actually changed (avoids dropping active connections)
	if existingConfig, err := os.ReadFile(xrayConfigPath); err == nil {
		if bytes.Equal(existingConfig, configJSON) {
			_ = os.Remove(tmpPath)
			log.Println("[xray-sync] Конфиг не изменился, пропускаем restart")
			return nil
		}
	}

	// Atomic rename
	if err := os.Rename(tmpPath, xrayConfigPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename xray config: %w", err)
	}

	// Restart Xray (Xray-core не поддерживает reload — только restart)
	cmd := exec.Command("systemctl", "restart", "xray")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart xray: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	recordXrayRestart()

	// Notify clients that subscription config has changed.
	biz.BumpSubscriptionVersion()

	log.Printf("[xray-sync] Конфиг обновлён: %d VLESS клиентов, %d exit-нод, %d правил маскировки",
		len(vlessClients), len(exitTags), len(maskRouteEntries))

	return nil
}

// buildExitNodeOutbound создаёт Xray outbound для exit-ноды.
// Поддерживает VLESS Reality, Shadowsocks и прокси-протоколы.
func buildExitNodeOutbound(node biz.ExitNodeTunnel) map[string]interface{} {
	tag := fmt.Sprintf("exit-%d", node.ID)

	switch node.TunnelProtocol {
	case "vless":
		if node.TunnelUUID == "" || len(node.TunnelUUID) < 32 {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — невалидный tunnel_uuid", node.ID, node.Name)
			return nil
		}
		// Валидация: publicKey должен быть base64-строкой длиной ~43 символа (x25519)
		if node.TunnelPubKey != "" && len(node.TunnelPubKey) < 40 {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — невалидный tunnel_public_key %q", node.ID, node.Name, node.TunnelPubKey)
			return nil
		}
		sni := node.TunnelSNI
		if sni == "" {
			sni = node.IPAddress
		}
		// Без flow: "xtls-rprx-vision" — чтобы работал mux (мультиплексирование).
		// Vision хорош для single-stream, но mux критичен для скорости
		// при множестве мелких подключений (Telegram, Instagram, YouTube).
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "vless",
			"settings": map[string]interface{}{
				"vnext": []map[string]interface{}{
					{
						"address": node.IPAddress, "port": node.TunnelPort,
						"users": []map[string]interface{}{
							{"id": node.TunnelUUID, "encryption": "none"},
						},
					},
				},
			},
			"streamSettings": map[string]interface{}{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]interface{}{
					"fingerprint":   getFingerprint(),
					"serverName":    sni,
					"xPaddingBytes": "100-1000", // Normalize packet sizes — anti-DPI behavioral analysis
				},
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
					"tcpKeepAliveInterval": 30,
				},
			},
			// Мультиплексирование: несколько стримов по одному VLESS соединению.
			// Кратно снижает время установки подключения (один TLS handshake на все стримы).
			"mux": map[string]interface{}{
				"enabled":         true,
				"concurrency":     getMuxConcurrency(),
				"xudpConcurrency": 16,      // UDP мультиплексирование (DNS, QUIC)
				"xudpProxyUDP443": "allow", // Разрешаем QUIC/HTTP3 — ускоряет YouTube, Instagram
			},
		}
		// Добавляем publicKey и shortId если заданы
		if node.TunnelPubKey != "" {
			rs := outbound["streamSettings"].(map[string]interface{})["realitySettings"].(map[string]interface{})
			rs["publicKey"] = node.TunnelPubKey
			if node.TunnelShortID != "" {
				rs["shortId"] = node.TunnelShortID // singular — клиентская сторона Reality
			}
		}
		return outbound

	case "shadowsocks":
		if node.TunnelSSPass == "" {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — нет tunnel_ss_password", node.ID, node.Name)
			return nil
		}
		method := node.TunnelSSMethod
		if method == "" {
			method = "2022-blake3-aes-128-gcm"
		}
		// Stream transport: port 443 → WS (legacy, masquerades as HTTPS through nginx);
		// any other port → plain TCP (lower latency, no WS framing overhead).
		// Plain TCP is preferred when the exit listens on a dedicated SS port
		// (e.g. :8388) — saves the WS handshake RTT and per-message framing.
		streamSettings := map[string]interface{}{
			"network": "tcp",
			"sockopt": map[string]interface{}{
				"tcpFastOpen":          true, // Экономия 1 RTT
				"tcpNoDelay":           true, // Без буферизации Nagle
				"tcpKeepAliveInterval": 30,   // Keepalive от DPI
			},
		}
		if node.TunnelPort == 443 {
			streamSettings["network"] = "ws"
			streamSettings["wsSettings"] = map[string]interface{}{"path": "/stream"}
		}
		return map[string]interface{}{
			"tag": tag, "protocol": "shadowsocks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address":  node.IPAddress,
						"port":     node.TunnelPort,
						"method":   method,
						"password": node.TunnelSSPass,
					},
				},
			},
			"streamSettings": streamSettings,
			// NOTE: NO mux — пользователь сообщил bistable behavior ("летает или n/a"
			// при подключении). Mux multiplexes user streams over one TCP connection
			// to exit; if mux session is in flaky state (cold start, reconnect),
			// new user requests may block or fail intermittently while the mux
			// pool stabilizes. Without mux, each user request opens its own TCP
			// to exit — more handshakes (slightly higher latency) but deterministic
			// per-stream success/failure, no shared mux state to coordinate.
			// xudpProxyUDP443=allow (mux-only feature) also removed: YouTube QUIC
			// now falls back to TCP HTTPS through the tunnel — slower in theory
			// but more reliable in practice on flaky mobile networks.
		}

	case "hysteria2":
		// Hysteria2: UDP/QUIC — нет head-of-line blocking, стабильный пинг.
		// Hysteria2 клиент бежит отдельным сервисом (hysteria2-client.service)
		// и предоставляет локальный SOCKS5 на 127.0.0.1:1090.
		// Xray просто маршрутизирует через этот SOCKS5.
		hy2Port := 1088 + node.ID // ID=2 → 1090, ID=3 → 1091, etc
		return map[string]interface{}{
			"tag": tag, "protocol": "socks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address": "127.0.0.1",
						"port":    hy2Port,
					},
				},
			},
		}

	case "socks5":
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "socks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address": node.IPAddress,
						"port":    node.TunnelPort,
					},
				},
			},
			"mux": map[string]interface{}{"enabled": true, "concurrency": getMuxConcurrency()},
		}
		if node.TunnelUUID != "" {
			servers := outbound["settings"].(map[string]interface{})["servers"].([]map[string]interface{})
			servers[0]["users"] = []map[string]interface{}{
				{"user": "heyearth", "pass": node.TunnelUUID},
			}
		}
		return outbound

	case "http":
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "http",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address": node.IPAddress,
						"port":    node.TunnelPort,
					},
				},
			},
			"mux": map[string]interface{}{"enabled": true, "concurrency": getMuxConcurrency()},
		}
		if node.TunnelUUID != "" {
			servers := outbound["settings"].(map[string]interface{})["servers"].([]map[string]interface{})
			servers[0]["users"] = []map[string]interface{}{
				{"user": "heyearth", "pass": node.TunnelUUID},
			}
		}
		return outbound

	default:
		log.Printf("[xray-sync] exit-нода %d (%s): неизвестный протокол %q", node.ID, node.Name, node.TunnelProtocol)
		return nil
	}
}

// ─────────────────────── Tunnel Health Monitoring ───────────────────────
//
// Фоновый мониторинг состояния туннелей до exit-нод.
// Каждые 30 секунд проверяет TCP-подключение к каждой exit-ноде.
// Hysteresis: нужно 3 consecutive неудачи → unhealthy, 2 consecutive успеха → healthy.
// При изменении статуса — пересинхронизирует Xray конфиг.

const (
	hysteresisUnhealthy = 3 // consecutive failures before marking unhealthy
	hysteresisHealthy   = 2 // consecutive successes before marking healthy
)

// healthCounter tracks consecutive check results per node for hysteresis.
var healthCounter sync.Map // map[int]*nodeHealthCounter

type nodeHealthCounter struct {
	consecutiveOK   int
	consecutiveFail int
}

// mtprotoFailCount / mtprotoSuccessCount track consecutive MTProto probe
// results per server ID for 3-fail / 2-success hysteresis.
var (
	mtprotoFailCount    = map[int]int{}
	mtprotoSuccessCount = map[int]int{}
	mtprotoCountMu      sync.Mutex
)

// StartTunnelHealthMonitor запускает фоновый мониторинг exit-нод.
func StartTunnelHealthMonitor() {
	go func() {
		// Первая проверка через 10 секунд после старта
		time.Sleep(10 * time.Second)
		checkAllTunnels()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			checkAllTunnels()
		}
	}()
	log.Printf("[tunnel-monitor] Фоновый мониторинг exit-нод запущен (каждые 30 сек, hysteresis %d/%d)",
		hysteresisUnhealthy, hysteresisHealthy)
}

func getHealthCounter(nodeID int) *nodeHealthCounter {
	val, _ := healthCounter.LoadOrStore(nodeID, &nodeHealthCounter{})
	return val.(*nodeHealthCounter)
}

// checkAllTunnels проверяет все exit-ноды и обновляет их статус.
func checkAllTunnels() {
	nodes, err := biz.ListAllExitNodes()
	if err != nil {
		log.Printf("[tunnel-monitor] Ошибка загрузки exit-нод: %v", err)
		return
	}

	if len(nodes) == 0 {
		return
	}

	statusChanged := false
	for _, node := range nodes {
		probeResult, latencyMs, probeError := checkTunnelHealth(node)

		// Persist per-probe RTT for adaptive Express-tunnel exit selection
		// (biz.DecideExpressTunnel). We write last_rtt_ms/last_rtt_at on EVERY
		// successful probe — independently of hysteresis-driven status flips —
		// so the lowest-RTT exit selection always sees fresh data. Failures
		// are intentionally ignored so transient blips don't poison selection.
		if probeResult == "healthy" && latencyMs > 0 {
			if rttErr := biz.UpdateExitRTT(node.ID, latencyMs); rttErr != nil {
				log.Printf("[tunnel-monitor] UpdateExitRTT(%d) failed: %v", node.ID, rttErr)
			}
		}

		oldStatus := node.TunnelStatus
		hc := getHealthCounter(node.ID)

		// Update hysteresis counters.
		if probeResult == "healthy" {
			hc.consecutiveOK++
			hc.consecutiveFail = 0
		} else {
			hc.consecutiveFail++
			hc.consecutiveOK = 0
		}

		// Determine effective status with hysteresis.
		var newStatus string
		switch oldStatus {
		case "healthy":
			if hc.consecutiveFail >= hysteresisUnhealthy {
				newStatus = "unhealthy"
			} else {
				newStatus = "healthy"
			}
		case "unhealthy":
			if hc.consecutiveOK >= hysteresisHealthy {
				newStatus = "healthy"
			} else {
				newStatus = "unhealthy"
			}
		default: // "unknown" or empty — use probe result directly
			newStatus = probeResult
		}

		if newStatus != oldStatus {
			if err := biz.UpdateTunnelStatusWithError(node.ID, newStatus, latencyMs, probeError); err != nil {
				log.Printf("[tunnel-monitor] Ошибка обновления статуса ноды %d: %v", node.ID, err)
				continue
			}
			// Reset counters after status flip.
			hc.consecutiveOK = 0
			hc.consecutiveFail = 0

			statusChanged = true

			if newStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] ALERT: Exit-нода %d (%s, %s) — туннель НЕДОСТУПЕН (после %d проверок)",
					node.ID, node.Name, node.IPAddress, hysteresisUnhealthy)
				biz.RecordFailedConnection(node.ID)
			} else if newStatus == "healthy" && oldStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] OK: Exit-нода %d (%s, %s) — туннель ВОССТАНОВЛЕН",
					node.ID, node.Name, node.IPAddress)
				biz.RecordSuccessfulConnection(node.ID)
			} else {
				log.Printf("[tunnel-monitor] Exit-нода %d (%s): %s -> %s",
					node.ID, node.Name, oldStatus, newStatus)
			}
		} else {
			// Обновляем last_tunnel_check при КАЖДОЙ проверке, независимо от статуса
			_ = biz.UpdateTunnelStatusWithError(node.ID, newStatus, latencyMs, probeError)
		}
	}

	// Если статус какой-то ноды изменился — пересинхронизируем Xray (debounced)
	if statusChanged {
		log.Printf("[tunnel-monitor] Статус exit-нод изменился, запрос на пересинхронизацию Xray...")
		RequestXraySync()
	}

	// ── MTProto :mtproto_port TCP probe ──
	// Runs after the main tunnel loop every 30 s. Uses 3-fail / 2-success
	// hysteresis independently of the general tunnel-health counters.
	checkMTProtoHealth()
}

// checkMTProtoHealth probes the MTProto port on every online exit node that
// has MTProto installed (mtproto_installed_at IS NOT NULL) and updates
// mtproto_status via biz.MarkMTProtoStatus with 3-fail / 2-success hysteresis.
func checkMTProtoHealth() {
	mtprotoPort := biz.GetSettingOrDefault("mtproto_port", "8443")

	rows, err := db.DB.Query(`
		SELECT id, ip_address, mtproto_status
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_installed_at IS NOT NULL
	`)
	if err != nil {
		log.Printf("[mtproto-health] query error: %v", err)
		return
	}

	type probeRow struct {
		ID     int
		IP     string
		Status string
	}
	var probes []probeRow
	for rows.Next() {
		var p probeRow
		if scanErr := rows.Scan(&p.ID, &p.IP, &p.Status); scanErr == nil {
			probes = append(probes, p)
		}
	}
	rows.Close()

	for _, p := range probes {
		addr := fmt.Sprintf("%s:%s", p.IP, mtprotoPort)
		conn, dialErr := net.DialTimeout("tcp", addr, 3*time.Second)
		ok := dialErr == nil
		if conn != nil {
			_ = conn.Close()
		}

		mtprotoCountMu.Lock()
		if ok {
			mtprotoSuccessCount[p.ID]++
			mtprotoFailCount[p.ID] = 0
			if mtprotoSuccessCount[p.ID] >= 2 && p.Status != "healthy" {
				if markErr := biz.MarkMTProtoStatus(p.ID, "healthy"); markErr != nil {
					log.Printf("[mtproto-health] MarkMTProtoStatus(%d, healthy): %v", p.ID, markErr)
				} else {
					log.Printf("[mtproto-health] server %d (%s) → healthy after 2 successes", p.ID, p.IP)
				}
			} else {
				// Always refresh mtproto_last_check even without a status change.
				_ = biz.MarkMTProtoStatus(p.ID, p.Status)
			}
		} else {
			mtprotoFailCount[p.ID]++
			mtprotoSuccessCount[p.ID] = 0
			if mtprotoFailCount[p.ID] >= 3 && p.Status != "unhealthy" {
				if markErr := biz.MarkMTProtoStatus(p.ID, "unhealthy"); markErr != nil {
					log.Printf("[mtproto-health] MarkMTProtoStatus(%d, unhealthy): %v", p.ID, markErr)
				} else {
					log.Printf("[mtproto-health] server %d (%s) → unhealthy after 3 fails: %v", p.ID, p.IP, dialErr)
				}
			} else {
				_ = biz.MarkMTProtoStatus(p.ID, p.Status)
			}
		}
		mtprotoCountMu.Unlock()
	}
}

// checkTunnelHealth проверяет доступность exit-ноды по протоколу.
// - VLESS: TCP + TLS handshake (Reality endpoint)
// - Shadowsocks: TCP connect (WS порт 443)
// - Hysteria2: проверка локального SOCKS5 порта Hysteria2-клиента на Hub
// - Остальные: TCP connect
// Возвращает статус и latency в миллисекундах.
func checkTunnelHealth(node biz.ExitNodeTunnel) (status string, latencyMs int, errMsg string) {
	start := time.Now()

	switch node.TunnelProtocol {
	case "vless":
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("vless %s: %v", addr, err)
		}
		sni := node.TunnelSNI
		if sni == "" {
			sni = node.IPAddress
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
		})
		tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return "unhealthy", 0, fmt.Sprintf("vless %s: TLS handshake failed: %v", addr, err)
		}
		tlsConn.Close()

	case "hysteria2":
		hy2Port := 1088 + node.ID
		localAddr := fmt.Sprintf("127.0.0.1:%d", hy2Port)
		conn, err := net.DialTimeout("tcp", localAddr, 3*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("hysteria2 local SOCKS %s: %v", localAddr, err)
		}
		conn.Close()

	case "shadowsocks":
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("shadowsocks %s: %v", addr, err)
		}
		conn.Close()

	default:
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("%s %s: %v", node.TunnelProtocol, addr, err)
		}
		conn.Close()
	}

	latencyMs = int(time.Since(start).Milliseconds())
	return "healthy", latencyMs, ""
}

// ─────────────────────── Sync Loop ───────────────────────

// StartXraySyncLoop запускает фоновую горутину для периодической синхронизации Xray.
func StartXraySyncLoop() {
	// Проверяем что Xray установлен
	if _, err := os.Stat(xrayConfigPath); os.IsNotExist(err) {
		log.Println("[xray-sync] Xray не установлен, синхронизация отключена")
		return
	}

	// Начальная синхронизация
	runXraySync("Начальная синхронизация")

	// Периодическая синхронизация каждые 5 минут
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runXraySync("Периодическая синхронизация")
		}
	}()

	log.Println("[xray-sync] Фоновая синхронизация запущена (каждые 5 мин)")

	// Запускаем мониторинг туннелей до exit-нод
	StartTunnelHealthMonitor()

	// Запускаем мониторинг истечения mTLS сертификатов
	StartCertExpiryMonitor()

	// Запускаем auto-tuner для оптимизации VPN конфигов
	StartAutoTuner()
}

// ─────────────────────── mTLS Certificate Expiry Monitor ───────────────────────

// StartCertExpiryMonitor проверяет mTLS сертификаты при старте и каждые 24 часа.
// Логирует WARNING если сертификат истекает в течение 30 дней.
func StartCertExpiryMonitor() {
	go func() {
		checkCertExpiry()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			checkCertExpiry()
		}
	}()
}

func checkCertExpiry() {
	certsDir := filepath.Join(config.AppDir(), "certs")
	certFiles := []string{"ca-cert.pem", "client-cert.pem", "server-cert.pem"}

	for _, name := range certFiles {
		path := filepath.Join(certsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // файл не существует — OK, может быть не настроен
		}
		block, _ := pem.Decode(data)
		if block == nil {
			log.Printf("[cert-monitor] %s: не удалось прочитать PEM", name)
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			log.Printf("[cert-monitor] %s: ошибка парсинга: %v", name, err)
			continue
		}
		daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
		if daysLeft < 0 {
			log.Printf("[cert-monitor] CRITICAL: %s ИСТЁК %d дней назад!", name, -daysLeft)
		} else if daysLeft < 7 {
			log.Printf("[cert-monitor] CRITICAL: %s истекает через %d дней!", name, daysLeft)
		} else if daysLeft < 30 {
			log.Printf("[cert-monitor] WARNING: %s истекает через %d дней", name, daysLeft)
		}
	}
}

// ─── Dynamic config helpers (read from app_settings, auto-tuner writes) ───

func getDNSServers() []string {
	s := biz.GetSettingOrDefault("dns_servers", "8.8.8.8,1.1.1.1")
	parts := strings.Split(s, ",")
	var servers []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			servers = append(servers, p)
		}
	}
	if len(servers) == 0 {
		return []string{"8.8.8.8", "1.1.1.1"}
	}
	return servers
}

func getFingerprint() string {
	fp := biz.GetSettingOrDefault("reality_fingerprint", "chrome")
	if fp == "" {
		return "chrome"
	}
	return fp
}

func getMuxConcurrency() int {
	s := biz.GetSettingOrDefault("mux_concurrency", "8")
	v, _ := strconv.Atoi(s)
	if v < 1 || v > 64 {
		return 8
	}
	return v
}
