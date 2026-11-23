package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/provision"
)

func TestReturnDecision(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	down := nodeSeen{Up: false, Since: t0}
	up := func(at time.Time) nodeSeen { return nodeSeen{Up: true, Since: at} }

	if returnDecision(down, up(t0.Add(time.Minute)), time.Time{}, t0.Add(time.Minute)) {
		t.Error("down 1 minute and back: no verify (a blip)")
	}
	if !returnDecision(down, up(t0.Add(3*time.Minute)), time.Time{}, t0.Add(3*time.Minute)) {
		t.Error("down 3 minutes and back: one verify")
	}
	lastVerify := t0.Add(3 * time.Minute)
	again := t0.Add(20 * time.Minute)
	if returnDecision(nodeSeen{Up: false, Since: again.Add(-5 * time.Minute)}, up(again), lastVerify, again) {
		t.Error("a second return 20 minutes after a verify: no (at most once an hour per node)")
	}
	later := t0.Add(2 * time.Hour)
	if !returnDecision(nodeSeen{Up: false, Since: later.Add(-5 * time.Minute)}, up(later), lastVerify, later) {
		t.Error("a return two hours after the last verify: yes")
	}
	if returnDecision(up(t0), up(t0.Add(time.Hour)), time.Time{}, t0.Add(time.Hour)) {
		t.Error("a node that never went down: no")
	}
	if returnDecision(nodeSeen{}, up(t0), time.Time{}, t0) {
		t.Error("first sight of a node (no previous state): no")
	}
}

// The watcher turns tunnel/entry health into nodeSeen and starts one verify per return, with
// a line in the feed (rule A13) and a second line with the job's result.
func TestNodeReturnWatcher(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	var started []int
	var feed []biz.OpsEvent
	w := &nodeReturnWatch{
		seen:       map[string]nodeSeen{},
		lastVerify: map[string]time.Time{},
		start: func(ctx context.Context, serverID int) (int64, error) {
			started = append(started, serverID)
			return int64(100 + serverID), nil
		},
		jobResult: func(ctx context.Context, jobID int64) (string, bool) { return provision.JobSucceeded, true },
		record:    func(e biz.OpsEvent) { feed = append(feed, e) },
	}
	step := func(at time.Time, exit3Up bool, entry5 string) {
		w.observe(context.Background(), at, []nodeState{
			{Key: "exit:3", ServerID: 3, Up: exit3Up, Known: true},
			{Key: "entry:5", ServerID: 5, Up: entry5 == biz.EntryHealthy, Known: entry5 != biz.EntryUnknown},
		})
	}
	step(t0, true, biz.EntryHealthy)
	step(t0.Add(time.Minute), false, biz.EntryUnknown) // exit down; the entry's check is unknown
	step(t0.Add(4*time.Minute), true, biz.EntryHealthy)
	if len(started) != 1 || started[0] != 3 {
		t.Fatalf("verify started for %v, want only exit 3 (the entry's unknown is not a down)", started)
	}
	w.wait() // the job's result line
	if len(feed) != 2 || feed[0].Kind != "auto" || feed[0].Target != "exit:3" || feed[0].Details["rule"] != "A13" ||
		!strings.Contains(feed[0].Summary, "узел вернулся после недоступности 3 мин") || feed[0].Impact != biz.ImpactK0 {
		t.Fatalf("feed %+v", feed)
	}
	if !strings.Contains(feed[1].Summary, provision.JobSucceeded) || !feed[1].OK {
		t.Errorf("result line %+v", feed[1])
	}
}
