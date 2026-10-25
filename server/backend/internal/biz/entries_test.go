package biz

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/heyearth/backend/internal/db"
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
		if err := SaveEntryCheck(context.Background(), 1, c.kind, EntryCheck{Status: c.status, At: time.Now()}); err == nil {
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

// entriesFake is a database/sql driver for the entries reads and writes (the pattern of
// bamboo_status_test.go): answer routes each query by its SQL, every statement is logged.
type entriesFake struct {
	mu     sync.Mutex
	log    []entriesCall
	answer func(q string, args []driver.Value) (driver.Rows, error)
}

type entriesCall struct {
	q    string
	args []driver.Value
	exec bool
}

func (f *entriesFake) Connect(context.Context) (driver.Conn, error) { return entriesConn{f}, nil }
func (f *entriesFake) Driver() driver.Driver                        { return entriesDriver{f} }

func (f *entriesFake) calls() []entriesCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]entriesCall(nil), f.log...)
}

type entriesDriver struct{ f *entriesFake }

func (d entriesDriver) Open(string) (driver.Conn, error) { return entriesConn{d.f}, nil }

type entriesConn struct{ f *entriesFake }

func (c entriesConn) Prepare(q string) (driver.Stmt, error) { return entriesStmt{c.f, q}, nil }
func (c entriesConn) Close() error                          { return nil }
func (c entriesConn) Begin() (driver.Tx, error)             { return nil, driver.ErrSkip }

type entriesStmt struct {
	f *entriesFake
	q string
}

func (s entriesStmt) Close() error  { return nil }
func (s entriesStmt) NumInput() int { return -1 }
func (s entriesStmt) record(args []driver.Value, exec bool) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()
	s.f.log = append(s.f.log, entriesCall{q: s.q, args: append([]driver.Value(nil), args...), exec: exec})
}
func (s entriesStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.record(args, true)
	return driver.RowsAffected(1), nil
}
func (s entriesStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.record(args, false)
	s.f.mu.Lock()
	answer := s.f.answer
	s.f.mu.Unlock()
	if answer == nil {
		return nil, fmt.Errorf("unexpected query %q", s.q)
	}
	return answer(s.q, args)
}

// entriesRows: width columns, rows in order (no rows = sql.ErrNoRows for QueryRow).
type entriesRows struct {
	width int
	rows  [][]driver.Value
}

func rowsOf(width int, rows ...[]driver.Value) *entriesRows { return &entriesRows{width, rows} }

func (r *entriesRows) Columns() []string {
	cols := make([]string, r.width)
	for i := range cols {
		cols[i] = fmt.Sprintf("c%d", i)
	}
	return cols
}
func (r *entriesRows) Close() error { return nil }
func (r *entriesRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}

func withEntriesDB(t *testing.T, f *entriesFake) {
	t.Helper()
	old := db.DB
	db.DB = sql.OpenDB(f)
	t.Cleanup(func() {
		db.DB.Close()
		db.DB = old
	})
}

var errEntriesRead = errors.New("connection reset")

// entriesAnswer answers the ListEntries reads: main_server_host, the Hub's entry_state row, the
// relays. fail names the read that errors ("setting", "hub", "relays").
func entriesAnswer(host *string, hub []driver.Value, relays [][]driver.Value, fail string) func(string, []driver.Value) (driver.Rows, error) {
	return func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM app_settings WHERE key = $1"):
			if fail == "setting" {
				return nil, errEntriesRead
			}
			if args[0] == "main_server_host" && host != nil {
				return rowsOf(1, []driver.Value{*host}), nil
			}
			return rowsOf(1), nil
		case strings.Contains(q, "FROM servers s LEFT JOIN entry_state e"):
			if fail == "relays" {
				return nil, errEntriesRead
			}
			return rowsOf(16, relays...), nil
		case strings.Contains(q, "FROM entry_state e WHERE e.entry_id = 0"):
			if fail == "hub" {
				return nil, errEntriesRead
			}
			if hub == nil {
				return rowsOf(11), nil
			}
			return rowsOf(11, hub), nil
		}
		return nil, fmt.Errorf("unexpected query %q", q)
	}
}

func TestListEntriesAssembly(t *testing.T) {
	at := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	host := " 138.16.181.225 "
	f := &entriesFake{answer: entriesAnswer(&host,
		[]driver.Value{"domain", "hub.example.test", "healthy", at, "", "healthy", at, "", "unknown", nil, ""},
		[][]driver.Value{
			{int64(2), "RU-relay-1", "198.51.100.9", "RU", "online", "ip", "", "unhealthy", at, "timeout", "unknown", nil, "", "reachable", at, "https://check-host.net/x"},
			{int64(5), "RU-relay-2", "198.51.100.10", "RU", "online", "domain", "r.example.test", "unknown", nil, "", "unknown", nil, "", "unknown", nil, ""},
		}, "")}
	withEntriesDB(t, f)

	es, err := ListEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 {
		t.Fatalf("want Hub + 2 relays, got %+v", es)
	}
	hub, r1, r2 := es[0], es[1], es[2]
	if hub.ID != 0 || hub.Kind != EntryKindHub || hub.Name != "Основной вход (Hub)" || hub.IP != "138.16.181.225" ||
		hub.AddressMode != EntryAddressDomain || hub.Address != "hub.example.test" || hub.Country != "RU" || hub.Lifecycle != "online" {
		t.Errorf("hub: %+v", hub)
	}
	if hub.Health.External != EntryHealthy || hub.Health.ExternalAt == nil || !hub.Health.ExternalAt.Equal(at) ||
		hub.Health.UDPAt == nil || hub.Health.RU != EntryUnknown || hub.Health.RUAt != nil {
		t.Errorf("hub health: %+v", hub.Health)
	}
	if r1.ID != 2 || r1.Kind != EntryKindRelay || r1.Name != "RU-relay-1" || r1.Address != "198.51.100.9" || r1.Lifecycle != "online" ||
		r1.Health.External != EntryUnhealthy || r1.Health.ExternalError != "timeout" || r1.Health.UDPAt != nil ||
		r1.Health.RU != "reachable" || r1.Health.RUAt == nil || r1.Health.RULink != "https://check-host.net/x" {
		t.Errorf("relay 2: %+v", r1)
	}
	if r2.ID != 5 || r2.Address != "r.example.test" || r2.IP != "198.51.100.10" || r2.Health.ExternalAt != nil {
		t.Errorf("relay 5: %+v", r2)
	}
	raw, err := json.Marshal(hub)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":0`, `"kind":"hub"`, `"address_mode":"domain"`,
		`"health":{"external":"healthy","external_at":"2026-10-26T08:00:00Z"`, `"ru_at":null`, `"ru_link":""`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON lacks %s: %s", want, raw)
		}
	}
}

// No Hub row in entry_state (deleted by hand, a fresh table): the Hub is still listed, by IP,
// unchecked; the relays follow.
func TestListEntriesHubWithoutStateRow(t *testing.T) {
	host := "138.16.181.225"
	f := &entriesFake{answer: entriesAnswer(&host, nil, [][]driver.Value{
		{int64(2), "RU-relay-1", "198.51.100.9", "RU", "online", "ip", "", "unknown", nil, "", "unknown", nil, "", "unknown", nil, ""},
	}, "")}
	withEntriesDB(t, f)
	es, err := ListEntries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := EntryHealth{External: EntryUnknown, UDP: EntryUnknown, RU: EntryUnknown}
	if len(es) != 2 || es[0].AddressMode != EntryAddressIP || es[0].Address != host || es[0].Health != want || es[1].ID != 2 {
		t.Fatalf("got %+v", es)
	}
}

// A failed read is an error, never a partial list (the subscription answers 503 on it).
func TestListEntriesReadErrors(t *testing.T) {
	host := "138.16.181.225"
	hubRow := []driver.Value{"ip", "", "unknown", nil, "", "unknown", nil, "", "unknown", nil, ""}
	for _, fail := range []string{"setting", "hub", "relays"} {
		t.Run(fail, func(t *testing.T) {
			withEntriesDB(t, &entriesFake{answer: entriesAnswer(&host, hubRow, nil, fail)})
			es, err := ListEntries(context.Background())
			if !errors.Is(err, errEntriesRead) || es != nil {
				t.Fatalf("ListEntries = %+v, %v; want nil, %v", es, err, errEntriesRead)
			}
		})
	}
}

func TestSetEntryFailoverArgs(t *testing.T) {
	msk := time.FixedZone("MSK", 3*3600)
	cases := []struct {
		name string
		st   EntryFailover
		want []driver.Value
	}{
		{"active", EntryFailover{Active: true, Target: 5, Since: time.Date(2026, 10, 12, 13, 0, 0, 0, msk)},
			[]driver.Value{"entry_failover_active", "true", "entry_failover_target", "5", "entry_failover_since", "2026-10-12T10:00:00Z"}},
		{"active, no since", EntryFailover{Active: true, Target: 5},
			[]driver.Value{"entry_failover_active", "true", "entry_failover_target", "5", "entry_failover_since", ""}},
		{"off (target and since ignored)", EntryFailover{Target: 7, Since: time.Now()},
			[]driver.Value{"entry_failover_active", "false", "entry_failover_target", "0", "entry_failover_since", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &entriesFake{}
			withEntriesDB(t, f)
			if err := SetEntryFailover(context.Background(), c.st); err != nil {
				t.Fatal(err)
			}
			calls := f.calls()
			if len(calls) != 1 || !calls[0].exec || !strings.Contains(normSQL(calls[0].q), "ON CONFLICT (KEY) DO UPDATE SET VALUE = EXCLUDED.VALUE") {
				t.Fatalf("want one upsert of the three keys, got %+v", calls)
			}
			if !reflect.DeepEqual(calls[0].args, c.want) {
				t.Errorf("args = %q, want %q", calls[0].args, c.want)
			}
		})
	}
}

// The three keys come from one statement; per-key reads answer the same values, so only a
// single-query read passes.
func TestGetEntryFailoverOneQuery(t *testing.T) {
	settings := map[string]string{"entry_failover_active": "true", "entry_failover_target": " 5 ", "entry_failover_since": "2026-10-12T10:00:00Z"}
	answer := func(fail bool) func(string, []driver.Value) (driver.Rows, error) {
		return func(q string, args []driver.Value) (driver.Rows, error) {
			if fail {
				return nil, errEntriesRead
			}
			var rows [][]driver.Value
			switch {
			case strings.Contains(q, "WHERE key IN"):
				for _, a := range args {
					if v, ok := settings[a.(string)]; ok {
						rows = append(rows, []driver.Value{a, v})
					}
				}
				return rowsOf(2, rows...), nil
			case strings.Contains(q, "WHERE key = $1"):
				if v, ok := settings[args[0].(string)]; ok {
					return rowsOf(1, []driver.Value{v}), nil
				}
				return rowsOf(1), nil
			}
			return nil, fmt.Errorf("unexpected query %q", q)
		}
	}
	f := &entriesFake{answer: answer(false)}
	withEntriesDB(t, f)
	st, err := GetEntryFailover(context.Background())
	if err != nil || !st.Active || st.Target != 5 || !st.Since.Equal(time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("GetEntryFailover = %+v, %v", st, err)
	}
	calls := f.calls()
	wantArgs := []driver.Value{"entry_failover_active", "entry_failover_target", "entry_failover_since"}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].args, wantArgs) {
		t.Fatalf("want one read of the three keys, got %+v", calls)
	}
	if ok, err := EntryIsFailoverTarget(context.Background(), 5); !ok || err != nil {
		t.Errorf("target 5: %v, %v", ok, err)
	}
	if ok, err := EntryIsFailoverTarget(context.Background(), 2); ok || err != nil {
		t.Errorf("target 2: %v, %v", ok, err)
	}

	f.mu.Lock()
	f.answer = answer(true)
	f.mu.Unlock()
	if _, err := GetEntryFailover(context.Background()); !errors.Is(err, errEntriesRead) {
		t.Errorf("read error swallowed: %v", err)
	}
	if ok, err := EntryIsFailoverTarget(context.Background(), 5); ok || err == nil {
		t.Errorf("failed read: %v, %v", ok, err)
	}
}

// Each check kind writes only its own columns, and only when it is not older than the stored check.
func TestEntryCheckQueriesKeepNewest(t *testing.T) {
	for kind, q := range entryCheckQueries {
		n, col := normSQL(q), strings.ToUpper(kind)
		guard := fmt.Sprintf("WHERE ENTRY_STATE.%[1]s_CHECKED_AT IS NULL OR ENTRY_STATE.%[1]s_CHECKED_AT <= EXCLUDED.%[1]s_CHECKED_AT", col)
		if !strings.HasSuffix(n, guard) {
			t.Errorf("%s: lacks the newest-wins guard %q:\n%s", kind, guard, q)
		}
		if !strings.Contains(n, fmt.Sprintf("%[1]s_STATUS = EXCLUDED.%[1]s_STATUS, %[1]s_CHECKED_AT = EXCLUDED.%[1]s_CHECKED_AT", col)) {
			t.Errorf("%s: does not update its own status and time:\n%s", kind, q)
		}
		for other := range entryCheckQueries {
			if other != kind && strings.Contains(n, strings.ToUpper(other)+"_") {
				t.Errorf("%s: touches %s columns:\n%s", kind, other, q)
			}
		}
	}
}

func TestSaveEntryCheckArgs(t *testing.T) {
	at := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	f := &entriesFake{}
	withEntriesDB(t, f)
	if err := SaveEntryCheck(context.Background(), 2, "udp", EntryCheck{Status: EntryHealthy}); err == nil {
		t.Error("check without a time accepted")
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("a refused check reached the database: %+v", calls)
	}
	if err := SaveEntryCheck(context.Background(), 2, "udp", EntryCheck{Status: EntryUnhealthy, At: at, Detail: "нет ответа"}); err != nil {
		t.Fatal(err)
	}
	calls := f.calls()
	want := []driver.Value{int64(2), EntryUnhealthy, at, "нет ответа"}
	if len(calls) != 1 || !calls[0].exec || calls[0].q != entryCheckQueries["udp"] || !reflect.DeepEqual(calls[0].args, want) {
		t.Fatalf("want the udp upsert with %v, got %+v", want, calls)
	}
}
