// Package biz — hub-side provisioner.
//
// heyearth-api runs ON the hub as root, so we use os/exec for systemd
// operations and ioutil for file writes. Every function is idempotent
// (checks current state before acting) so retrying an interrupted flow is safe.
//
// All artifacts (.service files, .yaml configs, archives) live under well-known
// paths so the Ghost Reconciler can scan them.
package biz

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/heyearth/backend/internal/config"
)

const (
	hubHysteriaConfigDir = "/opt/heyearth/hysteria"
	hubSystemdDir        = "/etc/systemd/system"
	hubArchiveBase       = "/opt/heyearth/state/server-archive"
)

// HysteriaClientConfig is the data needed to render the client.yaml template.
type HysteriaClientConfig struct {
	ServerID  int
	ExitIP    string
	ExitPort  int    // Hysteria2 server port range, usually "20000-50000" passed via ExitPortRange
	AuthKey   string // matches Hysteria2 server `auth.password`
	SocksPort int    // local SOCKS5 listener (1088 + ServerID)
	CertPin   string // servers.tunnel_cert_sha256: the exit's certificate (config.HubHysteriaClientYAML)
}

// hysteriaClientYAML returns the rendered config for a given server: the shared template
// config.HubHysteriaClientYAML (port hopping, hopInterval, client-side ACL), which deploy
// --setup-exit-node writes as well.
func hysteriaClientYAML(c HysteriaClientConfig) string {
	return config.HubHysteriaClientYAML(c.ServerID, c.ExitIP, c.AuthKey, c.SocksPort, c.CertPin)
}

// hysteriaServiceUnit returns the systemd unit for hysteria2-client-<id>.service (shared
// template config.HubHysteriaClientUnit).
func hysteriaServiceUnit(serverID int, exitIP string) string {
	return config.HubHysteriaClientUnit(serverID, exitIP)
}

// WriteHysteriaClientConfig writes /opt/heyearth/hysteria/client-<id>.yaml.
// Idempotent — overwrites any existing file. Mode 0600 (contains auth secret).
func WriteHysteriaClientConfig(c HysteriaClientConfig) error {
	if err := os.MkdirAll(hubHysteriaConfigDir, 0o755); err != nil {
		return fmt.Errorf("mkdir hysteria dir: %w", err)
	}
	path := filepath.Join(hubHysteriaConfigDir, fmt.Sprintf("client-%d.yaml", c.ServerID))
	if err := os.WriteFile(path, []byte(hysteriaClientYAML(c)), 0o600); err != nil {
		return fmt.Errorf("write client-%d.yaml: %w", c.ServerID, err)
	}
	return nil
}

// InstallSystemdUnit writes /etc/systemd/system/hysteria2-client-<id>.service
// and runs `systemctl daemon-reload`. Idempotent.
func InstallSystemdUnit(serverID int, exitIP string) error {
	unitName := fmt.Sprintf("hysteria2-client-%d.service", serverID)
	path := filepath.Join(hubSystemdDir, unitName)
	if err := os.WriteFile(path, []byte(hysteriaServiceUnit(serverID, exitIP)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", unitName, err)
	}
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("daemon-reload: %w", err)
	}
	return nil
}

// EnableAndStartUnit enables + starts hysteria2-client-<id>. Idempotent.
func EnableAndStartUnit(serverID int) error {
	unitName := fmt.Sprintf("hysteria2-client-%d", serverID)
	if err := exec.Command("systemctl", "enable", unitName).Run(); err != nil {
		return fmt.Errorf("enable %s: %w", unitName, err)
	}
	if err := exec.Command("systemctl", "restart", unitName).Run(); err != nil {
		return fmt.Errorf("restart %s: %w", unitName, err)
	}
	time.Sleep(2 * time.Second)
	if err := exec.Command("systemctl", "is-active", "--quiet", unitName).Run(); err != nil {
		return fmt.Errorf("%s did not become active", unitName)
	}
	return nil
}

// StopAndDisableUnit stops + disables hysteria2-client-<id>. Idempotent (no error
// if already stopped/disabled or if the unit does not exist).
func StopAndDisableUnit(serverID int) error {
	unitName := fmt.Sprintf("hysteria2-client-%d", serverID)
	if exec.Command("systemctl", "is-active", "--quiet", unitName).Run() == nil {
		_ = exec.Command("systemctl", "stop", unitName).Run()
	}
	if exec.Command("systemctl", "is-enabled", "--quiet", unitName).Run() == nil {
		_ = exec.Command("systemctl", "disable", unitName).Run()
	}
	return nil
}

// ArchiveServerArtefacts moves the .service + .yaml + last 100 journal lines
// to /opt/heyearth/state/server-archive/<id>-<unix-ts>/.
// Returns the archive directory path. Idempotent — if files are already gone,
// it just creates the directory and writes the journal snapshot.
func ArchiveServerArtefacts(serverID int) (string, error) {
	ts := time.Now().Unix()
	archiveDir := filepath.Join(hubArchiveBase, fmt.Sprintf("%d-%d", serverID, ts))
	if err := os.MkdirAll(archiveDir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir archive: %w", err)
	}
	yamlSrc := filepath.Join(hubHysteriaConfigDir, fmt.Sprintf("client-%d.yaml", serverID))
	yamlDst := filepath.Join(archiveDir, fmt.Sprintf("client-%d.yaml.bak", serverID))
	if _, err := os.Stat(yamlSrc); err == nil {
		_ = os.Rename(yamlSrc, yamlDst)
	}
	svcSrc := filepath.Join(hubSystemdDir, fmt.Sprintf("hysteria2-client-%d.service", serverID))
	svcDst := filepath.Join(archiveDir, fmt.Sprintf("hysteria2-client-%d.service.bak", serverID))
	if _, err := os.Stat(svcSrc); err == nil {
		_ = os.Rename(svcSrc, svcDst)
	}
	jLog := filepath.Join(archiveDir, "journal-snapshot.log")
	cmd := exec.Command("journalctl", "-u", fmt.Sprintf("hysteria2-client-%d", serverID), "-n", "100", "--no-pager")
	out, _ := cmd.CombinedOutput()
	_ = os.WriteFile(jLog, out, 0o644)
	return archiveDir, nil
}

// RemoveSystemdUnitFile removes the .service file (after stop+disable+archive)
// and runs daemon-reload. Idempotent.
func RemoveSystemdUnitFile(serverID int) error {
	unitPath := filepath.Join(hubSystemdDir, fmt.Sprintf("hysteria2-client-%d.service", serverID))
	if _, err := os.Stat(unitPath); err == nil {
		if err := os.Remove(unitPath); err != nil {
			return fmt.Errorf("remove %s: %w", unitPath, err)
		}
	}
	return exec.Command("systemctl", "daemon-reload").Run()
}

// KillStaleProcess sends SIGKILL to any hysteria process whose -c argument
// points to client-<id>.yaml. Last-resort fallback if systemctl stop failed.
func KillStaleProcess(serverID int) {
	pattern := fmt.Sprintf("client-%d.yaml", serverID)
	out, err := exec.Command("pgrep", "-f", pattern).Output()
	if err != nil {
		return
	}
	for _, pidStr := range splitLines(string(out)) {
		_ = exec.Command("kill", "-9", pidStr).Run()
	}
}

// ListHubHysteriaUnits returns the server IDs whose hysteria2-client-<id>.service
// file currently exists on the hub. Used by the Ghost Reconciler.
func ListHubHysteriaUnits() ([]int, error) {
	pattern := filepath.Join(hubSystemdDir, "hysteria2-client-*.service")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	var ids []int
	for _, m := range matches {
		var id int
		_, err := fmt.Sscanf(filepath.Base(m), "hysteria2-client-%d.service", &id)
		if err != nil || id <= 0 {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
