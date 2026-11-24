package main

import (
	"context"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/testpg"
)

func TestSilencesRefreshAndExpiry(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	var feed []biz.OpsEvent
	old := recordOps
	recordOps = func(e biz.OpsEvent) { feed = append(feed, e) }
	t.Cleanup(func() { recordOps = old; silenceLive.set(nil) })
	if _, err := db.DB.ExecContext(ctx, `
		INSERT INTO alert_silences (pattern, reason, created_by, until) VALUES
		  ('relay-5-*', 'перезагрузка', 'owner', now() + interval '1 hour'),
		  ('backup-stale', 'старое', 'owner', now() - interval '1 minute')`); err != nil {
		t.Fatal(err)
	}
	refreshSilences(ctx)
	if _, ok := silenceLive.lookup("relay-5-unreachable", time.Now()); !ok {
		t.Fatal("the active silence is in the cache")
	}
	if _, ok := silenceLive.lookup("backup-stale", time.Now()); ok {
		t.Fatal("an expired silence is not")
	}
	if len(feed) != 1 || feed[0].Kind != "maintenance" {
		t.Fatalf("the end of a silence is one feed line: %+v", feed)
	}
	refreshSilences(ctx)
	if len(feed) != 1 {
		t.Fatalf("only once: %+v", feed)
	}
	if _, err := db.DB.ExecContext(ctx, `INSERT INTO alert_events (alert_key, level, kind, title) VALUES ('relay-5-unreachable', 'critical', 'silenced', 'Вход 5')`); err != nil {
		t.Fatalf("the journal takes «silenced»: %v", err)
	}
}
