package main

import (
	"context"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/testpg"
)

// A15: the first pass seeds the hashes; an SQL edit of a watched setting is recorded in the
// feed («мимо пульта», actor unknown, no value) and raises an alert; an edit with an audit
// record is not.
func TestSettingsWatchPG(t *testing.T) {
	testpg.Open(t)
	ctx := context.Background()
	var feed []biz.OpsEvent
	var raised []string
	prevRecord, prevRaise := recordOps, settingsWatchRaise
	t.Cleanup(func() { recordOps, settingsWatchRaise = prevRecord, prevRaise })
	recordOps = func(e biz.OpsEvent) { feed = append(feed, e) }
	settingsWatchRaise = func(key, title, details string) { raised = append(raised, key) }

	set := func(k, v string) {
		if _, err := db.DB.Exec(`INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	set("sub_json_dns", "test")
	set("bamboo_optimise", "night")
	if err := checkSettingsOutside(ctx); err != nil {
		t.Fatal(err)
	}
	if len(feed)+len(raised) != 0 {
		t.Fatalf("the seeding pass reported: feed %v, alerts %v", feed, raised)
	}
	var n int
	if err := db.DB.QueryRow(`SELECT count(*) FROM watched_settings`).Scan(&n); err != nil || n != len(watchedSettingKeys) {
		t.Fatalf("watched_settings rows %d (%v), want %d", n, err, len(watchedSettingKeys))
	}
	var stored string
	_ = db.DB.QueryRow(`SELECT value_hash FROM watched_settings WHERE key = 'sub_json_dns'`).Scan(&stored)
	if stored == "test" || len(stored) != 64 {
		t.Fatalf("the value itself is stored, or not a SHA-256: %q", stored)
	}

	// an SQL edit, no audit record
	set("sub_json_dns", "on")
	// an edit through the console: an audit record names the key
	set("bamboo_optimise", "off")
	if _, err := db.DB.Exec(`INSERT INTO admin_audit_log (admin_username, action, target_type, target_id, details) VALUES ('owner', 'update_setting', 'setting', 'bamboo_optimise', '{"value":"off"}')`); err != nil {
		t.Fatal(err)
	}
	if err := checkSettingsOutside(ctx); err != nil {
		t.Fatal(err)
	}
	if len(feed) != 1 || feed[0].Actor != "unknown" || feed[0].Summary != "настройка sub_json_dns изменена мимо пульта" {
		t.Fatalf("feed %+v", feed)
	}
	if strings.Contains(feed[0].Summary, "on") && strings.Contains(feed[0].Summary, "= on") {
		t.Errorf("the value is in the feed: %q", feed[0].Summary)
	}
	if len(raised) != 1 || raised[0] != "setting-changed-outside:sub_json_dns" {
		t.Fatalf("alerts %v", raised)
	}
	// nothing changed since: silence
	if err := checkSettingsOutside(ctx); err != nil || len(feed) != 1 || len(raised) != 1 {
		t.Fatalf("a quiet pass reported: %v feed %d alerts %d", err, len(feed), len(raised))
	}
}
