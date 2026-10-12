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

	// Adaptive tunnel decision — picks shared (N≤1 healthy exits, reuse the
	// per-exit hysteria2-client-<id>.service SOCKS5) vs dedicated (N≥2 exits,
	// spin up hysteria2-client-express.service on :1091 to lowest-RTT exit).
	// Failure here is non-fatal: fall back to the legacy shared:1090 layout
	// so the sync still produces a config (matches pre-T6 behaviour).
	decision, decErr := biz.DecideExpressTunnel()
	if decErr != nil {
		log.Printf("[hysteria2-sync] tunnel decision failed: %v — defaulting to shared:1090", decErr)
		decision = biz.ExpressTunnelDecision{Mode: biz.ExpressTunnelModeShared, OutboundPort: 1090}
	}
	log.Printf("[hysteria2-sync] tunnel decision: mode=%s port=%d exit=%d (%s, RTT=%dms)",
		decision.Mode, decision.OutboundPort, decision.BestExitID, decision.BestExitIP, decision.BestExitRTT)

	yaml := renderHysteria2Config(maxUp, maxDown, obfsPassword, statsSecret, decision.OutboundPort, hubOwnIPs())

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

	// ── Express dedicated-tunnel client management ──
	// In dedicated mode (N≥2 healthy exits) we run hysteria2-client-express.service
	// connecting to the lowest-RTT exit, exposing SOCKS5 on 127.0.0.1:1091 for
	// hysteria2-server's outbound. In shared mode we stop+disable that unit so
	// it doesn't fight with the per-exit client for resources. Errors here are
	// logged but don't abort the sync — the server config has already been
	// applied; the dedicated client is best-effort and self-heals on next probe.
	if decision.Mode == biz.ExpressTunnelModeDedicated && decision.BestExitIP != "" {
		if werr := writeExpressClientConfig(decision.BestExitIP); werr != nil {
			log.Printf("[hysteria2-sync] write express client config: %v", werr)
		} else {
			_ = exec.Command("systemctl", "enable", "hysteria2-client-express").Run()
			if out, rerr := exec.Command("systemctl", "restart", "hysteria2-client-express").CombinedOutput(); rerr != nil {
				log.Printf("[hysteria2-sync] restart hysteria2-client-express: %v (%s)", rerr, strings.TrimSpace(string(out)))
			}
		}
	} else {
		// Shared mode (or fallback) — ensure dedicated client is NOT running so
		// it doesn't fight with the per-exit client for resources. is-active/
		// is-enabled returns non-zero (and we skip) when the unit is absent
		// (setup script not re-run yet) — harmless no-op.
		if exec.Command("systemctl", "is-active", "--quiet", "hysteria2-client-express").Run() == nil {
			_ = exec.Command("systemctl", "stop", "hysteria2-client-express").Run()
		}
		if exec.Command("systemctl", "is-enabled", "--quiet", "hysteria2-client-express").Run() == nil {
			_ = exec.Command("systemctl", "disable", "hysteria2-client-express").Run()
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
//
// outboundSocksPort is the loopback SOCKS5 port that `outbounds.via_exit`
// targets. It is chosen by biz.DecideExpressTunnel():
//   - shared mode (N=1 exit) → 1088 + exitID (the per-exit
//     hysteria2-client-<id>.service that already serves Xray)
//   - dedicated mode (N≥2)   → biz.ExpressDedicatedSocksPort (1091)
//   - fallback (N=0)         → 1090 (legacy port; outbound fails closed)
//
// hubIPs are the Hub's own public addresses (hubOwnIPs); see hysteria2ACLRules.
func renderHysteria2Config(maxUp, maxDown int, obfsPassword, statsSecret string, outboundSocksPort int, hubIPs []string) string {
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
  # Disable Path MTU Discovery — cell DPI on Tele2 (and other RU carriers)
  # fingerprints VPN tunnels via ICMP fragmentation-needed packets that
  # PMTUD generates when probing path MTU. Cellular path MTU is typically
  # 1400-1450 (vs our Hub 1500) so PMTUD activity leaks. With PMTUD disabled,
  # Hysteria2 uses QUIC default ~1252-byte payload regardless of network —
  # no probing packets, predictable size, no DPI fingerprint surface.
  # Throughput cost: ~3%% on high-MTU paths (negligible vs cellular).
  # See docs/research/2026-05-26-mobile-dpi-tele2.md (R3).
  disablePathMTUDiscovery: true

# Disable UDP traffic from clients only on misconfigured Hub (we want UDP).
disableUDP: false

# Sniff incoming connection metadata for logging only; we don't route based on it.
sniff:
  enable: true
  timeout: 2s
  rewriteDomain: false

# Outbound routing — hub-and-spoke architecture: the Hub itself does NOT egress
# user traffic directly. Every client connection is forwarded via a local
# Hub→Exit SOCKS5 tunnel which a separate hysteria-client process maintains to
# one of the exit nodes (NL/LV/DE). The target port is chosen at sync-time by
# biz.DecideExpressTunnel() — see renderHysteria2Config doc above. Without this
# block, hysteria-server would egress from the Hub's own Russia IP and the
# tunnel would establish (good ping) but real traffic would either be blocked
# at the Hub firewall or appear from RU (broken VPN).
outbounds:
  - name: via_exit
    type: socks5
    socks5:
      addr: 127.0.0.1:%d
  - name: hub_direct
    type: direct

# ACL: first match wins, GeoIP-aware routing.
#   reject(...)      — FIRST, before any direct(...): loopback, private ranges and
#                      the Hub's own public addresses. A user must never be able to
#                      make the Hub connect to itself (sshd, PostgreSQL, the API,
#                      the xray API) — hysteria-server sits on the Hub, past the
#                      firewall. Ports 80/443 of the Hub's public addresses are
#                      reached through the exit like for any internet client.
#   direct(geoip:ru) — RU traffic exits the Hub directly (Hub is in RU);
#                      saves a NL hop and avoids geoblocks on yandex.ru,
#                      vk.com, habr.com, etc.
#   via_exit(all)    — everything else goes through the Hub→Exit SOCKS5
#                      tunnel (adaptive port, see outbounds.via_exit) to
#                      NL/LV/DE.
# geoip.dat is provisioned by cmd/deploy/hysteria2_hub.go (Loyalsoldier release)
# and refreshed monthly by /etc/cron.monthly/heyearth-hysteria-geoip.
acl:
  geoip: /etc/hysteria/geoip.dat
  inline:
%s`, hysteria2Listen, hysteria2TLSCert, hysteria2TLSKey, obfsPassword, hysteria2AuthURL, statsSecret, maxUp, maxDown, outboundSocksPort, hysteria2ACLInline(hubIPs))
}

// hysteria2ACLRules returns the inline ACL rules, in order. The order is the security
// property: hysteria2 evaluates them top-down and the first match wins, so everything that
// leads back to the Hub itself is rejected before direct(geoip:ru) can send it out locally.
//
//	reject(geoip:private), reject(127.0.0.0/8), reject(::1/128),
//	reject(0.0.0.0/8), reject(::/128)                             private ranges, loopback, "this host"
//	per Hub address: via_exit(ip, tcp/80), via_exit(ip, tcp/443), reject(ip)
//	direct(geoip:ru), via_exit(all)                               the rest, as before
//
// (the explicit entries do not rely on the geoip file knowing every range). This is
// defence in depth: a hostname that resolves to one of these addresses is still matched here
// (hysteria resolves it first), but the authoritative protection is the kernel egress guard
// (cmd/deploy/ops_egress_guard.go), which also covers what an ACL cannot see.
func hysteria2ACLRules(hubIPs []string) []string {
	rules := []string{"reject(geoip:private)", "reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)"}
	for _, ip := range hubIPs {
		rules = append(rules,
			fmt.Sprintf("via_exit(%s, tcp/%d)", ip, hubOpenPortHTTP),
			fmt.Sprintf("via_exit(%s, tcp/%d)", ip, hubOpenPortHTTPS),
			fmt.Sprintf("reject(%s)", ip))
	}
	return append(rules, "direct(geoip:ru)", "via_exit(all)")
}

// hysteria2ACLInline renders hysteria2ACLRules as the YAML list items under acl.inline.
func hysteria2ACLInline(hubIPs []string) string {
	var sb strings.Builder
	for _, r := range hysteria2ACLRules(hubIPs) {
		sb.WriteString("    - " + r + "\n")
	}
	return sb.String()
}

// writeExpressClientConfig writes /etc/hysteria/client-express.yaml pointing at
// the lowest-RTT exit. AUTH password comes from app_settings.hub_tunnel_password
// (set by exit-node provisioning); falls back to hysteria2_obfs_password if
// the operator hasn't run --setup-exit-node on this exit yet (smoke env).
//
// We chown to hysteria:hysteria so the dedicated systemd unit (running as
// `hysteria`) can read it, mirroring server.yaml's permissions dance above.
func writeExpressClientConfig(exitIP string) error {
	authKey := biz.GetSettingOrDefault("hub_tunnel_password", "")
	if authKey == "" {
		// Fallback: reuse the obfs password as auth so the smoke env still works.
		// Production exits set hub_tunnel_password during --setup-exit-node.
		authKey = biz.GetHysteria2ObfsPassword()
	}
	obfsPw := biz.GetHysteria2ObfsPassword()

	yaml := fmt.Sprintf(`# /etc/hysteria/client-express.yaml
# Generated by heyearth-api hysteria2_sync.go. DO NOT EDIT BY HAND.
# Hub→Exit dedicated tunnel for Express (lowest-RTT exit selection).
server: %s:50443

auth: %s

tls:
  insecure: true

socks5:
  listen: 127.0.0.1:%d

obfs:
  type: salamander
  salamander:
    password: %s

transport:
  udp:
    hopInterval: 30s

quic:
  initStreamReceiveWindow: 8388608
  maxStreamReceiveWindow: 8388608
  initConnReceiveWindow: 20971520
  maxConnReceiveWindow: 20971520
  maxIdleTimeout: 60s

bandwidth:
  up: 200 mbps
  down: 200 mbps
`, exitIP, authKey, biz.ExpressDedicatedSocksPort, obfsPw)

	path := "/etc/hysteria/client-express.yaml"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(yaml), 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	if hu, err := user.Lookup("hysteria"); err == nil {
		uid, _ := strconv.Atoi(hu.Uid)
		gid, _ := strconv.Atoi(hu.Gid)
		_ = os.Chown(path, uid, gid)
		_ = os.Chmod(path, 0o640)
	}
	return nil
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
