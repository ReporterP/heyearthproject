package main

// Hysteria2 server sync — generates /etc/hysteria/server.yaml from current
// plan/user state and reloads the local hysteria2-server.service.
//
// Architecture decision: Hysteria2 runs as its own systemd service alongside
// Xray. We do NOT try to host QUIC inside Xray (its QUIC stack is weak); we
// shell out to the official `hysteria` binary and let it own the UDP listen,
// Salamander obfuscation, and Brutal congestion control.
//
// Reload mechanism: render the config; if it is byte-identical to the file on disk, stop
// (no restart). Otherwise write it to .tmp, validate it with validateHysteria2Config
// (hysteria2_validate.go: a real `hysteria server` on a copy whose QUIC and trafficStats
// listeners are moved to ephemeral loopback ports, accepted only if it is still running after
// hysteria2ValidateTimeout), atomic rename only on success, then `systemctl restart
// hysteria2-server`. This code never relies on a signal-based hot reload; the pinned hysteria
// release is config.HysteriaVersion (internal/config/versions.go).
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
	"sort"
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
	// hysteria2AuthURL points hysteria-server at the auth-only listener (hysteria2_auth_listener.go),
	// never at the main API listener :8000. Changing it changes the rendered bytes, so the next
	// sync rewrites server.yaml and restarts hysteria2-server once.
	hysteria2AuthURL = "http://" + hysteria2AuthListenAddr + hysteria2AuthPath
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
		defer recoverBG("hysteria2-sync")
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

	// Bamboo's exit (biz.ExpressExit): the hysteria2-client-<id> SOCKS of one exit, chosen by
	// the smoothed RTT of its Hysteria path with hysteresis. A database error keeps server.yaml
	// as it is (no restart onto a wrong port). A switch is committed only once server.yaml
	// carries it (applyBambooChoice).
	choice, err := biz.ExpressExit()
	if err != nil {
		return fmt.Errorf("выбор exit для Бамбука: %w (server.yaml не меняется)", err)
	}
	hubIPs := hubOwnIPs()
	render := func(socksPort int) string {
		return renderHysteria2Config(maxUp, maxDown, obfsPassword, statsSecret, socksPort, hubIPs)
	}
	changed, err := applyBambooChoice(choice, render, hysteria2Live.install, biz.ExpressCommit)
	if err != nil || !changed {
		return err
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

// applyBambooChoice installs the server.yaml rendered for choice and commits a switch (memory +
// app_settings, biz.ExpressCommit) only once the file carries it: the bytes on disk already
// match, or the new file was validated, installed and hysteria2-server restarted. On a failure
// the old exit stays current and the next sync proposes the switch again.
func applyBambooChoice(choice biz.ExpressChoice, render func(socksPort int) string,
	install func(yaml string) (changed bool, err error), commit func(biz.ExpressChoice) error) (bool, error) {
	changed, err := install(render(choice.SocksPort))
	if err != nil {
		return false, err
	}
	if choice.Switched {
		if cerr := commit(choice); cerr != nil {
			log.Printf("[hysteria2-sync] Бамбук: выбор exit #%d не сохранён: %v", choice.ExitID, cerr)
		}
		log.Printf("[hysteria2-sync] Бамбук → exit #%d (127.0.0.1:%d, сглаженный RTT %d мс): %s",
			choice.ExitID, choice.SocksPort, choice.RTTms, choice.Reason)
	}
	return changed, nil
}

// hysteria2Installer puts rendered configs in place as server.yaml. Only SyncHysteria2Config
// uses it, under hysteria2SyncMu; tests build their own with a temp path and stub steps.
type hysteria2Installer struct {
	path     string
	validate func(path string) error
	restart  func() error
	// pendingRestart: the file on disk was replaced but hysteria2-server did not restart, so the
	// running server still has the old config. The next install retries the restart even when
	// the bytes are equal (before the caller commits Bamboo's exit). In memory only.
	pendingRestart bool
}

// hysteria2Live is the Hub's /etc/hysteria/server.yaml and the hysteria2-server unit.
var hysteria2Live = &hysteria2Installer{
	path:     hysteria2ConfigPath,
	validate: validateHysteria2Config,
	restart:  restartHysteria2Server,
}

// restartHysteria2Server restarts the unit to pick up the new file. This code does not use a
// signal-based hot reload (no SIGHUP); `systemctl restart` is the one reload path. Connected
// clients are dropped and have to reconnect (and re-authenticate).
func restartHysteria2Server() error {
	if out, err := exec.Command("systemctl", "restart", "hysteria2-server").CombinedOutput(); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// install writes yaml to in.path and restarts hysteria2-server. changed is false when the file
// already holds these bytes and no restart is pending: no validation, no restart.
func (in *hysteria2Installer) install(yaml string) (changed bool, err error) {
	// No-op short-circuit FIRST — if content matches what's on disk, nothing
	// changed since last sync: no validation, no rename, no restart. This is the
	// only change detection; any byte difference (e.g. a new auth URL) leads to
	// exactly one validate + rename + restart, and the next sync is a no-op again —
	// unless that restart failed: then the file is already in place and only the
	// restart is retried.
	if existing, err := os.ReadFile(in.path); err == nil {
		if bytes.Equal(existing, []byte(yaml)) {
			if !in.pendingRestart {
				return false, nil
			}
			return in.restartNow()
		}
	}

	// Atomic write with pre-rename validation. Mirrors the xray_sync.go pattern:
	// 1) write generated content to a .tmp file;
	// 2) validateHysteria2Config(tmp) — starts hysteria on a copy bound to ephemeral
	//    loopback ports (never the live :50443 / 127.0.0.1:7653) and accepts the
	//    config only if the process is still running after hysteria2ValidateTimeout;
	// 3) atomic rename only if validation passes — a rejected config never reaches
	//    systemctl restart, the live server keeps its current file, error captured
	//    into app_settings for admin UI / deploy --status.
	tmpPath := in.path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(yaml), 0600); err != nil {
		return false, fmt.Errorf("write tmp config: %w", err)
	}

	if vErr := in.validate(tmpPath); vErr != nil {
		_ = os.Remove(tmpPath)
		recordHysteria2SyncError(vErr)
		return false, fmt.Errorf("validation failed (keeping existing config): %w", vErr)
	}

	if err := os.Rename(tmpPath, in.path); err != nil {
		_ = os.Remove(tmpPath)
		return false, fmt.Errorf("rename hysteria2 config: %w", err)
	}
	in.pendingRestart = true // the file on disk is now ahead of the running server

	// Hand the file off to the hysteria service account. heyearth-api runs as
	// root, so the renamed file is root:root 0600 by default — the hysteria
	// systemd unit runs as user `hysteria` and would otherwise hit EACCES on
	// open. We don't fail the sync on chown error: best-effort, log only.
	if hu, err := user.Lookup("hysteria"); err == nil {
		uid, _ := strconv.Atoi(hu.Uid)
		gid, _ := strconv.Atoi(hu.Gid)
		if cerr := os.Chown(in.path, uid, gid); cerr != nil {
			log.Printf("[hysteria2-sync] chown config to hysteria failed: %v", cerr)
		}
		_ = os.Chmod(in.path, 0640)
	}
	return in.restartNow()
}

// restartNow restarts hysteria2-server and tracks whether a restart is still owed.
func (in *hysteria2Installer) restartNow() (bool, error) {
	if err := in.restart(); err != nil {
		in.pendingRestart = true
		return false, fmt.Errorf("restart hysteria2-server: %w", err)
	}
	in.pendingRestart = false
	return true, nil
}

// recordHysteria2SyncError captures the error into app_settings so admin UI
// can surface it without reading journalctl. hysteria2_last_error is returned by the admin API
// (/hysteria2/status) and hysteria's own output (the validation error) may quote the config, so
// the secrets server.yaml carries are masked first. When they cannot be read, the text is not
// stored at all (fail closed): the journal keeps the full error.
func recordHysteria2SyncError(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	secrets, serr := hysteria2ConfigSecrets()
	if serr != nil {
		msg = "Ошибка синхронизации Бамбука (текст скрыт: не удалось прочитать секреты для маскировки, см. journalctl -u heyearth-api)"
	} else {
		msg = maskSecrets(msg, secrets)
	}
	_ = hysteria2SaveSetting("hysteria2_last_error", msg)
}

// hysteria2SaveSetting is biz.SetSetting; tests swap it.
var hysteria2SaveSetting = biz.SetSetting

// hysteria2ConfigSecrets returns the secret values server.yaml carries: the salamander obfs
// password and the trafficStats secret (auth is an HTTP callback without a credential). Read
// without biz.GetHysteria2ObfsPassword, which would generate one as a side effect. Tests swap it.
var hysteria2ConfigSecrets = func() ([]string, error) {
	var out []string
	for _, key := range []string{"hysteria2_obfs_password", "hysteria2_stats_secret"} {
		v, err := settingOrEmpty(biz.GetSetting, key)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", key, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// maskSecrets replaces every occurrence of each non-empty secret in s with "***", longest
// first so a secret containing another is masked whole.
func maskSecrets(s string, secrets []string) string {
	sorted := append([]string(nil), secrets...)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, v := range sorted {
		if v = strings.TrimSpace(v); v != "" {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
}

// renderHysteria2Config produces a deterministic YAML config so the no-op
// short-circuit (bytes.Equal) works reliably across invocations.
//
// outboundSocksPort is the loopback SOCKS5 port `outbounds.via_exit` targets: 1088+id of the exit
// biz.ExpressExit chose (its hysteria2-client-<id>), 1090 before any exit was chosen.
//
// hubIPs are the Hub's own public addresses (hubOwnIPs); see hysteria2ACLRules.
//
// The template text is part of the bytes compared above: any edit to it, comments included,
// restarts hysteria2-server on every Hub once. The "biz.DecideExpressTunnel()" in the outbounds
// comment below names the removed function on purpose — it is pinned by
// testdata/hysteria2_server_exit2.golden so the s6yc rollout does not restart Bamboo; reword it
// together with the next change that restarts hysteria2-server anyway.
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
//	reject(<hysteria2PrivateRanges>...)                           RFC1918, CGNAT, link-local, ULA
//	per Hub address: via_exit(ip, tcp/80), via_exit(ip, tcp/443), reject(ip)
//	direct(geoip:ru), via_exit(all)                               the rest, as before
//
// The explicit entries do not rely on the geoip file: geoip:private only covers what the
// installed geoip.dat lists under "private", and that file is downloaded and refreshed
// separately from this code. This is defence in depth: a hostname that resolves to one of these
// addresses is still matched here (hysteria resolves it first), but the authoritative
// protection is the kernel egress guard (cmd/deploy/ops_egress_guard.go), which also covers
// what an ACL cannot see.
func hysteria2ACLRules(hubIPs []string) []string {
	rules := []string{"reject(geoip:private)", "reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)"}
	for _, cidr := range hysteria2PrivateRanges {
		rules = append(rules, "reject("+cidr+")")
	}
	for _, ip := range hubIPs {
		rules = append(rules,
			fmt.Sprintf("via_exit(%s, tcp/%d)", ip, hubOpenPortHTTP),
			fmt.Sprintf("via_exit(%s, tcp/%d)", ip, hubOpenPortHTTPS),
			fmt.Sprintf("reject(%s)", ip))
	}
	return append(rules, "direct(geoip:ru)", "via_exit(all)")
}

// hysteria2PrivateRanges are rejected explicitly in the Bamboo ACL, right after the loopback /
// "this host" rejects: RFC1918, shared address space (CGNAT, RFC 6598), IPv4 link-local, IPv6
// unique-local and IPv6 link-local.
var hysteria2PrivateRanges = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"fc00::/7",
	"fe80::/10",
}

// hysteria2ACLInline renders hysteria2ACLRules as the YAML list items under acl.inline.
func hysteria2ACLInline(hubIPs []string) string {
	var sb strings.Builder
	for _, r := range hysteria2ACLRules(hubIPs) {
		sb.WriteString("    - " + r + "\n")
	}
	return sb.String()
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

var legacyExpressCleanup sync.Once

// legacyExpressStopTimeout bounds the systemctl call: it runs in main before the first sync.
const legacyExpressStopTimeout = 15 * time.Second

// stopLegacyExpressClient stops the dedicated Express client of the removed "dedicated" mode
// (s6yc): hysteria2-client-express listened on :1091, which is exit #3's own SOCKS port. Once per
// process; every step is a no-op when the unit or the file is absent. The unit file itself is
// removed by deploy --setup-hysteria2-hub (hysteria2_hub.go).
//
// app_settings rows of the removed mode stay in the database, nothing reads them any more:
// hysteria2_tunnel_mode, hysteria2_bound_exit_ip, hysteria2_bound_exit_rtt_ms and
// hub_tunnel_password (a secret, never returned by the admin API). The phase-5 settings
// whitelist drops them from the admin UI.
func stopLegacyExpressClient() {
	legacyExpressCleanup.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), legacyExpressStopTimeout)
		defer cancel()
		_ = exec.CommandContext(ctx, "systemctl", "disable", "--now", "hysteria2-client-express").Run()
		_ = os.Remove("/etc/hysteria/client-express.yaml")
	})
}

// StartHysteria2SyncLoop kicks off an initial sync and a 5-minute periodic sync.
// Mirrors StartXraySyncLoop. Safe to call even if hysteria isn't installed:
// SyncHysteria2Config is a no-op in that case.
func StartHysteria2SyncLoop() {
	if _, err := exec.LookPath("hysteria"); err != nil {
		log.Println("[hysteria2-sync] hysteria binary not found, sync loop disabled")
		return
	}
	stopLegacyExpressClient()

	if err := SyncHysteria2Config(); err != nil {
		log.Printf("[hysteria2-sync] initial sync: %v", err)
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			safely("hysteria2-sync", func() {
				if err := SyncHysteria2Config(); err != nil {
					log.Printf("[hysteria2-sync] periodic sync: %v", err)
				}
			})
		}
	}()

	log.Println("[hysteria2-sync] background sync started (every 5 min)")
}
