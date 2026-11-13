package admin

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

// B1: the card's charts bucket a server's samples (and the Hub's) by the range.
func TestServerLoadHandler(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status) VALUES (401, 'A', '203.0.113.41', 'exit', 'online', 'online')`)
	for i := 0; i < 30; i++ {
		testpg.MustExec(t, conn, `INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores, net_rx_mbps, net_tx_mbps, tcp, conntrack, conntrack_max)
			VALUES (401, NOW() - make_interval(mins => $1), $2, 40, 1, $3, 5, 10, 50, 100)`, i, 10+i, float64(i))
		testpg.MustExec(t, conn, `INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores) VALUES (NULL, NOW() - make_interval(mins => $1), 20, 30, 4)`, i)
	}
	// Older than 24 h: only in the 7-day range.
	testpg.MustExec(t, conn, `INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores) VALUES (401, NOW() - INTERVAL '3 days', 90, 40, 1)`)

	type resp struct {
		Range  string      `json:"range"`
		Points []loadPoint `json:"points"`
	}
	get := func(path string) resp {
		rec := call(ServerLoadHandler, "GET", path, "")
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		var r resp
		_ = json.Unmarshal(rec.Body.Bytes(), &r)
		return r
	}
	day := get("/api/admin/servers/401/load?range=24h")
	if len(day.Points) < 3 || len(day.Points) > 5 {
		t.Fatalf("30 minutes in 10-minute points: %d", len(day.Points))
	}
	var maxCPU, maxRx float64
	for _, p := range day.Points {
		maxCPU = max(maxCPU, p.CPUMax)
		if p.RxMbps != nil {
			maxRx = max(maxRx, *p.RxMbps)
		}
		if p.ConntrackPct == nil || *p.ConntrackPct != 50 || p.Connections != 10 {
			t.Errorf("point %+v", p)
		}
	}
	if maxCPU != 39 || maxRx != 29 || maxCPU > 50 {
		t.Errorf("peaks cpu %v rx %v (the 3-day-old 90 %% must not be in 24 h)", maxCPU, maxRx)
	}
	week := get("/api/admin/servers/401/load?range=7d")
	if len(week.Points) < 2 {
		t.Errorf("7d points: %d", len(week.Points))
	}
	if hub := get("/api/admin/servers/hub/load"); hub.Range != "24h" || len(hub.Points) == 0 || hub.Points[0].CPU != 20 {
		t.Errorf("hub load: %+v", hub)
	}
	if rec := call(ServerLoadHandler, "GET", "/api/admin/servers/401/load?range=1y", ""); rec.Code != 400 {
		t.Errorf("bad range: %d", rec.Code)
	}
	if rec := call(ServerLoadHandler, "GET", "/api/admin/servers/x/load", ""); rec.Code != 400 {
		t.Errorf("bad id: %d", rec.Code)
	}
}

// B5: the owner's numbers — a server's in its row, the Hub's in app_settings; «Мощность» uses them.
func TestServerOwnerNumbers(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status) VALUES (402, 'B', '203.0.113.42', 'exit', 'online', 'online')`)
	put := func(path, body string) int {
		return call(ServerOwnerHandler, "PUT", path, body).Code
	}
	if c := put("/api/admin/servers/402/capacity", `{"traffic_quota_tb": 2.5, "monthly_price_rub": 700, "port_mbps": 1000, "cpu_cores": null}`); c != 200 {
		t.Fatalf("save: %d", c)
	}
	var quota float64
	var price, port int
	var cores *int
	_ = conn.QueryRow(`SELECT traffic_quota_tb, monthly_price_rub, port_mbps, cpu_cores FROM servers WHERE id = 402`).Scan(&quota, &price, &port, &cores)
	if quota != 2.5 || price != 700 || port != 1000 || cores != nil {
		t.Errorf("stored %v %d %d %v", quota, price, port, cores)
	}
	for _, bad := range []string{`{"cpu_cores": 0}`, `{"port_mbps": -1}`, `{"monthly_price_rub": 99999999}`, `{"traffic_quota_tb": -2}`, `nope`} {
		if c := put("/api/admin/servers/402/capacity", bad); c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
	if c := put("/api/admin/servers/999/capacity", `{}`); c != 404 {
		t.Errorf("unknown server: %d", c)
	}

	// The Hub: entered cores beat the detected ones in «Мощность».
	testpg.MustExec(t, conn, `INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores) VALUES (NULL, NOW(), 20, 30, 4)`)
	if c := put("/api/admin/servers/hub/capacity", `{"traffic_quota_tb": 32, "monthly_price_rub": 2600, "port_mbps": 1000, "cpu_cores": 8}`); c != 200 {
		t.Fatalf("hub save: %d", c)
	}
	rec := call(HubHandler, "GET", "/api/admin/hub", "")
	var hub struct {
		Load  nodeLoadView  `json:"load"`
		Owner nodeOwnerView `json:"owner"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hub); err != nil || rec.Code != 200 {
		t.Fatalf("hub: %d %s", rec.Code, rec.Body)
	}
	if hub.Owner.MonthlyPriceRub == nil || *hub.Owner.MonthlyPriceRub != 2600 || hub.Owner.CPUCores == nil || *hub.Owner.CPUCores != 8 ||
		hub.Load.CPUPct == nil || *hub.Load.CPUPct != 20 || hub.Load.Stale || hub.Load.Cores != 4 {
		t.Errorf("hub view %s", rec.Body)
	}
	rep, err := LoadCapacityReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rep.Groups {
		for _, n := range g.Nodes {
			if n.Role == "hub" && (n.Cores != 8 || n.PriceRub != 2600) {
				t.Errorf("capacity hub %+v", n)
			}
		}
	}
	// Clearing the Hub's cores goes back to the detected count.
	if c := put("/api/admin/servers/hub/capacity", `{"traffic_quota_tb": 32, "monthly_price_rub": 2600, "port_mbps": 1000, "cpu_cores": null}`); c != 200 {
		t.Fatal(c)
	}
	rep, _ = LoadCapacityReport(context.Background())
	for _, g := range rep.Groups {
		for _, n := range g.Nodes {
			if n.Role == "hub" && n.Cores != 4 {
				t.Errorf("hub cores after clearing: %d", n.Cores)
			}
		}
	}
}

func TestParseLoadMetrics(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	// The old gRPC collector's values: no SSH sample, never shown as current.
	if v := parseLoadMetrics(`{"cpu_usage": 3, "memory_usage": 10}`, now); !v.Stale || v.CPUPct != nil {
		t.Errorf("legacy: %+v", v)
	}
	// An error newer than the sample is shown; an older one is not.
	raw := `{"source":"ssh","synced_at":"2026-11-13T11:58:00Z","cpu_usage":5,"load_error":"ssh: handshake failed","load_error_at":"2026-11-13T11:59:30.123456+00:00"}`
	if v := parseLoadMetrics(raw, now); v.Stale || !strings.Contains(v.Error, "handshake") {
		t.Errorf("newer error: %+v", v)
	}
	raw = strings.Replace(raw, "11:59:30", "11:50:00", 1)
	if v := parseLoadMetrics(raw, now); v.Error != "" {
		t.Errorf("older error shown: %+v", v)
	}
	if v := parseLoadMetrics(`{"source":"ssh","synced_at":"2026-11-13T11:50:00Z","cpu_usage":5}`, now); !v.Stale {
		t.Error("a 10-minute-old sample is stale")
	}
	if v := parseLoadMetrics(`{"load_error":"ключ не подтверждён","load_error_at":"2026-11-13T11:59:00Z"}`, now); v.Error == "" || !v.Stale {
		t.Errorf("error without a sample: %+v", v)
	}
}

// B4 (review): «another exit» means another WORKING exit — agent alive, tunnel healthy, not
// being switched off.
func TestOtherOnlineExitsQuery(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, tunnel_status) VALUES
		(501, 'A', '203.0.113.51', 'exit', 'online', 'online', 'healthy'),
		(502, 'B', '203.0.113.52', 'exit', 'online', 'offline', 'healthy'),
		(503, 'C', '203.0.113.53', 'exit', 'online', 'online', 'unhealthy'),
		(504, 'R', '203.0.113.54', 'relay', 'online', 'online', 'healthy')`)
	count := func() int {
		n, err := countOtherOnlineExits(context.Background(), 501)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 0 {
		t.Fatalf("a dead agent, an unhealthy tunnel or a relay counted as a reserve: %d", n)
	}
	testpg.MustExec(t, conn, `UPDATE servers SET status = 'online' WHERE id = 502`)
	if n := count(); n != 1 {
		t.Fatalf("a working exit: %d", n)
	}
	testpg.MustExec(t, conn, `INSERT INTO provision_jobs (server_id, kind, status) VALUES (502, 'deprovision', 'running')`)
	if n := count(); n != 0 {
		t.Errorf("an exit being switched off counted: %d", n)
	}
}
