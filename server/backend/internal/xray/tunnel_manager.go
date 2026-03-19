package xray

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// TunnelManager manages persistent tunnels to exit nodes and the main Xray process.
type TunnelManager struct {
	mu         sync.Mutex
	configPath string
	xrayCmd    *exec.Cmd
	running    bool
}

// NewTunnelManager creates a new tunnel manager.
func NewTunnelManager(configPath string) *TunnelManager {
	return &TunnelManager{configPath: configPath}
}

// SyncTunnels reads active exit nodes from DB, regenerates Xray config, and reloads.
func (tm *TunnelManager) SyncTunnels() error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	exitNodes, err := loadExitNodesFromDB()
	if err != nil {
		return fmt.Errorf("load exit nodes: %w", err)
	}

	maskingRules, err := loadMaskingRulesFromDB()
	if err != nil {
		return fmt.Errorf("load masking rules: %w", err)
	}

	opts := HubConfigOptions{
		ServerDomain:  os.Getenv("SITE_DOMAIN"),
		VLESSPort:     443,
		SOCKS5Port:    1080,
		HTTPProxyPort: 8080,
		ExitNodes:     exitNodes,
		MaskingRules:  maskingRules,
	}

	cfg, err := GenerateHubConfig(opts)
	if err != nil {
		return fmt.Errorf("generate config: %w", err)
	}

	configJSON, err := cfg.ToJSON()
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}

	if err := os.WriteFile(tm.configPath, configJSON, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	// Update tunnel status for all exit nodes
	for _, node := range exitNodes {
		updateTunnelStatus(node.ID, "configured")
	}

	log.Printf("[xray] synced %d exit nodes, %d masking rules", len(exitNodes), len(maskingRules))
	return nil
}

// CheckTunnelHealth verifies each exit node tunnel is alive.
func (tm *TunnelManager) CheckTunnelHealth() {
	exitNodes, err := loadExitNodesFromDB()
	if err != nil {
		log.Printf("[xray] health check: load nodes: %v", err)
		return
	}

	for _, node := range exitNodes {
		status := "healthy"
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.Port)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			status = "unhealthy"
			log.Printf("[xray] health check failed for node %d (%s): %v", node.ID, addr, err)
		} else {
			conn.Close()
		}
		updateTunnelStatus(node.ID, status)
	}
}

// StartPeriodicHealthCheck runs health checks every interval.
func (tm *TunnelManager) StartPeriodicHealthCheck(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			tm.CheckTunnelHealth()
		}
	}()
}

// ─────────────────────── DB Helpers ───────────────────────

func loadExitNodesFromDB() ([]ExitNode, error) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(name, '')
		FROM servers
		WHERE is_exit_node = TRUE AND status = 'online'
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []ExitNode
	for rows.Next() {
		var node ExitNode
		var name string
		if err := rows.Scan(&node.ID, &node.IPAddress, &name); err != nil {
			return nil, err
		}
		node.Port = 443
		node.Tag = fmt.Sprintf("exit-%d", node.ID)
		// UUID is derived from node ID for simplicity
		// In production, this should be stored in the DB or generated per-node
		node.UUID = fmt.Sprintf("00000000-0000-4000-8000-%012d", node.ID)
		nodes = append(nodes, node)
	}
	return nodes, rows.Err()
}

func loadMaskingRulesFromDB() ([]MaskingRule, error) {
	rows, err := db.DB.Query(`
		SELECT source_domain, mask_domain
		FROM traffic_masking_rules
		WHERE is_active = TRUE
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []MaskingRule
	for rows.Next() {
		var rule MaskingRule
		if err := rows.Scan(&rule.SourceDomain, &rule.MaskDomain); err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

func updateTunnelStatus(serverID int, status string) {
	_, _ = db.DB.Exec(`
		UPDATE servers
		SET tunnel_status = $1, last_tunnel_check = CURRENT_TIMESTAMP
		WHERE id = $2
	`, status, serverID)
}
