// Package biz — Express (Hysteria2) adaptive tunnel decision.
//
// Decides, at every hysteria2 sync, whether the hub's hysteria2-server should
// egress via the shared per-exit SOCKS5 (when only one exit is healthy) or via
// a dedicated lowest-RTT SOCKS5 (when 2+ exits are healthy).
//
// Decision is persisted to app_settings.hysteria2_tunnel_mode for ops
// visibility (deploy --status surfaces it in T9).
package biz

import (
	"database/sql"
	"fmt"

	"github.com/heyearth/backend/internal/db"
)

const (
	// ExpressTunnelModeShared — only one healthy exit, hysteria2-server
	// uses the per-exit hysteria2-client-<id>.service that already serves Xray.
	ExpressTunnelModeShared = "shared"

	// ExpressTunnelModeDedicated — 2+ healthy exits. We pick the lowest-RTT
	// one and run hysteria2-client-express.service against it on SOCKS5 :1091.
	ExpressTunnelModeDedicated = "dedicated"

	// ExpressDedicatedSocksPort — fixed port for the dedicated client.
	// NOT 1088+id (those are owned by per-exit shared clients).
	ExpressDedicatedSocksPort = 1091
)

// ExpressTunnelDecision is everything SyncHysteria2Config needs to render the
// correct outbound block + (re)write the dedicated client systemd unit.
type ExpressTunnelDecision struct {
	Mode         string // "shared" | "dedicated"
	OutboundPort int    // 127.0.0.1:<port> for hysteria2-server `outbounds` block
	BestExitID   int    // 0 in shared mode if no exits at all
	BestExitIP   string // populated in dedicated mode (also populated in shared mode when N=1, for logging)
	BestExitRTT  int    // ms (informational)
}

// DecideExpressTunnel reads the active-healthy exit set, decides the mode,
// and persists the chosen mode + best-exit-id to app_settings.
//
// Selection rule:
//   - N=0 healthy exits → shared mode pointing at legacy port 1090 (no exit
//     to bind to; users see "no route" but VPN handshake holds).
//   - N=1 healthy exit  → shared mode at 1088+exitID (per-exit SOCKS that
//     hysteria2-client-<id>.service already owns).
//   - N≥2 healthy exits → dedicated mode at :1091, target = lowest-RTT exit.
//
// Resilience: COALESCE(last_rtt_ms, latency_ms, 9999) handles three cases —
// fresh probe → use last_rtt_ms; stale post-restart probe → fall back to
// latency_ms (overwritten on status flips); no data → sort as "worst" so
// such exits never win the dedicated selection over a measured peer.
func DecideExpressTunnel() (ExpressTunnelDecision, error) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(last_rtt_ms, latency_ms, 9999) AS rtt
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND COALESCE(tunnel_status, '') = 'healthy'
		 ORDER BY COALESCE(last_rtt_ms, latency_ms, 9999) ASC, id ASC
	`)
	if err != nil {
		return ExpressTunnelDecision{}, fmt.Errorf("query healthy exits: %w", err)
	}
	defer rows.Close()

	type exit struct {
		id  int
		ip  string
		rtt int
	}
	var exits []exit
	for rows.Next() {
		var e exit
		if scanErr := rows.Scan(&e.id, &e.ip, &e.rtt); scanErr == nil {
			exits = append(exits, e)
		}
	}

	var d ExpressTunnelDecision
	switch {
	case len(exits) == 0:
		// Edge case: no healthy exit. Fall back to shared mode pointing at a
		// nonexistent port (1090) — hysteria2-server's outbound will fail to
		// connect and users will see "no route", but the VPN handshake still
		// holds so they don't get bounced. Operator notices via deploy --status.
		d = ExpressTunnelDecision{
			Mode:         ExpressTunnelModeShared,
			OutboundPort: 1090, // intentionally legacy default, no exit to bind to
		}
	case len(exits) == 1:
		e := exits[0]
		d = ExpressTunnelDecision{
			Mode:         ExpressTunnelModeShared,
			OutboundPort: 1088 + e.id, // per-exit client SOCKS port already running
			BestExitID:   e.id,
			BestExitIP:   e.ip,
			BestExitRTT:  e.rtt,
		}
	default:
		// 2+ exits → dedicated tunnel to the lowest-RTT one.
		best := exits[0] // ORDER BY rtt ASC, id ASC ensured this is the winner
		d = ExpressTunnelDecision{
			Mode:         ExpressTunnelModeDedicated,
			OutboundPort: ExpressDedicatedSocksPort,
			BestExitID:   best.id,
			BestExitIP:   best.ip,
			BestExitRTT:  best.rtt,
		}
	}

	_ = SetSetting("hysteria2_tunnel_mode", d.Mode)
	_ = SetSetting("hysteria2_bound_exit_id", fmt.Sprintf("%d", d.BestExitID))
	_ = SetSetting("hysteria2_bound_exit_ip", d.BestExitIP)
	_ = SetSetting("hysteria2_bound_exit_rtt_ms", fmt.Sprintf("%d", d.BestExitRTT))
	return d, nil
}

// SelectExpressExit returns the lowest-RTT healthy exit (id, ip, rtt). Used by
// deploy --status and by the dedicated-tunnel writer to know the target.
// Returns a "no healthy exit available" error when none qualify.
func SelectExpressExit() (id int, ip string, rttMs int, err error) {
	err = db.DB.QueryRow(`
		SELECT id, ip_address, COALESCE(last_rtt_ms, latency_ms, 9999) AS rtt
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND COALESCE(tunnel_status, '') = 'healthy'
		 ORDER BY COALESCE(last_rtt_ms, latency_ms, 9999) ASC, id ASC
		 LIMIT 1
	`).Scan(&id, &ip, &rttMs)
	if err == sql.ErrNoRows {
		return 0, "", 0, fmt.Errorf("no healthy exit available")
	}
	return
}

// UpdateExitRTT writes the per-probe RTT into last_rtt_ms / last_rtt_at.
// Called by tunnel_monitor on every successful probe; ignored for failures
// (we want last_rtt_ms to reflect the most recent SUCCESSFUL handshake, so
// that exit selection isn't poisoned by transient failures).
func UpdateExitRTT(serverID int, rttMs int) error {
	if rttMs <= 0 {
		return nil
	}
	_, err := db.DB.Exec(`
		UPDATE servers
		   SET last_rtt_ms = $1,
		       last_rtt_at = CURRENT_TIMESTAMP
		 WHERE id = $2
	`, rttMs, serverID)
	return err
}
