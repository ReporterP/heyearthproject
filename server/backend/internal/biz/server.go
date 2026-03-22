package biz

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"

	"github.com/heyearth/backend/internal/db"
)

type ServerNode struct {
	ID              int
	IPAddress       string
	Name            string
	ReputationScore float64
	UptimeRatio     float64
}

type LoadMetrics struct {
	CPUUsage          float64 `json:"cpu_usage"`
	MemoryUsage       float64 `json:"memory_usage"`
	ActiveConnections int     `json:"active_connections"`
}

const reputationWeight = 5.0

func serverLoadScore(metrics LoadMetrics, reputation, uptime float64) float64 {
	score := metrics.CPUUsage + (float64(metrics.ActiveConnections) * 0.1)
	score -= reputation * reputationWeight
	if uptime > 0 && uptime < 1 {
		score += (1 - uptime) * 10
	}
	return score
}

type candidateServer struct {
	node  ServerNode
	score float64
}

func queryGroupCandidates(groupID int) ([]candidateServer, error) {
	rows, err := db.DB.Query(`
		SELECT s.id, s.ip_address, s.name, s.load_metrics,
		       COALESCE(s.reputation_score, 0), COALESCE(s.uptime_ratio, 1)
		FROM servers s
		JOIN server_groups sg ON s.id = sg.server_id
		WHERE sg.group_id = $1 AND s.status != 'offline'
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []candidateServer
	for rows.Next() {
		var srv ServerNode
		var metricsJSON []byte
		if err := rows.Scan(&srv.ID, &srv.IPAddress, &srv.Name, &metricsJSON, &srv.ReputationScore, &srv.UptimeRatio); err != nil {
			log.Printf("Error scanning server: %v", err)
			continue
		}
		var metrics LoadMetrics
		if len(metricsJSON) > 0 {
			if err := json.Unmarshal(metricsJSON, &metrics); err != nil {
				log.Printf("Error parsing metrics for server %d: %v", srv.ID, err)
			}
		}
		candidates = append(candidates, candidateServer{
			node:  srv,
			score: serverLoadScore(metrics, srv.ReputationScore, srv.UptimeRatio),
		})
	}
	return candidates, rows.Err()
}

func GetBestServerForGroup(groupID int) (*ServerNode, error) {
	candidates, err := queryGroupCandidates(groupID)
	if err != nil {
		return nil, err
	}

	var best *candidateServer
	for i := range candidates {
		if best == nil || candidates[i].score < best.score {
			best = &candidates[i]
		}
	}
	if best != nil {
		return &best.node, nil
	}

	var srv ServerNode
	err = db.DB.QueryRow(`
		SELECT id, ip_address, name
		FROM servers
		WHERE status != 'offline'
		ORDER BY RANDOM()
		LIMIT 1
	`).Scan(&srv.ID, &srv.IPAddress, &srv.Name)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no available servers found")
	} else if err != nil {
		return nil, err
	}
	return &srv, nil
}

// GetBestServerChainForGroup picks numHops distinct nodes by load+reputation.
func GetBestServerChainForGroup(groupID int, numHops int) ([]*ServerNode, error) {
	if numHops < 1 {
		numHops = 2
	}

	candidates, err := queryGroupCandidates(groupID)
	if err != nil {
		return nil, err
	}

	// sort by score ascending
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score < candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	var chain []*ServerNode
	for i := 0; i < len(candidates) && len(chain) < numHops; i++ {
		chain = append(chain, &candidates[i].node)
	}

	if len(chain) == 0 {
		return nil, fmt.Errorf("no available servers for multi-hop chain")
	}

	return chain, nil
}

// RecordSuccessfulConnection bumps the reputation of a server.
func RecordSuccessfulConnection(serverID int) {
	_, err := db.DB.Exec(`
		UPDATE servers
		SET reputation_score = LEAST(reputation_score + 0.01, 1),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, serverID)
	if err != nil {
		log.Printf("RecordSuccessfulConnection(%d): %v", serverID, err)
	}
}

// RecordFailedConnection degrades the reputation of a server.
func RecordFailedConnection(serverID int) {
	_, err := db.DB.Exec(`
		UPDATE servers
		SET reputation_score = GREATEST(reputation_score - 0.05, 0),
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, serverID)
	if err != nil {
		log.Printf("RecordFailedConnection(%d): %v", serverID, err)
	}
}

// ─────────────────────── Exit Nodes (Hub-and-Spoke) ───────────────────────

// ExitNodeTunnel — конфигурация туннеля к exit-ноде.
// Hub подключается к каждой exit-ноде через зашифрованный канал.
type ExitNodeTunnel struct {
	ID             int
	Name           string
	IPAddress      string
	TunnelProtocol string // vless, shadowsocks, socks5, http
	TunnelPort     int
	TunnelUUID     string // VLESS UUID / auth credential
	TunnelPubKey   string // Reality public key (для VLESS)
	TunnelShortID  string // Reality short ID (для VLESS)
	TunnelSNI      string // SNI для Reality
	TunnelSSMethod string // Shadowsocks cipher
	TunnelSSPass   string // Shadowsocks password
	TunnelStatus   string // healthy, unhealthy, unknown
}

// ListHealthyExitNodes возвращает все exit-ноды со статусом healthy.
// Используется для генерации Xray outbound-ов на Hub.
func ListHealthyExitNodes() ([]ExitNodeTunnel, error) {
	return listExitNodesByStatus("healthy")
}

// ListAllExitNodes возвращает все exit-ноды (для health-check).
func ListAllExitNodes() ([]ExitNodeTunnel, error) {
	return listExitNodesByStatus("")
}

func listExitNodesByStatus(statusFilter string) ([]ExitNodeTunnel, error) {
	query := `
		SELECT id, COALESCE(name, ''), ip_address,
		       tunnel_protocol, tunnel_port,
		       COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''),
		       COALESCE(tunnel_short_id, ''), COALESCE(tunnel_sni, ''),
		       COALESCE(tunnel_ss_method, ''), COALESCE(tunnel_ss_password, ''),
		       tunnel_status
		FROM servers
		WHERE is_exit_node = TRUE
	`
	if statusFilter != "" {
		query += ` AND tunnel_status = '` + statusFilter + `'`
	}
	query += ` ORDER BY id`

	rows, err := db.DB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("list exit nodes: %w", err)
	}
	defer rows.Close()

	var nodes []ExitNodeTunnel
	for rows.Next() {
		var n ExitNodeTunnel
		if err := rows.Scan(
			&n.ID, &n.Name, &n.IPAddress,
			&n.TunnelProtocol, &n.TunnelPort,
			&n.TunnelUUID, &n.TunnelPubKey,
			&n.TunnelShortID, &n.TunnelSNI,
			&n.TunnelSSMethod, &n.TunnelSSPass,
			&n.TunnelStatus,
		); err != nil {
			log.Printf("scan exit node: %v", err)
			continue
		}
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}

// UpdateTunnelStatus обновляет статус туннеля для ноды.
func UpdateTunnelStatus(serverID int, status string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		SET tunnel_status = $1, last_tunnel_check = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, status, serverID)
	return err
}

// UpdateTunnelStatusWithLatency обновляет статус и latency для ноды.
func UpdateTunnelStatusWithLatency(serverID int, status string, latencyMs int) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		SET tunnel_status = $1, latency_ms = $2, last_tunnel_check = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`, status, latencyMs, serverID)
	return err
}
