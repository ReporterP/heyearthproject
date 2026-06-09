package admin

// Capacity widget backend (Beads heyearthproject-7ufk).
//
// Read-only snapshot of infrastructure headroom for the admin dashboard:
//   - Hub: read LOCALLY from /proc (heyearth-api runs on the Hub itself) —
//     CPU load, RAM%, traffic/day; active subs from the DB.
//   - Exit: from servers.load_metrics (node-agent fills it every 10s).
//
// Computes a CPU- and traffic-bound ceiling on active subscriptions, fill %,
// per-user hardware cost, and a scale-out verdict. Nothing is written. Additive:
// does NOT touch the metrics collector, servers, billing, or VPN services.

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

// vdsina tariff constants (Hub and Exit share the same plan: 2 core / 32 TB / 40₽day).
const (
	capCores      = 2.0
	capQuotaTB    = 32.0
	capCostRubMon = 1200.0 // 40₽/day × 30
	capTargetLoad = 1.4    // 70% of 2 cores — safe ceiling before peak lag
)

func capQuotaGBDay() float64 { return capQuotaTB * 1024.0 / 30.0 } // ≈1092 GB/day

// CapacityHandler — GET /api/admin/capacity. Always 200 with best-effort data.
func CapacityHandler(w http.ResponseWriter, r *http.Request) {
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"hub":  hubCapacity(),
		"exit": exitCapacity(),
	})
}

func hubCapacity() map[string]any {
	load := readLoadAvg1()      // 1-min load, -1 on failure
	ramPct := readMemUsedPct()  // -1 on failure
	gbDay := readTrafficGBDay() // -1 on failure
	active := countActiveSubs() // active paying subscriptions
	quotaGBDay := capQuotaGBDay()

	out := map[string]any{
		"cpu_cores":      int(capCores),
		"active_subs":    active,
		"quota_gb_day":   math.Round(quotaGBDay),
		"cost_rub_month": int(capCostRubMon),
	}
	if load >= 0 {
		out["cpu_load"] = round1(load)
		out["cpu_pct"] = int(math.Round(load / capCores * 100))
	}
	if ramPct >= 0 {
		out["ram_pct"] = int(math.Round(ramPct))
	}
	if gbDay >= 0 {
		out["traffic_gb_day"] = math.Round(gbDay)
		out["traffic_pct"] = int(math.Round(gbDay / quotaGBDay * 100))
	}

	// Ceiling: min(CPU-bound, traffic-bound) extrapolated from current load.
	var maxSubs float64
	if active > 0 {
		maxCPU := math.Inf(1)
		if load > 0.01 {
			maxCPU = float64(active) * capTargetLoad / load
		}
		maxTraffic := math.Inf(1)
		if gbDay > 1 {
			maxTraffic = float64(active) * quotaGBDay / gbDay
		}
		maxSubs = math.Min(maxCPU, maxTraffic)
	}
	costPerSub := capCostRubMon / math.Max(float64(active), 1)
	out["cost_per_sub"] = int(math.Round(costPerSub))

	fillPct := 0.0
	if maxSubs > 0 && !math.IsInf(maxSubs, 1) {
		out["max_subs"] = int(math.Round(maxSubs))
		fillPct = float64(active) / maxSubs * 100
		out["fill_pct"] = int(math.Round(fillPct))
	}
	out["verdict"] = verdictFor(fillPct)
	return out
}

func exitCapacity() map[string]any {
	var name, status string
	var metricsJSON []byte
	err := db.DB.QueryRow(`
		SELECT name, status, COALESCE(load_metrics::text, '{}')
		FROM servers ORDER BY id LIMIT 1
	`).Scan(&name, &status, &metricsJSON)
	if err != nil {
		return map[string]any{"online": false}
	}

	out := map[string]any{"name": name, "online": status == "online"}
	var m struct {
		CPU   float64 `json:"cpu_usage"`
		Mem   float64 `json:"memory_usage"`
		Conns int     `json:"active_connections"`
	}
	if json.Unmarshal(metricsJSON, &m) == nil {
		out["cpu_pct"] = int(math.Round(m.CPU))
		out["ram_pct"] = int(math.Round(m.Mem))
		out["active_connections"] = m.Conns
		out["verdict"] = verdictFor(m.CPU)
	} else {
		out["verdict"] = "unknown"
	}
	return out
}

// verdictFor maps a 0-100 utilization/fill percentage to a traffic-light verdict.
func verdictFor(pct float64) string {
	switch {
	case pct <= 0:
		return "ok"
	case pct < 60:
		return "ok"
	case pct < 85:
		return "warn"
	default:
		return "critical"
	}
}

func countActiveSubs() int {
	var n int
	_ = db.DB.QueryRow(`SELECT COUNT(*) FROM user_subscriptions WHERE status='active' AND valid_until > NOW()`).Scan(&n)
	return n
}

// ── /proc readers (best-effort; return -1 on any failure) ──

func readLoadAvg1() float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return -1
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return -1
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return -1
	}
	return v
}

func readMemUsedPct() float64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return -1
	}
	var total, avail float64
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, _ = strconv.ParseFloat(f[1], 64)
		case "MemAvailable:":
			avail, _ = strconv.ParseFloat(f[1], 64)
		}
	}
	if total <= 0 {
		return -1
	}
	return (total - avail) / total * 100
}

// readTrafficGBDay sums RX+TX bytes across real interfaces (excluding lo/docker/
// tun/awg) and divides by uptime in days. Average since boot — a headroom hint,
// not billing-grade.
func readTrafficGBDay() float64 {
	netData, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return -1
	}
	var totalBytes float64
	for _, line := range strings.Split(string(netData), "\n") {
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" || strings.HasPrefix(iface, "docker") || strings.HasPrefix(iface, "br-") ||
			strings.HasPrefix(iface, "veth") || strings.HasPrefix(iface, "awg") ||
			strings.HasPrefix(iface, "tun") || strings.HasPrefix(iface, "wg") {
			continue
		}
		f := strings.Fields(parts[1])
		if len(f) < 9 {
			continue
		}
		rx, _ := strconv.ParseFloat(f[0], 64)
		tx, _ := strconv.ParseFloat(f[8], 64)
		totalBytes += rx + tx
	}

	upData, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return -1
	}
	upFields := strings.Fields(string(upData))
	if len(upFields) < 1 {
		return -1
	}
	upSec, err := strconv.ParseFloat(upFields[0], 64)
	if err != nil || upSec < 1 {
		return -1
	}
	days := upSec / 86400.0
	if days < 0.01 {
		days = 0.01
	}
	return totalBytes / 1e9 / days // GB per day
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
