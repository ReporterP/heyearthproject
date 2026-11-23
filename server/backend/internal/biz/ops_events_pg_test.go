package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

func TestListOpsFeedMergesSources(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// ops: recorded through RecordOpsEvent (scrubbed), then moved to a known time
	if err := RecordOpsEvent(ctx, OpsEvent{Kind: "restart", Actor: "system", Target: "xray", Impact: "K3", OK: true,
		Summary: "xray перезапущен: revoked(quota) 203.0.113.7",
		Details: map[string]any{"cause": "revoked(quota)", "note": "user 11111111-2222-3333-4444-555555555555", "n": 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`UPDATE ops_events SET at = $1`, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// audit: a setting change, and a sign-in that must not show
	if _, err := conn.Exec(`INSERT INTO admin_audit_log (admin_username, action, target_type, target_id, created_at) VALUES
		('owner', 'update_setting', 'setting', 'sub_json_dns', $1),
		('owner', 'admin.login', NULL, NULL, $2)`, now.Add(-5*time.Minute), now.Add(-4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// alerts: a raise shows, a repeat and a test message do not
	if _, err := conn.Exec(`INSERT INTO alert_events (alert_key, level, kind, title, details, created_at) VALUES
		('exit-down-3', 'critical', 'raise', 'Выход 3 недоступен', 'd', $1),
		('exit-down-3', 'critical', 'repeat', 'Выход 3 недоступен', 'd', $2),
		('test', 'warn', 'test', 'Тестовое сообщение', '', $2)`, now.Add(-20*time.Minute), now.Add(-2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// older than the window
	if _, err := conn.Exec(`INSERT INTO ops_events (at, kind, actor, summary) VALUES ($1, 'note', 'system', 'old')`, now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}

	feed, err := ListOpsFeed(ctx, now.Add(-24*time.Hour), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(feed) != 3 {
		t.Fatalf("feed has %d items, want 3 (audit, ops, alert): %+v", len(feed), feed)
	}
	audit, ops, alert := feed[0], feed[1], feed[2] // newest first
	if audit.Source != "audit" || audit.Actor != "owner" || audit.Kind != "update_setting" || audit.Target != "setting:sub_json_dns" || !audit.OK {
		t.Errorf("audit item = %+v", audit)
	}
	if ops.Source != "ops" || ops.Kind != "restart" || ops.Target != "xray" || ops.Impact != "K3" || !ops.OK {
		t.Errorf("ops item = %+v", ops)
	}
	if strings.Contains(ops.Summary, "203.0.113.7") || !strings.Contains(ops.Summary, "revoked(quota)") {
		t.Errorf("ops summary = %q, want it scrubbed and otherwise intact", ops.Summary)
	}
	if alert.Source != "alert" || alert.Kind != "raise" || alert.Target != "exit-down-3" || alert.Summary != "Выход 3 недоступен" || alert.OK {
		t.Errorf("alert item = %+v", alert)
	}
	if !audit.At.After(ops.At) || !ops.At.After(alert.At) {
		t.Errorf("order: %s, %s, %s — want newest first", audit.At, ops.At, alert.At)
	}

	// string details are scrubbed on the way in, other values kept
	var note string
	var n int
	if err := conn.QueryRow(`SELECT details->>'note', (details->>'n')::int FROM ops_events WHERE kind = 'restart'`).Scan(&note, &n); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "2222-3333") || n != 2 {
		t.Errorf("details: note=%q n=%d", note, n)
	}

	if limited, err := ListOpsFeed(ctx, now.Add(-24*time.Hour), 1); err != nil || len(limited) != 1 || limited[0].Source != "audit" {
		t.Errorf("limit 1: %+v, %v", limited, err)
	}

	// LastOpsEventDetail: the previous build of a «start» event
	if err := RecordOpsEvent(ctx, OpsEvent{Kind: "start", Actor: "deploy", Summary: "запущена сборка aaa111", Impact: "K1", OK: true, Details: map[string]any{"sha": "aaa111"}}); err != nil {
		t.Fatal(err)
	}
	if sha, err := LastOpsEventDetail(ctx, "start", "sha"); err != nil || sha != "aaa111" {
		t.Errorf("LastOpsEventDetail = %q, %v", sha, err)
	}
	if sha, err := LastOpsEventDetail(ctx, "boot", "sha"); err != nil || sha != "" {
		t.Errorf("no such event: %q, %v", sha, err)
	}

	// retention
	if n, err := PurgeOpsEvents(ctx, 24*time.Hour); err != nil || n != 1 {
		t.Errorf("PurgeOpsEvents = %d, %v; want the one 48-hour-old row", n, err)
	}
}
