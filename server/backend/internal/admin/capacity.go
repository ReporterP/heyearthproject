package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

// CapacityHandler — GET /api/admin/capacity: «Мощность» for the dashboard widget and the
// «Серверы → Мощность» tab (cleanup plan B5).
func CapacityHandler(w http.ResponseWriter, r *http.Request) {
	rep, err := LoadCapacityReport(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось посчитать мощность: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, rep)
}

// CapacitySummary is what the hourly «пора докупить» alert needs.
type CapacitySummary struct {
	Level string
	Text  string
}

// EvaluateCapacity is the overall verdict (for the alert).
func EvaluateCapacity(ctx context.Context) (CapacitySummary, error) {
	rep, err := LoadCapacityReport(ctx)
	if err != nil {
		return CapacitySummary{}, err
	}
	return CapacitySummary{rep.Verdict.Level, rep.Verdict.Text}, nil
}

func settingFloat(ctx context.Context, key string, def float64) float64 {
	var v string
	if err := db.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v); err != nil {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || f < 0 {
		return def
	}
	return f
}

// LoadCapacityReport reads the nodes, their peaks and the day's active users.
func LoadCapacityReport(ctx context.Context) (capReport, error) {
	nodes := []capNode{{ID: 0, Name: "Hub", Role: "hub", Cores: 1,
		PortMbps: settingFloat(ctx, "hub_port_mbps", 1000), QuotaTB: settingFloat(ctx, "hub_traffic_quota_tb", 0),
		PriceRub: int(settingFloat(ctx, "hub_monthly_price_rub", 0))}}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, COALESCE(NULLIF(name, ''), 'Нода ' || id), COALESCE(role, 'exit'),
		       COALESCE(cpu_cores, NULLIF(load_metrics->>'cores', '')::int, 1),
		       COALESCE(port_mbps, 1000), COALESCE(traffic_quota_tb, 0), COALESCE(monthly_price_rub, 0)
		FROM servers
		WHERE lifecycle_state = 'online' AND COALESCE(status, '') <> 'archived'
		ORDER BY id`)
	if err != nil {
		return capReport{}, err
	}
	for rows.Next() {
		var n capNode
		if err := rows.Scan(&n.ID, &n.Name, &n.Role, &n.Cores, &n.PortMbps, &n.QuotaTB, &n.PriceRub); err != nil {
			rows.Close()
			return capReport{}, err
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	idx := map[int]int{}
	for i, n := range nodes {
		idx[n.ID] = i
	}

	// Daily peaks of hourly p95 (14 days), per server; server_id NULL is the Hub (id 0).
	rows, err = db.DB.QueryContext(ctx, `
		WITH h AS (
			SELECT COALESCE(server_id, 0) sid, date_trunc('hour', at) hr,
			       percentile_cont(0.95) WITHIN GROUP (ORDER BY cpu_pct) cpu95,
			       percentile_cont(0.95) WITHIN GROUP (ORDER BY GREATEST(COALESCE(net_rx_mbps, 0), COALESCE(net_tx_mbps, 0))) net95,
			       MAX(CASE WHEN conntrack_max > 0 THEN 100.0 * conntrack / conntrack_max ELSE 0 END) ct,
			       MAX(cores) cores, COUNT(*) n
			FROM server_metric_samples WHERE at > NOW() - INTERVAL '14 days'
			GROUP BY 1, 2)
		SELECT sid, date_trunc('day', hr) d, MAX(cpu95), MAX(net95), MAX(ct), MAX(cores),
		       SUM(n) FILTER (WHERE hr > NOW() - INTERVAL '24 hours'),
		       MAX(cpu95) FILTER (WHERE hr > NOW() - INTERVAL '24 hours'),
		       MAX(net95) FILTER (WHERE hr > NOW() - INTERVAL '24 hours')
		FROM h GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return capReport{}, err
	}
	for rows.Next() {
		var sid, cores int
		var day any
		var cpu, net, ct float64
		var n24 *int64
		var cpu24, net24 *float64
		if err := rows.Scan(&sid, &day, &cpu, &net, &ct, &cores, &n24, &cpu24, &net24); err != nil {
			rows.Close()
			return capReport{}, err
		}
		i, ok := idx[sid]
		if !ok {
			continue
		}
		n := &nodes[i]
		n.DailyCPU = append(n.DailyCPU, cpu)
		n.DailyNet = append(n.DailyNet, net)
		if sid == 0 && cores > 0 {
			n.Cores = cores
		}
		if n24 != nil {
			n.Samples += int(*n24)
		}
		if cpu24 != nil && *cpu24 > n.CPUPeak {
			n.CPUPeak = *cpu24
		}
		if net24 != nil && *net24 > n.NetPeakMbps {
			n.NetPeakMbps = *net24
		}
		if ct > n.ConntrackPct {
			n.ConntrackPct = ct
		}
	}
	rows.Close()

	// This month's traffic through each interface, TB.
	rows, err = db.DB.QueryContext(ctx, `
		SELECT COALESCE(server_id, 0), SUM(rx_bytes + tx_bytes)::float8 / 1e12
		FROM server_metric_samples WHERE at >= date_trunc('month', NOW()) GROUP BY 1`)
	if err != nil {
		return capReport{}, err
	}
	for rows.Next() {
		var sid int
		var tb float64
		if rows.Scan(&sid, &tb) == nil {
			if i, ok := idx[sid]; ok {
				nodes[i].MonthTB = tb
			}
		}
	}
	rows.Close()

	if c := int(settingFloat(ctx, "hub_cpu_cores", 0)); c > 0 {
		nodes[0].Cores = c
	}

	var active int
	_ = db.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT user_id) FROM traffic_stats WHERE recorded_at > NOW() - INTERVAL '24 hours'`).Scan(&active)
	return buildCapacityReport(nodes, active), nil
}
