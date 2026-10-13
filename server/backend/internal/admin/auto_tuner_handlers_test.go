package admin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/db"
)

// «Запустить сейчас» used to answer "triggered" while no goroutine read the channel (the tuner
// starts only if it was enabled at API start). Now it says so.
func TestAutoTunerRunNowIsHonest(t *testing.T) {
	oldSetting := autoTunerSetting
	t.Cleanup(func() { autoTunerSetting = oldSetting; SetAutoTunerLoopRunning(false) })
	drain := func() {
		select {
		case <-AutoTunerRunChan:
		default:
		}
	}
	drain()
	run := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		AutoTunerRunNowHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/auto-tuner/run-now", nil))
		return rec
	}

	// Switched on after the API started: enabled, but no goroutine reads the channel.
	autoTunerSetting = func(string, string) string { return "true" }
	SetAutoTunerLoopRunning(false)
	if rec := run(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "не запущен") || len(AutoTunerRunChan) != 0 {
		t.Fatalf("no tuner goroutine: %d %s (queued %d)", rec.Code, rec.Body.String(), len(AutoTunerRunChan))
	}
	SetAutoTunerLoopRunning(true)
	autoTunerSetting = func(string, string) string { return "false" }
	if rec := run(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "выключен") || len(AutoTunerRunChan) != 0 {
		t.Fatalf("tuner switched off: %d %s", rec.Code, rec.Body.String())
	}
	autoTunerSetting = func(string, string) string { return "true" }
	if rec := run(); rec.Code != http.StatusOK || len(AutoTunerRunChan) != 1 {
		t.Fatalf("running and enabled: %d %s (queued %d)", rec.Code, rec.Body.String(), len(AutoTunerRunChan))
	}
	drain()
}

// tunerDB stands in for PostgreSQL: every read fails (settings fall back to their defaults,
// counts stay 0), every write is journaled and answers execErr.
type tunerDB struct {
	execErr error
	writes  [][]driver.Value
}

func (d *tunerDB) Connect(context.Context) (driver.Conn, error) { return tunerConn{d}, nil }
func (d *tunerDB) Driver() driver.Driver                        { return tunerDriver{d} }

type tunerDriver struct{ d *tunerDB }

func (x tunerDriver) Open(string) (driver.Conn, error) { return tunerConn{x.d}, nil }

type tunerConn struct{ d *tunerDB }

func (c tunerConn) Prepare(string) (driver.Stmt, error) { return tunerStmt{c.d}, nil }
func (c tunerConn) Close() error                        { return nil }
func (c tunerConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

type tunerStmt struct{ d *tunerDB }

func (s tunerStmt) Close() error  { return nil }
func (s tunerStmt) NumInput() int { return -1 }
func (s tunerStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.d.writes = append(s.d.writes, args)
	if s.d.execErr != nil {
		return nil, s.d.execErr
	}
	return driver.RowsAffected(1), nil
}
func (s tunerStmt) Query([]driver.Value) (driver.Rows, error) {
	return nil, errors.New("db read failed")
}

func withTunerDB(t *testing.T, execErr error) (*tunerDB, *[]string) {
	t.Helper()
	d := &tunerDB{execErr: execErr}
	oldDB, oldAudit := db.DB, auditLogExec
	db.DB = sql.OpenDB(d)
	var audits []string
	auditLogExec = func(_ string, args ...any) (sql.Result, error) {
		audits = append(audits, args[2].(string)) // action
		return nil, nil
	}
	t.Cleanup(func() { db.DB.Close(); db.DB, auditLogExec = oldDB, oldAudit; SetAutoTunerLoopRunning(false) })
	return d, &audits
}

func toggle(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	AutoTunerToggleHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/auto-tuner/toggle", strings.NewReader(body)))
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("toggle answered non-JSON %q: %v", rec.Body.String(), err)
	}
	return rec.Code, m
}

// The toggle reports a failed save instead of a cheerful "Auto-tuner true", and says honestly
// whether switching on does anything before the next API restart.
func TestAutoTunerToggleIsHonest(t *testing.T) {
	t.Run("save fails", func(t *testing.T) {
		_, audits := withTunerDB(t, errors.New("connection refused"))
		code, m := toggle(t, `{"enabled":true}`)
		if msg, _ := m["error"].(string); code != http.StatusInternalServerError || !strings.Contains(msg, "Не удалось сохранить") || len(*audits) != 0 {
			t.Fatalf("got %d %v (audits %v), want 500 with a Russian error and no audit row", code, m, *audits)
		}
	})
	t.Run("enabled, no tuner goroutine", func(t *testing.T) {
		d, audits := withTunerDB(t, nil)
		SetAutoTunerLoopRunning(false)
		code, m := toggle(t, `{"enabled":true}`)
		msg, _ := m["message"].(string)
		if code != http.StatusOK || m["loop_running"] != false || !strings.Contains(msg, "после следующего перезапуска API") {
			t.Fatalf("got %d %v", code, m)
		}
		if len(d.writes) != 1 || d.writes[0][0] != "auto_tuner_enabled" || d.writes[0][1] != "true" || len(*audits) != 1 || (*audits)[0] != "auto_tuner_toggle" {
			t.Fatalf("writes %v, audits %v", d.writes, *audits)
		}
	})
	t.Run("disabled, tuner goroutine running", func(t *testing.T) {
		d, _ := withTunerDB(t, nil)
		SetAutoTunerLoopRunning(true)
		code, m := toggle(t, `{"enabled":false}`)
		if code != http.StatusOK || m["loop_running"] != true || m["message"] != "Автотюнер выключен" || d.writes[0][1] != "false" {
			t.Fatalf("got %d %v (writes %v)", code, m, d.writes)
		}
	})
}

func TestAutoTunerStatusReportsLoopRunning(t *testing.T) {
	withTunerDB(t, nil)
	for _, running := range []bool{false, true} {
		SetAutoTunerLoopRunning(running)
		rec := httptest.NewRecorder()
		AutoTunerStatusHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/auto-tuner/status", nil))
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("status %d %q: %v", rec.Code, rec.Body.String(), err)
		}
		if v, ok := m["loop_running"]; !ok || v != running {
			t.Fatalf("loop_running = %v (present %v), want %v: %s", v, ok, running, rec.Body.String())
		}
	}
}
