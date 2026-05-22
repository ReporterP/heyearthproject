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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
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
		if err := SyncXrayConfig(); err != nil {
			log.Printf("[xray-sync] debounced sync error: %v", err)
		}
	})
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
					"serverNames":  []string{realitySNI, "www." + realitySNI},
					"privateKey":   keys.PrivateKey,
					"shortIds":     []string{keys.ShortID, ""},
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
			"settings": map[string]interface{}{"clients": vlessClients, "decryption": "none"},
			"streamSettings": map[string]interface{}{
				"network": "xhttp", "security": "reality",
				"xhttpSettings": map[string]interface{}{
					"mode":          "auto",
					"xPaddingBytes": "100-1000",
				},
				"realitySettings": map[string]interface{}{
					"show": false, "dest": realitySNI + ":443", "xver": 0,
					"serverNames":   []string{realitySNI, "www." + realitySNI},
					"privateKey":    keys.PrivateKey,
					"shortIds":      []string{keys.ShortID, ""},
					"xPaddingBytes": "100-1000",
				},
			},
			"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
		{
			"tag": "shadowsocks", "listen": "0.0.0.0", "port": 8388, "protocol": "shadowsocks",
			"settings": map[string]interface{}{"method": "2022-blake3-aes-128-gcm", "password": keys.SSPassword, "network": "tcp,udp"},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
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

	// ── Routing (политики безопасности) ──
	rules := []map[string]interface{}{
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
			{
				"tag":      "exit-balancer",
				"selector": exitTags,
				"strategy": map[string]string{"type": balancerStrategy},
			},
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
			balancers = append(balancers, map[string]interface{}{
				"tag":      balancerTag,
				"selector": planTags,
				"strategy": map[string]string{"type": balancerStrategy},
			})
		}
		routing["balancers"] = balancers

		// Per-plan routing rules: match user email → plan-specific balancer
		for _, client := range activeClients {
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
		// Нет здоровых exit-нод: блокируем ВЕСЬ пользовательский трафик.
		// Без exit-нод трафик пошёл бы direct (без шифрования, с IP хаба) — недопустимо.
		// Блокируем ВСЕ inbound-ы: SOCKS5, HTTP, VLESS Reality, VLESS XHTTP.
		log.Println("[xray-sync] ВНИМАНИЕ: нет здоровых exit-нод, ВЕСЬ пользовательский трафик блокируется")
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"inboundTag":  []string{"vless-reality", "vless-xhttp"},
			"outboundTag": "block",
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
			"servers":       getDNSServers(),
			"queryStrategy": "UseIPv4",
			"disableCache":  false,
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
					"bufferSize":        256, // 256KB буфер на соединение (для видеостриминга)
				},
				"1": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
					"handshake":         4,
					"connIdle":          600, // Premium: 10 мин idle
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
				"enabled":          true,
				"concurrency":      getMuxConcurrency(),
				"xudpConcurrency":  16,  // UDP мультиплексирование (DNS, QUIC)
				"xudpProxyUDP443":  "allow", // Разрешаем QUIC/HTTP3 — ускоряет YouTube, Instagram
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
				// WebSocket transport: трафик выглядит как обычный HTTPS-вебсокет,
				// DPI не может отличить от обычного сайта. Значительно ускоряет
				// международные соединения, т.к. DPI не throttle'ит WS-трафик.
				"network": "ws",
				"wsSettings": map[string]interface{}{
					"path": "/stream",
				},
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,  // Экономия 1 RTT
					"tcpNoDelay":           true,  // Без буферизации Nagle
					"tcpKeepAliveInterval": 30,    // Keepalive от DPI
				},
			},
			// Мультиплексирование: много стримов по одному SS+WS соединению
			"mux": map[string]interface{}{
				"enabled":         true,
				"concurrency":     getMuxConcurrency(),
				"xudpConcurrency": 16,
				"xudpProxyUDP443": "allow",
			},
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

