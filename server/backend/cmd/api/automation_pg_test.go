package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// The drill «вход теряет потоки» (plan C10): in dry-run A1 writes «сделал бы» and leaves the
// rotation; in «on» it takes the entry out and counts the case.
func TestAutomationDryRun(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node) VALUES
		(31, 'r31', '198.51.100.31', 'relay', 'online', 'online', FALSE)`)
	testpg.MustExec(t, conn, `INSERT INTO entry_state (entry_id, rotation, ext_status) VALUES (31, 'all', 'healthy')`)
	ctx := context.Background()
	store := sqlAutoStore{}
	for _, id := range []string{"A12", "A14", "A16"} {
		if err := store.setMode(ctx, id, autoOff); err != nil {
			t.Fatal(err)
		}
	}
	var feed []biz.OpsEvent
	urgent := 0
	deps := autoDeps{
		store: store, now: time.Now,
		record: func(e biz.OpsEvent) { feed = append(feed, e) },
		raise:  func(string, AlertLevel, string, string) { t.Fatal("no alert: another healthy path exists") },
		a1Inputs: func(context.Context) ([]a1Input, error) {
			return []a1Input{{EntryID: 31, DownRuns: 3, Paths: []pathHealth{{ID: 32, Known: true, Healthy: true}}}}, nil
		},
		rotateOff: func(ctx context.Context, id int, reason string) error {
			return biz.SetEntryRotation(ctx, id, []string{biz.EntryRotationAll}, biz.EntryRotationOff, reason, "automation")
		},
		bump:   func(context.Context) error { return nil },
		urgent: func(context.Context, int, string) error { urgent++; return nil },
	}
	rotation := func() string {
		var r string
		if err := conn.QueryRow(`SELECT rotation FROM entry_state WHERE entry_id = 31`).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}

	newAutoEngine(deps).tick(ctx)
	if rotation() != "all" || urgent != 0 {
		t.Fatalf("dry-run changes nothing: rotation %s, urgent %d", rotation(), urgent)
	}
	if len(feed) != 1 || !strings.Contains(feed[0].Summary, "сделал бы") || feed[0].Kind != "auto" {
		t.Fatalf("dry-run writes «сделал бы»: %+v", feed)
	}

	if err := store.setMode(ctx, "A1", autoOn); err != nil {
		t.Fatal(err)
	}
	newAutoEngine(deps).tick(ctx)
	if rotation() != "off" || urgent != 1 {
		t.Fatalf("on: the entry leaves the rotation, urgent update on: rotation %s, urgent %d", rotation(), urgent)
	}
	states, _ := store.load(ctx)
	if s := states["A1"]; s.Fired != 2 || s.Mode != autoOn || s.LastFiredAt == nil || strings.Contains(s.LastSummary, "сделал бы") {
		t.Fatalf("the counter and the last case: %+v", s)
	}
}
