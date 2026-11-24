package main

import (
	"context"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/testpg"
)

func TestRecentIncidentsFromTheJournal(t *testing.T) {
	conn := testpg.Open(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	for _, e := range []struct {
		key, level, kind string
		at               time.Time
	}{
		{"relay-6-unstable", "critical", "raise", at(5 * time.Hour)},
		{"relay-6-unstable", "critical", "resolve", at(4*time.Hour + 30*time.Minute)},
		{"synth-way:x", "critical", "raise", at(4*time.Hour + 25*time.Minute)},
		{"synth-way:x", "critical", "resolve", at(4 * time.Hour)},
		{"backup-stale", "warn", "raise", at(2 * time.Hour)},
		{"backup-stale", "warn", "resolve", at(2*time.Hour - 5*time.Minute)},
	} {
		testpg.MustExec(t, conn, `INSERT INTO alert_events (alert_key, level, kind, title, created_at) VALUES ($1, $2, $3, $1, $4)`, e.key, e.level, e.kind, e.at)
	}
	ctx := context.Background()
	list, err := recentIncidents(ctx, now)
	if err != nil || len(list) != 1 || len(list[0].Keys) != 2 || !list[0].From.Equal(at(5*time.Hour)) {
		t.Fatalf("one incident of two alerts (the short warning is none): %+v %v", list, err)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO incidents (started_at, notes) VALUES (to_timestamp($1), 'провайдер')`, list[0].ID); err != nil {
		t.Fatal(err)
	}
	list, _ = recentIncidents(ctx, now)
	if list[0].Notes != "провайдер" {
		t.Fatalf("notes are kept by the start: %+v", list[0])
	}
}
