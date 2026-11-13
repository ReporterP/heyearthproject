package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

const sampleOut = "cpu 1000 800\ncpu 1100 850\nmem 2000000 500000\nnproc 2\nnet ens3: 1000 0 0 0 0 0 0 0 2000 0\nct 10 100\ntcp 5\n"

// B3: an exit whose agent does not answer becomes offline after 3 probes in a row; a
// node that is not switched on is never touched.
func TestAgentLiveness(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status) VALUES
		(101, 'up', '203.0.113.1', 'exit', 'online', 'online'),
		(102, 'pending', '203.0.113.2', 'exit', 'pending', 'online')`)
	prev := probeAgent
	t.Cleanup(func() { probeAgent = prev })
	probeAgent = func(context.Context, string) error { return errors.New("timeout") }
	status := func(id int) string {
		var s string
		_ = conn.QueryRow(`SELECT status FROM servers WHERE id = $1`, id).Scan(&s)
		return s
	}
	for i := 1; i <= 3; i++ {
		collectAllServerMetrics()
		want := "online"
		if i == 3 {
			want = "offline"
		}
		if got := status(101); got != want {
			t.Errorf("after %d failed probes: %s, want %s", i, got, want)
		}
	}
	if status(102) != "online" {
		t.Error("a node not switched on was judged by the probe")
	}
	probeAgent = func(context.Context, string) error { return nil }
	collectAllServerMetrics()
	if status(101) != "online" {
		t.Error("the agent answers again, still offline")
	}
}

// B3: the Hub's sample is a row with no server; a node's sample also lands in
// load_metrics with the throughput from the previous sample.
func TestRecordNodeSample(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status) VALUES (201, 'x', '203.0.113.9', 'exit', 'online', 'online')`)
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	prevNow := nodeLoadNow
	t.Cleanup(func() { nodeLoadNow = prevNow })
	nodeLoadNow = func() time.Time { return at }
	recordNodeSample(ctx, 0, sampleOut)
	recordNodeSample(ctx, 201, sampleOut)
	nodeLoadNow = func() time.Time { return at.Add(60 * time.Second) }
	recordNodeSample(ctx, 201, "cpu 1000 800\ncpu 1100 850\nmem 2000000 500000\nnproc 2\nnet ens3: 7501000 0 0 0 0 0 0 0 2000 0\nct 10 100\ntcp 5\n")

	var hub, node int
	_ = conn.QueryRow(`SELECT COUNT(*) FILTER (WHERE server_id IS NULL), COUNT(*) FILTER (WHERE server_id = 201) FROM server_metric_samples`).Scan(&hub, &node)
	if hub != 1 || node != 2 {
		t.Fatalf("samples: hub %d node %d", hub, node)
	}
	var raw string
	var cores sql.NullInt64
	_ = conn.QueryRow(`SELECT load_metrics::text, cpu_cores FROM servers WHERE id = 201`).Scan(&raw, &cores)
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	if m["cpu_usage"] != 50.0 || m["memory_usage"] != 75.0 || m["net_rx_mbps"] != 1.0 || m["cores"] != 2.0 || cores.Valid {
		t.Errorf("load_metrics %s cpu_cores %v (the owner's field, never filled by the collector)", raw, cores)
	}
	// The list reads it back as a fresh sample.
	v := parseLoadMetrics(raw, at.Add(90*time.Second))
	if v.Stale || v.CPUPct == nil || *v.CPUPct != 50 || v.NetRxMbps == nil || *v.NetRxMbps != 1 || v.Cores != 2 || *v.ConntrackPct != 10 {
		t.Errorf("parsed load %+v", v)
	}
}

// B5: the report's queries run on Postgres and read the samples back.
func TestLoadCapacityReport(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, cpu_cores, port_mbps, monthly_price_rub)
		VALUES (301, 'Exit A', '203.0.113.30', 'exit', 'online', 'online', 1, 1000, 700)`)
	for i := 0; i < 30; i++ {
		testpg.MustExec(t, conn, `INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores, net_rx_mbps, net_tx_mbps, rx_bytes, tx_bytes)
			VALUES (301, NOW() - make_interval(mins => $1), 70, 40, 1, 100, 20, 1000000000, 1000000000)`, i)
		testpg.MustExec(t, conn, `INSERT INTO server_metric_samples (server_id, at, cpu_pct, mem_pct, cores) VALUES (NULL, NOW() - make_interval(mins => $1), 20, 30, 4)`, i)
	}
	rep, err := LoadCapacityReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ex capGroup
	for _, g := range rep.Groups {
		if g.Role == "exit" {
			ex = g
		}
	}
	if len(ex.Nodes) != 1 || ex.Nodes[0].CPUPeak < 69 || ex.Nodes[0].NetPeakMbps < 99 || ex.Nodes[0].MonthTB < 0.05 || ex.Buy != buyExit {
		t.Fatalf("exit group %+v", ex)
	}
	if rep.MonthlyCostRub != 700+2400 {
		t.Errorf("cost %d", rep.MonthlyCostRub)
	}
}
