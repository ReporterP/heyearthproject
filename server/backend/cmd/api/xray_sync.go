package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────────────── Xray Config Sync ───────────────────────
//
// Синхронизация конфига Xray с активными пользователями.
// При изменении подписок (создание, истечение, блокировка) вызывается
// SyncXrayConfig(), который перезаписывает /opt/heyearth/xray/config.json
// со списком всех активных пользовательских UUID и перезапускает Xray.

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

// xrayConfig — структура Xray конфига для сериализации
type xrayConfig struct {
	Log       map[string]string        `json:"log"`
	DNS       map[string]interface{}   `json:"dns"`
	Inbounds  []map[string]interface{} `json:"inbounds"`
	Outbounds []map[string]interface{} `json:"outbounds"`
	Routing   map[string]interface{}   `json:"routing"`
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

// SyncXrayConfig генерирует Xray конфиг со всеми активными пользователями и перезапускает.
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

	// Получаем все активные UUID пользователей
	activeUUIDs, err := biz.ListActiveVlessUUIDs()
	if err != nil {
		return fmt.Errorf("list active uuids: %w", err)
	}

	// Всегда добавляем серверный UUID (для обратной совместимости и админ-доступа)
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

	// Формируем список VLESS клиентов
	vlessClients := make([]map[string]interface{}, 0, len(activeUUIDs))
	for _, uuid := range activeUUIDs {
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":   uuid,
			"flow": "xtls-rprx-vision",
		})
	}

	// Если нет клиентов — добавляем серверный UUID чтобы Xray не падал
	if len(vlessClients) == 0 && serverUUID != "" {
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":   serverUUID,
			"flow": "xtls-rprx-vision",
		})
	}

	// Формируем список SOCKS5 аккаунтов (per-user: login=userID, pass=vlessUUID)
	socksAccounts, httpAccounts := buildProxyAccounts()

	// Всегда добавляем серверный аккаунт для обратной совместимости
	if serverUUID != "" {
		socksAccounts = append(socksAccounts, map[string]string{"user": "heyearth", "pass": serverUUID})
		httpAccounts = append(httpAccounts, map[string]string{"user": "heyearth", "pass": serverUUID})
	}

	config := xrayConfig{
		Log: map[string]string{"loglevel": "warning"},
		DNS: map[string]interface{}{
			"servers": []string{"8.8.8.8", "1.1.1.1"},
		},
		Inbounds: []map[string]interface{}{
			{
				"tag":      "vless-reality",
				"listen":   "127.0.0.1",
				"port":     10443,
				"protocol": "vless",
				"settings": map[string]interface{}{
					"clients":    vlessClients,
					"decryption": "none",
				},
				"streamSettings": map[string]interface{}{
					"network":  "tcp",
					"security": "reality",
					"realitySettings": map[string]interface{}{
						"show":        false,
						"dest":        "microsoft.com:443",
						"xver":        0,
						"serverNames": []string{"microsoft.com", "www.microsoft.com"},
						"privateKey":  keys.PrivateKey,
						"shortIds":    []string{keys.ShortID},
					},
				},
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls", "quic"},
				},
			},
			{
				"tag":      "shadowsocks",
				"listen":   "0.0.0.0",
				"port":     8388,
				"protocol": "shadowsocks",
				"settings": map[string]interface{}{
					"method":   "2022-blake3-aes-128-gcm",
					"password": keys.SSPassword,
					"network":  "tcp,udp",
				},
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls", "quic"},
				},
			},
			{
				"tag":      "socks5-proxy",
				"listen":   "0.0.0.0",
				"port":     1080,
				"protocol": "socks",
				"settings": map[string]interface{}{
					"auth":     "password",
					"accounts": socksAccounts,
					"udp":      true,
				},
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls", "quic"},
				},
			},
			{
				"tag":      "http-proxy",
				"listen":   "0.0.0.0",
				"port":     8080,
				"protocol": "http",
				"settings": map[string]interface{}{
					"allowTransparent": false,
					"accounts":         httpAccounts,
				},
				"sniffing": map[string]interface{}{
					"enabled":      true,
					"destOverride": []string{"http", "tls"},
				},
			},
		},
		Outbounds: []map[string]interface{}{
			{"protocol": "freedom", "tag": "direct", "settings": map[string]interface{}{"domainStrategy": "UseIP"}},
			{"protocol": "blackhole", "tag": "block", "settings": map[string]interface{}{"response": map[string]string{"type": "none"}}},
		},
		Routing: map[string]interface{}{
			"domainStrategy": "IPIfNonMatch",
			"rules": []map[string]interface{}{
				{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "block"},
				{"type": "field", "protocol": []string{"bittorrent"}, "outboundTag": "block"},
			},
		},
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
	log.Printf("[xray-sync] Конфиг обновлён: %d VLESS клиентов, %d SOCKS5/HTTP аккаунтов", len(vlessClients), len(socksAccounts))

	return nil
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
		acc := map[string]string{"user": u.UserID, "pass": u.VlessUUID}
		socksAccounts = append(socksAccounts, acc)
		httpAccounts = append(httpAccounts, map[string]string{"user": u.UserID, "pass": u.VlessUUID})
	}
	return
}

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
}
