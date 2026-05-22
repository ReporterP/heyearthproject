// Package biz — best-effort SSH teardown on exit-nodes.
//
// We never block hub-side cleanup on exit-node reachability. If the SSH
// connection fails within ~10 seconds we log a warning and the caller
// continues. This protects us from VPS-already-decommissioned scenarios.
package biz

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/heyearth/backend/internal/crypto"
	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/ssh"
)

const exitTeardownTimeout = 10 * time.Second

// TeardownExitNode SSH-es into the exit-node and stops xray, hysteria2-server,
// node-agent. Returns nil only on full success; on any partial failure or
// timeout, returns a wrapped error which the caller is expected to log as a
// warning (NOT abort hub-side cleanup).
func TeardownExitNode(serverID int) error {
	ctx, cancel := context.WithTimeout(context.Background(), exitTeardownTimeout)
	defer cancel()

	cfg, err := loadServerSSHConfig(serverID)
	if err != nil {
		return fmt.Errorf("load ssh config: %w", err)
	}

	addr := fmt.Sprintf("%s:%d", cfg.ip, cfg.port)
	dialer := net.Dialer{Timeout: exitTeardownTimeout}
	tcpConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	c, chans, reqs, err := ssh.NewClientConn(tcpConn, addr, cfg.sshConfig)
	if err != nil {
		_ = tcpConn.Close()
		return fmt.Errorf("ssh handshake: %w", err)
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	script := "systemctl stop xray hysteria2-server node-agent 2>&1; " +
		"echo TEARDOWN_OK"
	out, runErr := runRemoteCommand(client, script, exitTeardownTimeout)
	if runErr != nil {
		return fmt.Errorf("remote stop: %w (output=%s)", runErr, out)
	}
	if !bytes.Contains(out, []byte("TEARDOWN_OK")) {
		return fmt.Errorf("teardown did not complete cleanly: %s", out)
	}
	log.Printf("[exit-teardown] server %d (%s): services stopped OK", serverID, cfg.ip)
	return nil
}

type exitSSHConfig struct {
	ip        string
	port      int
	sshConfig *ssh.ClientConfig
}

// loadServerSSHConfig reads ssh credentials from the servers row and decrypts the password.
// Mirrors sshDialForServer() in internal/admin but lives in biz so deprovision can use it
// without depending on the admin package.
func loadServerSSHConfig(serverID int) (exitSSHConfig, error) {
	var (
		ip      string
		port    int
		user    string
		passEnc []byte
		hostKey string
	)
	err := db.DB.QueryRow(`
		SELECT ip_address, COALESCE(ssh_port, 22), COALESCE(ssh_user, 'root'),
		       COALESCE(ssh_password_enc, ''::bytea), COALESCE(ssh_host_key, '')
		  FROM servers WHERE id=$1
	`, serverID).Scan(&ip, &port, &user, &passEnc, &hostKey)
	if err != nil {
		return exitSSHConfig{}, err
	}
	password, err := crypto.Decrypt(passEnc)
	if err != nil {
		return exitSSHConfig{}, fmt.Errorf("decrypt ssh password: %w", err)
	}
	hostKeyCb := ssh.InsecureIgnoreHostKey()
	if hostKey != "" {
		pub, _, _, _, perr := ssh.ParseAuthorizedKey([]byte(hostKey))
		if perr == nil {
			hostKeyCb = ssh.FixedHostKey(pub)
		}
	}
	return exitSSHConfig{
		ip:   ip,
		port: port,
		sshConfig: &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: hostKeyCb,
			Timeout:         exitTeardownTimeout,
		},
	}, nil
}

// runRemoteCommand runs a single command and returns combined stdout+stderr.
func runRemoteCommand(client *ssh.Client, cmd string, timeout time.Duration) ([]byte, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = &buf

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case err := <-done:
		return buf.Bytes(), err
	case <-time.After(timeout):
		_ = session.Signal(ssh.SIGKILL)
		return buf.Bytes(), fmt.Errorf("ssh command timeout after %s", timeout)
	}
}
