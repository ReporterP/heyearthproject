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
// --config <file>` smoke parse, atomic rename, then `systemctl reload hysteria-server`
// (SIGHUP). Reload preserves active sessions.
//
// Per-plan Brutal targets: Hysteria2 accepts global bandwidth caps in the
// server config; per-user enforcement happens via the auth-backend response
// (extended later) or by maximum-of-plan-targets here. For the initial release
// we set the global cap to the highest enabled plan limit so the server doesn't
// throttle premium tiers below their advertised speed.

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
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

	yaml := renderHysteria2Config(maxUp, maxDown, obfsPassword)

	tmpPath := hysteria2ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(yaml), 0600); err != nil {
		return fmt.Errorf("write tmp config: %w", err)
	}

	// Smoke-validate by parsing config without starting (hysteria has no --check flag,
	// so we use `--disable-update-check` + a short-running `server --config` with
	// SIGTERM. Simpler: rely on `hysteria server --config X` returning a parse error
	// in stderr if YAML is broken — we shell-out with a 1-second timeout.
	if err := validateHysteria2Config(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("hysteria2 config validation failed: %w", err)
	}

	// No-op if content matches disk.
	if existing, err := os.ReadFile(hysteria2ConfigPath); err == nil {
		if bytes.Equal(existing, []byte(yaml)) {
			_ = os.Remove(tmpPath)
			return nil
		}
	}

	if err := os.Rename(tmpPath, hysteria2ConfigPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename hysteria2 config: %w", err)
	}

	// Reload (SIGHUP). Hysteria2 supports config hot-reload without dropping connections.
	if out, err := exec.Command("systemctl", "reload", "hysteria2-server").CombinedOutput(); err != nil {
		// Fall back to restart only if reload not supported by the unit.
		log.Printf("[hysteria2-sync] reload failed (%v: %s), trying restart", err, strings.TrimSpace(string(out)))
		if out2, err2 := exec.Command("systemctl", "restart", "hysteria2-server").CombinedOutput(); err2 != nil {
			return fmt.Errorf("restart hysteria2-server: %w (%s)", err2, strings.TrimSpace(string(out2)))
		}
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
func renderHysteria2Config(maxUp, maxDown int, obfsPassword string) string {
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
`, hysteria2Listen, hysteria2TLSCert, hysteria2TLSKey, obfsPassword, hysteria2AuthURL, maxUp, maxDown)
}

// validateHysteria2Config runs `hysteria server --config <path>` with a short
// timeout. If the binary exits with a parse error before the timeout, we report
// it. If it starts listening (which it shouldn't on the same port as the live
// service), we kill it and consider the syntax OK.
//
// Practical note: there's no first-class `--check` flag, so we use `server --help`
// + a substring search for known YAML errors when reading the stderr of a quick
// invocation. This is best-effort; a malformed config will still be caught by
// the subsequent `systemctl reload` failing.
func validateHysteria2Config(path string) error {
	// Quick syntax sanity: try parsing the file with `hysteria server --config <path>`
	// for 500ms then send SIGTERM. If the process exits with stderr mentioning
	// "yaml" or "config" before the timeout, it's a parse error.
	cmd := exec.Command("hysteria", "server", "--config", path, "--disable-update-check")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("hysteria start: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(500 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-done
		out := stderr.String()
		if strings.Contains(strings.ToLower(out), "yaml") || strings.Contains(strings.ToLower(out), "unmarshal") {
			return fmt.Errorf("yaml parse: %s", strings.TrimSpace(out))
		}
		return nil
	case err := <-done:
		out := stderr.String()
		// If hysteria exited before the timeout, it crashed on parse/config.
		return fmt.Errorf("exited early: %v (%s)", err, strings.TrimSpace(out))
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
