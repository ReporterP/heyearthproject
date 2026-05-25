package main

// Hysteria2 server sync — generates /etc/hysteria/server.yaml from current
// plan/user state and reloads the local hysteria2-server.service.
//
// Architecture decision: Hysteria2 runs as its own systemd service alongside
// Xray. We do NOT try to host QUIC inside Xray (its QUIC stack is weak); we
// shell out to the official `hysteria` binary and let it own the UDP listen,
// Salamander obfuscation, and Brutal congestion control.
//
// Reload mechanism: write to .tmp, validate with `hysteria server --disable-update-check
// --config <file>` (2s timeout, treat bind-conflict on :50443 as success — the
// running service holding the port means parse+TLS+auth init all passed before
// the bind step), atomic rename only on success, then `systemctl restart
// hysteria2-server`. Restart (not SIGHUP reload) — hysteria v2.6.x treats SIGHUP
// as terminate.
//
// Per-plan Brutal targets: Hysteria2 accepts global bandwidth caps in the
// server config; per-user enforcement happens via the auth-backend response
// (extended later) or by maximum-of-plan-targets here. For the initial release
// we set the global cap to the highest enabled plan limit so the server doesn't
// throttle premium tiers below their advertised speed.

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

const (
	hysteria2ConfigPath = "/etc/hysteria/server.yaml"
	hysteria2TLSCert    = "/etc/letsencrypt/live/hiearth.ru/fullchain.pem"
	hysteria2TLSKey     = "/etc/letsencrypt/live/hiearth.ru/privkey.pem"
	hysteria2Listen     = ":50443"
	hysteria2AuthURL    = "http://127.0.0.1:8000/internal/hysteria2/auth"
)

var (
	hysteria2SyncMu     sync.Mutex
	hysteria2DebounceMu sync.Mutex
	hysteria2Debounce   *time.Timer
)

// RequestHysteria2Sync debounces sync requests so a burst of plan/subscription
// changes results in a single config regeneration.
func RequestHysteria2Sync() {
	hysteria2DebounceMu.Lock()
	defer hysteria2DebounceMu.Unlock()
	if hysteria2Debounce != nil {
		hysteria2Debounce.Stop()
	}
	hysteria2Debounce = time.AfterFunc(2*time.Second, func() {
		if err := SyncHysteria2Config(); err != nil {
			log.Printf("[hysteria2-sync] debounced sync error: %v", err)
			recordHysteria2SyncError(err)
		}
	})
}

// SyncHysteria2Config regenerates /etc/hysteria/server.yaml and reloads the service.
// Idempotent: if the generated content matches what's on disk, no reload happens.
// Returns nil with a log message when hysteria isn't installed (dev/staging without
// the binary just don't sync — Xray Reality keeps working).
func SyncHysteria2Config() error {
	hysteria2SyncMu.Lock()
	defer hysteria2SyncMu.Unlock()

	if _, err := exec.LookPath("hysteria"); err != nil {
		// hysteria binary not on PATH → not installed. Don't fail the api process.
		return nil
	}
	if _, err := os.Stat("/etc/hysteria"); os.IsNotExist(err) {
		return nil
	}

	// Aggregate Express-enabled plan limits. Global cap = highest active up/down.
	plans, err := biz.ListAllPlans()
	if err != nil {
		return fmt.Errorf("list plans: %w", err)
	}
	var maxUp, maxDown int
	expressActive := false
	for _, p := range plans {
		if !p.IsActive || !p.ExpressEnabled {
			continue
		}
		if !containsString(p.AllowedProtocols, "hysteria2") && !containsString(p.AllowedProtocols, "all") {
			continue
		}
		expressActive = true
		if p.ExpressUpMbps > maxUp {
			maxUp = p.ExpressUpMbps
		}
		if p.ExpressDownMbps > maxDown {
			maxDown = p.ExpressDownMbps
		}
	}
	if !expressActive {
		// No plan currently grants Hysteria2 → leave config untouched, log once per sync.
		log.Println("[hysteria2-sync] no Express-enabled active plans, skipping config write")
		return nil
	}
	if maxUp == 0 {
		maxUp = 50
	}
	if maxDown == 0 {
		maxDown = 100
	}

	obfsPassword := biz.GetHysteria2ObfsPassword()
	if obfsPassword == "" {
		return fmt.Errorf("hysteria2 obfs password empty (app_settings not initialised)")
	}

	// trafficStats secret (migration 000049 seeds it). Polling collector
	// (cmd/api/hysteria2_stats.go) authenticates against this value to read
	// per-user cumulative byte counters from 127.0.0.1:7653.
	statsSecret := biz.GetSettingOrDefault("hysteria2_stats_secret", "")
	if statsSecret == "" {
		return fmt.Errorf("hysteria2_stats_secret empty (app_settings not initialised — run migration 000049)")
	}

	yaml := renderHysteria2Config(maxUp, maxDown, obfsPassword, statsSecret)

	// No-op short-circuit FIRST — if content matches what's on disk, nothing
	// changed since last sync and we don't need to touch validation/reload.
	// This also avoids the "port already in use" false-failure during validation
	// when a running hysteria2-server is holding :50443 with the same config.
	if existing, err := os.ReadFile(hysteria2ConfigPath); err == nil {
		if bytes.Equal(existing, []byte(yaml)) {
			return nil
		}
	}

	// Atomic write with pre-rename validation. Mirrors the xray_sync.go pattern:
	// 1) write generated content to a .tmp file;
	// 2) run `hysteria server --config <tmp>` with a 2s timeout — bind conflicts
	//    on :50443 are treated as success (means config parsed and reached the
	//    bind step, i.e. structurally valid);
	// 3) atomic rename only if validation passes — broken YAML never reaches
	//    systemctl restart, the live server stays healthy, error captured into
	//    app_settings for admin UI / deploy --status.
	tmpPath := hysteria2ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(yaml), 0600); err != nil {
		return fmt.Errorf("write tmp config: %w", err)
	}

	if vErr := validateHysteria2Config(tmpPath); vErr != nil {
		_ = os.Remove(tmpPath)
		recordHysteria2SyncError(vErr)
		return fmt.Errorf("validation failed (keeping existing config): %w", vErr)
	}

	if err := os.Rename(tmpPath, hysteria2ConfigPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename hysteria2 config: %w", err)
	}

	// Hand the file off to the hysteria service account. heyearth-api runs as
	// root, so the renamed file is root:root 0600 by default — the hysteria
	// systemd unit runs as user `hysteria` and would otherwise hit EACCES on
	// open. We don't fail the sync on chown error: best-effort, log only.
	if hu, err := user.Lookup("hysteria"); err == nil {
		uid, _ := strconv.Atoi(hu.Uid)
		gid, _ := strconv.Atoi(hu.Gid)
		if cerr := os.Chown(hysteria2ConfigPath, uid, gid); cerr != nil {
			log.Printf("[hysteria2-sync] chown config to hysteria failed: %v", cerr)
		}
		_ = os.Chmod(hysteria2ConfigPath, 0640)
	}

	// Restart. Hysteria v2.6.x does NOT support SIGHUP hot-reload — it treats
	// SIGHUP as a terminate signal and exits cleanly without restarting itself.
	// Plain `systemctl restart` is the only reliable way to pick up new config.
	// QUIC clients reconnect within ~1s after the brief downtime.
	if out, err := exec.Command("systemctl", "restart", "hysteria2-server").CombinedOutput(); err != nil {
		return fmt.Errorf("restart hysteria2-server: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// Bump config_version so admin UI / clients can detect changes.
	cur := biz.GetSettingOrDefault("hysteria2_config_version", "1")
	if n := atoiSafe(cur); n > 0 {
		_ = biz.SetSetting("hysteria2_config_version", fmt.Sprintf("%d", n+1))
	}

	_ = biz.SetSetting("hysteria2_last_sync_at", fmt.Sprintf("%d", time.Now().Unix()))
	_ = biz.SetSetting("hysteria2_last_error", "")
	_ = biz.SetSetting("hysteria2_max_up_mbps", fmt.Sprintf("%d", maxUp))
	_ = biz.SetSetting("hysteria2_max_down_mbps", fmt.Sprintf("%d", maxDown))

	log.Printf("[hysteria2-sync] config updated: maxUp=%d Mbps, maxDown=%d Mbps", maxUp, maxDown)
	return nil
}

// recordHysteria2SyncError captures the error into app_settings so admin UI
// can surface it without reading journalctl.
func recordHysteria2SyncError(err error) {
	if err == nil {
		return
	}
	_ = biz.SetSetting("hysteria2_last_error", err.Error())
}

// renderHysteria2Config produces a deterministic YAML config so the no-op
// short-circuit (bytes.Equal) works reliably across invocations.
func renderHysteria2Config(maxUp, maxDown int, obfsPassword, statsSecret string) string {
	return fmt.Sprintf(`# /etc/hysteria/server.yaml
# Generated by heyearth-api hysteria2_sync.go. DO NOT EDIT BY HAND.
listen: %s

tls:
  cert: %s
  key:  %s

obfs:
  type: salamander
  salamander:
    password: %s

auth:
  type: http
  http:
    url: %s
    insecure: true

# trafficStats — exposes per-user cumulative byte counters for the heyearth-api
# collector (cmd/api/hysteria2_stats.go) to poll over loopback. Counters
# increment monotonically until hysteria2-server restarts; the collector diffs
# against its previous snapshot and inserts deltas into traffic_stats so
# cycle-quota logic (biz.IsCycleExceeded) works for Express users transparently.
trafficStats:
  listen: 127.0.0.1:7653
  secret: %s

bandwidth:
  up:   %d mbps
  down: %d mbps

# When the Hub gets probed on its non-Hysteria2 endpoint, answer with a generic
# 404 (looks like a static-only webserver) so DPI fingerprinting doesn't get a
# different signature than nginx.
masquerade:
  type: 404

# Server-side QUIC tuning. Defaults are mostly OK; we bump max idle timeout so
# mobile clients with intermittent radio don't churn through reconnects.
quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow:  8388608
  initConnReceiveWindow:   20971520
  maxConnReceiveWindow:    20971520
  maxIdleTimeout: 60s
  maxIncomingStreams: 1024
  disablePathMTUDiscovery: false

# Disable UDP traffic from clients only on misconfigured Hub (we want UDP).
disableUDP: false

# Sniff incoming connection metadata for logging only; we don't route based on it.
sniff:
  enable: true
  timeout: 2s
  rewriteDomain: false

# Outbound routing — hub-and-spoke architecture: the Hub itself does NOT egress
# user traffic directly. Every client connection is forwarded via the local
# Hub→Exit SOCKS5 tunnel (127.0.0.1:1090) which a separate hysteria-client
# process maintains to one of the exit nodes (NL/LV/DE). This keeps Hysteria2
# users on the same egress path as VLESS Reality users — same exit IPs, same
# geo. Without this block, hysteria-server would egress from the Hub's own
# Russia IP and the tunnel would establish (good ping) but real traffic would
# either be blocked at the Hub firewall or appear from RU (broken VPN).
outbounds:
  - name: via_exit
    type: socks5
    socks5:
      addr: 127.0.0.1:1090
  - name: hub_direct
    type: direct

# ACL: GeoIP-aware routing.
#   direct(geoip:ru)      — RU traffic exits the Hub directly (Hub is in RU);
#                           saves a NL hop and avoids geoblocks on yandex.ru,
#                           vk.com, habr.com, etc.
#   direct(geoip:private) — RFC1918 (10/8, 172.16/12, 192.168/16) stays on-host
#                           for admin loopback probes / health checks.
#   via_exit(all)         — everything else goes through the Hub→Exit SOCKS5
#                           tunnel (127.0.0.1:1090) to NL/LV/DE.
# geoip.dat is provisioned by cmd/deploy/hysteria2_hub.go (Loyalsoldier release)
# and refreshed monthly by /etc/cron.monthly/heyearth-hysteria-geoip.
acl:
  geoip: /etc/hysteria/geoip.dat
  inline:
    - direct(geoip:ru)
    - direct(geoip:private)
    - via_exit(all)
`, hysteria2Listen, hysteria2TLSCert, hysteria2TLSKey, obfsPassword, hysteria2AuthURL, statsSecret, maxUp, maxDown)
}

// validateHysteria2Config runs `hysteria server --config <path> --disable-update-check`
// for up to 2 seconds. Three outcomes:
//
//  1. Process is killed by our timeout — means it parsed YAML, loaded TLS,
//     started the auth backend, and reached the UDP bind step (or beyond). For
//     our purposes that's a passing config; we kill it and return nil.
//  2. Process exits within the timeout with "address already in use" — bind
//     conflict against the live hysteria2-server holding :50443. Same as (1):
//     config got past every parse/load gate; bind failure is environmental.
//  3. Process exits within the timeout with any other error — real failure
//     (YAML parse, missing cert, malformed Brutal limits, etc). Return the
//     captured stdout+stderr verbatim so the admin sees the actual diagnostic.
//
// We capture BOTH stdout and stderr because hysteria writes FATAL log lines to
// either depending on log-format and version. The 2s budget is the upper bound
// for healthy startup on this server class — generous enough to absorb cert +
// socket init, tight enough not to block sync for long on a real crash.
//
// Hysteria v2.6.x has no first-class `--check`/`--validate` flag (verified via
// `hysteria server --help`), so the launch-with-timeout pattern is the only
// available syntactic gate.
func validateHysteria2Config(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "hysteria", "server", "--config", path, "--disable-update-check")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hysteria: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		// Timed out — process reached/passed the bind step. Kill and treat as
		// valid. CommandContext should already SIGKILL on ctx expiry, but call
		// Kill explicitly to be deterministic across Go versions / platforms.
		_ = cmd.Process.Kill()
		<-done
		return nil

	case err := <-done:
		combined := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		lower := strings.ToLower(combined)

		// Port conflict ≠ bad config — the live server is holding :50443.
		// Everything before bind passed; treat as valid.
		if strings.Contains(lower, "address already in use") {
			return nil
		}

		// Anything else within the 2s budget is a real failure. Return the
		// captured output verbatim so callers / admin UI see the diagnostic.
		return fmt.Errorf("hysteria exited early: %v (%s)", err, combined)
	}
}

func containsString(arr []string, want string) bool {
	for _, s := range arr {
		if strings.EqualFold(s, want) {
			return true
		}
	}
	return false
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// StartHysteria2SyncLoop kicks off an initial sync and a 5-minute periodic sync.
// Mirrors StartXraySyncLoop. Safe to call even if hysteria isn't installed:
// SyncHysteria2Config is a no-op in that case.
func StartHysteria2SyncLoop() {
	if _, err := exec.LookPath("hysteria"); err != nil {
		log.Println("[hysteria2-sync] hysteria binary not found, sync loop disabled")
		return
	}

	if err := SyncHysteria2Config(); err != nil {
		log.Printf("[hysteria2-sync] initial sync: %v", err)
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if err := SyncHysteria2Config(); err != nil {
				log.Printf("[hysteria2-sync] periodic sync: %v", err)
			}
		}
	}()

	log.Println("[hysteria2-sync] background sync started (every 5 min)")
}
