package biz

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRelayEntriesQuery(t *testing.T) {
	q := normSQL(relayEntriesQuery)
	for _, must := range []string{"FROM SERVERS S LEFT JOIN ENTRY_STATE E ON E.ENTRY_ID = S.ID",
		"S.ROLE = 'RELAY'", "S.LIFECYCLE_STATE = 'ONLINE'", "ORDER BY S.ID"} {
		if !strings.Contains(q, must) {
			t.Errorf("relayEntriesQuery lost %q:\n%s", must, relayEntriesQuery)
		}
	}
}

func TestParseEntryFailover(t *testing.T) {
	at := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		active, target, since string
		want                  EntryFailover
	}{
		{"true", "5", "2026-10-12T10:00:00Z", EntryFailover{Active: true, Target: 5, Since: at}},
		{"true", "5", "", EntryFailover{Active: true, Target: 5}},
		{"true", "0", "2026-10-12T10:00:00Z", EntryFailover{}}, // nowhere to point: inactive
		{"true", "x", "", EntryFailover{}},
		{"false", "5", "2026-10-12T10:00:00Z", EntryFailover{}},
		{"", "", "", EntryFailover{}},
	}
	for _, c := range cases {
		got := ParseEntryFailover(c.active, c.target, c.since)
		if got.Active != c.want.Active || got.Target != c.want.Target || !got.Since.Equal(c.want.Since) {
			t.Errorf("ParseEntryFailover(%q,%q,%q) = %+v, want %+v", c.active, c.target, c.since, got, c.want)
		}
	}
}

func TestEntryAddressAndGuards(t *testing.T) {
	if got := EntryAddress("203.0.113.5", EntryAddressDomain, "r.example.test"); got != "r.example.test" {
		t.Errorf("domain mode: %q", got)
	}
	for _, mode := range []string{EntryAddressIP, EntryAddressDomain} {
		if got := EntryAddress("203.0.113.5", mode, map[string]string{"ip": "x.example.test"}[mode]); got != "203.0.113.5" {
			t.Errorf("%s mode without a domain must use the IP: %q", mode, got)
		}
	}
	if EntryDomainModeAvailable() || !errors.Is(EntryDomainCheck(context.Background(), 0, "r.example.test"), ErrEntryDomainUnavailable) {
		t.Error("domain mode must stay closed until phase 4")
	}
	// Validation happens before any DB access (db.DB is nil here).
	if err := SaveEntryCheck(context.Background(), 1, "bogus", EntryCheck{}); err == nil {
		t.Error("unknown check kind accepted")
	}
	if err := SetEntryFailover(context.Background(), EntryFailover{Active: true}); err == nil {
		t.Error("active failover without a target accepted")
	}
	if err := SetEntryAddressMode(context.Background(), 0, "dns", ""); err == nil {
		t.Error("unknown address mode accepted")
	}
}

// A status the entry_state CHECKs would refuse is rejected before the write (db.DB is nil here),
// and a detail is always storable: valid UTF-8, no NUL, at most entryDetailMax bytes, no split rune.
func TestEntryCheckGuards(t *testing.T) {
	for _, c := range []struct{ kind, status string }{
		{"ru", EntryHealthy}, {"ext", "reachable"}, {"udp", "unreachable"}, {"ext", ""},
	} {
		if err := SaveEntryCheck(context.Background(), 1, c.kind, EntryCheck{Status: c.status}); err == nil {
			t.Errorf("%s check with status %q accepted", c.kind, c.status)
		}
	}
	long := strings.Repeat("a", 499) + strings.Repeat("ж", 10) // byte 500 is inside a rune
	for _, c := range []struct{ in, want string }{
		{"ok", "ok"},
		{strings.Repeat("x", 600), strings.Repeat("x", entryDetailMax)},
		{long, strings.Repeat("a", 499)},
		{"bad \xff\x00byte", "bad �byte"},
	} {
		got := entryDetail(c.in)
		if got != c.want || !utf8.ValidString(got) || len(got) > entryDetailMax {
			t.Errorf("entryDetail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

var migrationKV = regexp.MustCompile(`\('([a-z0-9_]+)',\s*'([^']*)'\)`)

func TestMigration000062(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("../../migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	up, down := read("000062_entries_failover.up.sql"), read("000062_entries_failover.down.sql")
	want := map[string]string{"entry_failover_enabled": "true", "entry_failover_active": "false",
		"entry_failover_target": "0", "entry_failover_since": "", "entry_failover_test_user": "",
		"entry_probe_simulate_hub_fail_until": "", "subscription_update_interval_hours": "4"}
	got := map[string]string{}
	for _, m := range migrationKV.FindAllStringSubmatch(up, -1) {
		got[m[1]] = m[2]
	}
	for k, v := range want {
		if gv, ok := got[k]; !ok || gv != v {
			t.Errorf("up: %s = %q (present %v), want %q", k, gv, ok, v)
		}
		if !strings.Contains(down, "'"+k+"'") {
			t.Errorf("down does not delete %s", k)
		}
	}
	for _, must := range []string{"CREATE TABLE IF NOT EXISTS entry_state", "ON CONFLICT (key) DO NOTHING",
		"INSERT INTO entry_state (entry_id) VALUES (0) ON CONFLICT (entry_id) DO NOTHING",
		"DROP CONSTRAINT IF EXISTS servers_relay_not_exit", "CHECK (role <> 'relay' OR is_exit_node IS NOT TRUE)"} {
		if !strings.Contains(up, must) {
			t.Errorf("up lacks %q", must)
		}
	}
	for _, must := range []string{"DROP TABLE IF EXISTS entry_state", "DROP CONSTRAINT IF EXISTS servers_relay_not_exit"} {
		if !strings.Contains(down, must) {
			t.Errorf("down lacks %q", must)
		}
	}
}
