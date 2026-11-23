package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

func TestProbeRunStored(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	store := sqlSynthStore{}
	if got, err := store.loadStreaks(ctx); err != nil || len(got) != 0 {
		t.Fatalf("fresh database: streaks %v, %v", got, err)
	}
	run := synthRunRecord{At: time.Now().UTC().Truncate(time.Second), Vantage: "outside", Trigger: "timer", SubVersion: 42, State: "fail", Summary: "1 из 2 проверок не прошли"}
	rows := []synthResult{
		{Line: "⚡ Авто", Way: "p-hub-tcp", Target: "bulk", OK: true, Ms: 600, Bytes: 87533},
		{Line: "⚡ Авто", Target: "blocked", OK: false, Ms: 15000, Err: "connect: timeout"},
	}
	streaks := map[string]streak{"⚡ Авто/p-hub-tcp/bulk": {OKs: 1}, "⚡ Авто//blocked": {Fails: 1}, "~blind": {Fails: 2}}
	id, err := store.save(ctx, run, rows, streaks)
	if err != nil || id == 0 {
		t.Fatalf("save: %d, %v", id, err)
	}
	if got, err := store.loadStreaks(ctx); err != nil || len(got) != 3 || got["~blind"] != (streak{Fails: 2}) || got["⚡ Авто//blocked"] != (streak{Fails: 1}) {
		t.Fatalf("streaks read back: %v, %v", got, err)
	}
	// the next save replaces the streaks; a report (streaks nil) leaves them alone
	if _, err := store.save(ctx, run, nil, map[string]streak{"x//ru": {OKs: 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.save(ctx, synthRunRecord{At: run.At, Vantage: "ru-agent:test", Trigger: "timer", State: "pass"}, rows[:1], nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.loadStreaks(ctx); len(got) != 1 || got["x//ru"] != (streak{OKs: 3}) {
		t.Fatalf("streaks after a replace and a report: %v", got)
	}

	// through the console's endpoint
	rec := httptest.NewRecorder()
	handleAdminOpsProbes(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ops/probes?hours=24", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Runs    []probeRunView `json:"runs"`
		Running bool           `json:"running"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Runs) != 3 {
		t.Fatalf("%d runs, want 3: %s", len(resp.Runs), rec.Body.String())
	}
	var first *probeRunView
	for i := range resp.Runs {
		if resp.Runs[i].ID == id {
			first = &resp.Runs[i]
		}
	}
	if first == nil || first.State != "fail" || first.SubVersion != 42 || len(first.Results) != 2 {
		t.Fatalf("the stored run did not come back whole: %+v", first)
	}
	var blocked probeResultView
	for _, r := range first.Results {
		if r.Target == "blocked" {
			blocked = r
		}
	}
	if blocked.OK || blocked.Error != "connect: timeout" || blocked.Way != "" || blocked.Ms != 15000 {
		t.Errorf("result = %+v", blocked)
	}

	// retention: results go with their run
	if _, err := conn.Exec(`UPDATE probe_runs SET at = now() - interval '15 days' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := purgeProbeRuns(ctx, synthKeep); err != nil || n != 1 {
		t.Fatalf("purge: %d, %v", n, err)
	}
	var left int
	if err := conn.QueryRow(`SELECT count(*) FROM probe_results WHERE run_id = $1`, id).Scan(&left); err != nil || left != 0 {
		t.Fatalf("results of a purged run: %d, %v", left, err)
	}
}

func TestProbeReportStored(t *testing.T) {
	conn := testpg.Open(t)
	withProbeReportToken(t, "agent-secret-0123456789", nil)
	rec := postProbeReport("Bearer agent-secret-0123456789", goodProbeReport)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var vantage, state string
	var n int
	if err := conn.QueryRow(`SELECT p.vantage, p.state, count(r.*) FROM probe_runs p JOIN probe_results r ON r.run_id = p.id GROUP BY p.id`).Scan(&vantage, &state, &n); err != nil {
		t.Fatal(err)
	}
	if vantage != "ru-agent:test" || state != "fail" || n != 2 {
		t.Fatalf("stored: vantage %q, state %q, %d results", vantage, state, n)
	}
	// a report does not touch the verdict streaks of the Hub's own client
	var streaks int
	if err := conn.QueryRow(`SELECT count(*) FROM probe_streaks`).Scan(&streaks); err != nil || streaks != 0 {
		t.Fatalf("streaks after a report: %d, %v", streaks, err)
	}
	if rec := postProbeReport("Bearer agent-secret-0123456789", `{"vantage":"outside","results":[{"line":"l","target":"ru","ok":true}]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("vantage without the prefix: status %d, want 400", rec.Code)
	}
}
