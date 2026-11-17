package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// fakeDBConnector is a database/sql driver that answers app_settings lookups
// from a map, optionally answers the subscribe_token lookup, and fails every
// other query. That is a half-reachable database (users table times out,
// settings still readable), which is exactly when the old code fell back to the
// server master UUID. It lets the tests drive the real error paths of the biz
// layer without PostgreSQL.
type fakeDBConnector struct {
	settings map[string]string
	// subscribeUser answers `SELECT user_id FROM users WHERE subscribe_token...`:
	// nil -> the query fails, pointer to "" -> no rows (sql.ErrNoRows),
	// otherwise that user_id is returned.
	subscribeUser *string
	// hy2Gate answers the Hysteria2 plan-gate query (p.express_enabled): nil -> the
	// query fails like any other, otherwise it returns these {allowed_protocols,
	// express_enabled} rows (none -> sql.ErrNoRows).
	hy2Gate *fakeGate
}

type fakeGate struct{ rows [][]string }

func (c fakeDBConnector) Connect(context.Context) (driver.Conn, error) { return fakeDBConn(c), nil }
func (c fakeDBConnector) Driver() driver.Driver                        { return fakeDBDriver{} }

type fakeDBDriver struct{}

func (fakeDBDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake driver: open by name not supported")
}

type fakeDBConn fakeDBConnector

func (fakeDBConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake driver: prepare not supported")
}
func (fakeDBConn) Close() error { return nil }
func (fakeDBConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fake driver: transactions not supported")
}

func (c fakeDBConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "app_settings"):
		rows := &fakeDBRows{cols: []string{"value"}}
		if len(args) == 1 {
			if key, ok := args[0].Value.(string); ok {
				if v, found := c.settings[key]; found {
					rows.values = []string{v}
				}
			}
		}
		return rows, nil
	case c.subscribeUser != nil && strings.Contains(query, "WHERE subscribe_token"):
		rows := &fakeDBRows{cols: []string{"user_id"}}
		if *c.subscribeUser != "" {
			rows.values = []string{*c.subscribeUser}
		}
		return rows, nil
	case c.hy2Gate != nil && strings.Contains(query, "p.express_enabled"):
		return &fakeDBRows{cols: []string{"allowed_protocols", "express_enabled"}, table: c.hy2Gate.rows}, nil
	default:
		return nil, errors.New("fake driver: database unavailable")
	}
}

type fakeDBRows struct {
	cols   []string
	values []string   // single-column rows
	table  [][]string // multi-column rows (used instead of values when set)
	pos    int
}

func (r *fakeDBRows) Columns() []string { return r.cols }
func (*fakeDBRows) Close() error        { return nil }
func (r *fakeDBRows) Next(dest []driver.Value) error {
	if r.table != nil {
		if r.pos >= len(r.table) {
			return io.EOF
		}
		for i, v := range r.table[r.pos] {
			dest[i] = v
		}
		r.pos++
		return nil
	}
	if r.pos >= len(r.values) {
		return io.EOF
	}
	dest[0] = r.values[r.pos]
	r.pos++
	return nil
}

// useFakeDB points the package-global db.DB and the global logger at test
// doubles for the duration of the test. The returned stopLog restores the
// logger and returns everything logged so far; call it BEFORE asserting on the
// log (bytes.Buffer is not goroutine-safe, a stray log writer must not race
// with the reads).
//
// Tests using this MUST NOT use t.Parallel() and must not run alongside
// parallel tests that touch db.DB or the log output.
func useFakeDB(t *testing.T, c fakeDBConnector) (stopLog func() string) {
	t.Helper()
	fakeDB := sql.OpenDB(c)
	prevDB := db.DB
	db.DB = fakeDB

	var logBuf bytes.Buffer
	prevLogOut := log.Writer()
	log.SetOutput(&logBuf)
	stopLog = func() string {
		log.SetOutput(prevLogOut)
		return logBuf.String()
	}
	t.Cleanup(func() {
		log.SetOutput(prevLogOut) // idempotent: stopLog may already have run
		db.DB = prevDB
		_ = fakeDB.Close() // close our own handle, never whatever db.DB points at
	})
	return stopLog
}

const testMasterUUID = "deadbeef-0000-4000-8000-000000000001"

func testSettingsWithMasterUUID() map[string]string {
	return map[string]string{
		"main_server_host":   "hub.example.test",
		"vless_uuid":         testMasterUUID,
		"reality_public_key": "test-public-key",
		"reality_short_id":   "abcd",
	}
}

// H7: when the per-user VLESS UUID lookup fails, buildHubConnectionOptions must
// return an error and no options. It must never substitute the server master
// UUID (app_settings.vless_uuid), which bypasses accounting, quota and
// revocation and gets cached by clients forever.
func TestBuildHubConnectionOptions_LookupFailureNeverFallsBackToMasterUUID(t *testing.T) {
	stopLog := useFakeDB(t, fakeDBConnector{settings: testSettingsWithMasterUUID()})

	// Precondition: the master UUID really is readable, so the old fallback
	// would have had something to hand out.
	if got, gerr := biz.GetSetting("vless_uuid"); gerr != nil || got != testMasterUUID {
		t.Fatalf("fake DB does not serve the master uuid (got %q, err %v): test would prove nothing", got, gerr)
	}

	opts, err := buildHubConnectionOptions("user-1")
	logged := stopLog()
	if err == nil {
		t.Fatalf("expected an error when the per-user uuid lookup fails, got %d options", len(opts))
	}
	if len(opts) != 0 {
		t.Errorf("expected no options on error, got %d", len(opts))
	}
	for _, o := range opts {
		if strings.Contains(o.GetUri(), testMasterUUID) {
			t.Errorf("master uuid leaked into option %q", o.GetLabel())
		}
	}
	if strings.Contains(err.Error(), testMasterUUID) {
		t.Errorf("master uuid leaked into the error: %v", err)
	}
	if !strings.Contains(logged, "vless uuid lookup failed") {
		t.Errorf("lookup failure was not logged, log output: %q", logged)
	}
	if strings.Contains(logged, testMasterUUID) {
		t.Errorf("master uuid leaked into the log: %q", logged)
	}
}

// A database failure on the subscription endpoint must read as "try again
// later" (503 + Retry-After), not as an invalid link (404) or lost access (403):
// Happ drops its cached profiles on those. A genuinely unknown token stays 404.
func TestHandleSubscriptionLink_DBFailureIsTemporaryNotInvalid(t *testing.T) {
	const token = "11111111-2222-4333-8444-555555555555"
	user := "user-1"
	noUser := ""

	cases := []struct {
		name      string
		conn      fakeDBConnector
		wantCode  int
		wantBody  string
		wantRetry string
		wantInLog string
	}{
		{
			name:      "token lookup fails",
			conn:      fakeDBConnector{settings: testSettingsWithMasterUUID()},
			wantCode:  http.StatusServiceUnavailable,
			wantBody:  "temporarily unavailable",
			wantRetry: "300",
			wantInLog: "subscribe_token lookup failed",
		},
		{
			name:     "unknown token",
			conn:     fakeDBConnector{settings: testSettingsWithMasterUUID(), subscribeUser: &noUser},
			wantCode: http.StatusNotFound,
			wantBody: "invalid token",
		},
		{
			name:      "access state lookup fails",
			conn:      fakeDBConnector{settings: testSettingsWithMasterUUID(), subscribeUser: &user},
			wantCode:  http.StatusServiceUnavailable,
			wantBody:  "temporarily unavailable",
			wantRetry: "300",
			wantInLog: "access state lookup failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stopLog := useFakeDB(t, tc.conn)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/user/subscribe/"+token, nil)
			rec := httptest.NewRecorder()
			handleSubscriptionLink(rec, req)
			logged := stopLog()

			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
			if got := rec.Header().Get("Retry-After"); got != tc.wantRetry {
				t.Errorf("Retry-After = %q, want %q", got, tc.wantRetry)
			}
			if tc.wantInLog != "" && !strings.Contains(logged, tc.wantInLog) {
				t.Errorf("log does not contain %q: %q", tc.wantInLog, logged)
			}
			if strings.Contains(logged, token) {
				t.Errorf("subscribe token leaked into the log: %q", logged)
			}
			if strings.Contains(rec.Body.String(), testMasterUUID) {
				t.Errorf("master uuid leaked into the response")
			}
		})
	}
}

// CanUseHysteria2 is the plan gate for the Bamboo profiles. "No such plan" is a plain
// false; a database failure must surface as an error so the callers answer 503 instead
// of serving a subscription that silently lacks Bamboo.
func TestCanUseHysteria2(t *testing.T) {
	cases := []struct {
		name    string
		gate    *fakeGate
		want    bool
		wantErr bool
	}{
		{"db failure is an error, not false", nil, false, true},
		{"no active subscription", &fakeGate{}, false, false},
		{"express plan with hysteria2", &fakeGate{[][]string{{"{vless,hysteria2}", "true"}}}, true, false},
		{"express plan with all", &fakeGate{[][]string{{"{all}", "true"}}}, true, false},
		{"hysteria2 allowed but express disabled", &fakeGate{[][]string{{"{vless,hysteria2}", "false"}}}, false, false},
		{"express enabled but protocol not allowed", &fakeGate{[][]string{{"{vless}", "true"}}}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stopLog := useFakeDB(t, fakeDBConnector{hy2Gate: c.gate})
			got, err := biz.CanUseHysteria2("user-1")
			stopLog()
			if got != c.want || (err != nil) != c.wantErr {
				t.Fatalf("CanUseHysteria2 = (%v, %v), want (%v, err=%v)", got, err, c.want, c.wantErr)
			}
		})
	}
}
