package biz

import (
	"context"
	"fmt"
	"log"

	"github.com/heyearth/backend/internal/db"
)

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
	TunnelProtocol string // hysteria2 (+ VLESS Reality on :10443) or legacy vless
	TunnelPort     int
	TunnelUUID     string // VLESS UUID of the Hub on the exit's xray
	TunnelPubKey   string // Reality public key of the exit's xray
	TunnelShortID  string // Reality short ID
	TunnelSNI      string // SNI для Reality
	TunnelSSMethod string // legacy
	TunnelSSPass   string // Hysteria auth password (historical column name)
	TunnelStatus   string // healthy, unhealthy, unknown
	LoadWeight     int    // 1-10: preference of the exit in the leastLoad balancers
	LifecycleState string // pending, provisioning, online, failed, deprovisioning, offline
}

// exitNodeColumns is the select list scanExitNode reads.
const exitNodeColumns = `id, COALESCE(name, ''), ip_address,
	       COALESCE(tunnel_protocol, ''), COALESCE(tunnel_port, 0),
	       COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''),
	       COALESCE(tunnel_short_id, ''), COALESCE(tunnel_sni, ''),
	       COALESCE(tunnel_ss_method, ''), COALESCE(tunnel_ss_password, ''),
	       COALESCE(tunnel_status, 'unknown'), COALESCE(load_weight, 1), lifecycle_state`

func scanExitNode(row interface{ Scan(dest ...any) error }) (ExitNodeTunnel, error) {
	var n ExitNodeTunnel
	err := row.Scan(&n.ID, &n.Name, &n.IPAddress, &n.TunnelProtocol, &n.TunnelPort,
		&n.TunnelUUID, &n.TunnelPubKey, &n.TunnelShortID, &n.TunnelSNI,
		&n.TunnelSSMethod, &n.TunnelSSPass, &n.TunnelStatus, &n.LoadWeight, &n.LifecycleState)
	return n, err
}

// ListHealthyExitNodes возвращает все exit-ноды со статусом healthy.
func ListHealthyExitNodes() ([]ExitNodeTunnel, error) {
	return listExitNodesByStatus(context.Background(), "healthy")
}

// ListOnlineExitNodesContext returns every online exit whatever its tunnel status: the xray
// generator routes the healthy ones and keeps a probe pair for each (cmd/api/xray_exits.go).
func ListOnlineExitNodesContext(ctx context.Context) ([]ExitNodeTunnel, error) {
	return listExitNodesByStatus(ctx, "")
}

// ListAllExitNodes возвращает все online exit-ноды (для health-check).
func ListAllExitNodes() ([]ExitNodeTunnel, error) {
	return listExitNodesByStatus(context.Background(), "")
}

// GetExitNodeTunnel reads one exit in any lifecycle state. sql.ErrNoRows when the id is unknown
// or not an exit. Used by «Проверить туннель» (node-pool plan, Task 1.6).
func GetExitNodeTunnel(ctx context.Context, id int) (ExitNodeTunnel, error) {
	return scanExitNode(db.DB.QueryRowContext(ctx,
		`SELECT `+exitNodeColumns+` FROM servers WHERE id = $1 AND is_exit_node = TRUE`, id))
}

func listExitNodesByStatus(ctx context.Context, statusFilter string) ([]ExitNodeTunnel, error) {
	// Defense-in-depth: only return servers whose lifecycle is 'online'.
	// tunnel_status alone is not enough — a server marked offline in the FSM
	// must never appear in xray routing (otherwise we get ghosts).
	query := `SELECT ` + exitNodeColumns + ` FROM servers WHERE is_exit_node = TRUE AND lifecycle_state = 'online'`
	var args []any
	if statusFilter != "" {
		query += ` AND tunnel_status = $1`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY id`

	rows, err := db.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list exit nodes: %w", err)
	}
	defer rows.Close()

	var nodes []ExitNodeTunnel
	for rows.Next() {
		n, err := scanExitNode(rows)
		if err != nil {
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

// UpdateTunnelStatusWithError updates tunnel status + stores a human-readable error reason.
func UpdateTunnelStatusWithError(serverID int, status string, latencyMs int, tunnelError string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		SET tunnel_status = $1, latency_ms = $2, tunnel_error = $3,
		    last_tunnel_check = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, status, latencyMs, tunnelError, serverID)
	return err
}
