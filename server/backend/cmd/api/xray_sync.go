package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/xraydiff"
)

// ─────────────────────── Xray Config Sync (Hub-and-Spoke) ───────────────────────
//
// Hub-and-Spoke архитектура:
//   - Hub (главный сервер) принимает подключения пользователей
//   - Spoke (exit-ноды) подключены к Hub через зашифрованные туннели
//   - Hub маршрутизирует трафик пользователей через здоровые exit-ноды
//   - Балансировка нагрузки: стратегия из app_settings balancer_strategy (по
//     умолчанию random) по здоровым exit-нодам; observatory пробует их для
//     fallback (см. newBalancer)
//   - Если exit-нод нет — пользовательский трафик блокируется (кроме явных RU-direct правил)
//
// Безопасность:
//   - Per-user UUID для VLESS, per-user login/pass для SOCKS5/HTTP
//   - Блокировка приватных IP (geoip:private) — межсегментная изоляция
//   - Блокировка BitTorrent протокола
//   - Все политики фильтрации применяются только на Hub
//   - Spoke-ноды не имеют прямого доступа друг к другу (hair-pinning через Hub)

const (
	xrayConfigPath = "/opt/heyearth/xray/config.json"
	xrayKeysPath   = "/opt/heyearth/state/reality-keys.json"
)

var (
	xraySyncMu sync.Mutex

	// Debounce: timer-based coalescing of sync requests.
	debounceMu    sync.Mutex
	debounceTimer *time.Timer
)

// xrayRealityKeys — ключи Reality из /opt/heyearth/state/reality-keys.json
type xrayRealityKeys struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
	ShortID    string `json:"shortId"`
	UUID       string `json:"uuid"`
	SSPassword string `json:"ssPassword"`
}

// loadRealityKeys загружает Reality-ключи с диска
func loadRealityKeys() (*xrayRealityKeys, error) {
	data, err := os.ReadFile(xrayKeysPath)
	if err != nil {
		return nil, fmt.Errorf("read reality keys: %w", err)
	}
	var keys xrayRealityKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("parse reality keys: %w", err)
	}
	return &keys, nil
}

// ─────────────────────── Config Generation ───────────────────────

// RequestXraySync запрашивает синхронизацию с debounce (2 секунды тишины).
// Множественные вызовы за 2 сек сливаются в один SyncXrayConfig.
func RequestXraySync() {
	debounceMu.Lock()
	defer debounceMu.Unlock()
	if debounceTimer != nil {
		debounceTimer.Stop()
	}
	debounceTimer = time.AfterFunc(2*time.Second, func() {
		runXraySync("debounced sync error")
	})
}

// xraySyncAlertKey — ключ алерта «конфиг xray не применяется».
const xraySyncAlertKey = "xray-sync-failed"

// xraySyncAlertDetailsMax limits the error text in the alert: `xray run -test`
// output can be long and a Telegram message is capped at 4096 characters.
const xraySyncAlertDetailsMax = 800

// runXraySync is the single entry point of all background syncs (startup,
// periodic, debounced): it runs SyncXrayConfig, logs a failure, keeps the
// "xray-sync-failed" alert in step with the result and, when a validated
// config could not be put into service, schedules a quick retry.
func runXraySync(label string) {
	err := SyncXrayConfig()
	if err != nil {
		log.Printf("[xray-sync] %s: %v", label, err)
	}
	reportXraySync(alerts, err)
	if d, ok := xrayApplyRetry.after(err, RequestXraySync); ok {
		log.Printf("[xray-sync] конфиг не применён — повторная синхронизация через %s", d)
	}
}

// reportXraySync raises a critical alert on any SyncXrayConfig error (leak
// guard, `xray run -test`, restart failure, DB errors) — until it is fixed the
// running config silently stays stale. A nil result, including "config
// unchanged" and "xray not installed", resolves the alert; resolving a key that
// was never raised is a no-op.
func reportXraySync(m *alertManager, err error) {
	if err == nil {
		m.Resolve(xraySyncAlertKey, "Конфиг xray снова применяется")
		return
	}
	details := []rune(err.Error())
	if len(details) > xraySyncAlertDetailsMax {
		details = append(details[:xraySyncAlertDetailsMax], '…')
	}
	m.Raise(xraySyncAlertKey, AlertCritical, "Конфиг xray не применяется", string(details))
}

// ─────────────── Quick retry after a failed apply (M-5) ───────────────

// errXrayApplyFailed marks a sync that built and validated a config but could
// not put it into service (rename or restart failed). The runtime is stale
// (xrayRuntimeState) or still runs the previous config: new users may be
// missing, a revoked one may still be served. Such a sync is retried after
// xrayRetryDelay instead of waiting for the 5-minute periodic one.
var errXrayApplyFailed = errors.New("xray config not put into service")

const (
	xrayRetryBase = 30 * time.Second
	xrayRetryMax  = 5 * time.Minute // the periodic sync runs every 5 minutes anyway
)

// xrayRetryDelay is the backoff of the n-th consecutive retry (n from 0):
// 30 s, 1 min, 2 min, 4 min, then 5 min.
func xrayRetryDelay(n int) time.Duration {
	d := xrayRetryBase
	for i := 0; i < n && d < xrayRetryMax; i++ {
		d *= 2
	}
	if d > xrayRetryMax {
		d = xrayRetryMax
	}
	return d
}

// xrayRetrySchedule keeps at most one pending quick retry.
type xrayRetrySchedule struct {
	mu      sync.Mutex
	attempt int         // consecutive failed applies
	stop    func() bool // the pending retry, nil if none
	// afterFunc schedules f after d and returns its stop function; nil means
	// time.AfterFunc. Injectable for tests.
	afterFunc func(d time.Duration, f func()) func() bool
}

var xrayApplyRetry = &xrayRetrySchedule{}

// after is fed the result of every sync. A failed apply schedules request
// after the next backoff delay (unless a retry is already pending); a
// successful sync cancels a pending retry and resets the backoff; any other
// error (database, validation) changes nothing — the periodic sync covers it.
func (r *xrayRetrySchedule) after(err error, request func()) (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case err == nil:
		if r.stop != nil {
			r.stop()
			r.stop = nil
		}
		r.attempt = 0
		return 0, false
	case !errors.Is(err, errXrayApplyFailed) || r.stop != nil:
		return 0, false
	}
	d := xrayRetryDelay(r.attempt)
	r.attempt++
	schedule := r.afterFunc
	if schedule == nil {
		schedule = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	r.stop = schedule(d, func() {
		r.mu.Lock()
		r.stop = nil
		r.mu.Unlock()
		request()
	})
	return d, true
}

// SyncXrayConfig генерирует Xray конфиг со всеми активными пользователями,
// exit-нодами и политиками безопасности и вводит его в работу (без изменений /
// live через xray api / рестарт). Вызывается только через runXraySync.
func SyncXrayConfig() error {
	xraySyncMu.Lock()
	defer xraySyncMu.Unlock()

	// Проверяем что Xray установлен
	if _, err := os.Stat(xrayConfigPath); os.IsNotExist(err) {
		return nil // Xray не установлен — пропускаем
	}

	// Per-user quota cutoff: the decision is resolved ONCE per sync. Users over
	// their current billing cycle quota (judged on their effective subscription,
	// keyed by users.user_id == ActiveVlessClient.UserID) are excluded from the
	// VLESS clients arrays and the per-plan routing rules, but ONLY while the
	// app_settings flag quota_cutoff_enabled is "true" (default: off). The set
	// re-computes on every sync, so cycle resets pick up automatically.
	// syncWithQuota resolves read failures without changing the cutoff
	// (loadQuotaForSync: the last applied flag and the last read set, up to
	// quotaDecisionMaxAge old, are kept; before the first apply of this process
	// the sync is aborted instead), otherwise a DB blip would restore every
	// cut-off user and the collector would cut them again a minute later — with
	// a forced xray restart for everyone. It records the decision as applied
	// only after applyXrayConfig succeeded.
	return syncWithQuota(time.Now(), applyXrayConfig)
}

// ─────────────── Inputs of the generator: strict reads ───────────────

// xraySyncDBTimeout bounds every database call of the sync path: the sync runs
// under xraySyncMu, so a hung query must not hold every later sync.
const xraySyncDBTimeout = 15 * time.Second

// withSyncTimeout runs fn with a context that expires after xraySyncDBTimeout.
func withSyncTimeout[T any](fn func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), xraySyncDBTimeout)
	defer cancel()
	return fn(ctx)
}

// settingForSync reads an app_settings value for the xray config. Only a key
// that is absent (sql.ErrNoRows) or blank yields def; any other error is
// returned, so the sync aborts and xray keeps running its current config.
// biz.GetSettingOrDefault must not be used on this path: it turns a failed read
// into the default, i.e. a one-off DB error into a structural diff and an xray
// restart with a wrong config (an SNI no client uses, a vanished CDN inbound…).
func settingForSync(ctx context.Context, get func(context.Context, string) (string, error), key, def string) (string, error) {
	v, err := get(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", fmt.Errorf("read setting %s: %w", key, err)
	}
	if v = strings.TrimSpace(v); v == "" {
		return def, nil
	}
	return v, nil
}

// xraySyncReads are the database reads of the generator. Every one of them
// returns its error and load aborts on it: a config built from a partial read
// (no exits → every user inbound blocked, no clients → everyone revoked, a
// default SNI → every handshake fails) would be put into service with a
// restart. Injectable for tests.
type xraySyncReads struct {
	setting        func(ctx context.Context, key string) (string, error) // sql.ErrNoRows = absent
	clients        func(ctx context.Context) ([]biz.ActiveVlessClient, error)
	exitNodes      func(ctx context.Context) ([]biz.ExitNodeTunnel, error)
	maskingRules   func(ctx context.Context) ([]biz.TrafficMaskingRule, error)
	directPlanIDs  func(ctx context.Context) ([]int, error)
	directServices func(ctx context.Context) ([]biz.DirectRouteService, error)
	planServers    func(ctx context.Context) (map[int][]int, error)
	// hubIPs returns the Hub's own public addresses (hubOwnIPsStrict); setting
	// reads a host setting strictly ("" when absent).
	hubIPs func(setting func(key string) (string, error)) ([]string, error)
}

var xrayDBReads = xraySyncReads{
	setting:        biz.GetSettingContext,
	clients:        biz.ListActiveVlessClientsContext,
	exitNodes:      biz.ListHealthyExitNodesContext,
	maskingRules:   biz.ListActiveTrafficMaskingRulesContext,
	directPlanIDs:  biz.GetPlanIDsWithDirectRoutesContext,
	directServices: biz.ListActiveDirectRouteServicesContext,
	planServers:    biz.ListPlanServersContext,
	hubIPs:         hubOwnIPsStrict,
}

// xraySettings are the app_settings the generator uses, already defaulted
// (absent/blank key only) and parsed.
type xraySettings struct {
	realitySNI       string   // reality_sni_domain
	mainServerHost   string   // main_server_host (XHTTP Host header)
	cdnEnabled       bool     // cdn_enabled
	cdnXHPath        string   // cdn_xh_path (read only while the CDN is enabled)
	cdnHost          string   // cdn_host (read only while the CDN is enabled)
	balancerStrategy string   // balancer_strategy
	dnsServers       []string // dns_servers
	fingerprint      string   // reality_fingerprint (Hub→exit VLESS tunnels)
	muxConcurrency   int      // mux_concurrency (Hub→exit tunnels)
	// stream_proxy_protocol: nginx's 443 stream sends a PROXY header to the
	// Reality inbound (deploy --stream-proxy-protocol switches both sides).
	streamProxyProtocol bool
}

// xrayInputs is everything buildXrayConfig needs; nothing else is read.
type xrayInputs struct {
	keys           xrayRealityKeys
	settings       xraySettings
	clients        []biz.ActiveVlessClient
	exitNodes      []biz.ExitNodeTunnel // healthy, lifecycle online
	maskingRules   []biz.TrafficMaskingRule
	directPlanIDs  []int                    // active plans with direct_routing_enabled, ascending
	directServices []biz.DirectRouteService // active services, shared by all those plans
	planServers    map[int][]int            // plan_servers: plan id → server ids
	hubIPs         []string
}

// Defaults of the generator settings, used only while a key is absent or blank.
const (
	defaultMainServerHost   = "hiearth.ru"
	defaultCDNXHPath        = "/cdn-xh"
	defaultCDNHost          = "cdn.hiearth.ru"
	defaultBalancerStrategy = "random"
	defaultDNSServers       = "8.8.8.8,1.1.1.1"
	defaultFingerprint      = "chrome"
	defaultMuxConcurrency   = 8
)

// load reads the generator inputs, each database call bounded by
// xraySyncDBTimeout. The first failed read is returned (wrapped).
func (r xraySyncReads) load(keys xrayRealityKeys) (xrayInputs, error) {
	in := xrayInputs{keys: keys}
	setting := func(key, def string) (string, error) {
		return withSyncTimeout(func(ctx context.Context) (string, error) {
			return settingForSync(ctx, r.setting, key, def)
		})
	}
	s := &in.settings
	var err error
	if s.realitySNI, err = setting("reality_sni_domain", biz.DefaultRealitySNI); err != nil {
		return in, err
	}
	if s.mainServerHost, err = setting("main_server_host", defaultMainServerHost); err != nil {
		return in, err
	}
	cdn, err := setting("cdn_enabled", "false")
	if err != nil {
		return in, err
	}
	if s.cdnEnabled = cdn == "true"; s.cdnEnabled {
		if s.cdnXHPath, err = setting("cdn_xh_path", defaultCDNXHPath); err != nil {
			return in, err
		}
		if s.cdnHost, err = setting("cdn_host", defaultCDNHost); err != nil {
			return in, err
		}
	}
	if s.balancerStrategy, err = setting("balancer_strategy", defaultBalancerStrategy); err != nil {
		return in, err
	}
	dns, err := setting("dns_servers", defaultDNSServers)
	if err != nil {
		return in, err
	}
	s.dnsServers = parseDNSServers(dns)
	if s.fingerprint, err = setting("reality_fingerprint", defaultFingerprint); err != nil {
		return in, err
	}
	mux, err := setting("mux_concurrency", strconv.Itoa(defaultMuxConcurrency))
	if err != nil {
		return in, err
	}
	s.muxConcurrency = parseMuxConcurrency(mux)
	pp, err := setting("stream_proxy_protocol", "false")
	if err != nil {
		return in, err
	}
	if s.streamProxyProtocol, err = parseStreamProxyProtocol(pp); err != nil {
		return in, err
	}

	if in.clients, err = withSyncTimeout(r.clients); err != nil {
		return in, fmt.Errorf("list active clients: %w", err)
	}
	if in.exitNodes, err = withSyncTimeout(r.exitNodes); err != nil {
		return in, fmt.Errorf("list healthy exit nodes: %w", err)
	}
	if in.maskingRules, err = withSyncTimeout(r.maskingRules); err != nil {
		return in, fmt.Errorf("list traffic masking rules: %w", err)
	}
	if in.directPlanIDs, err = withSyncTimeout(r.directPlanIDs); err != nil {
		return in, fmt.Errorf("list plans with direct routes: %w", err)
	}
	if len(in.directPlanIDs) > 0 {
		if in.directServices, err = withSyncTimeout(r.directServices); err != nil {
			return in, fmt.Errorf("list direct route services: %w", err)
		}
	}
	if in.planServers, err = withSyncTimeout(r.planServers); err != nil {
		return in, fmt.Errorf("list plan servers: %w", err)
	}
	if in.hubIPs, err = r.hubIPs(func(key string) (string, error) { return setting(key, "") }); err != nil {
		return in, fmt.Errorf("hub addresses: %w", err)
	}
	return in, nil
}

// parseStreamProxyProtocol is strict: only "true" or "false". A typo must not
// silently mean "off" while nginx already sends PROXY headers (every Reality
// handshake would then fail); the sync aborts instead and keeps the running
// config.
func parseStreamProxyProtocol(v string) (bool, error) {
	switch v {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("setting stream_proxy_protocol: want true or false, got %q", v)
}

// parseDNSServers splits the comma-separated dns_servers value; an empty list
// falls back to the default servers.
func parseDNSServers(s string) []string {
	var servers []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			servers = append(servers, p)
		}
	}
	if len(servers) == 0 {
		return parseDNSServers(defaultDNSServers)
	}
	return servers
}

// parseMuxConcurrency parses mux_concurrency; anything outside 1..64 is the default.
func parseMuxConcurrency(s string) int {
	v, _ := strconv.Atoi(s)
	if v < 1 || v > 64 {
		return defaultMuxConcurrency
	}
	return v
}

// ─────────────── The generator (pure) ───────────────

// xrayServerEmail is the client "email" of the server UUID entry.
const xrayServerEmail = "server@heyearth"

// xrayBuild is a generated config plus what applyXrayConfig logs about it.
type xrayBuild struct {
	config       map[string]interface{}
	vlessClients int  // VLESS clients per inbound (server entry included)
	excluded     int  // clients the quota cutoff left out
	exits        int  // exit outbounds (healthy exits with a valid tunnel)
	maskRules    int  // mask-* outbounds
	noExit       bool // no usable exit: every user inbound is blocked
}

// buildXrayConfig generates the xray config from in under the quota decision.
// Pure: no I/O, no clock, no settings reads (everything comes from in). It ends
// with the leak guard (checkNoDirectLeak): a config violating the routing
// invariants is an error and is never written.
func buildXrayConfig(in xrayInputs, quota quotaCutoff) (xrayBuild, error) {
	keys := in.keys
	settings := in.settings

	// ── Per-user клиенты ──
	// The server UUID entry (admin access, backward compatibility) is always
	// the first client, so no VLESS inbound is ever left without clients.
	clients := in.clients
	if keys.UUID != "" && !hasClientUUID(clients, keys.UUID) {
		clients = append([]biz.ActiveVlessClient{{UUID: keys.UUID, UserID: xrayServerEmail, IsPremium: true}}, clients...)
	}

	// Policy levels: 0 = standard, 1 = premium
	vlessClients := make([]map[string]interface{}, 0, len(clients))
	excluded := 0
	for _, c := range clients {
		if quota.excludes(c) {
			excluded++
			continue // over cycle quota — re-included when cycle resets
		}
		email := c.UserID
		if email == "" {
			email = xrayServerEmail
		}
		level := 0
		if c.IsPremium {
			level = 1
		}
		vlessClients = append(vlessClients, map[string]interface{}{
			"id":    c.UUID,
			"flow":  "xtls-rprx-vision",
			"email": email,
			"level": level,
		})
	}

	// vlessClientsXHTTP mirrors vlessClients minus the `flow` field. XTLS Vision
	// (`xtls-rprx-vision`) only works with `network=tcp`; on an xhttp/splithttp
	// transport the Xray server rejects the handshake when clients have flow
	// set. Until now both inbounds shared `vlessClients` — the xhttp inbound
	// has been accepting connections but failing them right after Reality
	// handshake (visible to happ as "н/д" / no latency).
	vlessClientsXHTTP := make([]map[string]interface{}, 0, len(vlessClients))
	for _, c := range vlessClients {
		cc := make(map[string]interface{}, len(c))
		for k, v := range c {
			if k == "flow" {
				continue
			}
			cc[k] = v
		}
		vlessClientsXHTTP = append(vlessClientsXHTTP, cc)
	}

	// ── Reality SNI ──
	// app_settings reality_sni_domain (the auto-tuner picks it from the RU
	// candidates); while the key is absent biz.DefaultRealitySNI, the same
	// default the subscription URIs use. The subscription's sni= must be one of
	// serverNames, or every handshake fails.
	realitySNI := settings.realitySNI

	// ── Inbounds ──
	realityStream := map[string]interface{}{
		"network": "tcp", "security": "reality",
		"realitySettings": map[string]interface{}{
			"show": false, "dest": realitySNI + ":443", "xver": 0,
			"serverNames": []string{realitySNI, "www." + realitySNI},
			"privateKey":  keys.PrivateKey,
			"shortIds":    []string{keys.ShortID, ""},
		},
	}
	if settings.streamProxyProtocol {
		// nginx's 443 stream (proxy_protocol on) prefixes every connection with
		// a PROXY header carrying the real client address. xray then REQUIRES
		// it: nginx and this flag are switched together by
		// deploy --stream-proxy-protocol. Only this inbound sits behind the
		// stream; vless-xhttp faces clients on :2443, the CDN inbound sits
		// behind nginx http.
		realityStream["sockopt"] = map[string]interface{}{"acceptProxyProtocol": true}
	}
	inbounds := []map[string]interface{}{
		{
			// Loopback behind the nginx stream SNI router (:443 → :10443); the
			// client-facing TCP is nginx's, so no keepalive sockopt here.
			"tag": "vless-reality", "listen": "127.0.0.1", "port": 10443, "protocol": "vless",
			"settings":       map[string]interface{}{"clients": vlessClients, "decryption": "none"},
			"streamSettings": realityStream,
			"sniffing":       map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
		{
			// VLESS Reality + XHTTP (splithttp) — anti-behavioral-DPI transport
			// XHTTP splits upload/download into separate HTTP transactions,
			// making traffic statistically indistinguishable from normal web browsing.
			// Port 2443 direct (no nginx stream needed).
			"tag": "vless-xhttp", "listen": "0.0.0.0", "port": 2443, "protocol": "vless",
			"settings": map[string]interface{}{"clients": vlessClientsXHTTP, "decryption": "none"},
			"streamSettings": map[string]interface{}{
				"network": "xhttp", "security": "reality",
				"xhttpSettings": map[string]interface{}{
					// path MUST match what clients send in their URI's `path=` param.
					// `/` is the minimal-friction default — looks like a generic web
					// site root request, no path-specific filtering hits.
					"path": "/",
					// host is the SNI that the xhttp transport uses for the HTTP
					// Host header. MUST be one of realitySettings.serverNames OR
					// listed here explicitly; without it Xray validates against
					// realitySettings.serverNames (yandex.ru) but happ sends
					// Host: hiearth.ru → silent handshake failure (Reality OK,
					// xhttp layer rejects). This was the «н/д» bug.
					"host":          settings.mainServerHost,
					"mode":          "auto",
					"xPaddingBytes": "100-1000",
					// R4: cap each POST chunk under 15 KB so a single TCP doesn't
					// accumulate enough data to trigger the RU "freezing" attack
					// on foreign-datacenter IPs (~15-20 KB threshold observed).
					"scMaxEachPostBytes": "10000-15000",
					// R4: spread response across many buffered POSTs so the
					// "freezing" attack can't latch onto a single long-lived TCP.
					// NB: Xray 26.x types this as int64 (NOT a "min-max" string range
					// like scMaxEachPostBytes). Verified via Xray source code +
					// runtime crash: `cannot unmarshal string into Go struct field
					// SplitHTTPConfig.scMaxBufferedPosts of type int64`. Default 30.
					// 40 chosen as conservative upper bound to absorb burst response
					// without hitting connection-drop threshold.
					"scMaxBufferedPosts": 40,
				},
				"realitySettings": map[string]interface{}{
					"show": false, "dest": realitySNI + ":443", "xver": 0,
					"serverNames": []string{realitySNI, "www." + realitySNI},
					"privateKey":  keys.PrivateKey,
					"shortIds":    []string{keys.ShortID, ""},
				},
				// T1.2: bilateral TCP keepalive. Unlike vless-reality (loopback
				// behind nginx, whose client-facing socket is nginx's), this
				// inbound faces clients directly on :2443. XHTTP is HTTP/1.1
				// long-poll over TCP, so its socket is exposed to the mobile
				// CGNAT idle reap like any long-lived TCP.
				"sockopt": map[string]interface{}{
					"tcpKeepAliveInterval": 30,
					"tcpKeepAliveIdle":     120,
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
				},
			},
			"sniffing": map[string]interface{}{"enabled": true, "destOverride": []string{"http", "tls", "quic"}},
		},
	}

	// ☁️ vless-xhttp-cdn — VLESS over XHTTP inbound on loopback :11443 for
	// CDN-fronted path. Cloudflare terminates user-side TLS, our nginx vhost
	// for cdn.hiearth.ru proxies plain HTTP to here. No security layer at
	// xray level — TLS already terminated by CF.
	//
	// XHTTP chosen over WebSocket because xray 26.x deprecates WS ("Please
	// migrate to XHTTP H2 & H3"). XHTTP also splits up/down streams (packet-up
	// mode) which plays better with CF's HTTP/2 multiplexing quirks.
	//
	// Toggle-gated via app_settings.cdn_enabled — inbound only generated when
	// feature is enabled. Re-uses vlessClientsXHTTP (same per-user UUIDs, no
	// flow because XHTTP is incompatible with XTLS Vision).
	if settings.cdnEnabled {
		inbounds = append(inbounds, map[string]interface{}{
			"tag":      "vless-xhttp-cdn",
			"listen":   "127.0.0.1",
			"port":     11443,
			"protocol": "vless",
			"settings": map[string]interface{}{
				"clients":    vlessClientsXHTTP,
				"decryption": "none",
			},
			"streamSettings": map[string]interface{}{
				"network": "xhttp",
				"xhttpSettings": map[string]interface{}{
					"path": settings.cdnXHPath,
					"host": settings.cdnHost,
					"mode": "packet-up", // CDN-friendly: separate up/down HTTP requests
				},
			},
			"sniffing": map[string]interface{}{
				"enabled":      true,
				"destOverride": []string{"http", "tls", "quic"},
			},
		})
	}

	// ── Xray Stats API ──
	// dokodemo-door inbound для gRPC API доступа к статистике
	inbounds = append(inbounds, map[string]interface{}{
		"tag": "api-in", "listen": "127.0.0.1", "port": 10085, "protocol": "dokodemo-door",
		"settings": map[string]interface{}{"address": "127.0.0.1"},
	})

	// SOCKS5 inbound для AmneziaWG (Palma) egress. AWG's tun2socks
	// (hev-socks5-tunnel) dials this on loopback; traffic then flows through
	// the SAME global routing rules as Reality (inboundTag: null) → RU domains
	// + RU geoip go to direct-freedom (Hub Moscow), everything else to the
	// exit-balancer (Amsterdam). sniffing recovers the domain from TLS SNI /
	// HTTP Host so geosite (domain) rules fire, matching Happ's split.
	// Loopback-only (127.0.0.1) — never exposed to the network.
	inbounds = append(inbounds, map[string]interface{}{
		"tag":      "socks-awg",
		"listen":   "127.0.0.1",
		"port":     10808,
		"protocol": "socks",
		"settings": map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
			"ip":   "127.0.0.1",
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
			"routeOnly":    true,
		},
	})

	// Every inbound except api-in carries user traffic; the no-exit block rule
	// must cover all of them (C2: the old shadowsocks inbound, socks-awg and CDN used to leak).
	userInbounds := userInboundTags(inbounds)

	// ── Outbounds ──
	// outbounds[0] is xray's default handler: unmatched traffic and balancer
	// failures without fallbackTag land there. It MUST be block (C2).
	outbounds := []map[string]interface{}{
		newBlockOutbound(),
		{
			// Hub-local egress for xray's own DNS client only (dnsDirectRule).
			"protocol": "freedom", "tag": xrayDirectTag,
			"settings": map[string]interface{}{"domainStrategy": "UseIP"},
			"streamSettings": map[string]interface{}{
				"sockopt": map[string]interface{}{
					"tcpFastOpen": true,
					"tcpNoDelay":  true,
				},
			},
		},
	}

	// ── Exit-ноды (Spoke outbounds) ──
	var exitTags []string
	healthyExit := map[string]bool{}
	for _, node := range in.exitNodes {
		outbound := buildExitNodeOutbound(node, settings.fingerprint, settings.muxConcurrency)
		if outbound != nil {
			outbounds = append(outbounds, outbound)
			tag := fmt.Sprintf("exit-%d", node.ID)
			healthyExit[tag] = true
			// Weighted: duplicate tag by load_weight for proportional distribution.
			// NB (verified in Xray-core 26.3.27, app/proxyman/outbound Manager.Select):
			// balancer selectors are tag PREFIXES and every outbound is listed once
			// (break per tag), so the duplicates do not weight anything, and a
			// selector "exit-2" also matches "exit-20".."exit-29". There is no exact
			// or regex form; unambiguous selectors need other outbound tags (e.g.
			// "exit-2." with selector "exit-2."), which the stats/probe code also
			// uses — left as is, see the report of 2026-10-12.
			w := node.LoadWeight
			if w < 1 {
				w = 1
			}
			for i := 0; i < w; i++ {
				exitTags = append(exitTags, tag)
			}
		}
	}

	// ── Traffic masking rules ──
	// Each rule gets a Hub-local freedom outbound mask-<id>; mask_domain is not
	// used by the generated config (see the routing rules below).
	type maskRouteEntry struct {
		sourceDomain string
		outboundTag  string
	}
	var maskRouteEntries []maskRouteEntry
	for _, rule := range in.maskingRules {
		tag := fmt.Sprintf("mask-%d", rule.ID)
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "freedom",
			"settings": map[string]interface{}{
				"domainStrategy": "UseIP",
			},
		}
		outbounds = append(outbounds, outbound)
		maskRouteEntries = append(maskRouteEntries, maskRouteEntry{
			sourceDomain: rule.SourceDomain,
			outboundTag:  tag,
		})
	}

	// ── Direct-routing outbound ──
	// Unconditional: cheap, idempotent, avoids "tag not found" errors at Xray
	// startup if rule construction below emits outboundTag before outbound is
	// registered. UseIP so Xray resolves FQDN locally on the Hub (RU IP).
	outbounds = append(outbounds, map[string]interface{}{
		"tag":      "direct-freedom",
		"protocol": "freedom",
		"settings": map[string]interface{}{"domainStrategy": "UseIP"},
	})

	// ── Routing (политики безопасности) ──
	// The first rules are the security baseline (see baseSecurityRules): xray's own DNS, then
	// everything that leads back to the Hub itself or to a private range is blocked BEFORE any
	// direct-freedom / mask-* rule appended below can match it.
	rules := baseSecurityRules(in.hubIPs)

	// ── Per-plan direct-routing rules ──
	// Для каждого плана с прикреплёнными direct-сервисами добавляем правила
	// (user → direct-freedom) с фильтром по domain И/ИЛИ ip. Вставляются ДО
	// per-plan balancer rules и ДО no-exit blackhole — поэтому при падении
	// всех exit-нод пользователи с direct-сервисами продолжают достукиваться.
	// Services are global (a plan either has direct routing or not), so the
	// domain/IP lists are the same for every such plan.
	var directDomains, directIPs []string
	for _, svc := range in.directServices {
		if !svc.IsActive {
			continue
		}
		for _, d := range svc.Domains {
			if d != "" {
				directDomains = append(directDomains, "domain:"+d)
			}
		}
		directIPs = append(directIPs, svc.IP4...)
		directIPs = append(directIPs, svc.IP6...)
		directIPs = append(directIPs, svc.CIDR4...)
		directIPs = append(directIPs, svc.CIDR6...)
	}
	for _, planID := range in.directPlanIDs {
		var userIDs []string
		for _, client := range clients {
			// A cut-off user is absent from the whole config (clients and every
			// plan-* rule), like from the plan-<id>-users rule below.
			if client.PlanID == planID && client.UserID != "" && !quota.excludes(client) {
				userIDs = append(userIDs, client.UserID)
			}
		}
		if len(userIDs) == 0 {
			continue
		}
		// One rule per plan with a stable ruleTag (xraydiff: plan-* rules are
		// user-only and are applied live through `xray api adrules`).
		sort.Strings(userIDs)
		if len(directDomains) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"ruleTag":     fmt.Sprintf("plan-%d-direct-domains", planID),
				"user":        userIDs,
				"domain":      directDomains,
				"outboundTag": "direct-freedom",
			})
		}
		if len(directIPs) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"ruleTag":     fmt.Sprintf("plan-%d-direct-ip", planID),
				"user":        userIDs,
				"ip":          directIPs,
				"outboundTag": "direct-freedom",
			})
		}
	}

	// ── socks-awg (AmneziaWG / Palma) split-routing ──
	// The per-plan rules above are user-bound (VLESS UUID); the socks-awg SOCKS5
	// inbound (fed by tun2socks) has no user, so it would fall straight to the
	// catch-all exit-balancer (Amsterdam) — ALL traffic incl. RU. To match Happ's
	// split we emit inbound-bound rules from the SAME RU direct domains/IPs
	// (deduplicated) as soon as any plan has direct routing: RU → direct-freedom
	// (Hub Moscow), everything else falls through to the catch-all exit-balancer
	// rule below. Sorted for deterministic config output.
	if len(in.directPlanIDs) > 0 {
		if awgDomains := sortedUnique(directDomains); len(awgDomains) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"inboundTag":  []string{"socks-awg"},
				"domain":      awgDomains,
				"outboundTag": "direct-freedom",
			})
		}
		if awgIPs := sortedUnique(directIPs); len(awgIPs) > 0 {
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"inboundTag":  []string{"socks-awg"},
				"ip":          awgIPs,
				"outboundTag": "direct-freedom",
			})
		}
	}

	// Traffic masking routing rules. NOT premium-only, whatever the name
	// suggests: each rule matches its source domain for EVERY user and inbound
	// (socks-awg included) and sends it out of the Hub's own IP through its
	// mask-* freedom outbound — no SNI or Host rewriting happens (mask_domain is
	// unused). They precede the plan rules, the catch-all balancer and, with no
	// exit up, noExitBlockRule, so such domains keep working Hub-direct like the
	// RU-direct services. Kept as is (pre-existing behaviour, flagged for the
	// owner); checkNoDirectLeak requires them to follow the private-range block.
	for _, entry := range maskRouteEntries {
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"domain":      []string{"domain:" + entry.sourceDomain},
			"outboundTag": entry.outboundTag,
		})
	}

	routing := map[string]interface{}{
		"domainStrategy": "IPIfNonMatch",
		"rules":          rules,
	}

	// Если есть здоровые exit-ноды — маршрутизируем через них (балансировка)
	noExit := len(exitTags) == 0
	if !noExit {
		// Default balancer with all exit nodes
		balancers := []map[string]interface{}{
			newBalancer("exit-balancer", exitTags, settings.balancerStrategy),
		}

		// Per-plan balancers come from plan_servers and exit health only, never
		// from the users: one per plan with at least one healthy exit, in plan-ID
		// order, whether or not the plan has a user right now. routing.balancers
		// is structural for xraydiff, so a balancer created by a plan's first
		// subscriber (and dropped with its last one) would restart xray for
		// everyone. A balancer no rule references is valid for xray (its router
		// builds every balancer; only a rule naming a missing balancer fails).
		// A plan without plan_servers rows, or none of them healthy, has no
		// balancer: its users take the catch-all rule.
		planIDs := make([]int, 0, len(in.planServers))
		for id := range in.planServers {
			planIDs = append(planIDs, id)
		}
		sort.Ints(planIDs)
		planBalancers := map[int]string{} // planID → balancer tag
		for _, planID := range planIDs {
			var planTags []string
			for _, serverID := range in.planServers[planID] {
				if tag := fmt.Sprintf("exit-%d", serverID); healthyExit[tag] {
					planTags = append(planTags, tag)
				}
			}
			if len(planTags) == 0 {
				continue
			}
			tag := fmt.Sprintf("plan-%d-balancer", planID)
			planBalancers[planID] = tag
			balancers = append(balancers, newBalancer(tag, planTags, settings.balancerStrategy))
		}
		routing["balancers"] = balancers

		// Per-plan routing rules: ONE rule per plan (ruleTag plan-<id>-users) with the
		// sorted emails of its users → plan balancer. Each client has exactly one
		// PlanID, so user sets of different plans are disjoint and the semantics equal
		// the former one-rule-per-user list at the same position (before catch-all).
		// These rules are the only user-dependent part of the routing (xraydiff:
		// user-only, applied live).
		planUsers := map[int][]string{}
		for _, client := range clients {
			if quota.excludes(client) {
				continue // quota cutoff — user already missing from clients arrays
			}
			if _, ok := planBalancers[client.PlanID]; ok && client.UserID != "" {
				planUsers[client.PlanID] = append(planUsers[client.PlanID], client.UserID)
			}
		}
		usedPlanIDs := make([]int, 0, len(planUsers))
		for id := range planUsers {
			usedPlanIDs = append(usedPlanIDs, id)
		}
		sort.Ints(usedPlanIDs)
		for _, planID := range usedPlanIDs {
			users := planUsers[planID]
			sort.Strings(users)
			rules = append(rules, map[string]interface{}{
				"type":        "field",
				"ruleTag":     fmt.Sprintf("plan-%d-users", planID),
				"user":        users,
				"balancerTag": planBalancers[planID],
			})
		}

		// Default rule: all remaining traffic → default balancer with all exit nodes
		rules = append(rules, map[string]interface{}{
			"type":        "field",
			"network":     "tcp,udp",
			"balancerTag": "exit-balancer",
		})
		routing["rules"] = rules

	} else {
		// Нет здоровых exit-нод: блокируем ВЕСЬ пользовательский трафик на всех
		// пользовательских inbound (включая socks-awg, CDN).
		// Явные RU-direct правила выше продолжают работать. Исключение — DNS (порт 53):
		// клиенты AWG резолвят через 1.1.1.1/8.8.8.8 внутри туннеля, без ответа не
		// откроются даже RU-direct сайты. Запрос глушит outbound dns-hijack и отвечает
		// встроенным DNS xray (он уходит с Hub по dnsDirectRule); остальное блокируется.
		outbounds = append(outbounds, dnsHijackOutbound())
		rules = append(rules, noExitDNSRule(userInbounds), noExitBlockRule(userInbounds))
		routing["rules"] = rules
	}

	// Observatory: probes the exits for the balancers. Used by leastPing, and
	// by random/roundRobin too once a fallbackTag is set (see newBalancer).
	var observatory map[string]interface{}
	if !noExit {
		observatory = map[string]interface{}{
			"subjectSelector": []string{"exit-"},
			"probeInterval":   "30s",
			"probeURL":        "https://www.google.com/generate_204",
		}
	}

	// API routing rule: перенаправляем трафик api-in → api handler
	apiRule := map[string]interface{}{
		"type": "field", "inboundTag": []string{"api-in"}, "outboundTag": "api",
	}
	// Вставляем api rule первым в routing rules
	existingRules := routing["rules"].([]map[string]interface{})
	routing["rules"] = append([]map[string]interface{}{apiRule}, existingRules...)

	// API outbound (handler для Stats/Handler/RoutingService)
	outbounds = append(outbounds, map[string]interface{}{
		"protocol": "freedom", "tag": "api",
	})

	config := map[string]interface{}{
		"log": map[string]string{"loglevel": "warning"},
		// DNS: только IPv4 (IPv6 сломан у многих РФ-провайдеров — убираем задержку на AAAA)
		"dns": map[string]interface{}{
			"servers":       settings.dnsServers,
			"queryStrategy": "UseIPv4",
			"disableCache":  false,
			"tag":           xrayDNSTag,
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"routing":   routing,
		// Stats API: включаем сбор статистики
		"stats": map[string]interface{}{},
		// API handler: stats + live user/rule changes (Task 7). Listens only via
		// the loopback dokodemo inbound api-in (127.0.0.1:10085).
		"api": map[string]interface{}{
			"tag": "api",
			"services": []string{
				"HandlerService",
				"RoutingService",
				"StatsService",
			},
		},
		// Policy: учёт трафика + буферы + таймауты
		// Level 0 = standard, Level 1 = premium
		"policy": map[string]interface{}{
			"levels": map[string]interface{}{
				"0": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
					"handshake":         4,    // Таймаут handshake (сек)
					"connIdle":          3600, // Idle timeout (сек) — 1 час, не рвём long-lived (AI streaming, WebSocket, SSE)
					"uplinkOnly":        2,    // Ждать 2 сек после закрытия download перед закрытием upload
					"downlinkOnly":      5,    // Ждать 5 сек после закрытия upload перед закрытием download
					"bufferSize":        256,  // 256KB буфер на соединение (для видеостриминга)
				},
				"1": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
					"handshake":         4,
					"connIdle":          3600, // Premium: тот же 1 час idle, что и standard (раньше было 600)
					"uplinkOnly":        2,
					"downlinkOnly":      5,
					"bufferSize":        512, // Premium: 512KB буфер (стриминг HD видео)
				},
			},
			"system": map[string]interface{}{
				"statsInboundUplink":    true,
				"statsInboundDownlink":  true,
				"statsOutboundUplink":   true,
				"statsOutboundDownlink": true,
			},
		},
	}

	if observatory != nil {
		config["observatory"] = observatory
	}

	// Leak guard: never write a config whose default outbound or balancer
	// fallback could send user traffic out via the Hub's RU IP.
	if err := checkNoDirectLeak(inbounds, outbounds, routing); err != nil {
		return xrayBuild{}, fmt.Errorf("xray config leak guard: %w", err)
	}
	return xrayBuild{
		config:       config,
		vlessClients: len(vlessClients),
		excluded:     excluded,
		exits:        len(healthyExit),
		maskRules:    len(maskRouteEntries),
		noExit:       noExit,
	}, nil
}

// hasClientUUID reports whether a client with this UUID is in the list.
func hasClientUUID(clients []biz.ActiveVlessClient, uuid string) bool {
	for _, c := range clients {
		if c.UUID == uuid {
			return true
		}
	}
	return false
}

// sortedUnique returns the distinct values of s, sorted (nil for none).
func sortedUnique(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(s))
	for _, v := range s {
		set[v] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ─────────────── Applying (I/O around the generator) ───────────────

// applyXrayConfig loads the inputs, builds the xray config for the quota
// decision, validates it and puts it into service (unchanged / live / restart).
// excluded is the number of VLESS clients the cutoff left out. Only for
// SyncXrayConfig (via syncWithQuota, under xraySyncMu): it neither loads nor
// records the quota decision itself. A failed read aborts before anything is
// written: xray keeps its current config and runXraySync alerts.
func applyXrayConfig(quota quotaCutoff) (excluded int, err error) {
	keys, err := loadRealityKeys()
	if err != nil {
		return 0, fmt.Errorf("load reality keys: %w", err)
	}
	in, err := xrayDBReads.load(*keys)
	if err != nil {
		return 0, fmt.Errorf("xray sync aborted, xray keeps its current config: %w", err)
	}

	// Persist Reality public key info to app_settings for client consumption
	persistRealityPublicInfo(keys)

	b, err := buildXrayConfig(in, quota)
	if err != nil {
		return 0, err
	}
	if b.noExit {
		log.Println("[xray-sync] ВНИМАНИЕ: нет здоровых exit-нод, ВЕСЬ пользовательский трафик блокируется")
	}
	// The quota summary (how many were excluded) is logged by finishQuotaSync,
	// only once this config is in service.

	// Сериализуем конфиг
	configJSON, err := json.MarshalIndent(b.config, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshal xray config: %w", err)
	}

	// Atomic write: tmp file → rename (prevents partial writes on crash/power loss)
	// Xray requires .json extension for config format detection
	tmpPath := strings.TrimSuffix(xrayConfigPath, ".json") + ".tmp.json"
	if err := os.WriteFile(tmpPath, configJSON, 0600); err != nil {
		return 0, fmt.Errorf("write xray config (tmp): %w", err)
	}

	// Validate config before applying (same binary as the systemd unit and `xray api`)
	xrayBin := xrayBinary()
	validateCmd := exec.Command(xrayBin, "run", "-test", "-config", tmpPath)
	if output, err := validateCmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmpPath)
		return 0, fmt.Errorf("xray config validation failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	// The config being replaced: the subscription notification (M-6) compares
	// its client-facing part with the new one when no notified state exists yet.
	oldJSON, oldErr := os.ReadFile(xrayConfigPath)

	// ── Apply: unchanged / user-only via the xray API / restart (Task 7) ──
	// Canonical comparison (parsed JSON, xraydiff) with the config xray runs from.
	// User-only changes go live without a restart; structural ones, any API
	// error and a stale runtime (failed apply or restart) restart xray.
	start := time.Now()
	restartCause := ""
	action, diff, liveErr, err := xrayApplier{
		readRunning: func() ([]byte, error) { return os.ReadFile(xrayConfigPath) },
		applyLive:   func(d xraydiff.Result) error { return applyUserOnlyDiff(xrayCLIRunner(xrayBin), d) },
		install:     func() error { return os.Rename(tmpPath, xrayConfigPath) },
		discard:     func() { _ = os.Remove(tmpPath) },
		restart:     restartXray,
		// The restart cut every open session: forget revoked users BEFORE the
		// stale state is cleared, so a verdict on the old list cannot re-mark it.
		onRestart: func(cause string) {
			revokedUsers.reset()
			restartCause = cause
		},
	}.apply(configJSON, xrayStale)
	if liveErr != nil {
		RaiseAlert(xrayLiveAlertKey, AlertWarn, "Горячее применение пользователей не удалось",
			truncateAlertDetails(liveErr.Error())+" — откат на рестарт xray")
	}
	if action == xrayApplyLive {
		// rmu does not cut open sessions: watch the revoked users' traffic. Also
		// when the rename after the live apply failed (err != nil below): the
		// runtime already lacks them, while the restart the stale marker asks for
		// may be delayed.
		revoked := diff.Revoked()
		revokedUsers.record(revoked, revocationReasons(revoked, in.clients, quota), addedEmails(diff), time.Now())
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errXrayApplyFailed, err)
	}
	// xray now runs this config (installed, applied live or unchanged): the nil
	// error lets finishQuotaSync record the quota decision it carries.
	xraySubscriptionNotifier.afterApply(configJSON, oldJSON, oldErr, action == xrayApplyRestart)
	switch action {
	case xrayApplyUnchanged:
		log.Println("[xray-sync] Конфиг не изменился, пропускаем restart")
		return b.excluded, nil
	case xrayApplyLive:
		ResolveAlert(xrayLiveAlertKey, "Горячее применение пользователей снова работает")
		added, removed := diff.UserTotals()
		log.Printf("[xray-sync] user-only applied via API: +%d/-%d users, rules %v (routing pushed=%v), %d ms",
			added, removed, diff.ChangedRules, diff.RoutingChanged, time.Since(start).Milliseconds())
		return b.excluded, nil
	}

	// Restarted: structural change, failed API apply or stale runtime.
	recordXrayRestart(restartCause)

	log.Printf("[xray-sync] Конфиг обновлён (рестарт: %s): %d VLESS клиентов, %d exit-нод, %d правил маскировки",
		restartCause, b.vlessClients, b.exits, b.maskRules)

	return b.excluded, nil
}

// persistRealityPublicInfo stores the Reality public key and short id for the
// subscription builders. A failed write is logged, not fatal: xray does not
// depend on it, and the next sync writes again.
func persistRealityPublicInfo(keys *xrayRealityKeys) {
	for _, kv := range [][2]string{{"reality_public_key", keys.PublicKey}, {"reality_short_id", keys.ShortID}} {
		_, err := withSyncTimeout(func(ctx context.Context) (struct{}, error) {
			return struct{}{}, biz.SetSettingContext(ctx, kv[0], kv[1])
		})
		if err != nil {
			log.Printf("[xray-sync] WARN: не удалось сохранить %s: %v", kv[0], err)
		}
	}
}

// revocationReasonsLookup is the database lookup of revocationReasons (test seam).
var revocationReasonsLookup = biz.RevocationReasonsContext

// revocationReasons labels the users a live apply revoked, for the revoked
// guard's logs and the restart-storm alert: "quota" when the user is still an
// active client and only the quota cutoff left it out, otherwise what the
// database says (blocked / expired / no-uuid / deleted). A failed lookup labels
// them "unknown": the labels decide nothing.
func revocationReasons(revoked []string, clients []biz.ActiveVlessClient, quota quotaCutoff) map[string]string {
	if len(revoked) == 0 {
		return nil
	}
	byEmail := make(map[string]biz.ActiveVlessClient, len(clients))
	for _, c := range clients {
		byEmail[strings.ToLower(c.UserID)] = c
	}
	reasons := make(map[string]string, len(revoked))
	var lookup []string
	for _, e := range revoked {
		e = strings.ToLower(e)
		if c, ok := byEmail[e]; ok && quota.excludes(c) {
			reasons[e] = revokedReasonQuota
			continue
		}
		lookup = append(lookup, e)
	}
	if len(lookup) == 0 {
		return reasons
	}
	found, err := withSyncTimeout(func(ctx context.Context) (map[string]string, error) {
		return revocationReasonsLookup(ctx, lookup)
	})
	if err != nil {
		log.Printf("[xray-live] причины отзыва не прочитаны: %v", err)
	}
	for _, e := range lookup {
		if r := found[e]; r != "" {
			reasons[e] = r
		} else {
			reasons[e] = biz.RevokedUnknown
		}
	}
	return reasons
}

// buildExitNodeOutbound создаёт Xray outbound для exit-ноды.
// Поддерживает VLESS Reality, Shadowsocks и прокси-протоколы. fingerprint
// (reality_fingerprint) and mux (mux_concurrency) come from the sync inputs.
// nil for a node without a usable tunnel config (logged; the same on every
// sync for the same row).
func buildExitNodeOutbound(node biz.ExitNodeTunnel, fingerprint string, mux int) map[string]interface{} {
	tag := fmt.Sprintf("exit-%d", node.ID)

	switch node.TunnelProtocol {
	case "vless":
		if node.TunnelUUID == "" || len(node.TunnelUUID) < 32 {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — невалидный tunnel_uuid", node.ID, node.Name)
			return nil
		}
		// Валидация: publicKey должен быть base64-строкой длиной ~43 символа (x25519)
		if node.TunnelPubKey != "" && len(node.TunnelPubKey) < 40 {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — невалидный tunnel_public_key %q", node.ID, node.Name, node.TunnelPubKey)
			return nil
		}
		sni := node.TunnelSNI
		if sni == "" {
			sni = node.IPAddress
		}
		// Без flow: "xtls-rprx-vision" — чтобы работал mux (мультиплексирование).
		// Vision хорош для single-stream, но mux критичен для скорости
		// при множестве мелких подключений (Telegram, Instagram, YouTube).
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "vless",
			"settings": map[string]interface{}{
				"vnext": []map[string]interface{}{
					{
						"address": node.IPAddress, "port": node.TunnelPort,
						"users": []map[string]interface{}{
							{"id": node.TunnelUUID, "encryption": "none"},
						},
					},
				},
			},
			"streamSettings": map[string]interface{}{
				"network": "tcp", "security": "reality",
				"realitySettings": map[string]interface{}{"fingerprint": fingerprint, "serverName": sni},
				"sockopt": map[string]interface{}{
					"tcpFastOpen":          true,
					"tcpNoDelay":           true,
					"tcpKeepAliveInterval": 30,
				},
			},
			// Мультиплексирование: несколько стримов по одному VLESS соединению.
			// Кратно снижает время установки подключения (один TLS handshake на все стримы).
			"mux": map[string]interface{}{
				"enabled":         true,
				"concurrency":     mux,
				"xudpConcurrency": 16,      // UDP мультиплексирование (DNS, QUIC)
				"xudpProxyUDP443": "allow", // Разрешаем QUIC/HTTP3 — ускоряет YouTube, Instagram
			},
		}
		// Добавляем publicKey и shortId если заданы
		if node.TunnelPubKey != "" {
			rs := outbound["streamSettings"].(map[string]interface{})["realitySettings"].(map[string]interface{})
			rs["publicKey"] = node.TunnelPubKey
			if node.TunnelShortID != "" {
				rs["shortId"] = node.TunnelShortID // singular — клиентская сторона Reality
			}
		}
		return outbound

	case "shadowsocks":
		if node.TunnelSSPass == "" {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — нет tunnel_ss_password", node.ID, node.Name)
			return nil
		}
		method := node.TunnelSSMethod
		if method == "" {
			method = "2022-blake3-aes-128-gcm"
		}
		// Stream transport: port 443 → WS (legacy, masquerades as HTTPS through nginx);
		// any other port → plain TCP (lower latency, no WS framing overhead).
		// Plain TCP is preferred when the exit listens on a dedicated SS port
		// (e.g. :8388) — saves the WS handshake RTT and per-message framing.
		streamSettings := map[string]interface{}{
			"network": "tcp",
			"sockopt": map[string]interface{}{
				"tcpFastOpen":          true, // Экономия 1 RTT
				"tcpNoDelay":           true, // Без буферизации Nagle
				"tcpKeepAliveInterval": 30,   // Keepalive от DPI
			},
		}
		if node.TunnelPort == 443 {
			streamSettings["network"] = "ws"
			streamSettings["wsSettings"] = map[string]interface{}{"path": "/stream"}
		}
		return map[string]interface{}{
			"tag": tag, "protocol": "shadowsocks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address":  node.IPAddress,
						"port":     node.TunnelPort,
						"method":   method,
						"password": node.TunnelSSPass,
					},
				},
			},
			"streamSettings": streamSettings,
			// NOTE: NO mux — пользователь сообщил bistable behavior ("летает или n/a"
			// при подключении). Mux multiplexes user streams over one TCP connection
			// to exit; if mux session is in flaky state (cold start, reconnect),
			// new user requests may block or fail intermittently while the mux
			// pool stabilizes. Without mux, each user request opens its own TCP
			// to exit — more handshakes (slightly higher latency) but deterministic
			// per-stream success/failure, no shared mux state to coordinate.
			// xudpProxyUDP443=allow (mux-only feature) also removed: YouTube QUIC
			// now falls back to TCP HTTPS through the tunnel — slower in theory
			// but more reliable in practice on flaky mobile networks.
		}

	case "hysteria2":
		// Hysteria2: UDP/QUIC — нет head-of-line blocking, стабильный пинг.
		// Hysteria2 клиент бежит отдельным сервисом (hysteria2-client.service)
		// и предоставляет локальный SOCKS5 на 127.0.0.1:1090.
		// Xray просто маршрутизирует через этот SOCKS5.
		hy2Port := 1088 + node.ID // ID=2 → 1090, ID=3 → 1091, etc
		return map[string]interface{}{
			"tag": tag, "protocol": "socks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address": "127.0.0.1",
						"port":    hy2Port,
					},
				},
			},
		}

	case "socks5":
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "socks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address": node.IPAddress,
						"port":    node.TunnelPort,
					},
				},
			},
			"mux": map[string]interface{}{"enabled": true, "concurrency": mux},
		}
		if node.TunnelUUID != "" {
			servers := outbound["settings"].(map[string]interface{})["servers"].([]map[string]interface{})
			servers[0]["users"] = []map[string]interface{}{
				{"user": "heyearth", "pass": node.TunnelUUID},
			}
		}
		return outbound

	case "http":
		outbound := map[string]interface{}{
			"tag": tag, "protocol": "http",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{
					{
						"address": node.IPAddress,
						"port":    node.TunnelPort,
					},
				},
			},
			"mux": map[string]interface{}{"enabled": true, "concurrency": mux},
		}
		if node.TunnelUUID != "" {
			servers := outbound["settings"].(map[string]interface{})["servers"].([]map[string]interface{})
			servers[0]["users"] = []map[string]interface{}{
				{"user": "heyearth", "pass": node.TunnelUUID},
			}
		}
		return outbound

	default:
		log.Printf("[xray-sync] exit-нода %d (%s): неизвестный протокол %q", node.ID, node.Name, node.TunnelProtocol)
		return nil
	}
}

// ─────────────────────── Tunnel Health Monitoring ───────────────────────
//
// Фоновый мониторинг состояния туннелей до exit-нод.
// Каждые 30 секунд проверяет каждую exit-ноду по её протоколу (checkTunnelHealth):
// VLESS — TCP + TLS handshake, Shadowsocks и прочие — TCP connect, Hysteria2 —
// HTTP 204 через локальный SOCKS5 клиента (весь путь Hub → exit → интернет).
// Hysteresis: нужно 3 consecutive неудачи → unhealthy, 2 consecutive успеха → healthy.
// При изменении статуса — пересинхронизирует Xray конфиг.

const (
	hysteresisUnhealthy = 3 // consecutive failures before marking unhealthy
	hysteresisHealthy   = 2 // consecutive successes before marking healthy
)

// healthCounter tracks consecutive check results per node for hysteresis.
var healthCounter sync.Map // map[int]*nodeHealthCounter

type nodeHealthCounter struct {
	consecutiveOK   int
	consecutiveFail int
}

// mtprotoFailCount / mtprotoSuccessCount track consecutive MTProto probe
// results per server ID for 3-fail / 2-success hysteresis.
var (
	mtprotoFailCount    = map[int]int{}
	mtprotoSuccessCount = map[int]int{}
	mtprotoCountMu      sync.Mutex
)

// StartTunnelHealthMonitor запускает фоновый мониторинг exit-нод.
func StartTunnelHealthMonitor() {
	go func() {
		// Первая проверка через 10 секунд после старта
		time.Sleep(10 * time.Second)
		checkAllTunnels()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			checkAllTunnels()
		}
	}()
	log.Printf("[tunnel-monitor] Фоновый мониторинг exit-нод запущен (каждые 30 сек, hysteresis %d/%d)",
		hysteresisUnhealthy, hysteresisHealthy)
}

func getHealthCounter(nodeID int) *nodeHealthCounter {
	val, _ := healthCounter.LoadOrStore(nodeID, &nodeHealthCounter{})
	return val.(*nodeHealthCounter)
}

// checkAllTunnels проверяет все exit-ноды и обновляет их статус.
func checkAllTunnels() {
	nodes, err := biz.ListAllExitNodes()
	if err != nil {
		log.Printf("[tunnel-monitor] Ошибка загрузки exit-нод: %v", err)
		return
	}

	if len(nodes) == 0 {
		// No online exit at all: SyncXrayConfig blocks every user inbound.
		RaiseAlert("all-exits-down", AlertCritical, "Нет ни одной exit-ноды",
			"В servers нет online exit-нод — пользовательский трафик заблокирован (кроме RU-direct).")
		return
	}

	statusChanged := false
	healthyCount := 0
	for _, node := range nodes {
		probeResult, latencyMs, probeError := checkTunnelHealth(node)

		// Persist per-probe RTT for adaptive Express-tunnel exit selection
		// (biz.DecideExpressTunnel). We write last_rtt_ms/last_rtt_at on EVERY
		// successful probe — independently of hysteresis-driven status flips —
		// so the lowest-RTT exit selection always sees fresh data. Failures
		// are intentionally ignored so transient blips don't poison selection.
		if probeResult == "healthy" && latencyMs > 0 {
			if rttErr := biz.UpdateExitRTT(node.ID, latencyMs); rttErr != nil {
				log.Printf("[tunnel-monitor] UpdateExitRTT(%d) failed: %v", node.ID, rttErr)
			}
		}

		oldStatus := node.TunnelStatus
		hc := getHealthCounter(node.ID)

		// Hysteresis counters -> effective status + alert action (see evalExitHealth).
		newStatus, alertAction := evalExitHealth(oldStatus, probeResult, hc)
		if newStatus == "healthy" {
			healthyCount++
		}

		// The per-exit alert follows the STATUS on every tick, not just the flip:
		// RaiseAlert/ResolveAlert dedup by key, so steady state is silent, while a
		// lost or failed delivery, a flap inside the cooldown or an API restart with
		// the exit already down are all retried on the next tick.
		switch alertAction {
		case exitAlertRaise:
			RaiseAlert(exitDownAlertKey(node.ID), AlertWarn,
				fmt.Sprintf("Exit-нода %s недоступна", node.Name),
				exitDownDetails(node, probeError))
		case exitAlertResolve:
			ResolveAlert(exitDownAlertKey(node.ID), exitUpMessage(node, latencyMs))
		}

		if newStatus != oldStatus {
			if err := biz.UpdateTunnelStatusWithError(node.ID, newStatus, latencyMs, probeError); err != nil {
				log.Printf("[tunnel-monitor] Ошибка обновления статуса ноды %d: %v", node.ID, err)
				continue
			}
			// Reset counters after status flip.
			hc.consecutiveOK = 0
			hc.consecutiveFail = 0

			statusChanged = true

			if newStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] ALERT: Exit-нода %d (%s, %s) — туннель НЕДОСТУПЕН (после %d проверок)",
					node.ID, node.Name, node.IPAddress, hysteresisUnhealthy)
				biz.RecordFailedConnection(node.ID)
			} else if newStatus == "healthy" && oldStatus == "unhealthy" {
				log.Printf("[tunnel-monitor] OK: Exit-нода %d (%s, %s) — туннель ВОССТАНОВЛЕН",
					node.ID, node.Name, node.IPAddress)
				biz.RecordSuccessfulConnection(node.ID)
			} else {
				log.Printf("[tunnel-monitor] Exit-нода %d (%s): %s -> %s",
					node.ID, node.Name, oldStatus, newStatus)
			}
		} else {
			// Обновляем last_tunnel_check при КАЖДОЙ проверке, независимо от статуса
			_ = biz.UpdateTunnelStatusWithError(node.ID, newStatus, latencyMs, probeError)
		}
	}

	// Evaluated every cycle (RaiseAlert dedups by key with a 30-min cooldown),
	// so an outage that began before an API restart still alerts.
	if healthyCount == 0 {
		RaiseAlert("all-exits-down", AlertCritical, "Все exit-ноды недоступны",
			fmt.Sprintf("0 из %d exit-нод healthy — пользовательский трафик заблокирован (кроме RU-direct).", len(nodes)))
	} else {
		ResolveAlert("all-exits-down",
			fmt.Sprintf("Exit-ноды снова доступны: %d из %d healthy", healthyCount, len(nodes)))
	}

	// Если статус какой-то ноды изменился — пересинхронизируем Xray (debounced)
	if statusChanged {
		log.Printf("[tunnel-monitor] Статус exit-нод изменился, запрос на пересинхронизацию Xray...")
		RequestXraySync()
	}

	// ── MTProto :mtproto_port TCP probe ──
	// Runs after the main tunnel loop every 30 s. Uses 3-fail / 2-success
	// hysteresis independently of the general tunnel-health counters.
	checkMTProtoHealth()
}

// checkMTProtoHealth probes the MTProto port on every online exit node that
// has MTProto installed (mtproto_installed_at IS NOT NULL) and updates
// mtproto_status via biz.MarkMTProtoStatus with 3-fail / 2-success hysteresis.
func checkMTProtoHealth() {
	mtprotoPort := biz.GetSettingOrDefault("mtproto_port", "8443")

	rows, err := db.DB.Query(`
		SELECT id, ip_address, mtproto_status
		  FROM servers
		 WHERE is_exit_node = TRUE
		   AND lifecycle_state = 'online'
		   AND mtproto_installed_at IS NOT NULL
	`)
	if err != nil {
		log.Printf("[mtproto-health] query error: %v", err)
		return
	}

	type probeRow struct {
		ID     int
		IP     string
		Status string
	}
	var probes []probeRow
	for rows.Next() {
		var p probeRow
		if scanErr := rows.Scan(&p.ID, &p.IP, &p.Status); scanErr == nil {
			probes = append(probes, p)
		}
	}
	rows.Close()

	for _, p := range probes {
		addr := fmt.Sprintf("%s:%s", p.IP, mtprotoPort)
		conn, dialErr := net.DialTimeout("tcp", addr, 3*time.Second)
		ok := dialErr == nil
		if conn != nil {
			_ = conn.Close()
		}

		mtprotoCountMu.Lock()
		if ok {
			mtprotoSuccessCount[p.ID]++
			mtprotoFailCount[p.ID] = 0
			if mtprotoSuccessCount[p.ID] >= 2 && p.Status != "healthy" {
				if markErr := biz.MarkMTProtoStatus(p.ID, "healthy"); markErr != nil {
					log.Printf("[mtproto-health] MarkMTProtoStatus(%d, healthy): %v", p.ID, markErr)
				} else {
					log.Printf("[mtproto-health] server %d (%s) → healthy after 2 successes", p.ID, p.IP)
				}
			} else {
				// Always refresh mtproto_last_check even without a status change.
				_ = biz.MarkMTProtoStatus(p.ID, p.Status)
			}
		} else {
			mtprotoFailCount[p.ID]++
			mtprotoSuccessCount[p.ID] = 0
			if mtprotoFailCount[p.ID] >= 3 && p.Status != "unhealthy" {
				if markErr := biz.MarkMTProtoStatus(p.ID, "unhealthy"); markErr != nil {
					log.Printf("[mtproto-health] MarkMTProtoStatus(%d, unhealthy): %v", p.ID, markErr)
				} else {
					log.Printf("[mtproto-health] server %d (%s) → unhealthy after 3 fails: %v", p.ID, p.IP, dialErr)
				}
			} else {
				_ = biz.MarkMTProtoStatus(p.ID, p.Status)
			}
		}
		mtprotoCountMu.Unlock()
	}
}

// checkTunnelHealth проверяет доступность exit-ноды по протоколу.
//   - VLESS: TCP + TLS handshake (Reality endpoint)
//   - Shadowsocks: TCP connect (WS порт 443)
//   - Hysteria2: GET generate_204 (gstatic, затем cloudflare) через локальный SOCKS5
//     Hysteria2-клиента (весь путь до exit); здоров, если любой из двух отвечает 204
//   - Остальные: TCP connect
//
// Возвращает статус и latency в миллисекундах.
func checkTunnelHealth(node biz.ExitNodeTunnel) (status string, latencyMs int, errMsg string) {
	start := time.Now()

	switch node.TunnelProtocol {
	case "vless":
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("vless %s: %v", addr, err)
		}
		sni := node.TunnelSNI
		if sni == "" {
			sni = node.IPAddress
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
		})
		tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			return "unhealthy", 0, fmt.Sprintf("vless %s: TLS handshake failed: %v", addr, err)
		}
		tlsConn.Close()

	case "hysteria2":
		// C4: a listening local SOCKS port says nothing about the exit. Fetch a
		// real 204 THROUGH the tunnel (Hub -> hysteria2 client -> exit -> internet).
		hy2Port := 1088 + node.ID
		localAddr := fmt.Sprintf("127.0.0.1:%d", hy2Port)
		// gstatic first, cloudflare as a fallback: healthy if either answers 204.
		// The error is a single line naming the exit and the SOCKS address once.
		rtt, err := probeExitViaSOCKS5(localAddr, exitProbeTargets, exitProbeTimeout)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("hysteria2 exit #%d via %s: %v", node.ID, localAddr, err)
		}
		ms := int(rtt.Milliseconds())
		if ms < 1 {
			ms = 1
		}
		return "healthy", ms, ""

	case "shadowsocks":
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("shadowsocks %s: %v", addr, err)
		}
		conn.Close()

	default:
		addr := fmt.Sprintf("%s:%d", node.IPAddress, node.TunnelPort)
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return "unhealthy", 0, fmt.Sprintf("%s %s: %v", node.TunnelProtocol, addr, err)
		}
		conn.Close()
	}

	latencyMs = int(time.Since(start).Milliseconds())
	return "healthy", latencyMs, ""
}

// ─────────────────────── Sync Loop ───────────────────────

// StartXraySyncLoop запускает фоновую горутину для периодической синхронизации Xray.
func StartXraySyncLoop() {
	// Проверяем что Xray установлен
	if _, err := os.Stat(xrayConfigPath); os.IsNotExist(err) {
		log.Println("[xray-sync] Xray не установлен, синхронизация отключена")
		return
	}

	// Stale marker of the previous process + leftover API payload files.
	prepareXrayLiveApply()

	// Начальная синхронизация
	runXraySync("Начальная синхронизация")

	// Периодическая синхронизация каждые 5 минут
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runXraySync("Периодическая синхронизация")
		}
	}()

	log.Println("[xray-sync] Фоновая синхронизация запущена (каждые 5 мин)")

	// Запускаем мониторинг туннелей до exit-нод
	StartTunnelHealthMonitor()

	// Запускаем мониторинг истечения mTLS сертификатов
	StartCertExpiryMonitor()

	// Запускаем auto-tuner для оптимизации VPN конфигов
	StartAutoTuner()
}

// ─────────────────────── mTLS Certificate Expiry Monitor ───────────────────────

// StartCertExpiryMonitor проверяет mTLS сертификаты при старте и каждые 24 часа.
// Логирует WARNING если сертификат истекает в течение 30 дней.
func StartCertExpiryMonitor() {
	go func() {
		checkCertExpiry()
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			checkCertExpiry()
		}
	}()
}

func checkCertExpiry() {
	certsDir := filepath.Join(config.AppDir(), "certs")
	certFiles := []string{"ca-cert.pem", "client-cert.pem", "server-cert.pem"}

	for _, name := range certFiles {
		path := filepath.Join(certsDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue // файл не существует — OK, может быть не настроен
		}
		block, _ := pem.Decode(data)
		if block == nil {
			log.Printf("[cert-monitor] %s: не удалось прочитать PEM", name)
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			log.Printf("[cert-monitor] %s: ошибка парсинга: %v", name, err)
			continue
		}
		daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
		if daysLeft < 0 {
			log.Printf("[cert-monitor] CRITICAL: %s ИСТЁК %d дней назад!", name, -daysLeft)
		} else if daysLeft < 7 {
			log.Printf("[cert-monitor] CRITICAL: %s истекает через %d дней!", name, daysLeft)
		} else if daysLeft < 30 {
			log.Printf("[cert-monitor] WARNING: %s истекает через %d дней", name, daysLeft)
		}
	}
}
