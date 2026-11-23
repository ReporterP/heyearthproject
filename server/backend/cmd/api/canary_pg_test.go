package main

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

func TestFlagChangesStore(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	s := sqlCanaryStore{}
	if c, err := s.open(ctx); err != nil || c != nil {
		t.Fatalf("fresh: %v %v", c, err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	c := canaryChange{Flag: "sub_json_dns", From: "off", To: "on", Phase: canaryTest, By: "owner", StartedAt: now, PhaseAt: now,
		Baseline: []string{"🎋 Бамбук/b-r5/bulk"}}
	id, err := s.insert(ctx, c)
	if err != nil || id == 0 {
		t.Fatalf("insert: %d %v", id, err)
	}
	c.ID = id
	c.GoodRuns, c.BadStreak, c.BlindFor, c.LastRunAt = 3, 1, 10*time.Minute, now.Add(5*time.Minute)
	c.NewFailing = []string{"⚡ Авто//blocked"}
	if err := s.save(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.open(ctx)
	if err != nil || got == nil || got.ID != id || got.GoodRuns != 3 || got.BadStreak != 1 || got.BlindFor != 10*time.Minute ||
		!slices.Equal(got.Baseline, c.Baseline) || !slices.Equal(got.NewFailing, c.NewFailing) || !got.LastRunAt.Equal(c.LastRunAt) {
		t.Fatalf("read back %+v (%v)", got, err)
	}
	// one unfinished change for the whole table
	if _, err := s.insert(ctx, canaryChange{Flag: "sub_direct_by_name", From: "off", To: "on", Phase: canaryTest, By: "owner", StartedAt: now, PhaseAt: now}); err == nil {
		t.Fatal("a second unfinished change was stored")
	}
	// finishing frees the place
	c.Phase, c.Note = canaryDone, "страховочный час прошёл: хуже не стало"
	if err := s.save(ctx, c); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.open(ctx); open != nil {
		t.Fatalf("a finished change is open: %+v", open)
	}
	if _, err := s.insert(ctx, canaryChange{Flag: "sub_direct_by_name", From: "off", To: "on", Phase: canaryTest, By: "owner", StartedAt: now, PhaseAt: now}); err != nil {
		t.Fatalf("after the finish: %v", err)
	}
	if rec, err := s.recent(ctx, 20); err != nil || len(rec) != 2 || rec[1].Note == "" {
		t.Errorf("recent %+v %v", rec, err)
	}
}

// The stand (plan B7 step 5): the real store and the real flag writes on PostgreSQL, a stand-in
// synthetic client. «На тест», a clean soak, «Всем», then two bad runs in the guard hour: the
// flag is back in its old state, the alert is raised, «Срочное обновление» is on.
func TestCanaryStandPG(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	bad := false
	runs := 0
	var raised []string
	r := &canaryRunner{d: canaryLive.d}
	r.d.now = func() time.Time { return now }
	r.d.latest = func(context.Context) (canaryRun, error) { return canaryRun{At: now, State: "pass"}, nil }
	r.d.run = func(context.Context, string) (int64, string, error) {
		runs++
		if bad {
			return int64(runs), "fail", nil
		}
		return int64(runs), "pass", nil
	}
	r.d.failingOf = func(context.Context, int64, string) ([]string, error) {
		if bad {
			return []string{"⚡ Авто//blocked"}, nil
		}
		return nil, nil
	}
	r.d.raise = func(key string, _ AlertLevel, _, _ string) { raised = append(raised, key) }
	r.d.resolve = func(string, string) {}
	r.d.record = func(biz.OpsEvent) {}
	r.d.autoOn = func(context.Context) bool { return true }

	if _, err := r.startTest(ctx, "sub_json_dns", "on", "owner"); err != nil {
		t.Fatal(err)
	}
	f, _ := flagByKey("sub_json_dns")
	if st, _ := flagState(ctx, f); st != "test" {
		t.Fatalf("after «на тест»: %q", st)
	}
	for i := 0; i < 31; i++ {
		now = now.Add(time.Minute)
		r.tick(ctx)
	}
	if _, err := r.promote(ctx, "sub_json_dns", "owner"); err != nil {
		t.Fatalf("promote after a clean soak: %v", err)
	}
	if st, _ := flagState(ctx, f); st != "on" {
		t.Fatalf("after «всем»: %q", st)
	}
	bad = true
	for i := 0; i < 11; i++ {
		now = now.Add(time.Minute)
		r.tick(ctx)
	}
	if st, _ := flagState(ctx, f); st != "off" {
		t.Errorf("after two bad runs in the guard hour: %q, want off (the state before the change)", st)
	}
	if !slices.Contains(raised, "flag-rollback:sub_json_dns") {
		t.Errorf("alerts %v", raised)
	}
	if u, _ := biz.GetSettingContext(ctx, subUrgentKey); u == "" {
		t.Error("«Срочное обновление» is not on")
	}
	if open, _ := (sqlCanaryStore{}).open(ctx); open != nil {
		t.Errorf("the change is still open: %+v", open)
	}
}
