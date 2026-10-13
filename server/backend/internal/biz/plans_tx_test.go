package biz

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/db"
)

// txLog is a database/sql connector that journals statements; statements run inside a
// transaction get the "tx:" prefix, and the first one containing failOn fails.
type txLog struct {
	mu     sync.Mutex
	events []string
	failOn string
}

func (l *txLog) add(e string) { l.mu.Lock(); l.events = append(l.events, e); l.mu.Unlock() }

func (l *txLog) Connect(context.Context) (driver.Conn, error) { return &txLogConn{log: l}, nil }
func (l *txLog) Driver() driver.Driver                        { return txLogDriver{l} }

type txLogDriver struct{ l *txLog }

func (d txLogDriver) Open(string) (driver.Conn, error) { return &txLogConn{log: d.l}, nil }

type txLogConn struct {
	log  *txLog
	inTx bool
}

func (c *txLogConn) Prepare(q string) (driver.Stmt, error) { return &txLogStmt{c: c, q: q}, nil }
func (c *txLogConn) Close() error                          { return nil }
func (c *txLogConn) Begin() (driver.Tx, error)             { c.inTx = true; c.log.add("BEGIN"); return c, nil }
func (c *txLogConn) Commit() error                         { c.inTx = false; c.log.add("COMMIT"); return nil }
func (c *txLogConn) Rollback() error                       { c.inTx = false; c.log.add("ROLLBACK"); return nil }

type txLogStmt struct {
	c *txLogConn
	q string
}

func (s *txLogStmt) Close() error  { return nil }
func (s *txLogStmt) NumInput() int { return -1 }

func (s *txLogStmt) record() error {
	f := strings.Fields(s.q)
	n := 3 // "INSERT INTO plans", "DELETE FROM plan_servers"
	if len(f) > 0 && strings.EqualFold(f[0], "UPDATE") {
		n = 2 // "UPDATE plans"
	}
	if len(f) < n {
		n = len(f)
	}
	e := strings.Join(f[:n], " ")
	if s.c.inTx {
		e = "tx:" + e
	}
	s.c.log.add(e)
	if s.c.log.failOn != "" && strings.Contains(s.q, s.c.log.failOn) {
		return errors.New("boom")
	}
	return nil
}

func (s *txLogStmt) Exec([]driver.Value) (driver.Result, error) {
	if err := s.record(); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func (s *txLogStmt) Query([]driver.Value) (driver.Rows, error) {
	if err := s.record(); err != nil {
		return nil, err
	}
	return &idRows{}, nil
}

type idRows struct{ done bool }

func (r *idRows) Columns() []string { return []string{"id"} }
func (r *idRows) Close() error      { return nil }
func (r *idRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done, dest[0] = true, int64(42)
	return nil
}

func withTxLog(t *testing.T, failOn string) *txLog {
	t.Helper()
	l := &txLog{failOn: failOn}
	old := db.DB
	db.DB = sql.OpenDB(l)
	db.DB.SetMaxOpenConns(2) // a statement outside the transaction gets its own (non-tx) connection
	t.Cleanup(func() { db.DB.Close(); db.DB = old })
	return l
}

func TestUpdatePlanWritesPlanServersInItsTransaction(t *testing.T) {
	l := withTxLog(t, "")
	if err := UpdatePlan(3, &Plan{Slug: "s", Name: "n", Features: "{}", ServerIDs: []int{2, 20}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"BEGIN", "tx:UPDATE plans", "tx:DELETE FROM plan_servers", "tx:INSERT INTO plan_servers", "COMMIT"}
	if !reflect.DeepEqual(l.events, want) {
		t.Fatalf("events %v, want %v", l.events, want)
	}
}

func TestUpdatePlanRollsBackWhenPlanServersFail(t *testing.T) {
	l := withTxLog(t, "INSERT INTO plan_servers")
	err := UpdatePlan(3, &Plan{Slug: "s", Name: "n", Features: "{}", ServerIDs: []int{2}})
	if err == nil || !strings.Contains(err.Error(), "plan_servers") {
		t.Fatalf("err = %v, want the plan_servers error (it used to be ignored)", err)
	}
	want := []string{"BEGIN", "tx:UPDATE plans", "tx:DELETE FROM plan_servers", "tx:INSERT INTO plan_servers", "ROLLBACK"}
	if !reflect.DeepEqual(l.events, want) {
		t.Fatalf("events %v, want %v", l.events, want)
	}
}

func TestUpdatePlanWithoutServersOnlyDeletes(t *testing.T) {
	l := withTxLog(t, "")
	if err := UpdatePlan(3, &Plan{Slug: "s", Name: "n", Features: "{}"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"BEGIN", "tx:UPDATE plans", "tx:DELETE FROM plan_servers", "COMMIT"}
	if !reflect.DeepEqual(l.events, want) {
		t.Fatalf("events %v, want %v", l.events, want)
	}
}

func TestCreatePlanBindsServersInItsTransaction(t *testing.T) {
	l := withTxLog(t, "")
	id, err := CreatePlan(&Plan{Slug: "s", Name: "n", Features: "{}", ServerIDs: []int{2}})
	if err != nil || id != 42 {
		t.Fatalf("CreatePlan = %d, %v", id, err)
	}
	want := []string{"BEGIN", "tx:INSERT INTO plans", "tx:DELETE FROM plan_servers", "tx:INSERT INTO plan_servers", "COMMIT"}
	if !reflect.DeepEqual(l.events, want) {
		t.Fatalf("events %v, want %v", l.events, want)
	}
}
