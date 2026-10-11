package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// fakeSettingsConnector is a database/sql driver that answers app_settings
// lookups from a map and fails every other query. That is a half-reachable
// database (users table times out, settings still readable) which is exactly
// when the old code fell back to the server master UUID. It lets the test
// drive the real biz.GetUserVlessUUID error path without PostgreSQL.
type fakeSettingsConnector struct{ settings map[string]string }

func (c fakeSettingsConnector) Connect(context.Context) (driver.Conn, error) {
	return fakeSettingsConn(c), nil
}
func (c fakeSettingsConnector) Driver() driver.Driver { return fakeSettingsDriver{} }

type fakeSettingsDriver struct{}

func (fakeSettingsDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake driver: open by name not supported")
}

type fakeSettingsConn fakeSettingsConnector

func (fakeSettingsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake driver: prepare not supported")
}
func (fakeSettingsConn) Close() error { return nil }
func (fakeSettingsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fake driver: transactions not supported")
}

func (c fakeSettingsConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !strings.Contains(query, "app_settings") {
		return nil, errors.New("fake driver: database unavailable")
	}
	rows := &fakeSettingsRows{}
	if len(args) == 1 {
		if key, ok := args[0].Value.(string); ok {
			if v, found := c.settings[key]; found {
				rows.values = []string{v}
			}
		}
	}
	return rows, nil
}

type fakeSettingsRows struct {
	values []string
	pos    int
}

func (*fakeSettingsRows) Columns() []string { return []string{"value"} }
func (*fakeSettingsRows) Close() error      { return nil }
func (r *fakeSettingsRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.values) {
		return io.EOF
	}
	dest[0] = r.values[r.pos]
	r.pos++
	return nil
}

// H7: when the per-user VLESS UUID lookup fails, buildHubConnectionOptions must
// return an error and no options. It must never substitute the server master
// UUID (app_settings.vless_uuid), which bypasses accounting, quota and
// revocation and gets cached by clients forever.
func TestBuildHubConnectionOptions_LookupFailureNeverFallsBackToMasterUUID(t *testing.T) {
	const masterUUID = "deadbeef-0000-4000-8000-000000000001"

	prevDB := db.DB
	db.DB = sql.OpenDB(fakeSettingsConnector{settings: map[string]string{
		"main_server_host":   "hub.example.test",
		"vless_uuid":         masterUUID,
		"reality_public_key": "test-public-key",
		"reality_short_id":   "abcd",
	}})
	var logBuf bytes.Buffer
	prevLogOut := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() {
		log.SetOutput(prevLogOut)
		_ = db.DB.Close()
		db.DB = prevDB
	})

	// Precondition: the master UUID really is readable, so the old fallback
	// would have had something to hand out.
	if got, gerr := biz.GetSetting("vless_uuid"); gerr != nil || got != masterUUID {
		t.Fatalf("fake DB does not serve the master uuid (got %q, err %v): test would prove nothing", got, gerr)
	}

	opts, err := buildHubConnectionOptions("user-1")
	if err == nil {
		t.Fatalf("expected an error when the per-user uuid lookup fails, got %d options", len(opts))
	}
	if len(opts) != 0 {
		t.Errorf("expected no options on error, got %d", len(opts))
	}
	for _, o := range opts {
		if strings.Contains(o.GetUri(), masterUUID) {
			t.Errorf("master uuid leaked into option %q", o.GetLabel())
		}
	}
	if strings.Contains(err.Error(), masterUUID) {
		t.Errorf("master uuid leaked into the error: %v", err)
	}
	if !strings.Contains(logBuf.String(), "vless uuid lookup failed") {
		t.Errorf("lookup failure was not logged, log output: %q", logBuf.String())
	}
}
