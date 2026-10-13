package main

import (
	cryptorand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// autoTunerIter counts completed runFullTuner iterations. Used by
// rotateHysteria2ObfsIfDue to fire every ~7 days (42 × 4h).
var autoTunerIter int

// ─────────────────────── Auto-Tuner v2 ───────────────────────
//
// Comprehensive VPN configuration optimizer. Tests ALL tunable parameters
// that affect DPI evasion and connection quality:
//
// SERVER-SIDE (applied automatically, user doesn't notice):
//   - Reality SNI domain (dest + serverNames for hub inbound)
//   - DNS servers (8.8.8.8 vs 1.1.1.1 vs DoH endpoints)
//   - Mux concurrency settings for Hub→Exit tunnels
//   - TCP sockopt (keepalive interval)
//   - Balancer strategy: not tuned — exit balancers are always leastLoad with per-path costs (xray_exits.go)
//   - Hub→Exit tunnel SNI per exit node
//
// CLIENT-SIDE (changes VLESS URI → increments config_version → users must refresh subscription):
//   - TLS fingerprint (chrome/firefox/safari/edge)
//   - Transport type (tcp/ws/grpc)
//   - Flow parameter (xtls-rprx-vision vs none)
//
// When client-side params change, `config_version` in app_settings is incremented.
// The subscription endpoint includes version in Subscription-Userinfo header.
// User dashboard shows "config updated, refresh subscription" banner when version mismatches.

// ─── Candidate parameters ───

// Per-country SNI candidates.
// Key = 2-letter country code of the server (Hub or Exit node).
// Good SNI = domain hosted on same/similar ASN, high traffic, not blocked.
// Bad SNI = ASN mismatch obvious to DPI (e.g. apple.com from non-Apple IP).
var sniByCountry = map[string][]string{
	// Russia — for Hub in Russia. Russian domains are OK because server is also in RU.
	// Avoid: apple.com (ASN mismatch), google.com (actively monitored)
	"RU": {
		"yandex.ru",
		"mail.ru",
		"vk.com",
		"cdn.jsdelivr.net", // CDN, works from any IP
		"cdnjs.cloudflare.com",
		"static.cloudflareinsights.com",
		"fonts.googleapis.com", // Google CDN — less scrutinized than google.com
		"ajax.googleapis.com",
	},
	// Netherlands — for exit nodes in NL (Amsterdam)
	"NL": {
		"github.com",    // Microsoft Azure, stable, no RU servers since 2022
		"www.twitch.tv", // AWS, high traffic, no Russian PoP
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
		"static.cloudflareinsights.com",
		"www.microsoft.com",
		"cloudflare.com",
		"fonts.googleapis.com",
	},
	// Latvia — for exit nodes in LV
	"LV": {
		"github.com",
		"www.twitch.tv",
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
		"cloudflare.com",
		"www.microsoft.com",
		"static.cloudflareinsights.com",
	},
	// Germany — common VPS location
	"DE": {
		"github.com",
		"www.twitch.tv",
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
		"www.microsoft.com",
		"cloudflare.com",
		"fonts.googleapis.com",
	},
	// Finland
	"FI": {
		"github.com",
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
		"www.microsoft.com",
		"cloudflare.com",
	},
	// Default — for any country not listed above
	"DEFAULT": {
		"github.com",
		"cdn.jsdelivr.net",
		"cdnjs.cloudflare.com",
		"static.cloudflareinsights.com",
		"cloudflare.com",
		"www.twitch.tv",
		"www.microsoft.com",
		"fonts.googleapis.com",
	},
}

// candidateSNIsForCountry returns SNI list for a given country code.
func candidateSNIsForCountry(countryCode string) []string {
	cc := strings.ToUpper(strings.TrimSpace(countryCode))
	if list, ok := sniByCountry[cc]; ok {
		return list
	}
	return sniByCountry["DEFAULT"]
}

var candidateFingerprints = []string{"chrome", "firefox", "safari", "edge"}

// NOTE: transport and flow are FIXED for VLESS Reality (tcp + xtls-rprx-vision).
// ws/grpc/empty-flow break Reality. Only fingerprint is tunable client-side.
var candidateDNS = [][]string{
	{"8.8.8.8", "1.1.1.1"},
	{"1.1.1.1", "8.8.8.8"},
	{"8.8.4.4", "1.0.0.1"},
}
var candidateMuxConcurrency = []int{4, 8, 16}

const (
	tunerPingsPerTest          = 5
	tunerMaxLatencyMs          = 500
	tunerTimeout               = 8 * time.Second // increased from 5s for unstable networks
	tunerRetries               = 2               // retry failed pings up to 2 times
	tunerSignificanceThreshold = 20              // minimum improvement % to switch (avoid flapping)
)

// testResult stores one parameter combination test.
type testResult struct {
	// What was tested
	SNI         string `json:"sni"`
	Fingerprint string `json:"fingerprint"`
	Transport   string `json:"transport"`
	Flow        string `json:"flow"`
	Category    string `json:"category"` // "sni", "fingerprint", "transport", "flow", "dns", "mux", "combined"

	// Results
	Pings       []int  `json:"pings"`
	AvgLatency  int    `json:"avg_latency"`
	SuccessRate int    `json:"success_rate"`
	IsStable    bool   `json:"is_stable"`
	ErrorMsg    string `json:"error_msg"`
}

// optimalConfig represents the best discovered configuration.
type optimalConfig struct {
	SNI            string `json:"sni"`
	Fingerprint    string `json:"fingerprint"`
	Transport      string `json:"transport"`
	Flow           string `json:"flow"`
	DNS            string `json:"dns"`
	MuxConcurrency int    `json:"mux_concurrency"`
	AvgLatency     int    `json:"avg_latency"`
}

// ─── Startup ───

func StartAutoTuner() {
	enabled := biz.GetSettingOrDefault("auto_tuner_enabled", "false")
	if enabled != "true" {
		log.Println("[auto-tuner] Disabled via setting")
		return
	}

	intervalStr := biz.GetSettingOrDefault("auto_tuner_interval_hours", "4")
	intervalHours, _ := strconv.Atoi(intervalStr)
	if intervalHours < 1 {
		intervalHours = 4
	}

	// Ensure config_version exists
	if v := biz.GetSettingOrDefault("config_version", ""); v == "" {
		_ = biz.SetSetting("config_version", "1")
	}

	go func() {
		time.Sleep(30 * time.Second)
		runFullTuner()

		ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// Re-read interval in case it changed
				if h, _ := strconv.Atoi(biz.GetSettingOrDefault("auto_tuner_interval_hours", "4")); h > 0 && h != intervalHours {
					intervalHours = h
					ticker.Reset(time.Duration(h) * time.Hour)
				}
				if biz.GetSettingOrDefault("auto_tuner_enabled", "false") != "true" {
					continue
				}
				runFullTuner()
			case <-admin.AutoTunerRunChan:
				// Manual trigger from admin UI
				log.Println("[auto-tuner] Manual run triggered from admin")
				runFullTuner()
			}
		}
	}()
	admin.SetAutoTunerLoopRunning(true) // «Запустить сейчас» has a reader now

	log.Printf("[auto-tuner] Started v2 (interval: %dh, params: SNI per-country, Fingerprint×%d, DNS×%d, MuxAdaptive, xPaddingBytes)",
		intervalHours, len(candidateFingerprints), len(candidateDNS))
}

// ─── Main run ───

func runFullTuner() {
	log.Println("[auto-tuner] Starting full optimization run...")
	_ = biz.SetSetting("auto_tuner_status", "running")
	_ = biz.SetSetting("auto_tuner_last_run", time.Now().Format(time.RFC3339))

	defer func() {
		_ = biz.SetSetting("auto_tuner_status", "idle")
	}()

	serverChanged := false
	clientChanged := false

	// ── Phase 1: Server-side params (Hub inbound SNI) ──
	if newSNI := tuneSNI(); newSNI != "" {
		serverChanged = true
	}

	// ── Phase 2: DNS optimization ──
	if optimizeDNS() {
		serverChanged = true
	}

	// ── Phase 3: Mux optimization ──
	if optimizeMux() {
		serverChanged = true
	}

	// ── Phase 4: Client-side params (fingerprint + transport + flow) ──
	if optimizeClientParams() {
		clientChanged = true
	}

	// ── Phase 5: Exit node SNI tuning ──
	nodes, err := biz.ListAllExitNodes()
	if err == nil {
		for _, node := range nodes {
			if !isAutoTuneEnabled(node.ID) {
				continue
			}
			if tuneExitNodeSNI(node) {
				serverChanged = true
			}
		}
	}

	// ── Apply changes ──
	if serverChanged || clientChanged {
		log.Println("[auto-tuner] Changes detected, triggering Xray sync...")
		RequestXraySync()
	}

	if clientChanged {
		// Increment config_version so clients know to refresh subscription
		incrementConfigVersion()
		log.Println("[auto-tuner] Client config changed — config_version incremented, users should refresh subscription")
	}

	// Hysteria2 obfs rotation — weekly cadence. Independent of Xray tuning;
	// piggybacks on the 4h tuner loop to avoid spawning another goroutine.
	rotateHysteria2ObfsIfDue(autoTunerIter)
	autoTunerIter++

	log.Println("[auto-tuner] Full optimization run complete")
}

// rotateHysteria2ObfsIfDue regenerates hysteria2_obfs_password once every
// ~7 days (42 iterations × 4h ≈ 168h). Bumps hysteria2_config_version so
// downstream sync detects the change. Existing Express users get the new
// URI on their next /api/v1/user/subscribe/<token> fetch (Happ refetches
// per Subscription-Userinfo `expire` timestamp).
func rotateHysteria2ObfsIfDue(iter int) {
	if iter == 0 {
		return // skip first iteration after boot
	}
	if iter%42 != 0 {
		return
	}
	newObfs := randHex32()
	if err := biz.SetSetting("hysteria2_obfs_password", newObfs); err != nil {
		log.Printf("[auto-tuner] obfs rotation: SET failed: %v", err)
		return
	}
	verStr := biz.GetSettingOrDefault("hysteria2_config_version", "0")
	ver, _ := strconv.Atoi(verStr)
	_ = biz.SetSetting("hysteria2_config_version", strconv.Itoa(ver+1))
	_ = biz.SetSetting("hysteria2_obfs_rotated_at", time.Now().Format(time.RFC3339))
	log.Printf("[auto-tuner] hysteria2 obfs rotated (config_version=%d)", ver+1)
	biz.RequestSync()
}

func randHex32() string {
	b := make([]byte, 16)
	_, _ = cryptorand.Read(b)
	return hex.EncodeToString(b)
}

// ─── Phase 1: SNI tuning (server-side) ───

func tuneSNI() string {
	currentSNI := biz.GetSettingOrDefault("reality_sni_domain", biz.DefaultRealitySNI)
	// Hub is in Russia — use RU-specific SNI list
	hubCountry := biz.GetSettingOrDefault("hub_country", "RU")
	candidates := candidateSNIsForCountry(hubCountry)
	var results []testResult

	for _, sni := range candidates {
		r := testSNIReachability(sni)
		r.Category = "sni"
		results = append(results, r)
		saveTestResult("hub_inbound", 0, r)
	}

	best := pickBestResult(results)
	if best == nil || best.SNI == currentSNI {
		if best != nil {
			log.Printf("[auto-tuner] SNI: %s still optimal (avg %dms)", currentSNI, best.AvgLatency)
		}
		return ""
	}

	// Check significance threshold — don't switch for marginal improvement
	currentLatency := getLatencyForSNI(results, currentSNI)
	if currentLatency > 0 && !isSignificantImprovement(currentLatency, best.AvgLatency) {
		log.Printf("[auto-tuner] SNI: %s → %s skipped (improvement %dms → %dms < %d%% threshold)",
			currentSNI, best.SNI, currentLatency, best.AvgLatency, tunerSignificanceThreshold)
		return ""
	}

	log.Printf("[auto-tuner] SNI: %s → %s (avg %dms → %dms)", currentSNI, best.SNI, currentLatency, best.AvgLatency)
	_ = biz.SetSetting("reality_sni_domain", best.SNI)
	markApplied("hub_inbound", 0, best)
	return best.SNI
}

// ─── Phase 2: DNS optimization (server-side) ───

func optimizeDNS() bool {
	currentDNS := biz.GetSettingOrDefault("dns_servers", "8.8.8.8,1.1.1.1")
	var bestDNS string
	var bestLatency int = 99999

	for _, servers := range candidateDNS {
		// Test DNS by checking if server responds to UDP
		start := time.Now()
		totalOK := 0
		for i := 0; i < tunerPingsPerTest; i++ {
			conn, err := net.DialTimeout("udp", servers[0]+":53", 3*time.Second)
			if err == nil {
				conn.Close()
				totalOK++
			}
		}
		latency := int(time.Since(start).Milliseconds()) / tunerPingsPerTest
		key := strings.Join(servers, ",")

		saveTestResult("dns", 0, testResult{
			SNI: key, Category: "dns", AvgLatency: latency,
			SuccessRate: totalOK, IsStable: totalOK == tunerPingsPerTest,
		})

		if totalOK == tunerPingsPerTest && latency < bestLatency {
			bestLatency = latency
			bestDNS = key
		}
	}

	if bestDNS != "" && bestDNS != currentDNS {
		log.Printf("[auto-tuner] DNS: %s → %s (avg %dms)", currentDNS, bestDNS, bestLatency)
		_ = biz.SetSetting("dns_servers", bestDNS)
		return true
	}
	return false
}

// ─── Phase 3: Mux optimization (server-side) ───

func optimizeMux() bool {
	currentMuxStr := biz.GetSettingOrDefault("mux_concurrency", "8")
	currentMux, _ := strconv.Atoi(currentMuxStr)
	if currentMux == 0 {
		currentMux = 8
	}

	// For mux we can't directly test — save preference based on exit node count
	nodes, _ := biz.ListAllExitNodes()
	healthyCount := 0
	for _, n := range nodes {
		if n.TunnelStatus == "healthy" {
			healthyCount++
		}
	}

	// Heuristic: fewer nodes → higher concurrency per connection
	optimalMux := 8
	if healthyCount >= 3 {
		optimalMux = 4 // many nodes: less mux per conn, more distribution
	} else if healthyCount <= 1 {
		optimalMux = 16 // single node: maximize throughput
	}

	if optimalMux != currentMux {
		log.Printf("[auto-tuner] Mux: %d → %d (based on %d healthy nodes)", currentMux, optimalMux, healthyCount)
		_ = biz.SetSetting("mux_concurrency", strconv.Itoa(optimalMux))
		return true
	}
	return false
}

// ─── Phase 4: Client-side params (fingerprint only) ───
//
// CRITICAL: VLESS Reality REQUIRES tcp transport + xtls-rprx-vision flow.
// These are FIXED by Xray-core architecture. Only TLS fingerprint is tunable
// for client-side Reality connections. Transport ws/grpc and flow="" break Reality.

func optimizeClientParams() bool {
	currentFP := biz.GetSettingOrDefault("reality_fingerprint", "chrome")

	// Enforce Reality-safe defaults (never allow ws/grpc/empty-flow for Reality)
	_ = biz.SetSetting("reality_transport", "tcp")
	_ = biz.SetSetting("reality_flow", "xtls-rprx-vision")

	testSNI := biz.GetSettingOrDefault("reality_sni_domain", biz.DefaultRealitySNI)

	var results []testResult
	for _, fp := range candidateFingerprints {
		r := testTLSWithParams(testSNI, fp)
		r.Fingerprint = fp
		r.Transport = "tcp"         // FIXED for Reality
		r.Flow = "xtls-rprx-vision" // FIXED for Reality
		r.Category = "fingerprint"
		results = append(results, r)
		saveTestResult("client_fingerprint", 0, r)
	}

	best := pickBestResult(results)
	if best == nil {
		log.Println("[auto-tuner] Fingerprint: no stable option found, keeping", currentFP)
		return false
	}

	// Pick by fingerprint field (results share same SNI)
	bestFP := best.Fingerprint
	if bestFP == currentFP {
		log.Printf("[auto-tuner] Fingerprint: %s still optimal (avg %dms)", currentFP, best.AvgLatency)
		return false
	}

	// Check significance threshold — fingerprint changes bump config_version
	currentLatency := getLatencyForFingerprint(results, currentFP)
	if currentLatency > 0 && !isSignificantImprovement(currentLatency, best.AvgLatency) {
		log.Printf("[auto-tuner] Fingerprint: %s → %s skipped (improvement %dms → %dms < %d%% threshold)",
			currentFP, bestFP, currentLatency, best.AvgLatency, tunerSignificanceThreshold)
		return false
	}

	log.Printf("[auto-tuner] Fingerprint: %s → %s (avg %dms → %dms)", currentFP, bestFP, currentLatency, best.AvgLatency)
	_ = biz.SetSetting("reality_fingerprint", bestFP)
	return true
}

// ─── Phase 5: Exit node SNI (server-side) ───

func tuneExitNodeSNI(node biz.ExitNodeTunnel) bool {
	if node.TunnelProtocol != "vless" {
		return false
	}

	currentSNI := node.TunnelSNI
	if currentSNI == "" {
		currentSNI = node.IPAddress
	}

	// Get country code for this exit node from DB
	nodeCountry := getServerCountry(node.ID)
	candidates := candidateSNIsForCountry(nodeCountry)

	var results []testResult
	for _, sni := range candidates {
		r := testTLSHandshake(node.IPAddress, node.TunnelPort, sni)
		r.Category = "exit_sni"
		results = append(results, r)
		saveTestResult(fmt.Sprintf("exit_%d", node.ID), node.ID, r)
	}

	best := pickBestResult(results)
	if best == nil || best.SNI == currentSNI {
		return false
	}

	// Check significance threshold
	currentLatency := getLatencyForSNI(results, currentSNI)
	if currentLatency > 0 && !isSignificantImprovement(currentLatency, best.AvgLatency) {
		log.Printf("[auto-tuner] Exit %d SNI: %s → %s skipped (improvement < %d%%)", node.ID, currentSNI, best.SNI, tunerSignificanceThreshold)
		return false
	}

	log.Printf("[auto-tuner] Exit %d SNI: %s → %s (avg %dms → %dms)", node.ID, currentSNI, best.SNI, currentLatency, best.AvgLatency)
	_, _ = db.DB.Exec("UPDATE servers SET tunnel_sni = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2", best.SNI, node.ID)
	markApplied(fmt.Sprintf("exit_%d", node.ID), node.ID, best)
	return true
}

// ─── Config version + client notification ───

func incrementConfigVersion() {
	currentStr := biz.GetSettingOrDefault("config_version", "1")
	current, _ := strconv.Atoi(currentStr)
	newVersion := current + 1
	_ = biz.SetSetting("config_version", strconv.Itoa(newVersion))
	_ = biz.SetSetting("config_version_updated_at", time.Now().Format(time.RFC3339))
	log.Printf("[auto-tuner] Config version: %d → %d", current, newVersion)
}

// ─── Test functions ───

func testSNIReachability(sni string) testResult {
	result := testResult{SNI: sni, Fingerprint: "chrome", Transport: "tcp"}
	var pings []int

	for i := 0; i < tunerPingsPerTest; i++ {
		latency := singleTLSPing(sni+":443", sni)
		if latency <= 0 {
			// Retry once on failure (network jitter)
			time.Sleep(500 * time.Millisecond)
			latency = singleTLSPing(sni+":443", sni)
		}
		if latency > 0 {
			pings = append(pings, latency)
			result.SuccessRate++
		} else {
			pings = append(pings, 0)
			result.ErrorMsg = fmt.Sprintf("TLS %s: failed after retry", sni)
		}
		// Small delay between pings to avoid rate limiting
		time.Sleep(200 * time.Millisecond)
	}

	result.Pings = pings
	result.AvgLatency = medianNonZero(pings)
	result.IsStable = result.SuccessRate == tunerPingsPerTest && allBelow(pings, tunerMaxLatencyMs)
	return result
}

// singleTLSPing does one TLS handshake and returns latency in ms, or 0 on failure.
func singleTLSPing(addr, sni string) int {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, tunerTimeout)
	if err != nil {
		return 0
	}
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
	})
	tlsConn.SetDeadline(time.Now().Add(tunerTimeout))
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return 0
	}
	latency := int(time.Since(start).Milliseconds())
	tlsConn.Close()
	return latency
}

func testTLSWithParams(sni, fingerprint string) testResult {
	// Fingerprint affects TLS ClientHello — Go's crypto/tls doesn't support
	// custom fingerprints directly, but we test the SNI + reachability.
	// The fingerprint is applied at Xray level, not in our test probe.
	// What we verify: the SNI domain is reachable, TLS works, latency is good.
	r := testSNIReachability(sni)
	r.Fingerprint = fingerprint
	return r
}

func testTLSHandshake(host string, port int, sni string) testResult {
	addr := fmt.Sprintf("%s:%d", host, port)
	result := testResult{SNI: sni, Fingerprint: "chrome", Transport: "tcp"}
	var pings []int

	for i := 0; i < tunerPingsPerTest; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, tunerTimeout)
		if err != nil {
			result.ErrorMsg = fmt.Sprintf("dial %s: %v", addr, err)
			pings = append(pings, 0)
			continue
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
		})
		tlsConn.SetDeadline(time.Now().Add(tunerTimeout))
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			pings = append(pings, 0)
			continue
		}
		latency := int(time.Since(start).Milliseconds())
		pings = append(pings, latency)
		result.SuccessRate++
		tlsConn.Close()
	}

	result.Pings = pings
	result.AvgLatency = medianNonZero(pings)
	result.IsStable = result.SuccessRate == tunerPingsPerTest && allBelow(pings, tunerMaxLatencyMs)
	return result
}

// ─── Helpers ───

func getServerCountry(serverID int) string {
	var cc string
	_ = db.DB.QueryRow("SELECT COALESCE(country_code, '') FROM servers WHERE id = $1", serverID).Scan(&cc)
	// Normalize: "Амстердам" → "NL", "Латвия" → "LV", etc.
	ccUpper := strings.ToUpper(strings.TrimSpace(cc))
	// Map common Russian-language country names to ISO codes
	nameToCode := map[string]string{
		"АМСТЕРДАМ": "NL", "НИДЕРЛАНДЫ": "NL", "NL": "NL",
		"ЛАТВИЯ": "LV", "LV": "LV",
		"ГЕРМАНИЯ": "DE", "DE": "DE",
		"ФИНЛЯНДИЯ": "FI", "FI": "FI",
		"РОССИЯ": "RU", "RU": "RU",
		"США": "US", "US": "US",
	}
	if code, ok := nameToCode[ccUpper]; ok {
		return code
	}
	if len(ccUpper) == 2 {
		return ccUpper // already ISO code
	}
	return "DEFAULT"
}

func isAutoTuneEnabled(serverID int) bool {
	var autoTune bool
	err := db.DB.QueryRow("SELECT auto_tune FROM servers WHERE id = $1", serverID).Scan(&autoTune)
	if err != nil {
		return true
	}
	return autoTune
}

func pickBestResult(results []testResult) *testResult {
	var stable []testResult
	for _, r := range results {
		if r.IsStable {
			stable = append(stable, r)
		}
	}
	if len(stable) == 0 {
		return nil
	}
	sort.Slice(stable, func(i, j int) bool {
		return stable[i].AvgLatency < stable[j].AvgLatency
	})
	return &stable[0]
}

func getLatencyForSNI(results []testResult, sni string) int {
	for _, r := range results {
		if r.SNI == sni {
			return r.AvgLatency
		}
	}
	return 0
}

func saveTestResult(targetType string, serverID int, r testResult) {
	pingsJSON, _ := json.Marshal(r.Pings)
	var srvID *int
	if serverID > 0 {
		srvID = &serverID
	}
	_, _ = db.DB.Exec(`
		INSERT INTO config_test_results (target_type, server_id, sni_domain, fingerprint, ping_results, avg_latency_ms, success_count, is_stable, error_msg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, targetType, srvID, r.SNI, r.Category+":"+r.Fingerprint+"/"+r.Transport+"/"+r.Flow,
		string(pingsJSON), r.AvgLatency, r.SuccessRate, r.IsStable, r.ErrorMsg)
}

func markApplied(targetType string, serverID int, r *testResult) {
	var srvID *int
	if serverID > 0 {
		srvID = &serverID
	}
	_, _ = db.DB.Exec(`
		UPDATE config_test_results SET was_applied = TRUE
		WHERE id = (SELECT id FROM config_test_results
			WHERE target_type = $1 AND COALESCE(server_id, 0) = COALESCE($2, 0)
			ORDER BY tested_at DESC LIMIT 1)
	`, targetType, srvID)
}

// medianNonZero returns the median of non-zero values (more stable than avg with network jitter).
func medianNonZero(vals []int) int {
	var nonZero []int
	for _, v := range vals {
		if v > 0 {
			nonZero = append(nonZero, v)
		}
	}
	if len(nonZero) == 0 {
		return 0
	}
	sort.Ints(nonZero)
	mid := len(nonZero) / 2
	if len(nonZero)%2 == 0 {
		return (nonZero[mid-1] + nonZero[mid]) / 2
	}
	return nonZero[mid]
}

// isSignificantImprovement returns true if newLatency is at least tunerSignificanceThreshold% better than currentLatency.
// This prevents flapping: switching on marginal 1-2ms differences wastes config_version bumps.
func isSignificantImprovement(currentLatency, newLatency int) bool {
	if currentLatency <= 0 {
		return true // no baseline, any result is an improvement
	}
	improvementPct := float64(currentLatency-newLatency) / float64(currentLatency) * 100
	return improvementPct >= float64(tunerSignificanceThreshold)
}

// getLatencyForFingerprint finds latency result for a given fingerprint.
func getLatencyForFingerprint(results []testResult, fp string) int {
	for _, r := range results {
		if r.Fingerprint == fp {
			return r.AvgLatency
		}
	}
	return 0
}

func allBelow(vals []int, threshold int) bool {
	for _, v := range vals {
		if v <= 0 || v > threshold {
			return false
		}
	}
	return true
}
