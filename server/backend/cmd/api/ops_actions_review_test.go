package main

// Part C review (576ad8d..fb1a52e): every finding graded Important has its test here.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/provision"
	"github.com/heyearth/backend/internal/xraydiff"
)

var opsMSK = time.FixedZone("MSK", 3*3600)

// #1: two entry reboots in one window never empty the rotation.
func TestNodeRebootTwoEntriesNeverEmptyTheRotation(t *testing.T) {
	fresh := factsT0.Add(-time.Minute)
	f := &fakeNodeOps{node: provision.Node{Role: "relay", Lifecycle: "online", HostKeyConfirmed: true}, verifyOK: true,
		entries: []biz.Entry{
			{ID: 5, Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: "all", Health: biz.EntryHealth{External: biz.EntryHealthy, ExternalAt: &fresh}},
			{ID: 6, Kind: biz.EntryKindRelay, Lifecycle: "online", Rotation: "all", Health: biz.EntryHealth{External: biz.EntryHealthy, ExternalAt: &fresh}},
		}}
	d := f.deps()
	d.node = func(_ context.Context, id int) (provision.Node, error) {
		n := f.node
		n.ID = id
		return n, nil
	}
	d.rotateOff = func(_ context.Context, id int, reason, by string) error {
		for i := range f.entries {
			if f.entries[i].ID == id {
				f.entries[i].Rotation = "off"
			}
		}
		f.rotations = append(f.rotations, reason)
		return nil
	}
	s := newOpsStand(t, buildNodeActions(d)...)
	_ = s.store.setMode(context.Background(), "node.reboot", opsModeOn, "owner")
	for _, target := range []string{"5", "6"} {
		if code, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/node.reboot/run",
			map[string]string{"target": target, "when": "window", "confirm_token": s.preview(t, "node.reboot", target)["confirm_token"].(string)}); code != http.StatusAccepted {
			t.Fatalf("schedule %s: %d %v", target, code, out)
		}
	}
	s.deferRuns = true // both runs start after the tick checked both «before»
	s.now = time.Date(2026, 11, 25, 4, 5, 0, 0, opsMSK)
	s.o.tick(context.Background())
	for _, run := range s.pending {
		run()
	}
	if len(f.rotations) != 1 {
		t.Fatalf("the second entry stays: rotations %v", f.rotations)
	}
}

// #2: A1 removes at most one entry per tick, and never one the other judged healthy a moment ago.
func TestA1OneRemovalPerTick(t *testing.T) {
	var removed []int
	paths := []pathHealth{{ID: 5, Known: true, Healthy: true}, {ID: 6, Known: true, Healthy: true}}
	e := newAutoEngine(autoDeps{
		store: &memAutoStore{modes: map[string]autoMode{"A1": autoOn, "A12": autoOff, "A14": autoOff, "A16": autoOff}},
		now:   func() time.Time { return autoT0 }, record: func(biz.OpsEvent) {}, raise: func(string, AlertLevel, string, string) {},
		resolve: func(string, string) {},
		a1Inputs: func(context.Context) ([]a1Input, error) {
			return []a1Input{{EntryID: 5, DownRuns: 3, Paths: paths}, {EntryID: 6, DownRuns: 3, Paths: paths}}, nil
		},
		rotateOff: func(_ context.Context, id int, _ string) error { removed = append(removed, id); return nil },
		bump:      func(context.Context) error { return nil },
		urgent:    func(context.Context, int, string) error { return nil },
	})
	e.tick(context.Background())
	if len(removed) != 1 {
		t.Fatalf("one entry per tick: %v", removed)
	}
}

// #3: a second schedule or a run while one is open is refused; the window re-checks the gap.
func TestActionNoSecondOpenRun(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-xray", Title: "Перезапуск xray", Impact: biz.ImpactK3, Perm: "ops.disrupt", Confirm: "XRAY", MinGap: 30 * time.Minute})
	_ = s.store.setMode(context.Background(), "hub.restart-xray", opsModeOn, "owner")
	sched := func() int {
		tok := s.preview(t, "hub.restart-xray", "")["confirm_token"].(string)
		code, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-xray/run", map[string]string{"when": "window", "confirm_token": tok, "typed": "XRAY"})
		return code
	}
	if c := sched(); c != http.StatusAccepted {
		t.Fatalf("first schedule: %d", c)
	}
	if c := sched(); c != http.StatusConflict {
		t.Fatalf("a second schedule of an open action → 409: %d", c)
	}
	tok := s.preview(t, "hub.restart-xray", "")["confirm_token"].(string)
	if c, _ := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-xray/run", map[string]string{"when": "now", "confirm_token": tok, "typed": "XRAY"}); c != http.StatusConflict {
		t.Fatalf("a run while one is planned → 409: %d", c)
	}
}

func TestTickRechecksTheGap(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Бамбук", Impact: biz.ImpactK2, Perm: "ops.disrupt", MinGap: 10 * time.Minute})
	_ = s.store.setMode(context.Background(), "hub.restart-bamboo", opsModeOn, "owner")
	tok := s.preview(t, "hub.restart-bamboo", "")["confirm_token"].(string)
	_, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-bamboo/run", map[string]string{"when": "window", "confirm_token": tok})
	id := int64(out["run_id"].(float64))
	recent := time.Date(2026, 11, 25, 4, 0, 0, 0, opsMSK)
	_, _ = s.store.insert(context.Background(), opsRun{Action: "hub.restart-bamboo", Status: opsOK, StartedAt: &recent}) // someone ran it at 04:00
	s.now = time.Date(2026, 11, 25, 4, 5, 0, 0, opsMSK)
	s.o.tick(context.Background())
	if r, _ := s.store.get(context.Background(), id); s.ran["hub.restart-bamboo"] != 0 || r.Status != opsCancelled {
		t.Fatalf("5 minutes after a run the planned one is skipped: ran %v, %+v", s.ran, r)
	}
}

// #4: a cancel while the tick checks «before» is honoured.
func TestCancelDuringTheChecksBeforeIsHonoured(t *testing.T) {
	var s *opsStand
	var runID int64
	inTick := false
	s = newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Бамбук", Impact: biz.ImpactK2, Perm: "ops.disrupt",
		Pre: func(context.Context, string) ([]opsCheck, error) {
			if inTick { // the owner cancels while the tick checks «before»
				s.do(t, http.MethodPost, "/api/admin/ops/actions/runs/"+itoa64(runID)+"/cancel", nil)
			}
			return []opsCheck{{Name: "x", OK: true}}, nil
		}})
	_ = s.store.setMode(context.Background(), "hub.restart-bamboo", opsModeOn, "owner")
	tok := s.preview(t, "hub.restart-bamboo", "")["confirm_token"].(string)
	_, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/hub.restart-bamboo/run", map[string]string{"when": "window", "confirm_token": tok})
	runID = int64(out["run_id"].(float64))
	s.now = time.Date(2026, 11, 25, 4, 5, 0, 0, opsMSK)
	inTick = true
	s.o.tick(context.Background())
	if r, _ := s.store.get(context.Background(), runID); s.ran["hub.restart-bamboo"] != 0 || r.Status != opsCancelled {
		t.Fatalf("cancelled stays cancelled: ran %v, %+v", s.ran, r)
	}
}

// #5: a panic or a long-dead run never stays «running».
func TestRunPanicIsClosed(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write",
		Run: func(context.Context, string, string, func(string)) error { panic("boom") }})
	tok := s.preview(t, "backup.run", "")["confirm_token"].(string)
	_, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/backup.run/run", map[string]string{"when": "now", "confirm_token": tok})
	if r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64))); r.Status != opsFailed {
		t.Fatalf("a panic closes the run as failed: %+v", r)
	}
}

func TestTickSweepsOldRunningRuns(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"},
		opsAction{ID: "hub.restart-api", Title: "API", Impact: biz.ImpactK1, Perm: "settings.write", HandOver: true})
	old := s.now.Add(-40 * time.Minute)
	young := s.now.Add(-5 * time.Minute)
	a, _ := s.store.insert(context.Background(), opsRun{Action: "backup.run", Status: opsRunning, StartedAt: &old})
	b, _ := s.store.insert(context.Background(), opsRun{Action: "backup.run", Target: "", Status: opsRunning, StartedAt: &young})
	s.o.tick(context.Background()) // outside the window: the sweep still runs
	if r, _ := s.store.get(context.Background(), a); r.Status != opsInterrupted {
		t.Fatalf("40 minutes «running» → interrupted: %+v", r)
	}
	if r, _ := s.store.get(context.Background(), b); r.Status != opsRunning {
		t.Fatalf("a young run is left alone: %+v", r)
	}
}

// #6: a run planned for a night that was missed is never run later.
func TestScheduledRunExpires(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "hub.restart-bamboo", Title: "Бамбук", Impact: biz.ImpactK2, Perm: "ops.disrupt"})
	_ = s.store.setMode(context.Background(), "hub.restart-bamboo", opsModeOn, "owner")
	missed := time.Date(2026, 11, 24, 4, 0, 0, 0, opsMSK)
	id, _ := s.store.insert(context.Background(), opsRun{Action: "hub.restart-bamboo", Status: opsScheduled, ScheduledFor: &missed, Impact: biz.ImpactK2})
	s.now = time.Date(2026, 11, 25, 4, 1, 0, 0, opsMSK) // the next night
	s.o.tick(context.Background())
	if r, _ := s.store.get(context.Background(), id); s.ran["hub.restart-bamboo"] != 0 || r.Status != opsCancelled {
		t.Fatalf("a missed night expires: ran %v, %+v", s.ran, r)
	}
}

// #7: a blind after-check proves nothing — not «проверка не прошла».
func TestBlindAfterCheckIsNotAFailure(t *testing.T) {
	s := newOpsStand(t, opsAction{ID: "probes.run", Title: "Прогон", Impact: biz.ImpactK0, Perm: "settings.write",
		Post: func(context.Context, string) ([]opsCheck, error) {
			return []opsCheck{{Name: "прогон синтетики", Blind: true, Detail: "слепой прогон"}}, nil
		}})
	tok := s.preview(t, "probes.run", "")["confirm_token"].(string)
	_, out := s.do(t, http.MethodPost, "/api/admin/ops/actions/probes.run/run", map[string]string{"when": "now", "confirm_token": tok})
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsOK {
		t.Fatalf("blind → ok with a note: %+v", r)
	}
	last := s.feed[len(s.feed)-1]
	if strings.Contains(last.Summary, "не прошла") || !strings.Contains(last.Summary, "не смотрела") {
		t.Fatalf("the feed says the check could not look: %+v", last)
	}
	if len(s.levels) != 1 || s.levels[0] != AlertWarn {
		t.Fatalf("a warning, not a critical alert: %v", s.levels)
	}
}

// #8: the next good run resolves the after-check alert.
func TestGoodRunResolvesThePostFailedAlert(t *testing.T) {
	fail := true
	s := newOpsStand(t, opsAction{ID: "probes.run", Title: "Прогон", Impact: biz.ImpactK0, Perm: "settings.write",
		Post: func(context.Context, string) ([]opsCheck, error) {
			return []opsCheck{{Name: "прогон", OK: !fail}}, nil
		}})
	run := func() {
		tok := s.preview(t, "probes.run", "")["confirm_token"].(string)
		s.do(t, http.MethodPost, "/api/admin/ops/actions/probes.run/run", map[string]string{"when": "now", "confirm_token": tok})
	}
	run()
	fail = false
	run()
	if len(s.resolved) != 1 || s.resolved[0] != "action-post-failed:probes.run" {
		t.Fatalf("resolved: %v", s.resolved)
	}
}

func TestA1BlockedAlertResolves(t *testing.T) {
	var raised, resolved []string
	down := 3
	e := newAutoEngine(autoDeps{
		store: &memAutoStore{modes: map[string]autoMode{"A1": autoDryRun, "A12": autoOff, "A14": autoOff, "A16": autoOff}},
		now:   func() time.Time { return autoT0 }, record: func(biz.OpsEvent) {},
		raise:   func(k string, _ AlertLevel, _, _ string) { raised = append(raised, k) },
		resolve: func(k, _ string) { resolved = append(resolved, k) },
		a1Inputs: func(context.Context) ([]a1Input, error) {
			return []a1Input{{EntryID: 5, DownRuns: down}}, nil // no other path: blocked
		},
	})
	e.tick(context.Background())
	down = 0
	e.tick(context.Background())
	if len(raised) != 1 || len(resolved) != 1 || resolved[0] != "a1-blocked:5" {
		t.Fatalf("raised %v resolved %v", raised, resolved)
	}
}

// #9: a stale xray (its next apply is a restart) refuses taking an exit out.
func TestExitOutRefusesWhileXrayIsStale(t *testing.T) {
	f := &fakeExitSvc{routed: []int{2, 3}, kind: xraydiff.UserOnly}
	d := f.deps()
	d.xrayStale = func() bool { return true }
	s := newOpsStand(t, buildExitActions(d)...)
	_ = s.store.setMode(context.Background(), "exit.out-of-service", opsModeOn, "owner")
	if code, _ := s.runAction(t, "exit.out-of-service", "3", ""); code != http.StatusUnprocessableEntity || f.synced != 0 {
		t.Fatalf("stale xray → refused before anything: %d", code)
	}
}

type memAutoStore struct {
	modes map[string]autoMode
	fires map[string]int
}

func (m *memAutoStore) load(context.Context) (map[string]autoRuleState, error) {
	out := map[string]autoRuleState{}
	for k, v := range m.modes {
		out[k] = autoRuleState{Mode: v, Fired: m.fires[k]}
	}
	return out, nil
}
func (m *memAutoStore) setMode(_ context.Context, id string, mode autoMode) error {
	m.modes[id] = mode
	return nil
}
func (m *memAutoStore) fired(_ context.Context, id, _ string, _ time.Time) error {
	if m.fires == nil {
		m.fires = map[string]int{}
	}
	m.fires[id]++
	return nil
}
