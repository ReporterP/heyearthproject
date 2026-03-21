package main

import (
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

	// ── Per-user клиенты ──
	activeUUIDs, err := biz.ListActiveVlessUUIDs()
	if err != nil {
		return fmt.Errorf("list active uuids: %w", err)
	}

	// Серверный UUID — для обратной совместимости и админ-доступа
	serverUUID := keys.UUID
	if serverUUID != "" {
		found := false
		for _, u := range activeUUIDs {
			if u == serverUUID {
				found = true
				break
			}
		}
		if !found {
			activeUUIDs = append([]string{serverUUID}, activeUUIDs...)
		}
	}

	vlessClients := make([]map[string]interface{}, 0, len(activeUUIDs))
	for _, uuid := range activeUUIDs {
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":   uuid,
			"flow": "xtls-rprx-vision",
		})
	}
	if len(vlessClients) == 0 && serverUUID != "" {
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":   serverUUID,
			"flow": "xtls-rprx-vision",
		})
	}

	// Per-user аккаунты для SOCKS5/HTTP
	socksAccounts, httpAccounts := buildProxyAccounts()
	if serverUUID != "" {
		socksAccounts = append(socksAccounts, map[string]string{"user": "heyearth", "pass": serverUUID})
		httpAccounts = append(httpAccounts, map[string]string{"user": "heyearth", "pass": serverUUID})
	}

	// ── Inbounds ──
	inbounds := []map[string]interface{}{
		{
			"tag": "vless-reality", "listen": "127.0.0.1", "port": 10443, "protocol": "vless",
			"settings": map[string]interface{}{"clients": vlessClients, "decryption": "none"},
			"streamSettings": map[string]interface{}{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]interface{}{
					"show": false, "dest": "microsoft.com:443", "xver": 0,
					"serverNames": []string{"microsoft.com", "www.microsoft.com"},
					"privateKey": keys.PrivateKey, "shortIds": []string{keys.ShortID},
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
		},
		{
			"tag": "http-proxy", "listen": "0.0.0.0", "port": 8080, "protocol": "http",
			"settings": map[string]interface{}{"allowTransparent": false, "accounts": httpAccounts},
			"sniffing":  map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls"}},
		},
	}

	// ── Outbounds ──
	outbounds := []map[string]interface{}{
		{"protocol": "freedom", "tag": "direct", "settings": map[string]interface{}{"domainStrategy": "UseIP"}},
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

		// Добавляем observatory для мониторинга задержки до exit-нод
		// Xray автоматически пингует exit-ноды и выбирает с наименьшей задержкой
	}

	config := map[string]interface{}{
		"log":       map[string]string{"loglevel": "warning"},
		"dns":       map[string]interface{}{"servers": []string{"8.8.8.8", "1.1.1.1"}},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   routing,
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
	log.Printf("[xray-sync] Конфиг обновлён: %d VLESS клиентов, %d SOCKS5/HTTP аккаунтов, %d exit-нод",
		len(vlessClients), len(socksAccounts), len(exitTags))

	return nil
}

// buildExitNodeOutbound создаёт Xray outbound для exit-ноды.
// Поддерживает VLESS Reality, Shadowsocks и прокси-протоколы.
func buildExitNodeOutbound(node biz.ExitNodeTunnel) map[string]interface{} {
	tag := fmt.Sprintf("exit-%d", node.ID)

	switch node.TunnelProtocol {
	case "vless":
		if node.TunnelUUID == "" {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — нет tunnel_uuid", node.ID, node.Name)
			return nil
		}
		sni := node.TunnelSNI
		if sni == "" {
			sni = node.IPAddress
		}
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "vless",
			"settings": map[string]interface{}{
				"vnext": []map[string]interface{}{
					{
						"address": node.IPAddress, "port": node.TunnelPort,
						"users": []map[string]interface{}{
							{"id": node.TunnelUUID, "encryption": "none", "flow": "xtls-rprx-vision"},
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
			},
		}
		// Добавляем publicKey и shortId если заданы
		if node.TunnelPubKey != "" {
			rs := outbound["streamSettings"].(map[string]interface{})["realitySettings"].(map[string]interface{})
			rs["publicKey"] = node.TunnelPubKey
			if node.TunnelShortID != "" {
				rs["shortIds"] = []string{node.TunnelShortID}
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
		}
		// Добавляем авторизацию если есть
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
		newStatus := checkTunnelHealth(node)
		oldStatus := node.TunnelStatus

		if newStatus != oldStatus {
			if err := biz.UpdateTunnelStatus(node.ID, newStatus); err != nil {
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
			// Обновляем last_tunnel_check даже без смены статуса
			_ = biz.UpdateTunnelStatus(node.ID, newStatus)
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

// checkTunnelHealth проверяет доступность exit-ноды через TCP.
func checkTunnelHealth(node biz.ExitNodeTunnel) string {
	addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return "unhealthy"
	}
	conn.Close()
	return "healthy"
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

