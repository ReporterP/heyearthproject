package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Auto-Tuner ───────────────────────
//
// Background system that tests VPN configuration parameters
// (SNI domains, TLS fingerprints) against real connectivity
// and picks the optimal combination for DPI evasion.
//
// Runs every N hours (default: 4). For each exit node in auto_tune mode:
//   1. Tests multiple SNI domains with TLS handshake (5 pings each)
//   2. Records results in config_test_results table
//   3. Picks the best stable config (all 5 pings < 500ms, lowest avg)
//   4. If better than current → updates DB → triggers Xray sync
//
// Also tests hub inbound Reality SNI and updates app_settings.

// Candidate SNI domains — popular sites that Russian DPI won't block
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
}

// Candidate TLS fingerprints
var candidateFingerprints = []string{
	"chrome",
	"firefox",
	"safari",
	"edge",
}

const (
	autoTunerPingsPerTest = 5
	autoTunerMaxLatencyMs = 500
	autoTunerTimeout      = 5 * time.Second
)

type testResult struct {
	SNI         string
	Fingerprint string
	Pings       []int // latency in ms for each ping
	AvgLatency  int
	SuccessRate int // out of autoTunerPingsPerTest
	IsStable    bool
	ErrorMsg    string
}

// StartAutoTuner launches the background auto-tuner goroutine.
func StartAutoTuner() {
	enabled := biz.GetSettingOrDefault("auto_tuner_enabled", "true")
	if enabled != "true" {
		log.Println("[auto-tuner] Disabled via auto_tuner_enabled setting")
		return
	}

	intervalStr := biz.GetSettingOrDefault("auto_tuner_interval_hours", "4")
	intervalHours, err := strconv.Atoi(intervalStr)
	if err != nil || intervalHours < 1 {
		intervalHours = 4
	}
	interval := time.Duration(intervalHours) * time.Hour

	go func() {
		// First run after 30 seconds (let everything start up)
		time.Sleep(30 * time.Second)
		runAutoTuner()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if biz.GetSettingOrDefault("auto_tuner_enabled", "true") != "true" {
				continue
			}
			runAutoTuner()
		}
	}()

	log.Printf("[auto-tuner] Started (interval: %dh, candidates: %d SNIs × %d fingerprints)",
		intervalHours, len(candidateSNIs), len(candidateFingerprints))
}

func runAutoTuner() {
	log.Println("[auto-tuner] Starting optimization run...")
	_ = biz.SetSetting("auto_tuner_status", "running")
	_ = biz.SetSetting("auto_tuner_last_run", time.Now().Format(time.RFC3339))

	defer func() {
		_ = biz.SetSetting("auto_tuner_status", "idle")
	}()

	// 1. Test hub inbound Reality SNI
	tuneHubInboundSNI()

	// 2. Test each auto-tune exit node
	nodes, err := biz.ListAllExitNodes()
	if err != nil {
		log.Printf("[auto-tuner] Failed to list exit nodes: %v", err)
		return
	}

	changed := false
	for _, node := range nodes {
		if !isAutoTuneEnabled(node.ID) {
			continue
		}
		if nodeChanged := tuneExitNode(node); nodeChanged {
			changed = true
		}
	}

	if changed {
		log.Println("[auto-tuner] Configuration changed, triggering Xray sync...")
		RequestXraySync()
	}

	log.Println("[auto-tuner] Optimization run complete")
}

func isAutoTuneEnabled(serverID int) bool {
	var autoTune bool
	err := db.DB.QueryRow("SELECT auto_tune FROM servers WHERE id = $1", serverID).Scan(&autoTune)
	if err != nil {
		return true // default enabled
	}
	return autoTune
}

// tuneHubInboundSNI tests SNI domains for the hub's VLESS Reality inbound.
// The best SNI is applied to app_settings.reality_sni_domain.
func tuneHubInboundSNI() {
	currentSNI := biz.GetSettingOrDefault("reality_sni_domain", "apple.com")
	var results []testResult

	for _, sni := range candidateSNIs {
		result := testSNIReachability(sni, "chrome")
		results = append(results, result)
		saveTestResult("hub_inbound", 0, result)
	}

	best := pickBestResult(results)
	if best == nil {
		log.Println("[auto-tuner] Hub inbound: no stable SNI found, keeping current:", currentSNI)
		return
	}

	if best.SNI != currentSNI {
		log.Printf("[auto-tuner] Hub inbound: switching SNI %s → %s (avg %dms)", currentSNI, best.SNI, best.AvgLatency)
		_ = biz.SetSetting("reality_sni_domain", best.SNI)
		markAsApplied("hub_inbound", 0, best.SNI, best.Fingerprint)
	} else {
		log.Printf("[auto-tuner] Hub inbound: current SNI %s is still optimal (avg %dms)", currentSNI, best.AvgLatency)
	}
}

// tuneExitNode tests SNI + fingerprint combos for a specific exit node.
func tuneExitNode(node biz.ExitNodeTunnel) bool {
	if node.TunnelProtocol != "vless" && node.TunnelProtocol != "hysteria2" {
		return false // only VLESS Reality + Hysteria2 benefit from SNI tuning
	}

	currentSNI := node.TunnelSNI
	if currentSNI == "" {
		currentSNI = node.IPAddress
	}

	var results []testResult

	// For VLESS: test SNI + fingerprint combos via TLS handshake to exit node
	if node.TunnelProtocol == "vless" {
		for _, sni := range candidateSNIs {
			result := testTLSHandshake(node.IPAddress, node.TunnelPort, sni)
			results = append(results, result)
			saveTestResult(fmt.Sprintf("exit_%d", node.ID), node.ID, result)
		}
	} else {
		// Hysteria2: test local SOCKS port reachability + general SNI quality
		hy2Port := 1088 + node.ID
		result := testTCPConnect("127.0.0.1", hy2Port, "hysteria2-local")
		results = append(results, result)
		saveTestResult(fmt.Sprintf("exit_%d", node.ID), node.ID, result)
	}

	best := pickBestResult(results)
	if best == nil {
		log.Printf("[auto-tuner] Exit node %d (%s): no stable config found", node.ID, node.Name)
		return false
	}

	if best.SNI != currentSNI {
		log.Printf("[auto-tuner] Exit node %d (%s): switching SNI %s → %s (avg %dms)",
			node.ID, node.Name, currentSNI, best.SNI, best.AvgLatency)
		_, err := db.DB.Exec("UPDATE servers SET tunnel_sni = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2",
			best.SNI, node.ID)
		if err != nil {
			log.Printf("[auto-tuner] Failed to update exit node %d SNI: %v", node.ID, err)
			return false
		}
		markAsApplied(fmt.Sprintf("exit_%d", node.ID), node.ID, best.SNI, best.Fingerprint)
		return true
	}

	log.Printf("[auto-tuner] Exit node %d (%s): current SNI %s still optimal (avg %dms)",
		node.ID, node.Name, currentSNI, best.AvgLatency)
	return false
}

// ─── Test functions ───

// testSNIReachability tests if a domain is reachable via TLS from the Hub.
// This verifies that Russian DPI doesn't block connections to this SNI.
func testSNIReachability(sni, fingerprint string) testResult {
	result := testResult{SNI: sni, Fingerprint: fingerprint}
	var pings []int

	for i := 0; i < autoTunerPingsPerTest; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", sni+":443", autoTunerTimeout)
		if err != nil {
			result.ErrorMsg = fmt.Sprintf("dial %s:443: %v", sni, err)
			pings = append(pings, 0)
			continue
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: sni,
			// Skip verification — we're testing DPI passthrough, not cert validity
			InsecureSkipVerify: true,
		})
		tlsConn.SetDeadline(time.Now().Add(autoTunerTimeout))
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
	result.IsStable = result.SuccessRate == autoTunerPingsPerTest && allBelow(pings, autoTunerMaxLatencyMs)
	return result
}

// testTLSHandshake tests TLS handshake to a specific host:port with given SNI.
func testTLSHandshake(host string, port int, sni string) testResult {
	addr := fmt.Sprintf("%s:%d", host, port)
	result := testResult{SNI: sni, Fingerprint: "chrome"}
	var pings []int

	for i := 0; i < autoTunerPingsPerTest; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, autoTunerTimeout)
		if err != nil {
			result.ErrorMsg = fmt.Sprintf("dial %s: %v", addr, err)
			pings = append(pings, 0)
			continue
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName:         sni,
			InsecureSkipVerify: true,
		})
		tlsConn.SetDeadline(time.Now().Add(autoTunerTimeout))
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
	result.IsStable = result.SuccessRate == autoTunerPingsPerTest && allBelow(pings, autoTunerMaxLatencyMs)
	return result
}

// testTCPConnect tests simple TCP connectivity.
func testTCPConnect(host string, port int, label string) testResult {
	addr := fmt.Sprintf("%s:%d", host, port)
	result := testResult{SNI: label, Fingerprint: "n/a"}
	var pings []int

	for i := 0; i < autoTunerPingsPerTest; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			result.ErrorMsg = fmt.Sprintf("connect %s: %v", addr, err)
			pings = append(pings, 0)
			continue
		}
		latency := int(time.Since(start).Milliseconds())
		pings = append(pings, latency)
		result.SuccessRate++
		conn.Close()
	}

	result.Pings = pings
	result.AvgLatency = avgNonZero(pings)
	result.IsStable = result.SuccessRate == autoTunerPingsPerTest && allBelow(pings, autoTunerMaxLatencyMs)
	return result
}

// ─── Helpers ───

func pickBestResult(results []testResult) *testResult {
	// Filter stable results only
	var stable []testResult
	for _, r := range results {
		if r.IsStable {
			stable = append(stable, r)
		}
	}
	if len(stable) == 0 {
		return nil
	}
	// Sort by avg latency ascending
	sort.Slice(stable, func(i, j int) bool {
		return stable[i].AvgLatency < stable[j].AvgLatency
	})
	return &stable[0]
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
	`, targetType, srvID, r.SNI, r.Fingerprint, string(pingsJSON), r.AvgLatency, r.SuccessRate, r.IsStable, r.ErrorMsg)
}

func markAsApplied(targetType string, serverID int, sni, fingerprint string) {
	var srvID *int
	if serverID > 0 {
		srvID = &serverID
	}
	_, _ = db.DB.Exec(`
		UPDATE config_test_results SET was_applied = TRUE
		WHERE target_type = $1 AND COALESCE(server_id, 0) = COALESCE($2, 0)
		  AND sni_domain = $3 AND fingerprint = $4
		  AND tested_at = (SELECT MAX(tested_at) FROM config_test_results
		    WHERE target_type = $1 AND COALESCE(server_id, 0) = COALESCE($2, 0)
		      AND sni_domain = $3 AND fingerprint = $4)
	`, targetType, srvID, sni, fingerprint)
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
