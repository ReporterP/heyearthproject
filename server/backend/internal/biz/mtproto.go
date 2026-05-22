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
	"fmt"
	"log"
	"math/rand"
	"os/exec"
	"time"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

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
// If status == 'healthy' or 'unhealthy', mtproto_installed_at is set to NOW()
// when previously NULL (first successful install / first observation).
func MarkMTProtoStatus(serverID int, status string) error {
	_, err := db.DB.Exec(`
		UPDATE servers
		   SET mtproto_status = $1,
		       mtproto_last_check = NOW(),
		       mtproto_installed_at = COALESCE(mtproto_installed_at,
		                                       CASE WHEN $1 IN ('healthy','unhealthy') THEN NOW() END),
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runSession.Run(fmt.Sprintf("bash /tmp/install-mtproxy.sh %s %s", port, secret)) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("install-mtproxy script failed: %w; stderr=%s", err, stderr.String())
		}
	case <-ctx.Done():
		return fmt.Errorf("install-mtproxy timeout after 5min")
	}

	log.Printf("[mtproto] InstallMTProtoOnExit server=%d (%s): success", serverID, ipAddress)
	return nil
}
