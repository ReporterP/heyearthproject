package main

import (
	"fmt"
	"strings"

	"github.com/heyearth/backend/internal/config"
	"golang.org/x/crypto/ssh"
)

// exitXrayGuardRules are the first routing rules of the exit's xray config (exitNodeSetupScript):
// the resilience tunnels (SS+WS, VLESS Reality) refuse the exit's loopback, private neighbours
// and its own address like the hysteria ACL does, the MTProxy port on the own address excepted.
func exitXrayGuardRules(exitIP string, mtprotoPort int) string {
	return strings.ReplaceAll(config.ExitXrayGuardRules(exitIP, "direct", "block", mtprotoPort), "\n", "\n      ")
}

// hubMTProtoPort reads app_settings.mtproto_port on the Hub (psql as postgres, peer auth); the
// default when it is absent or unreadable.
func hubMTProtoPort(hub *ssh.Client) int {
	out, err := runSSHQuiet(hub, `runuser -u postgres -- psql -d heyearth -tAc "SELECT value FROM app_settings WHERE key='mtproto_port'"`, "")
	if err != nil {
		fmt.Printf("[exit-node] WARN: mtproto_port не прочитан (%v) — использую %d\n", err, config.DefaultMTProtoPort)
	}
	return config.ParseMTProtoPort(out)
}

// exitTunnelCreds are the tunnel fields --setup-exit-node writes to servers.
type exitTunnelCreds struct {
	UUID, PublicKey, ShortID, SSPassword, Protocol string
	Port                                           int
	SNI                                            string
}

type exitDBAction int

const (
	exitDBKeep   exitDBAction = iota // the row already carries these credentials: nothing to write
	exitDBUpdate                     // the row exists with other credentials
	exitDBInsert                     // no row for this exit yet
)

// decideExitDBAction: a re-run on an unchanged exit writes nothing (no tunnel_status reset).
func decideExitDBAction(cur exitTunnelCreds, found bool, want exitTunnelCreds) exitDBAction {
	switch {
	case !found:
		return exitDBInsert
	case cur == want:
		return exitDBKeep
	default:
		return exitDBUpdate
	}
}

// exitDBNeedsResync: heyearth-api is restarted (xray resync, tunnel re-check) only when the
// tunnel row was written.
func exitDBNeedsResync(a exitDBAction) bool { return a != exitDBKeep }
