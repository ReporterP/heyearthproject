package admin

import (
	"context"
	"encoding/json"
	"log"
	"runtime/debug"
	"time"

	"github.com/heyearth/backend/internal/db"
	pbnode "github.com/heyearth/backend/internal/pb/node_agent_v1"
)

// ─────────────────────── Server Metrics Collector ───────────────────────
//
// Каждые 10 секунд опрашивает все серверы через gRPC (node-agent:50052 mTLS),
// получает CPU/RAM/connections и записывает в servers.load_metrics + metrics_synced_at.
// Это обеспечивает realtime-метрики в админке без ручного клика на "Синхронизировать".

const (
	metricsCollectInterval = 10 * time.Second
	metricsCollectTimeout  = 5 * time.Second
)

// StartServerMetricsCollector запускает фоновый сборщик метрик серверов.
// Вызывать один раз из main после InitDB.
func StartServerMetricsCollector() {
	go func() {
		// Ждём полного запуска API перед первым опросом
		time.Sleep(20 * time.Second)
		collectAllServerMetrics()

		ticker := time.NewTicker(metricsCollectInterval)
		defer ticker.Stop()
		for range ticker.C {
			collectAllServerMetrics()
		}
	}()
	log.Printf("[server-metrics] Фоновый сборщик метрик серверов запущен (каждые %v)", metricsCollectInterval)
}

// serverMetricsTargetsQuery: node-agent runs on exits only; a relay has no agent.
const serverMetricsTargetsQuery = `
		SELECT id, ip_address
		FROM servers
		WHERE (status != 'archived' OR status IS NULL)
		  AND COALESCE(role, 'exit') <> 'relay'`

func collectAllServerMetrics() {
	// A panic here would kill the whole API; one bad tick is only logged.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[server-metrics] panic: %v\n%s", r, debug.Stack())
		}
	}()
	rows, err := db.DB.Query(serverMetricsTargetsQuery)
	if err != nil {
		return
	}
	defer rows.Close()

	type serverRef struct {
		ID int
		IP string
	}
	var servers []serverRef
	for rows.Next() {
		var s serverRef
		if err := rows.Scan(&s.ID, &s.IP); err != nil {
			continue
		}
		servers = append(servers, s)
	}
	if len(servers) == 0 {
		return
	}

	// Параллельно с лимитом 5
	sem := make(chan struct{}, 5)
	done := make(chan struct{}, len(servers))
	for _, srv := range servers {
		sem <- struct{}{}
		go func(id int, ip string) {
			defer func() {
				<-sem
				done <- struct{}{}
			}()
			collectOneServerMetrics(id, ip)
		}(srv.ID, srv.IP)
	}
	// Ждём завершения всех
	for i := 0; i < len(servers); i++ {
		<-done
	}
}

func collectOneServerMetrics(serverID int, ipAddress string) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsCollectTimeout)
	defer cancel()

	client, conn, err := connectToNodeAgent(ctx, ipAddress)
	if err != nil {
		// Не удалось подключиться — помечаем offline если синк давно
		_, _ = db.DB.Exec(`
			UPDATE servers
			SET status = CASE
				WHEN metrics_synced_at IS NULL OR metrics_synced_at < NOW() - INTERVAL '2 minutes'
				THEN 'offline'
				ELSE status
			END
			WHERE id = $1
		`, serverID)
		return
	}
	defer conn.Close()

	resp, err := client.GetMetrics(ctx, &pbnode.GetMetricsRequest{})
	if err != nil {
		return
	}

	metricsBytes, _ := json.Marshal(map[string]any{
		"cpu_usage":          resp.GetCpuUsage(),
		"memory_usage":       resp.GetMemoryUsage(),
		"active_connections": resp.GetActiveConnections(),
		"synced_at":          time.Now().Format(time.RFC3339),
	})

	_, err = db.DB.Exec(`
		UPDATE servers
		SET load_metrics = $1::jsonb,
		    status = 'online',
		    metrics_synced_at = NOW(),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, string(metricsBytes), serverID)
	if err != nil {
		log.Printf("[server-metrics] Ошибка записи метрик для сервера %d: %v", serverID, err)
	}
}
