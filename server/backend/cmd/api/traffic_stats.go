package main

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/xraystats"
)

// ─────────────────────── Traffic Stats Collector ───────────────────────
//
// Каждые 60 секунд опрашивает Xray Stats API через gRPC (127.0.0.1:10085),
// получает per-user трафик (upload/download) и записывает в БД.
// Использует QueryStats с reset=true — счётчики обнуляются после чтения.

const xrayStatsAPIAddr = "127.0.0.1:10085"

// StartTrafficStatsCollector запускает фоновый сборщик трафика.
func StartTrafficStatsCollector() {
	go func() {
		// Ждём запуск Xray
		time.Sleep(15 * time.Second)
		collectTrafficStats()

		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			collectTrafficStats()
		}
	}()
	log.Println("[traffic-stats] Фоновый сборщик трафика запущен (каждые 60 сек)")
}

func collectTrafficStats() {
	// Быстрая проверка доступности
	conn, err := net.DialTimeout("tcp", xrayStatsAPIAddr, 2*time.Second)
	if err != nil {
		return // Xray Stats API недоступен — нормально если Xray не установлен
	}
	conn.Close()

	stats, err := xraystats.QueryStats(xrayStatsAPIAddr, "user>>>", true)
	if err != nil {
		return
	}

	// Группируем по user_id
	type trafficDelta struct {
		upload   int64
		download int64
	}
	deltas := make(map[string]*trafficDelta)

	for _, stat := range stats {
		// Формат: "user>>>user_id>>>traffic>>>uplink" / "...>>>downlink"
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" {
			continue
		}
		userID := parts[1]
		direction := parts[3]

		if userID == "server@heyearth" || userID == "" {
			continue
		}

		if deltas[userID] == nil {
			deltas[userID] = &trafficDelta{}
		}
		switch direction {
		case "uplink":
			deltas[userID].upload = stat.Value
		case "downlink":
			deltas[userID].download = stat.Value
		}
	}

	if len(deltas) == 0 {
		return
	}

	saved := 0
	for userID, delta := range deltas {
		if delta.upload == 0 && delta.download == 0 {
			continue
		}

		_, err := db.DB.Exec(`
			INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes)
			VALUES ($1, $2, $3)
		`, userID, delta.upload, delta.download)
		if err != nil {
			log.Printf("[traffic-stats] Ошибка записи для %s: %v", userID, err)
			continue
		}

		// Аккумулируем в user_subscriptions.traffic_used
		_, _ = db.DB.Exec(`
			UPDATE user_subscriptions us
			SET traffic_used = traffic_used + $1
			FROM users u
			WHERE u.id = us.user_id AND u.user_id = $2 AND us.valid_until > NOW()
		`, delta.upload+delta.download, userID)

		saved++
	}

	if saved > 0 {
		log.Printf("[traffic-stats] Записано: %d пользователей", saved)
	}

	// Проверяем лимиты — логируем превышения
	checkTrafficLimits()
}

// checkTrafficLimits логирует пользователей, превысивших лимит трафика.
func checkTrafficLimits() {
	rows, err := db.DB.Query(`
		SELECT u.user_id, us.traffic_used, us.traffic_limit
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE us.valid_until > NOW()
		  AND us.traffic_limit IS NOT NULL
		  AND us.traffic_used >= us.traffic_limit
		  AND COALESCE(u.is_blocked, false) = false
	`)
	if err != nil {
		return
	}
	defer rows.Close()

	exceeded := 0
	for rows.Next() {
		var userID string
		var used, limit int64
		if err := rows.Scan(&userID, &used, &limit); err != nil {
			continue
		}
		log.Printf("[traffic-stats] ⚠ Пользователь %s превысил лимит трафика: %d / %d bytes",
			userID, used, limit)
		exceeded++
	}

	if exceeded > 0 {
		RequestXraySync()
	}
}
