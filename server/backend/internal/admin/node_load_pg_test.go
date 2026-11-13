package admin

import (
	"context"
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
	var cores int
	_ = conn.QueryRow(`SELECT load_metrics::text, cpu_cores FROM servers WHERE id = 201`).Scan(&raw, &cores)
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	if m["cpu_usage"] != 50.0 || m["memory_usage"] != 75.0 || m["net_rx_mbps"] != 1.0 || cores != 2 {
		t.Errorf("load_metrics %s cores %d", raw, cores)
	}
}
