package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────────────── Xray Config Sync (Hub-and-Spoke) ───────────────────────
//
// Hub-and-Spoke архитектура:
//   - Hub (главный сервер) принимает подключения пользователей
//   - Spoke (exit-ноды) подключены к Hub через зашифрованные туннели
//   - Hub маршрутизирует трафик пользователей через здоровые exit-ноды
//   - Балансировка нагрузки: leastPing по всем доступным exit-нодам
//   - Если exit-нод нет — трафик идёт напрямую (freedom)
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
	xraySyncMu   sync.Mutex
	xraySyncLast time.Time
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
func SyncXrayConfig() error {
	xraySyncMu.Lock()
	defer xraySyncMu.Unlock()

	// Дебаунс — не чаще раза в 5 секунд
	if time.Since(xraySyncLast) < 5*time.Second {
		return nil
	}

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

	// Policy levels: 0 = standard, 1 = premium
	vlessClients := make([]map[string]interface{}, 0, len(activeClients))
	for _, c := range activeClients {
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

	// (activeClients уже использованы для заполнения vlessClients и uuidToUserID)
	if len(vlessClients) == 0 && serverUUID != "" {
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":    serverUUID,
			"flow":  "xtls-rprx-vision",
			"email": "server@heyearth",
		})
	}

	// Per-user аккаунты для SOCKS5/HTTP
	socksAccounts, httpAccounts := buildProxyAccounts()
	if serverUUID != "" {
		socksAccounts = append(socksAccounts, map[string]string{"user": "heyearth", "pass": serverUUID})
		httpAccounts = append(httpAccounts, map[string]string{"user": "heyearth", "pass": serverUUID})
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
					"serverNames": []string{realitySNI, "www." + realitySNI},
					"privateKey": keys.PrivateKey,
					"shortIds":   []string{keys.ShortID, ""}, // "" = принимать без shortId (совместимость)
				},
			},
			"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
		{
			"tag": "shadowsocks", "listen": "0.0.0.0", "port": 8388, "protocol": "shadowsocks",
			"settings": map[string]interface{}{"method": "2022-blake3-aes-128-gcm", "password": keys.SSPassword, "network": "tcp,udp"},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
		{
			"tag": "socks5-proxy", "listen": "0.0.0.0", "port": 1080, "protocol": "socks",
			"settings": map[string]interface{}{"auth": "password", "accounts": socksAccounts, "udp": true},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true, // Быстрый TCP handshake от клиента
					"tcpNoDelay":           true, // Без задержки Nagle
					"tcpKeepAliveInterval": 30,
				},
			},
		},
		{
			"tag": "http-proxy", "listen": "0.0.0.0", "port": 8080, "protocol": "http",
			"settings": map[string]interface{}{"allowTransparent": false, "accounts": httpAccounts},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls"}},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
					"tcpKeepAliveInterval": 30,
				},
			},
		},
	}

	// ── Outbounds ──
	outbounds := []map[string]interface{}{
		{
			"protocol": "freedom", "tag": "direct",
			"settings": map[string]interface{}{"domainStrategy": "UseIP"},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen": true,
					"tcpNoDelay":  true,
				},
			},
		},
		{"protocol": "blackhole", "tag": "block", "settings": map[string]interface{}{"response": map[string]string{"type": "none"}}},
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
			exitTags = append(exitTags, fmt.Sprintf("exit-%d", node.ID))
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

	// ── Routing (политики безопасности) ──
	rules := []map[string]interface{}{
		// 1. Блокировка приватных IP — inter-spoke изоляция
		// Spoke-ноды не могут обращаться к другим spoke или к внутренней сети Hub
		{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
		// 2. Блокировка BitTorrent
		{"type": "field", "protocol": []string{"bittorrent"}, "outboundTag": "block"},
		// 3. Блокировка DNS-over-HTTPS для предотвращения утечки DNS
		{"type": "field", "domain": []string{"domain:dns.google", "domain:cloudflare-dns.com", "domain:dns.quad9.net"}, "outboundTag": "block"},
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
		// Балансировщик: leastPing распределяет трафик по exit-нодам с наименьшим пингом
		balancers := []map[string]interface{}{
			{
				"tag":      "exit-balancer",
				"selector": exitTags,
				"strategy": map[string]string{"type": "leastPing"},
			},
		}
		routing["balancers"] = balancers

		// Финальное правило: весь оставшийся трафик — через балансировщик exit-нод
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"network":     "tcp,udp",
			"balancerTag": "exit-balancer",
		})
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

	// ── Xray Stats API ──
	// dokodemo-door inbound для gRPC API доступа к статистике
	inbounds = append(inbounds, map[string]interface{}{
		"tag": "api-in", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door",
		"settings": map[string]interface{}{"address": "127.0.0.1"},
	})

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
			"servers":       []string{"8.8.8.8", "1.1.1.1"},
			"queryStrategy": "UseIPv4", // Не запрашиваем AAAA записи
			"disableCache":  false,     // Кэш DNS включён (ускоряет повторные запросы)
			"tag":           "dns-out",
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
					"handshake":         4,   // Таймаут handshake (сек)
					"connIdle":          300, // Idle timeout (сек) — 5 мин
					"uplinkOnly":        2,   // Ждать 2 сек после закрытия download перед закрытием upload
					"downlinkOnly":      5,   // Ждать 5 сек после закрытия upload перед закрытием download
					"bufferSize":        64,  // 64KB буфер на соединение (дефолт 10KB — мало для видео)
				},
				"1": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
					"handshake":         4,
					"connIdle":          600, // Premium: 10 мин idle
					"uplinkOnly":        2,
					"downlinkOnly":      5,
					"bufferSize":        128, // Premium: 128KB буфер (стриминг видео)
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

	// Сериализуем и записываем конфиг
	configJSON, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal xray config: %w", err)
	}

	if err := os.WriteFile(xrayConfigPath, configJSON, 0644); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}

	// Перезапускаем Xray
	cmd := exec.Command("systemctl", "restart", "xray")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("restart xray: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	xraySyncLast = time.Now()
	log.Printf("[xray-sync] Конфиг обновлён: %d VLESS клиентов, %d SOCKS5/HTTP аккаунтов, %d exit-нод, %d правил маскировки",
		len(vlessClients), len(socksAccounts), len(exitTags), len(maskRouteEntries))

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
					"fingerprint": "chrome",
					"serverName":  sni,
				},
				"sockopt": map[string]interface{}{
					"tcpFastOpen": true,  // Экономия 1 RTT на новых подключениях
					"tcpNoDelay":  true,  // Без буферизации Nagle — снижает задержку
					"tcpKeepAliveInterval": 30, // Keepalive каждые 30 сек (не даёт DPI убить idle)
				},
			},
			// Мультиплексирование: несколько стримов по одному VLESS соединению.
			// Кратно снижает время установки подключения (один TLS handshake на все стримы).
			"mux": map[string]interface{}{
				"enabled":          true,
				"concurrency":      8,   // До 8 стримов на одно TCP соединение
				"xudpConcurrency":  16,  // UDP мультиплексирование (DNS, QUIC)
				"xudpProxyUDP443":  "reject", // QUIC/443 не через mux (лучше TCP fallback)
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
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,  // Экономия 1 RTT
					"tcpNoDelay":           true,  // Без буферизации Nagle
					"tcpKeepAliveInterval": 30,    // Keepalive от DPI
				},
			},
			// Мультиплексирование: много стримов по одному SS соединению
			"mux": map[string]interface{}{
				"enabled":         true,
				"concurrency":     8,
				"xudpConcurrency": 16,
				"xudpProxyUDP443": "reject",
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
			"mux": map[string]interface{}{"enabled": true, "concurrency": 8},
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
			"mux": map[string]interface{}{"enabled": true, "concurrency": 8},
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

// buildProxyAccounts формирует списки аккаунтов для SOCKS5 и HTTP proxy
// из всех активных пользователей (user_id → vless_uuid).
func buildProxyAccounts() (socksAccounts []map[string]string, httpAccounts []map[string]string) {
	rows, err := biz.ListActiveUsersWithUUIDs()
	if err != nil {
		log.Printf("[xray-sync] Ошибка получения активных пользователей: %v", err)
		return nil, nil
	}
	for _, u := range rows {
		socksAccounts = append(socksAccounts, map[string]string{"user": u.UserID, "pass": u.VlessUUID})
		httpAccounts = append(httpAccounts, map[string]string{"user": u.UserID, "pass": u.VlessUUID})
	}
	return
}

// ─────────────────────── Tunnel Health Monitoring ───────────────────────
//
// Фоновый мониторинг состояния туннелей до exit-нод.
// Каждые 30 секунд проверяет TCP-подключение к каждой exit-ноде.
// При изменении статуса — пересинхронизирует Xray конфиг.

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
	log.Println("[tunnel-monitor] Фоновый мониторинг exit-нод запущен (каждые 30 сек)")
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
		newStatus, latencyMs := checkTunnelHealth(node)
		oldStatus := node.TunnelStatus

		if newStatus != oldStatus {
			if err := biz.UpdateTunnelStatusWithLatency(node.ID, newStatus, latencyMs); err != nil {
				log.Printf("[tunnel-monitor] Ошибка обновления статуса ноды %d: %v", node.ID, err)
				continue
			}

			statusChanged = true

			// Алерт при изменении статуса
			if newStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] ⚠ ALERT: Exit-нода %d (%s, %s) — туннель НЕДОСТУПЕН",
					node.ID, node.Name, node.IPAddress)
				biz.RecordFailedConnection(node.ID)
			} else if newStatus == "healthy" && oldStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] ✓ Exit-нода %d (%s, %s) — туннель ВОССТАНОВЛЕН",
					node.ID, node.Name, node.IPAddress)
				biz.RecordSuccessfulConnection(node.ID)
			} else {
				log.Printf("[tunnel-monitor] Exit-нода %d (%s): %s → %s",
					node.ID, node.Name, oldStatus, newStatus)
			}
		} else if newStatus == "healthy" {
			// Обновляем last_tunnel_check и latency даже без смены статуса
			_ = biz.UpdateTunnelStatusWithLatency(node.ID, newStatus, latencyMs)
		}
	}

	// Если статус какой-то ноды изменился — пересинхронизируем Xray
	if statusChanged {
		log.Printf("[tunnel-monitor] Статус exit-нод изменился, пересинхронизация Xray...")
		// Сбрасываем дебаунс чтобы SyncXrayConfig сработал немедленно
		xraySyncMu.Lock()
		xraySyncLast = time.Time{}
		xraySyncMu.Unlock()

		if err := SyncXrayConfig(); err != nil {
			log.Printf("[tunnel-monitor] Ошибка синхронизации Xray: %v", err)
		}
	}
}

// checkTunnelHealth проверяет доступность exit-ноды.
// Для VLESS: TCP connect + TLS handshake (проверка Reality endpoint).
// Для остальных: TCP connect.
// Возвращает статус и latency в миллисекундах.
func checkTunnelHealth(node biz.ExitNodeTunnel) (string, int) {
	addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
	start := time.Now()

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "unhealthy", 0
	}

	// Для VLESS Reality — проверяем TLS handshake
	if node.TunnelProtocol == "vless" {
		sni := node.TunnelSNI
		if sni == "" {
			sni = node.IPAddress
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true, // Reality использует самоподписанные сертификаты
		})
		tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			// TLS handshake failed — порт открыт, но не Reality
			log.Printf("[tunnel-monitor] exit-нода %d (%s): TCP OK, TLS handshake failed: %v",
				node.ID, node.Name, err)
			return "unhealthy", 0
		}
		tlsConn.Close()
	} else {
		conn.Close()
	}

	latencyMs := int(time.Since(start).Milliseconds())
	return "healthy", latencyMs
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
	if err := SyncXrayConfig(); err != nil {
		log.Printf("[xray-sync] Начальная синхронизация: %v", err)
	}

	// Периодическая синхронизация каждые 5 минут
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := SyncXrayConfig(); err != nil {
				log.Printf("[xray-sync] Периодическая синхронизация: %v", err)
			}
		}
	}()

	log.Println("[xray-sync] Фоновая синхронизация запущена (каждые 5 мин)")

	// Запускаем мониторинг туннелей до exit-нод
	StartTunnelHealthMonitor()
}

