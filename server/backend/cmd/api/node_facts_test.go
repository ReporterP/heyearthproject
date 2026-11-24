package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/provision"
)

var factsT0 = time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC)

func TestNodeRebootPendingSince(t *testing.T) {
	need := provision.NodeFacts{Kernel: "6.8.0", BootAt: factsT0.Add(-40 * 24 * time.Hour), RebootRequired: true}
	first := mergeFacts(nil, 3, need, factsT0)
	if !first.RebootRequiredSince.Equal(factsT0) {
		t.Fatalf("the first sighting starts the clock: %v", first.RebootRequiredSince)
	}
	again := mergeFacts(&first, 3, need, factsT0.Add(13*24*time.Hour))
	if !again.RebootRequiredSince.Equal(factsT0) {
		t.Fatalf("a repeated collection never resets the clock: %v", again.RebootRequiredSince)
	}
	if _, _, alert := rebootPendingVerdict(again.RebootRequiredSince, factsT0.Add(13*24*time.Hour)); alert {
		t.Fatal("13 days — quiet")
	}
	if _, _, alert := rebootPendingVerdict(again.RebootRequiredSince, factsT0.Add(15*24*time.Hour)); !alert {
		t.Fatal("15 days — the alert")
	}
	done := mergeFacts(&again, 3, provision.NodeFacts{Kernel: "6.8.1", BootAt: factsT0.Add(14 * 24 * time.Hour)}, factsT0.Add(14*24*time.Hour))
	if _, _, alert := rebootPendingVerdict(done.RebootRequiredSince, factsT0.Add(20*24*time.Hour)); !done.RebootRequiredSince.IsZero() || alert {
		t.Fatalf("rebooted: the clock is gone: %+v", done)
	}
}

func TestNodeFactsAlerts(t *testing.T) {
	var raised, resolved []string
	j := factsAlerts{raise: func(k string, _ AlertLevel, _, _ string) { raised = append(raised, k) },
		resolve: func(k, _ string) { resolved = append(resolved, k) }}
	list := []nodeFacts{
		{ServerID: 0, RebootRequired: true, RebootRequiredSince: factsT0.Add(-20 * 24 * time.Hour), SecurityUpdates: 3},
		{ServerID: 5, SecurityUpdates: 0},
	}
	j.check(list, factsT0)
	if !reflect.DeepEqual(raised, []string{"node-security-updates:hub"}) || // the Hub's reboot-pending is its board cell's (phase 1)
		!reflect.DeepEqual(resolved, []string{"node-reboot-pending:5", "node-security-updates:5"}) {
		t.Fatalf("raised %v resolved %v", raised, resolved)
	}
}

func TestOtherHealthyPaths(t *testing.T) {
	now := factsT0
	fresh := now.Add(-2 * time.Minute)
	old := now.Add(-30 * time.Minute)
	e := func(id int, rot, ext string, at *time.Time) biz.Entry {
		return biz.Entry{ID: id, Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: rot,
			Health: biz.EntryHealth{External: ext, ExternalAt: at}}
	}
	hub := biz.Entry{ID: 0, Kind: biz.EntryKindHub, Rotation: "all", Health: biz.EntryHealth{External: biz.EntryUnknown}}
	cases := []struct {
		entries []biz.Entry
		want    int
	}{
		{[]biz.Entry{hub, e(5, "all", biz.EntryHealthy, &fresh)}, 0}, // only the one rebooted
		{[]biz.Entry{hub, e(5, "all", biz.EntryHealthy, &fresh), e(6, "all", biz.EntryHealthy, &fresh)}, 1},
		{[]biz.Entry{hub, e(5, "all", biz.EntryHealthy, &fresh), e(6, "all", biz.EntryUnhealthy, &fresh)}, 0},
		{[]biz.Entry{hub, e(5, "all", biz.EntryHealthy, &fresh), e(6, "all", biz.EntryHealthy, &old)}, 0}, // stale = unknown = not healthy
		{[]biz.Entry{hub, e(5, "all", biz.EntryHealthy, &fresh), e(6, "test", biz.EntryHealthy, &fresh)}, 0},
	}
	for i, c := range cases {
		if got := otherHealthyPaths(c.entries, 5, now); got != c.want {
			t.Errorf("#%d: %d, want %d", i, got, c.want)
		}
	}
}

type fakeNodeOps struct {
	node      provision.Node
	entries   []biz.Entry
	jobs      []string
	rotations []string
	silences  []string
	verifyOK  bool
	bumped    int
}

func (f *fakeNodeOps) deps() nodeOpsDeps {
	return nodeOpsDeps{
		node: func(context.Context, int) (provision.Node, error) {
			if f.node.ID == 0 {
				return provision.Node{}, errors.New("нет такого узла")
			}
			return f.node, nil
		},
		entries: func(context.Context) ([]biz.Entry, error) { return f.entries, nil },
		runJob: func(_ context.Context, id int, kind string) (string, error) {
			f.jobs = append(f.jobs, kind)
			if kind == provision.KindVerify && !f.verifyOK {
				return provision.JobFailed, nil
			}
			return provision.JobSucceeded, nil
		},
		rotateOff: func(_ context.Context, id int, reason, by string) error {
			f.rotations = append(f.rotations, reason)
			return nil
		},
		bump: func(context.Context) error { f.bumped++; return nil },
		silence: func(_ context.Context, pattern, reason, by string, d time.Duration) error {
			f.silences = append(f.silences, pattern)
			return nil
		},
		online: func(time.Time) onlineCounts { return onlineCounts{Xray: 30} },
		sleep:  func(time.Duration) {},
		now:    func() time.Time { return factsT0 },
	}
}

func nodeStand(t *testing.T, f *fakeNodeOps) *opsStand {
	t.Helper()
	s := newOpsStand(t, buildNodeActions(f.deps())...)
	_ = s.store.setMode(context.Background(), "node.reboot", opsModeOn, "owner")
	return s
}

func TestRebootEntryNotTheLastHealthyPath(t *testing.T) {
	fresh := factsT0.Add(-time.Minute)
	f := &fakeNodeOps{node: provision.Node{ID: 5, Role: "relay", Lifecycle: "online", HostKeyConfirmed: true}, verifyOK: true,
		entries: []biz.Entry{{ID: 5, Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: "all",
			Health: biz.EntryHealth{External: biz.EntryHealthy, ExternalAt: &fresh}}}}
	s := nodeStand(t, f)
	if code, _ := s.runAction(t, "node.reboot", "5", ""); code != http.StatusUnprocessableEntity || len(f.jobs) != 0 {
		t.Fatalf("the last healthy entry is not rebooted: %d %v", code, f.jobs)
	}
	f.entries = append(f.entries, biz.Entry{ID: 6, Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: "all",
		Health: biz.EntryHealth{External: biz.EntryHealthy, ExternalAt: &fresh}})
	code, out := s.runAction(t, "node.reboot", "5", "")
	if code != http.StatusAccepted {
		t.Fatalf("run: %d %v", code, out)
	}
	if !reflect.DeepEqual(f.jobs, []string{provision.KindReboot, provision.KindVerify}) {
		t.Fatalf("reboot, then verify once it is back: %v", f.jobs)
	}
	if len(f.rotations) != 1 || f.bumped != 1 || !strings.Contains(f.rotations[0], "перезагрузка") {
		t.Fatalf("the entry leaves the rotation first (and the subscription version moves): %v %d", f.rotations, f.bumped)
	}
	if !reflect.DeepEqual(f.silences, []string{"relay-5-*"}) {
		t.Fatalf("the entry's alerts are silenced for the reboot: %v", f.silences)
	}
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsOK {
		t.Fatalf("verify passed → ok: %+v", r)
	}
}

func TestRebootExitWaitsForVerify(t *testing.T) {
	f := &fakeNodeOps{node: provision.Node{ID: 3, Role: "exit", Lifecycle: "online", HostKeyConfirmed: true}}
	s := nodeStand(t, f)
	_, out := s.runAction(t, "node.reboot", "3", "")
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsFailed || len(f.rotations) != 0 {
		t.Fatalf("an exit that never verifies within 10 minutes: failed, no rotation touched: %+v %v", r, f.rotations)
	}
	if !reflect.DeepEqual(f.silences, []string{"exit-down-3", "exit-path-down-3-*"}) {
		t.Fatalf("the exit's alerts are silenced: %v", f.silences)
	}
	if n := strings.Count(strings.Join(f.jobs, ","), provision.KindVerify); n < 2 {
		t.Fatalf("verify is retried while the node comes back: %v", f.jobs)
	}
}

func TestNodeActionsClass(t *testing.T) {
	if a := opsActions["node.reboot"]; a.Impact != biz.ImpactK2 || requiredPerm(a) != "ops.disrupt" || a.Target != "node" {
		t.Errorf("node.reboot: %s %s %q", a.Impact, requiredPerm(a), a.Target)
	}
	if a := opsActions["hub.reboot"]; a.Impact != biz.ImpactK3 || a.Confirm == "" || !a.HandOver || a.Resume == nil {
		t.Errorf("hub.reboot: %s %q handover %v", a.Impact, a.Confirm, a.HandOver)
	}
}
