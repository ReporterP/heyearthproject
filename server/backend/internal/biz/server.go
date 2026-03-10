package biz

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"heyearth/internal/db"
	"log"
)

type ServerNode struct {
	ID        int
	IPAddress string
	Name      string
}

type LoadMetrics struct {
	CPUUsage          float64 `json:"cpu_usage"`
	MemoryUsage       float64 `json:"memory_usage"`
	ActiveConnections int     `json:"active_connections"`
}

func GetBestServerForGroup(groupID int) (*ServerNode, error) {
	// Query all online servers for the group
	rows, err := db.DB.Query(`
		SELECT s.id, s.ip_address, s.name, s.load_metrics 
		FROM servers s
		JOIN server_groups sg ON s.id = sg.server_id
		WHERE sg.group_id = $1 AND s.status != 'offline'
	`, groupID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bestServer *ServerNode
	var lowestLoad float64 = -1

	for rows.Next() {
		var srv ServerNode
		var metricsJSON []byte
		if err := rows.Scan(&srv.ID, &srv.IPAddress, &srv.Name, &metricsJSON); err != nil {
			log.Printf("Error scanning server: %v", err)
			continue
		}

		var metrics LoadMetrics
		if len(metricsJSON) > 0 {
			if err := json.Unmarshal(metricsJSON, &metrics); err != nil {
				log.Printf("Error parsing metrics for server %d: %v", srv.ID, err)
			}
		}

		// Calculate a simple load score (can be adjusted based on priority)
		// For example: CPU dominates, but connections also matter
		loadScore := metrics.CPUUsage + (float64(metrics.ActiveConnections) * 0.1)

		if bestServer == nil || loadScore < lowestLoad {
			bestServer = &ServerNode{
				ID:        srv.ID,
				IPAddress: srv.IPAddress,
				Name:      srv.Name,
			}
			lowestLoad = loadScore
		}
	}

	if bestServer != nil {
		return bestServer, nil
	}

	// Fallback to any server if none in group
	err = db.DB.QueryRow(`
		SELECT id, ip_address, name 
		FROM servers 
		WHERE status != 'offline'
		ORDER BY RANDOM()
		LIMIT 1
	`).Scan(&bestServer.ID, &bestServer.IPAddress, &bestServer.Name)
	
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no available servers found")
	} else if err != nil {
		return nil, err
	}

	return bestServer, nil
}
