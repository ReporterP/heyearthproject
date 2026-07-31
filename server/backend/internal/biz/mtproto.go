// Package biz — MTProto-proxy helpers: balancer (PickMTProtoExit),
// install-state queries (ListExitsNeedingMTProto), status mutation
// (MarkMTProtoStatus), and the SSH-based installer (InstallMTProtoOnExit).
//
// "Healthy" definition: is_exit_node=TRUE AND lifecycle_state='online' AND
// mtproto_status='healthy'. The Tunnel-health-monitor maintains the status
// via per-server TCP probe on the global mtproto_port.
package biz

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

// MTProtoFrontingDomain returns the configured fake-TLS fronting domain.
//
// The domain is hex-encoded into the mtg secret, and Telegram sends it as the
// TLS SNI of the fake-TLS ClientHello. Clients that sniff TLS (Happ/sing-box/
// Xray with destOverride) REPLACE the connection destination with this domain,
// which drops the proxy IP out of the flow — the DirectIp exception can no
// longer match and the request ends up dialled against the real domain on the
// proxy port. So the domain MUST resolve to the MTProto exit node(s); if it
// does not, the proxy is unreachable for those clients while every server-side
// check (daemon up, port listening, TCP probe) still reports healthy.
func MTProtoFrontingDomain() string {
	return strings.TrimSpace(GetSettingOrDefault("mtproto_fronting_domain", ""))
}

// BuildMTProtoSecret assembles an mtg fake-TLS secret: the "ee" marker, 16
// random bytes, and the hex-encoded fronting domain — the exact layout mtg's
// own `generate-secret --hex <domain>` produces.
func BuildMTProtoSecret(domain string) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return "", fmt.Errorf("fronting domain is empty")
	}
	buf := make([]byte, 16)
	if _, err := crand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random secret: %w", err)
	}
	return "ee" + hex.EncodeToString(buf) + hex.EncodeToString([]byte(domain)), nil
}

// MTProtoExit — minimal row returned by the picker.
type MTProtoExit struct {
	ID         int
	IPAddress  string
	LoadWeight int
}

// PickMTProtoExit returns a weighted-random healthy exit's host:port to use
// for a Telegram MTProto deeplink. `ok` is false if no healthy exit exists.
//
// Port comes from app_settings.mtproto_port (default 8443) — same on every exit.
func PickMTProtoExit() (host, port string, ok bool) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(load_weight, 1)
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
	`)
	if err != nil {
		log.Printf("[mtproto] PickMTProtoExit query: %v", err)
		return "", "", false
	}
	defer rows.Close()

	var pool []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight); err != nil {
			continue
		}
		w := ex.LoadWeight
		if w < 1 {
			w = 1
		}
		for i := 0; i < w; i++ {
			pool = append(pool, ex)
		}
	}
	if len(pool) == 0 {
		return "", "", false
	}
	chosen := pool[rand.Intn(len(pool))]
	port = GetSettingOrDefault("mtproto_port", "8443")
	return chosen.IPAddress, port, true
}

// ListMTProtoExitIPs returns IP addresses of all exit-nodes currently running
// a healthy mtproto-proxy daemon. Used by the subscription handler to build
// the "DirectIp" rule in the Happ routing profile so Telegram's connections
// to these IPs bypass the VPN tunnel (avoiding the VPN-traffic-loops-to-itself
// problem when the user has both VPN and MTProto-proxy enabled).
func ListMTProtoExitIPs() ([]string, error) {
	rows, err := db.DB.Query(`
		SELECT ip_address
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_status = 'healthy'
		 ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list mtproto exit ips: %w", err)
	}
	defer rows.Close()
	var ips []string
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			continue
		}
		if ip != "" {
			ips = append(ips, ip)
		}
	}
	return ips, rows.Err()
}

// ListExitsNeedingMTProto returns online exits that have no daemon installed
// yet (mtproto_installed_at IS NULL). Used by Ghost Reconciler.
func ListExitsNeedingMTProto() ([]MTProtoExit, error) {
	rows, err := db.DB.Query(`
		SELECT id, ip_address, COALESCE(load_weight, 1)
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_installed_at IS NULL
		   AND mtproto_status != 'installing'
	`)
	if err != nil {
		return nil, fmt.Errorf("list exits needing mtproto: %w", err)
	}
	defer rows.Close()

	var out []MTProtoExit
	for rows.Next() {
		var ex MTProtoExit
		if err := rows.Scan(&ex.ID, &ex.IPAddress, &ex.LoadWeight); err != nil {
			continue
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

// MarkMTProtoStatus sets mtproto_status and updates last_check timestamp.
// mtproto_installed_at is set to NOW() ONLY on first 'healthy' observation —
// preserves retry semantics: if install never succeeded, installed_at stays
// NULL and the reconciler keeps retrying until success.
func MarkMTProtoStatus(serverID int, status string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		   SET mtproto_status = $1,
		       mtproto_last_check = NOW(),
		       mtproto_installed_at = COALESCE(mtproto_installed_at,
		                                       CASE WHEN $1 = 'healthy' THEN NOW() END),
		       updated_at = NOW()
		 WHERE id = $2
	`, status, serverID)
	return err
}

// InstallMTProtoOnExit SSHes into the exit-node and runs install-mtproxy.sh
// with the shared secret. Caller already set mtproto_status='installing'.
// Returns nil on success; on error caller should MarkMTProtoStatus(id, 'unhealthy').
//
// The script source is uploaded fresh every call (it's tiny) — keeps the exit
// always running the current version of the installer.
func InstallMTProtoOnExit(serverID int, ipAddress, sshUser, sshPassword string, sshPort int, scriptBytes []byte) error {
	port := GetSettingOrDefault("mtproto_port", "8443")
	secret := GetSettingOrDefault("mtproto_secret", "")
	if secret == "" {
		return fmt.Errorf("mtproto_secret is empty in app_settings")
	}
	frontingDomain := MTProtoFrontingDomain()
	if frontingDomain == "" {
		return fmt.Errorf("mtproto_fronting_domain is empty in app_settings — " +
			"set a domain whose A record points at the exit nodes (see 000054 migration)")
	}

	cfg := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.Password(sshPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", ipAddress, sshPort)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", addr, err)
	}
	defer client.Close()

	uploadSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh upload session: %w", err)
	}
	defer uploadSession.Close()

	b64Cmd := exec.Command("base64", "-w0")
	b64Cmd.Stdin = bytes.NewReader(scriptBytes)
	var b64Out bytes.Buffer
	b64Cmd.Stdout = &b64Out
	if err := b64Cmd.Run(); err != nil {
		return fmt.Errorf("base64 local: %w", err)
	}
	uploadCmd := fmt.Sprintf(
		"echo '%s' | base64 -d > /tmp/install-mtproxy.sh && chmod +x /tmp/install-mtproxy.sh",
		b64Out.String(),
	)
	if err := uploadSession.Run(uploadCmd); err != nil {
		return fmt.Errorf("ssh upload exec: %w", err)
	}

	runSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh run session: %w", err)
	}
	defer runSession.Close()
	var stdout, stderr bytes.Buffer
	runSession.Stdout = &stdout
	runSession.Stderr = &stderr
	// First-time install on a slow VPS = apt install + git clone + make on 2 vCPU,
	// can take 8-10 min. Subsequent runs are seconds (script idempotently skips build).
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runSession.Run(fmt.Sprintf("bash /tmp/install-mtproxy.sh %s %s %s",
			port, secret, frontingDomain))
	}()
	select {
	case err := <-done:
		if err != nil {
			// Capture TAIL of each stream — the failure happens at the end, so
			// last 4KB is what matters. set -x stderr is long; first lines are
			// just dependency-install noise.
			tail := func(s string, n int) string {
				if len(s) > n {
					return "...[head-truncated]" + s[len(s)-n:]
				}
				return s
			}
			return fmt.Errorf("install-mtproxy script failed: %w; stdout=%q stderr=%q",
				err, tail(stdout.String(), 4000), tail(stderr.String(), 4000))
		}
	case <-ctx.Done():
		return fmt.Errorf("install-mtproxy timeout after 12min")
	}

	log.Printf("[mtproto] InstallMTProtoOnExit server=%d (%s): success", serverID, ipAddress)
	return nil
}
