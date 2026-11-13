package biz

import (
	"context"
	"fmt"
	"log"

	"github.com/heyearth/backend/internal/db"
)

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
	// FallbackEnabled is servers.tunnel_fallback_enabled (migration 000061, NOT NULL DEFAULT true):
	// the owner's switch of a Hysteria exit's TCP fallback path exit-<id>.vr (cmd/api exitHasVRPath).
	FallbackEnabled bool
}

// exitNodeColumns is the select list scanExitNode reads.
const exitNodeColumns = `id, COALESCE(name, ''), ip_address,
	       COALESCE(tunnel_protocol, ''), COALESCE(tunnel_port, 0),
	       COALESCE(tunnel_uuid, ''), COALESCE(tunnel_public_key, ''),
	       COALESCE(tunnel_short_id, ''), COALESCE(tunnel_sni, ''),
	       COALESCE(tunnel_ss_method, ''), COALESCE(tunnel_ss_password, ''),
	       COALESCE(tunnel_status, 'unknown'), COALESCE(load_weight, 1), lifecycle_state,
	       tunnel_fallback_enabled`

func scanExitNode(row interface{ Scan(dest ...any) error }) (ExitNodeTunnel, error) {
	var n ExitNodeTunnel
	err := row.Scan(&n.ID, &n.Name, &n.IPAddress, &n.TunnelProtocol, &n.TunnelPort,
		&n.TunnelUUID, &n.TunnelPubKey, &n.TunnelShortID, &n.TunnelSNI,
		&n.TunnelSSMethod, &n.TunnelSSPass, &n.TunnelStatus, &n.LoadWeight, &n.LifecycleState,
		&n.FallbackEnabled)
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

// updateTunnelStatusIfQuery writes the tunnel monitor's verdict only while tunnel_status is still
// what the monitor read ($6, through the same COALESCE as exitNodeColumns).
const updateTunnelStatusIfQuery = `
	UPDATE servers
	SET tunnel_status = $1, latency_ms = $2, tunnel_error = $3,
	    last_tunnel_check = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
	WHERE id = $4 AND COALESCE(tunnel_status, 'unknown') = $5`

// UpdateTunnelStatusIfUnchanged is UpdateTunnelStatusWithError for the tunnel monitor, which reads
// the status, probes for up to 10 s per exit and only then writes: it writes only if tunnel_status
// is still old. written is false when someone else changed it meanwhile (the node wizard, a
// re-setup): their write stands, the monitor starts over from it on its next tick.
func UpdateTunnelStatusIfUnchanged(serverID int, old, status string, latencyMs int, tunnelError string) (written bool, err error) {
	res, err := db.DB.Exec(updateTunnelStatusIfQuery, status, latencyMs, tunnelError, serverID, old)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
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
