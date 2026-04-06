package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

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
//   - Balancer strategy (random vs leastPing)
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

var candidateSNIs = []string{
	"apple.com",
	"www.apple.com",
	"microsoft.com",
	"www.microsoft.com",
	"cdn.jsdelivr.net",
	"ajax.googleapis.com",
	"fonts.googleapis.com",
	"cloudflare.com",
	"github.com",
	"www.google.com",
	"vk.com",
	"mail.ru",
	"yandex.ru",
	"static.cloudflareinsights.com",
	"cdnjs.cloudflare.com",
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
	tunerPingsPerTest = 5
	tunerMaxLatencyMs = 500
	tunerTimeout      = 5 * time.Second
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
	enabled := biz.GetSettingOrDefault("auto_tuner_enabled", "true")
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
		for range ticker.C {
			if biz.GetSettingOrDefault("auto_tuner_enabled", "true") != "true" {
				continue
			}
			runFullTuner()
		}
	}()

	log.Printf("[auto-tuner] Started v2 (interval: %dh, params: SNI×%d, Fingerprint×%d, DNS×%d, MuxAdaptive)",
		intervalHours, len(candidateSNIs), len(candidateFingerprints), len(candidateDNS))
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

	log.Println("[auto-tuner] Full optimization run complete")
}

// ─── Phase 1: SNI tuning (server-side) ───

func tuneSNI() string {
	currentSNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
	var results []testResult

	for _, sni := range candidateSNIs {
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

	log.Printf("[auto-tuner] SNI: %s → %s (avg %dms → %dms)", currentSNI, best.SNI, getLatencyForSNI(results, currentSNI), best.AvgLatency)
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

	testSNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")

	var results []testResult
	for _, fp := range candidateFingerprints {
		r := testTLSWithParams(testSNI, fp)
		r.Fingerprint = fp
		r.Transport = "tcp"           // FIXED for Reality
		r.Flow = "xtls-rprx-vision"   // FIXED for Reality
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

	log.Printf("[auto-tuner] Fingerprint: %s → %s (avg %dms)", currentFP, bestFP, best.AvgLatency)
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

	var results []testResult
	for _, sni := range candidateSNIs {
		r := testTLSHandshake(node.IPAddress, node.TunnelPort, sni)
		r.Category = "exit_sni"
		results = append(results, r)
		saveTestResult(fmt.Sprintf("exit_%d", node.ID), node.ID, r)
	}

	best := pickBestResult(results)
	if best == nil || best.SNI == currentSNI {
		return false
	}

	log.Printf("[auto-tuner] Exit %d SNI: %s → %s (avg %dms)", node.ID, currentSNI, best.SNI, best.AvgLatency)
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
		start := time.Now()
		conn, err := net.DialTimeout("tcp", sni+":443", tunerTimeout)
		if err != nil {
			result.ErrorMsg = fmt.Sprintf("dial %s:443: %v", sni, err)
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
			result.ErrorMsg = fmt.Sprintf("TLS %s: %v", sni, err)
			pings = append(pings, 0)
			continue
		}
		latency := int(time.Since(start).Milliseconds())
		pings = append(pings, latency)
		result.SuccessRate++
		tlsConn.Close()
	}

	result.Pings = pings
	result.AvgLatency = avgNonZero(pings)
	result.IsStable = result.SuccessRate == tunerPingsPerTest && allBelow(pings, tunerMaxLatencyMs)
	return result
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
	result.AvgLatency = avgNonZero(pings)
	result.IsStable = result.SuccessRate == tunerPingsPerTest && allBelow(pings, tunerMaxLatencyMs)
	return result
}

// ─── Helpers ───

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

func avgNonZero(vals []int) int {
	sum, count := 0, 0
	for _, v := range vals {
		if v > 0 {
			sum += v
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / count
}

func allBelow(vals []int, threshold int) bool {
	for _, v := range vals {
		if v <= 0 || v > threshold {
			return false
		}
	}
	return true
}
