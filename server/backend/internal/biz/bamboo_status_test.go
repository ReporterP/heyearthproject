package biz

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
)

func TestBambooProbeRecord(t *testing.T) {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	RecordBambooProbe(BambooProbe{OK: true, At: at, Detail: "UDP :41234 ответил за 135ms"})
	p := LastBambooProbe()
	if p == nil || !p.OK || !p.At.Equal(at) {
		t.Fatalf("last probe = %+v", p)
	}
	p.OK = false // the caller gets a copy
	if !LastBambooProbe().OK {
		t.Fatal("LastBambooProbe must return a copy")
	}
}

// The card shows the exit server.yaml carries: the selector's committed choice, never a proposal
// of ExpressExit that was not committed; before the selector exists, the persisted app_settings.
func TestBambooBoundExit(t *testing.T) {
	// 03:30 MSK: the proposal an hour later falls into the quiet window, the only time Bamboo
	// moves to a faster exit.
	since := time.Date(2026, 10, 13, 3, 30, 0, 0, time.FixedZone("MSK", 3*3600))
	settings := map[string]string{"hysteria2_bound_exit_id": "2", "hysteria2_bound_exit_since": "2026-10-12T10:00:00Z"}
	setting := func(k, def string) string {
		if v, ok := settings[k]; ok {
			return v
		}
		return def
	}

	sel := NewExpressSelector(3, since)
	for i := 0; i < expressMinSamples; i++ {
		sel.Observe(3, 80)
		sel.Observe(5, 20)
	}
	proposal := sel.Decide(since.Add(time.Hour), []ExpressCandidate{{ID: 3}, {ID: 5}}) // exit 5 is 4× faster
	if !proposal.Switched || proposal.ExitID != 5 {
		t.Fatalf("setup: want a proposed switch to exit 5, got %+v", proposal)
	}
	if b := bambooBoundExit(sel.Current(), true, setting); b != (bambooBound{ID: 3, Since: "2026-10-13T00:30:00Z", RTTms: 80}) {
		t.Fatalf("uncommitted proposal leaked into the status: %+v", b)
	}
	sel.Commit(proposal)
	if b := bambooBoundExit(sel.Current(), true, setting); b != (bambooBound{ID: 5, Since: "2026-10-13T01:30:00Z", RTTms: 20}) {
		t.Fatalf("committed switch: %+v", b)
	}

	if b := bambooBoundExit(ExpressChoice{}, false, setting); b != (bambooBound{ID: 2, Since: "2026-10-12T10:00:00Z"}) {
		t.Fatalf("selector not initialised: want the persisted exit, got %+v", b)
	}
	settings = map[string]string{"hysteria2_bound_exit_id": "x"}
	if b := bambooBoundExit(ExpressChoice{}, false, setting); b != (bambooBound{}) {
		t.Fatalf("nothing chosen: %+v", b)
	}
}

// statusDB answers the reads of GetHysteria2Status: app_settings by key, the Express plan count
// and server names by id.
type statusDB struct {
	settings map[string]string
	names    map[int64]string
}

func (d *statusDB) Connect(context.Context) (driver.Conn, error) { return statusConn{d}, nil }
func (d *statusDB) Driver() driver.Driver                        { return statusDriver{d} }

type statusDriver struct{ d *statusDB }

func (x statusDriver) Open(string) (driver.Conn, error) { return statusConn{x.d}, nil }

type statusConn struct{ d *statusDB }

func (c statusConn) Prepare(q string) (driver.Stmt, error) { return statusStmt{c.d, q}, nil }
func (c statusConn) Close() error                          { return nil }
func (c statusConn) Begin() (driver.Tx, error)             { return nil, driver.ErrSkip }

type statusStmt struct {
	d *statusDB
	q string
}

func (s statusStmt) Close() error                               { return nil }
func (s statusStmt) NumInput() int                              { return -1 }
func (s statusStmt) Exec([]driver.Value) (driver.Result, error) { return driver.RowsAffected(0), nil }
func (s statusStmt) Query(args []driver.Value) (driver.Rows, error) {
	switch {
	case strings.Contains(s.q, "FROM app_settings"):
		if v, ok := s.d.settings[args[0].(string)]; ok {
			return &oneRow{v: v}, nil
		}
		return &oneRow{done: true}, nil
	case strings.Contains(s.q, "FROM plans"):
		return &oneRow{v: int64(1)}, nil
	case strings.Contains(s.q, "FROM servers"):
		if n, ok := s.d.names[args[0].(int64)]; ok {
			return &oneRow{v: n}, nil
		}
		return &oneRow{done: true}, nil
	}
	return nil, io.ErrUnexpectedEOF
}

type oneRow struct {
	v    driver.Value
	done bool
}

func (r *oneRow) Columns() []string { return []string{"v"} }
func (r *oneRow) Close() error      { return nil }
func (r *oneRow) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done, dest[0] = true, r.v
	return nil
}

// Review Focus 5 → /hysteria2/status (the Bamboo card) carries status facts, never the obfs
// password or the trafficStats secret; the exit is the committed one, named.
func TestHysteria2StatusBambooCard(t *testing.T) {
	old := db.DB
	db.DB = sql.OpenDB(&statusDB{
		settings: map[string]string{
			"hysteria2_obfs_password": "SECRET_obfs", "hysteria2_stats_secret": "SECRET_stats",
			"hysteria2_bound_exit_id": "2", "hysteria2_bound_exit_since": "2026-10-12T10:00:00Z",
			"hysteria2_obfs_rotated_at": "2026-10-10T04:00:00+03:00",
		},
		names: map[int64]string{2: "NL-1", 3: "LV-1"},
	})
	expressMu.Lock()
	oldSel := expressSel
	expressSel = nil
	expressMu.Unlock()
	oldProbe := lastBambooProbe.Swap(nil)
	t.Cleanup(func() {
		db.DB.Close()
		db.DB = old
		expressMu.Lock()
		expressSel = oldSel
		expressMu.Unlock()
		lastBambooProbe.Store(oldProbe)
	})

	status := func() (string, map[string]any) {
		raw, err := json.Marshal(GetHysteria2Status())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "SECRET") {
			t.Fatalf("a secret leaked into the Bamboo status: %s", raw)
		}
		return string(raw), m
	}

	// Selector not initialised yet (API just started): the persisted choice, no verdict yet.
	raw, m := status()
	if m["bound_exit_id"] != 2.0 || m["bound_exit_name"] != "NL-1" || m["bound_exit_since"] != "2026-10-12T10:00:00Z" ||
		m["bound_exit_rtt_ms"] != 0.0 || m["obfs_rotated_at"] != "2026-10-10T04:00:00+03:00" {
		t.Fatalf("persisted choice: %s", raw)
	}
	if p, ok := m["probe"]; !ok || p != nil {
		t.Fatalf("probe must be null before the first verdict: %s", raw)
	}

	// The selector's committed exit wins over app_settings; the last verdict is shown.
	sel := NewExpressSelector(3, time.Date(2026, 10, 13, 6, 0, 0, 0, time.UTC))
	sel.Observe(3, 42)
	expressMu.Lock()
	expressSel = sel
	expressMu.Unlock()
	RecordBambooProbe(BambooProbe{OK: false, At: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC), Detail: "Бамбук недоступен снаружи: x"})
	raw, m = status()
	probe, _ := m["probe"].(map[string]any)
	if m["bound_exit_id"] != 3.0 || m["bound_exit_name"] != "LV-1" || m["bound_exit_since"] != "2026-10-13T06:00:00Z" ||
		m["bound_exit_rtt_ms"] != 42.0 || probe == nil || probe["ok"] != false || probe["at"] != "2026-10-13T09:00:00Z" {
		t.Fatalf("committed choice: %s", raw)
	}
}
