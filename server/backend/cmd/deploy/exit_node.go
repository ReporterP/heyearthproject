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
