package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

type memCanaryStore struct {
	rows   []canaryChange
	nextID int64
}

func (m *memCanaryStore) open(context.Context) (*canaryChange, error) {
	for i := range m.rows {
		if m.rows[i].active() {
			c := m.rows[i]
			return &c, nil
		}
	}
	return nil, nil
}
func (m *memCanaryStore) insert(_ context.Context, c canaryChange) (int64, error) {
	if c.active() {
		if open, _ := m.open(context.Background()); open != nil {
			return 0, errCanaryBusy
		}
	}
	m.nextID++
	c.ID = m.nextID
	m.rows = append(m.rows, c)
	return c.ID, nil
}
func (m *memCanaryStore) save(_ context.Context, c canaryChange) error {
	for i := range m.rows {
		if m.rows[i].ID == c.ID {
			m.rows[i] = c
		}
	}
	return nil
}
func (m *memCanaryStore) recent(context.Context, int) ([]canaryChange, error) { return m.rows, nil }

type canaryHarness struct {
	now      time.Time
	store    *memCanaryStore
	flags    map[string]string
	writes   []string
	urgent   int
	runs     int
	next     []canaryRun // what the next runs see
	busy     bool
	autoOn   bool
	raised   []string
	resolved []string
	feed     []biz.OpsEvent
}

func newCanaryHarness(t *testing.T) (*canaryHarness, *canaryRunner) {
	t.Helper()
	h := &canaryHarness{now: canaryT0, store: &memCanaryStore{}, flags: map[string]string{"sub_json_dns": "off"}, autoOn: true}
	return h, h.runner()
}

func (h *canaryHarness) runner() *canaryRunner {
	return &canaryRunner{d: canaryDeps{
		store: h.store,
		run: func(ctx context.Context, trigger string) (int64, string, error) {
			if h.busy {
				return 0, "", errSynthBusy
			}
			h.runs++
			r := canaryRun{State: "pass"}
			if len(h.next) > 0 {
				r, h.next = h.next[0], h.next[1:]
			}
			return int64(h.runs), r.State, nil
		},
		failingOf: func(ctx context.Context, id int64, state string) []string { return h.lastFailing(id) },
		latest: func(ctx context.Context) (canaryRun, error) {
			return canaryRun{At: h.now.Add(-2 * time.Minute), State: "pass"}, nil
		},
		state: func(ctx context.Context, f subFlag) (string, error) {
			if v := h.flags[f.Key]; v != "" {
				return v, nil
			}
			return "off", nil
		},
		apply: func(ctx context.Context, f subFlag, to string) (string, bool, error) {
			from := h.flags[f.Key]
			if from == to {
				return from, false, nil
			}
			h.flags[f.Key] = to
			h.writes = append(h.writes, f.Key+"="+to)
			return from, true, nil
		},
		urgent:  func(ctx context.Context, hours int, by string) error { h.urgent++; return nil },
		raise:   func(key string, level AlertLevel, title, details string) { h.raised = append(h.raised, key) },
		resolve: func(key, msg string) { h.resolved = append(h.resolved, key) },
		record:  func(e biz.OpsEvent) { h.feed = append(h.feed, e) },
		autoOn:  func(context.Context) bool { return h.autoOn },
		now:     func() time.Time { return h.now },
	}}
}

// lastFailing: the failing keys queued for run id (stored by queue()).
var canaryQueuedFailing = map[int64][]string{}

func (h *canaryHarness) lastFailing(id int64) []string { return canaryQueuedFailing[id] }

// tickTo ticks every minute until `to` minutes after t0; a run whose failing keys are given in
// bad (by the run's number) reports them.
func (h *canaryHarness) tickTo(r *canaryRunner, to int, bad map[int][]string) {
	for h.now.Before(canaryT0.Add(time.Duration(to) * time.Minute)) {
		h.now = h.now.Add(time.Minute)
		if f, ok := bad[h.runs+1]; ok {
			canaryQueuedFailing[int64(h.runs+1)] = f
			h.next = []canaryRun{{State: "fail"}}
		}
		r.tick(context.Background())
	}
}

func TestCanaryRunnerAppliesSteps(t *testing.T) {
	canaryQueuedFailing = map[int64][]string{}
	h, r := newCanaryHarness(t)
	c, err := r.startTest(context.Background(), "sub_json_dns", "on", "owner")
	if err != nil || c.Phase != canaryTest || h.flags["sub_json_dns"] != "test" {
		t.Fatalf("start: %+v %v flags %v", c, err, h.flags)
	}
	h.tickTo(r, 31, nil)
	open, _ := h.store.open(context.Background())
	if open == nil || open.Phase != canaryReady || h.runs < canaryMinRuns {
		t.Fatalf("after 31 min: %+v, runs %d", open, h.runs)
	}
	if _, err := r.promote(context.Background(), "sub_json_dns", "owner"); err != nil {
		t.Fatal(err)
	}
	if h.flags["sub_json_dns"] != "on" || h.urgent != 1 {
		t.Fatalf("after promote: flags %v urgent %d", h.flags, h.urgent)
	}
	h.tickTo(r, 95, nil)
	if open, _ := h.store.open(context.Background()); open != nil {
		t.Fatalf("after the guard hour the change is still open: %+v", open)
	}
	if last := h.store.rows[0]; last.Phase != canaryDone || !strings.Contains(last.Note, "хуже не стало") {
		t.Errorf("finished %+v", last)
	}
	if !slices.Equal(h.writes, []string{"sub_json_dns=test", "sub_json_dns=on"}) {
		t.Errorf("flag writes %v", h.writes)
	}
	if len(h.feed) < 2 || h.feed[0].Target != "flag:sub_json_dns" || h.feed[0].Kind != "action" {
		t.Errorf("feed %+v", h.feed)
	}
}

// Review Focus 2: a restart in the middle resumes from the stored phase and never rolls back twice.
func TestCanaryResumesAfterRestart(t *testing.T) {
	canaryQueuedFailing = map[int64][]string{}
	h, r := newCanaryHarness(t)
	h.flags["sub_json_dns"] = "on"
	g := canaryChange{Flag: "sub_json_dns", From: "test", To: "on", Phase: canaryGuard, By: "owner",
		StartedAt: canaryT0.Add(-40 * time.Minute), PhaseAt: canaryT0.Add(-10 * time.Minute), LastRunAt: canaryT0.Add(-5 * time.Minute),
		BadStreak: 1, NewFailing: []string{"⚡ Авто//blocked"}}
	g.ID, _ = h.store.insert(context.Background(), g)
	h.tickTo(r, 1, map[int][]string{1: {"⚡ Авто//blocked"}})
	if h.flags["sub_json_dns"] != "test" || !slices.Contains(h.raised, "flag-rollback:sub_json_dns") {
		t.Fatalf("rollback: flags %v raised %v", h.flags, h.raised)
	}
	auto := slices.IndexFunc(h.feed, func(e biz.OpsEvent) bool { return e.Kind == "auto" && e.Details["rule"] == "A3" })
	if auto < 0 {
		t.Errorf("no A3 line in the feed: %+v", h.feed)
	}
	// a new process over the same store
	writes, runs := len(h.writes), h.runs
	r2 := h.runner()
	h.tickTo(r2, 20, nil)
	if len(h.writes) != writes || h.runs != runs {
		t.Errorf("the finished change ran again: writes %v runs %d→%d", h.writes, runs, h.runs)
	}
}

func TestCanaryRunnerBusySynth(t *testing.T) {
	canaryQueuedFailing = map[int64][]string{}
	h, r := newCanaryHarness(t)
	if _, err := r.startTest(context.Background(), "sub_json_dns", "on", "owner"); err != nil {
		t.Fatal(err)
	}
	h.busy = true
	h.tickTo(r, 12, nil)
	c, _ := h.store.open(context.Background())
	if c.GoodRuns != 0 || c.BadStreak != 0 || c.BlindStreak != 0 || !c.LastRunAt.IsZero() {
		t.Fatalf("a busy synthetic run counted: %+v", c)
	}
	h.busy = false
	h.tickTo(r, 13, nil)
	if c, _ := h.store.open(context.Background()); c.GoodRuns != 1 {
		t.Errorf("the run after the busy ones: %+v", c)
	}
}

func TestCanaryAutoRollbackOff(t *testing.T) {
	canaryQueuedFailing = map[int64][]string{}
	h, r := newCanaryHarness(t)
	h.autoOn = false
	h.flags["sub_json_dns"] = "on"
	g := canaryChange{Flag: "sub_json_dns", From: "test", To: "on", Phase: canaryGuard, By: "owner",
		StartedAt: canaryT0.Add(-40 * time.Minute), PhaseAt: canaryT0.Add(-10 * time.Minute), LastRunAt: canaryT0.Add(-5 * time.Minute),
		BadStreak: 1, NewFailing: []string{"⚡ Авто//blocked"}}
	g.ID, _ = h.store.insert(context.Background(), g)
	h.tickTo(r, 1, map[int][]string{1: {"⚡ Авто//blocked"}})
	if h.flags["sub_json_dns"] != "on" || h.urgent != 0 {
		t.Errorf("auto rollback off: flags %v urgent %d", h.flags, h.urgent)
	}
	if !slices.Contains(h.raised, "flag-rollback:sub_json_dns") {
		t.Errorf("no alert: %v", h.raised)
	}
	if open, _ := h.store.open(context.Background()); open != nil {
		t.Errorf("the change stays open: %+v", open)
	}
}

func TestCanaryRunnerRefusals(t *testing.T) {
	canaryQueuedFailing = map[int64][]string{}
	h, r := newCanaryHarness(t)
	ctx := context.Background()
	if _, err := r.startTest(ctx, "subscription_update_interval_hours", "on", "owner"); err == nil {
		t.Error("a flag without the canary")
	}
	if _, err := r.startTest(ctx, "sub_json_dns", "on", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.startTest(ctx, "sub_direct_by_name", "on", "owner"); err != errCanaryBusy {
		t.Errorf("second change while one is open: %v", err)
	}
	if _, err := r.promote(ctx, "sub_json_dns", "owner"); err == nil || !strings.Contains(err.Error(), "PASS") {
		t.Errorf("promote from test: %v", err)
	}
	if _, err := r.bypass(ctx, "sub_direct_by_name", "test", "коротко", "owner"); err == nil {
		t.Error("bypass with a short reason")
	}
	_ = h
}
