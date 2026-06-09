// server/backend/cmd/api/awg_sync.go
package main

// AmneziaWG (Palma) server config sync.
//
// Architecture: amneziawg-go runs as its own systemd unit (awg.service) on
// the Hub, listening UDP :443. Per-user keys + CPS obfuscation params are
// stored in PostgreSQL (users.awg_public_key, users.awg_preshared_key,
// users.awg_cps_params, users.awg_ip_address). On every plan/subscription
// mutation, biz.NotifySubscriptionChange fans out to RequestAWGSync (wired
// in main.go alongside Xray + Hysteria2). We debounce 2s, then regenerate
// /opt/heyearth/awg/awg0.conf and apply via `wg syncconf` (no-downtime) or
// `systemctl restart awg` (cold-start fallback).
//
// Mirrors cmd/api/hysteria2_sync.go in structure, idempotence, and error
// reporting (app_settings.awg_last_error → admin UI).

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

const (
	awgConfigPath = "/opt/heyearth/awg/awg0.conf"
	awgConfigDir  = "/opt/heyearth/awg"
	awgInterface  = "awg0"
	awgListenPort = 443 // UDP 443 (QUIC-class): home/mobile ISPs rarely block it, unlike a high custom port. TCP 443 (Reality/nginx) is separate — no conflict.
	awgServerIP   = "10.66.66.1/24"
)

var (
	awgSyncMu     sync.Mutex
	awgDebounceMu sync.Mutex
	awgDebounce   *time.Timer
)

// RequestAWGSync debounces sync requests so a burst of plan/subscription
// changes results in a single config regeneration. 2s window matches Xray
// and Hysteria2 debounce.
func RequestAWGSync() {
	awgDebounceMu.Lock()
	defer awgDebounceMu.Unlock()
	if awgDebounce != nil {
		awgDebounce.Stop()
	}
	awgDebounce = time.AfterFunc(2*time.Second, func() {
		if err := SyncAWGConfig(); err != nil {
			log.Printf("[awg-sync] debounced sync error: %v", err)
			recordAWGSyncError(err)
		}
	})
}

// SyncAWGConfig regenerates /opt/heyearth/awg/awg0.conf and reloads the service.
// Idempotent: if the generated content matches what's on disk, no reload happens.
// Returns nil with a log message when the awg directory does not exist.
func SyncAWGConfig() error {
	awgSyncMu.Lock()
	defer awgSyncMu.Unlock()

	if _, err := os.Stat(awgConfigDir); os.IsNotExist(err) {
		return nil
	}

	serverPriv, _, err := biz.GetServerAWGKeys()
	if err != nil {
		return fmt.Errorf("get server awg keys: %w", err)
	}

	// Global obfuscation params for the [Interface] block. AmneziaWG CPS are
	// interface-level (same for server + every client), NOT per-peer.
	serverCPS, err := biz.GetServerCPSParams()
	if err != nil {
		return fmt.Errorf("get server cps: %w", err)
	}

	// traffic_limit enforcement: a peer over its subscription quota is excluded
	// from the rendered config, so the next sync drops it (NULL = unlimited).
	rows, err := db.DB.Query(`
		SELECT u.awg_public_key,
		       COALESCE(u.awg_preshared_key, ''),
		       u.awg_ip_address
		FROM users u
		JOIN user_subscriptions us ON us.user_id = u.id
		JOIN plans p ON p.id = us.plan_id
		WHERE p.amnezia_wg_enabled = TRUE
		  AND us.status = 'active'
		  AND us.valid_until > NOW()
		  AND (us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit)
		  AND u.awg_public_key IS NOT NULL
		  AND u.awg_public_key <> ''
		  AND u.awg_ip_address IS NOT NULL
	`)
	if err != nil {
		return fmt.Errorf("select awg peers: %w", err)
	}
	defer rows.Close()

	var peers []awgPeer
	for rows.Next() {
		var p awgPeer
		var ip sql.NullString
		if err := rows.Scan(&p.PublicKey, &p.PresharedKey, &ip); err != nil {
			return fmt.Errorf("scan peer row: %w", err)
		}
		if !ip.Valid || ip.String == "" {
			continue
		}
		p.AllowedIP = ip.String
		peers = append(peers, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate peer rows: %w", err)
	}

	conf := renderAWGConfig(serverPriv, serverCPS, peers)

	// No-op short-circuit: identical content → skip reload.
	if existing, err := os.ReadFile(awgConfigPath); err == nil {
		if bytes.Equal(existing, []byte(conf)) {
			return nil
		}
	}

	// Atomic write: tmp + rename.
	tmpPath := awgConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(conf), 0o640); err != nil {
		return fmt.Errorf("write tmp awg config: %w", err)
	}
	if err := os.Rename(tmpPath, awgConfigPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename awg config: %w", err)
	}
	if err := chownToAmneziaWG(awgConfigPath); err != nil {
		log.Printf("[awg-sync] chown awg config: %v (continuing)", err)
	}

	// Layered reload:
	//   1. `awg syncconf` (zero-downtime) — apply peer/CPS changes in place.
	//      MUST be `awg` not `wg`: the config carries CPS params (Jc/S1/H1…)
	//      vanilla wireguard-tools rejects. syncconf needs a stripped config
	//      (no [Interface] Address), produced by `awg-quick strip`. We strip to
	//      a temp file because os/exec runs no shell (no process substitution).
	//   2. Cold-start fallback: `awg-quick down/up` re-creates the interface
	//      with the full config (a bare `systemctl restart awg` only bounced the
	//      daemon without ever applying address/port/peers).
	if err := applyAWGConfig(); err != nil {
		log.Printf("[awg-sync] awg syncconf failed (%v) — cold-starting via awg-quick", err)
		if err2 := coldStartAWG(); err2 != nil {
			return fmt.Errorf("cold-start awg: %w", err2)
		}
	}

	// Bump config_version for admin UI / change detection.
	cur := biz.GetSettingOrDefault("awg_config_version", "1")
	if n, err := strconv.Atoi(cur); err == nil && n > 0 {
		_ = biz.SetSetting("awg_config_version", fmt.Sprintf("%d", n+1))
	}
	_ = biz.SetSetting("awg_last_sync_at", fmt.Sprintf("%d", time.Now().Unix()))
	_ = biz.SetSetting("awg_last_error", "")
	_ = biz.SetSetting("awg_peer_count", fmt.Sprintf("%d", len(peers)))

	log.Printf("[awg-sync] config updated: %d peers", len(peers))
	return nil
}

func recordAWGSyncError(err error) {
	if err == nil {
		return
	}
	_ = biz.SetSetting("awg_last_error", err.Error())
}

// awgStrippedPath holds the `awg-quick strip` output fed to `awg syncconf`.
const awgStrippedPath = "/opt/heyearth/awg/awg0.stripped"

// applyAWGConfig does a zero-downtime in-place reload: strip the full config to
// a syncconf-compatible form (no [Interface] Address), then apply via
// `awg syncconf`. `awg-quick strip` reads /etc/amnezia/amneziawg/awg0.conf
// (symlinked to awgConfigPath by --setup-awg).
func applyAWGConfig() error {
	stripped, err := exec.Command("awg-quick", "strip", awgInterface).Output()
	if err != nil {
		return fmt.Errorf("awg-quick strip: %w", err)
	}
	if err := os.WriteFile(awgStrippedPath, stripped, 0o640); err != nil {
		return fmt.Errorf("write stripped config: %w", err)
	}
	if out, err := exec.Command("awg", "syncconf", awgInterface, awgStrippedPath).CombinedOutput(); err != nil {
		return fmt.Errorf("awg syncconf: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// coldStartAWG fully re-creates the interface: awg-quick down (best-effort,
// the iface may already be absent) then up with the current config.
func coldStartAWG() error {
	_, _ = exec.Command("awg-quick", "down", awgInterface).CombinedOutput()
	if out, err := exec.Command("awg-quick", "up", awgInterface).CombinedOutput(); err != nil {
		return fmt.Errorf("awg-quick up: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type awgPeer struct {
	PublicKey    string
	PresharedKey string
	AllowedIP    string
}

// renderAWGConfig produces awg0.conf content. Deterministic ordering.
//
// CPS obfuscation params (Jc/Jmin/Jmax/S1/S2/H1-H4) go in [Interface] ONLY —
// they are global AmneziaWG settings shared by the server and every client.
// Emitting them per-[Peer] makes `awg setconf` reject the file with
// "Line unrecognized: Jc=…" (this was the bug that kept Palma from ever
// coming up). H1-H4 are uint32 in biz.AWGCPSParams; %d prints them fine.
func renderAWGConfig(serverPriv string, cps biz.AWGCPSParams, peers []awgPeer) string {
	var b strings.Builder
	b.WriteString("# /opt/heyearth/awg/awg0.conf\n")
	b.WriteString("# Generated by heyearth-api awg_sync.go. DO NOT EDIT BY HAND.\n")
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", serverPriv)
	fmt.Fprintf(&b, "ListenPort = %d\n", awgListenPort)
	fmt.Fprintf(&b, "Address = %s\n", awgServerIP)
	b.WriteString("MTU = 1280\n") // match client MTU; avoids AWG-over-UDP fragmentation stalls
	fmt.Fprintf(&b, "Jc = %d\n", cps.Jc)
	fmt.Fprintf(&b, "Jmin = %d\n", cps.Jmin)
	fmt.Fprintf(&b, "Jmax = %d\n", cps.Jmax)
	fmt.Fprintf(&b, "S1 = %d\n", cps.S1)
	fmt.Fprintf(&b, "S2 = %d\n", cps.S2)
	fmt.Fprintf(&b, "H1 = %d\n", cps.H1)
	fmt.Fprintf(&b, "H2 = %d\n", cps.H2)
	fmt.Fprintf(&b, "H3 = %d\n", cps.H3)
	fmt.Fprintf(&b, "H4 = %d\n", cps.H4)
	b.WriteString("\n")

	for _, p := range peers {
		b.WriteString("[Peer]\n")
		fmt.Fprintf(&b, "PublicKey = %s\n", p.PublicKey)
		if p.PresharedKey != "" {
			fmt.Fprintf(&b, "PresharedKey = %s\n", p.PresharedKey)
		}
		ip := p.AllowedIP
		if !strings.Contains(ip, "/") {
			ip = ip + "/32"
		}
		fmt.Fprintf(&b, "AllowedIPs = %s\n", ip)
		b.WriteString("\n")
	}

	return b.String()
}

// chownToAmneziaWG looks up amnezia-wg system user, sets file ownership. Best-effort.
func chownToAmneziaWG(path string) error {
	out, err := exec.Command("id", "-u", "amnezia-wg").CombinedOutput()
	if err != nil {
		return fmt.Errorf("lookup uid: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	uid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("parse uid: %w", err)
	}
	out, err = exec.Command("id", "-g", "amnezia-wg").CombinedOutput()
	if err != nil {
		return fmt.Errorf("lookup gid: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	gid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return fmt.Errorf("parse gid: %w", err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown: %w", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}

// StartAWGSyncLoop kicks off initial sync + 5-min periodic. Safe pre-install.
func StartAWGSyncLoop() {
	if _, err := os.Stat(awgConfigDir); os.IsNotExist(err) {
		log.Println("[awg-sync] /opt/heyearth/awg not present, sync loop disabled (run --setup-awg)")
		return
	}

	if err := SyncAWGConfig(); err != nil {
		log.Printf("[awg-sync] initial sync: %v", err)
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := SyncAWGConfig(); err != nil {
				log.Printf("[awg-sync] periodic sync: %v", err)
			}
		}
	}()

	log.Println("[awg-sync] background sync started (every 5 min)")
}
