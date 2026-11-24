package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// Review Focus 2: an API restart in the middle of an action never leaves it «выполняется»
// forever; a planned run is not touched.
func TestActionRunRecoveredAfterRestart(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	s := sqlOpsRunStore{}
	now := time.Now().UTC().Truncate(time.Second)
	long := now.Add(-40 * time.Minute)
	later := now.Add(6 * time.Hour)
	dead, err := s.insert(ctx, opsRun{Action: "backup.run", StartedBy: "owner", Impact: biz.ImpactK0, Status: opsRunning, StartedAt: &long,
		Pre: []opsCheck{{Name: "есть место", OK: true}}})
	if err != nil {
		t.Fatal(err)
	}
	planned, err := s.insert(ctx, opsRun{Action: "hub.restart-bamboo", StartedBy: "owner", Impact: biz.ImpactK2, Status: opsScheduled, ScheduledFor: &later})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.progress(ctx, dead, "шаг 1"); err != nil {
		t.Fatal(err)
	}
	var feed []biz.OpsEvent
	o := &opsRunner{actions: map[string]opsAction{
		"backup.run": {ID: "backup.run", Title: "Бэкап", Impact: biz.ImpactK0, Perm: "settings.write"},
	}, d: opsDeps{store: s, record: func(e biz.OpsEvent) { feed = append(feed, e) }, now: func() time.Time { return now },
		raise: func(string, AlertLevel, string, string) {}, audit: func(*http.Request, string, string, string, any) {},
		goRun: func(f func()) { f() }}}
	o.recover(ctx)

	r, err := s.get(ctx, dead)
	if err != nil || r.Status != opsInterrupted || r.FinishedAt == nil || r.Error == "" {
		t.Fatalf("a running row of a dead process → interrupted: %+v %v", r, err)
	}
	if len(r.Pre) != 1 || len(r.Progress) != 1 || r.Progress[0] != "шаг 1" {
		t.Fatalf("pre and progress survive the round trip: %+v", r)
	}
	if p, _ := s.get(ctx, planned); p.Status != opsScheduled || p.ScheduledFor == nil || !p.ScheduledFor.Equal(later) {
		t.Fatalf("a planned run is not touched: %+v", p)
	}
	if len(feed) != 1 || feed[0].OK {
		t.Fatalf("one feed line about the interrupted run: %+v", feed)
	}
	if open, _ := s.open(ctx); len(open) != 0 {
		t.Fatalf("nothing is left running: %+v", open)
	}
	// the planned run is due only from its time on
	if due, _ := s.due(ctx, now); len(due) != 0 {
		t.Fatalf("not due yet: %+v", due)
	}
	if due, _ := s.due(ctx, later.Add(time.Minute)); len(due) != 1 || due[0].ID != planned {
		t.Fatalf("due at its time: %+v", due)
	}
	// rate limit counts real starts only
	if last, _ := s.lastStart(ctx, "backup.run", ""); !last.Equal(long) {
		t.Fatalf("last real start: %v", last)
	}
	dry := now
	if _, err := s.insert(ctx, opsRun{Action: "backup.run", Impact: biz.ImpactK0, Status: opsOK, StartedAt: &dry, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if last, _ := s.lastStart(ctx, "backup.run", ""); !last.Equal(long) {
		t.Fatalf("a dry run is not a start: %v", last)
	}
	// modes
	if m, err := s.mode(ctx, "hub.restart-xray"); err != nil || m != "" {
		t.Fatalf("never set: %q %v", m, err)
	}
	if err := s.setMode(ctx, "hub.restart-xray", opsModeOn, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.setMode(ctx, "hub.restart-xray", opsModeDryRun, "owner"); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.mode(ctx, "hub.restart-xray"); m != opsModeDryRun {
		t.Fatalf("the last mode wins: %q", m)
	}
	if err := s.setMode(ctx, "hub.restart-xray", "maybe", "owner"); err == nil {
		t.Fatal("the table refuses an unknown mode")
	}
}

func TestExitServiceStore(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	if out, err := listExitsOutOfService(ctx); err != nil || len(out) != 0 {
		t.Fatalf("fresh: %v %v", out, err)
	}
	if err := setExitOutOfService(ctx, 3, true, "вне работы: owner", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := setExitOutOfService(ctx, 4, true, "вне работы: owner", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := setExitOutOfService(ctx, 4, false, "в работе: owner", "owner"); err != nil {
		t.Fatal(err)
	}
	out, err := listExitsOutOfService(ctx)
	if err != nil || len(out) != 1 || !out[3] {
		t.Fatalf("only exit 3 is out: %v %v", out, err)
	}
}

func TestOpsRunOpenFor(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	s := sqlOpsRunStore{}
	at := time.Now().Add(time.Hour)
	if open, err := s.openFor(ctx, "hub.restart-xray", ""); err != nil || open {
		t.Fatalf("fresh: %v %v", open, err)
	}
	id, _ := s.insert(ctx, opsRun{Action: "hub.restart-xray", Impact: biz.ImpactK3, Status: opsScheduled, ScheduledFor: &at})
	if open, _ := s.openFor(ctx, "hub.restart-xray", ""); !open {
		t.Fatal("a scheduled run is open")
	}
	if open, _ := s.openFor(ctx, "hub.restart-xray", "3"); open {
		t.Fatal("another target is another run")
	}
	r, _ := s.get(ctx, id)
	r.Status = opsCancelled
	_ = s.save(ctx, r)
	if open, _ := s.openFor(ctx, "hub.restart-xray", ""); open {
		t.Fatal("a cancelled run is not open")
	}
}

// The restart prediction runs the new build against the old schema (before its migrations): the
// generator must render without the exit_service table — then no exit is out of service.
func TestExitsOutOfServiceBeforeTheMigration(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `DROP TABLE exit_service`)
	out, err := listExitsOutOfService(context.Background())
	if err != nil || len(out) != 0 {
		t.Fatalf("no table → nothing out of service, no error: %v %v", out, err)
	}
}
