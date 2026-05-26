// server/backend/cmd/api/awg_sync.go
package main

// AmneziaWG (Palma) server config sync.
//
// Architecture: amneziawg-go runs as its own systemd unit (awg.service) on
// the Hub, listening UDP :51820. Per-user keys + CPS obfuscation params are
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
	"encoding/json"
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
	awgListenPort = 51820
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

	rows, err := db.DB.Query(`
		SELECT u.awg_public_key,
		       COALESCE(u.awg_preshared_key, ''),
		       COALESCE(u.awg_cps_params, '{}'::jsonb)::text,
		       u.awg_ip_address
		FROM users u
		JOIN user_subscriptions us ON us.user_id = u.id
		JOIN plans p ON p.id = us.plan_id
		WHERE p.amnezia_wg_enabled = TRUE
		  AND us.status = 'active'
		  AND us.valid_until > NOW()
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
		var cpsJSON string
		var ip sql.NullString
		if err := rows.Scan(&p.PublicKey, &p.PresharedKey, &cpsJSON, &ip); err != nil {
			return fmt.Errorf("scan peer row: %w", err)
		}
		if !ip.Valid || ip.String == "" {
			continue
		}
		p.AllowedIP = ip.String
		p.CPS = parseAWGCPSParams(cpsJSON)
		peers = append(peers, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate peer rows: %w", err)
	}

	conf := renderAWGConfig(serverPriv, peers)

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

	// Layered reload: wg syncconf (zero-downtime) → systemctl restart (cold-start).
	if out, err := exec.Command("wg", "syncconf", awgInterface, awgConfigPath).CombinedOutput(); err != nil {
		log.Printf("[awg-sync] wg syncconf failed (%v: %s) — falling back to systemctl restart awg",
			err, strings.TrimSpace(string(out)))
		if out2, err2 := exec.Command("systemctl", "restart", "awg").CombinedOutput(); err2 != nil {
			return fmt.Errorf("restart awg: %w (%s)", err2, strings.TrimSpace(string(out2)))
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

type awgPeer struct {
	PublicKey    string
	PresharedKey string
	AllowedIP    string
	CPS          awgCPS
}

type awgCPS struct {
	Jc   int
	Jmin int
	Jmax int
	S1   int
	S2   int
	H1   int
	H2   int
	H3   int
	H4   int
}

// parseAWGCPSParams pulls obfuscation fields from JSONB blob. Defaults on malformed.
//
// Note: biz.AWGCPSParams stores H1-H4 as uint32; this sync sees them via JSON which
// may round-trip them as numbers. We accept int (Go default JSON number type) which
// matches the wider range needed for full uint32 values (mod 2^31 since Go int on
// amd64 is 64-bit signed, so positive uint32 fits without overflow).
func parseAWGCPSParams(jsonText string) awgCPS {
	def := awgCPS{
		Jc: 4, Jmin: 50, Jmax: 1000,
		S1: 0, S2: 0,
		H1: 1, H2: 2, H3: 3, H4: 4,
	}
	if jsonText == "" || jsonText == "{}" {
		return def
	}
	// Map field tag (lowercase from biz/awg.go json tags) to int.
	var m map[string]int
	if err := json.Unmarshal([]byte(jsonText), &m); err != nil {
		log.Printf("[awg-sync] parse cps params: %v (using defaults)", err)
		return def
	}
	get := func(key string, fallback int) int {
		if v, ok := m[key]; ok {
			return v
		}
		return fallback
	}
	// biz/awg.go uses lowercase json tags: jc, jmin, jmax, s1, s2, h1, h2, h3, h4
	return awgCPS{
		Jc:   get("jc", def.Jc),
		Jmin: get("jmin", def.Jmin),
		Jmax: get("jmax", def.Jmax),
		S1:   get("s1", def.S1),
		S2:   get("s2", def.S2),
		H1:   get("h1", def.H1),
		H2:   get("h2", def.H2),
		H3:   get("h3", def.H3),
		H4:   get("h4", def.H4),
	}
}

// renderAWGConfig produces awg0.conf content. Deterministic ordering.
func renderAWGConfig(serverPriv string, peers []awgPeer) string {
	var b strings.Builder
	b.WriteString("# /opt/heyearth/awg/awg0.conf\n")
	b.WriteString("# Generated by heyearth-api awg_sync.go. DO NOT EDIT BY HAND.\n")
	b.WriteString("[Interface]\n")
	fmt.Fprintf(&b, "PrivateKey = %s\n", serverPriv)
	fmt.Fprintf(&b, "ListenPort = %d\n", awgListenPort)
	fmt.Fprintf(&b, "Address = %s\n", awgServerIP)
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
		fmt.Fprintf(&b, "Jc = %d\n", p.CPS.Jc)
		fmt.Fprintf(&b, "Jmin = %d\n", p.CPS.Jmin)
		fmt.Fprintf(&b, "Jmax = %d\n", p.CPS.Jmax)
		fmt.Fprintf(&b, "S1 = %d\n", p.CPS.S1)
		fmt.Fprintf(&b, "S2 = %d\n", p.CPS.S2)
		fmt.Fprintf(&b, "H1 = %d\n", p.CPS.H1)
		fmt.Fprintf(&b, "H2 = %d\n", p.CPS.H2)
		fmt.Fprintf(&b, "H3 = %d\n", p.CPS.H3)
		fmt.Fprintf(&b, "H4 = %d\n", p.CPS.H4)
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
