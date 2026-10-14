package biz

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

// lockWaitDB: every statement waits like a row lock held by another transaction, until
// the statement's own context ends.
type lockWaitDB struct{}

func (lockWaitDB) Connect(context.Context) (driver.Conn, error) { return lockWaitConn{}, nil }
func (lockWaitDB) Driver() driver.Driver                        { return lockWaitDriver{} }

type lockWaitDriver struct{}

func (lockWaitDriver) Open(string) (driver.Conn, error) { return lockWaitConn{}, nil }

type lockWaitConn struct{}

func (lockWaitConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("lockwait: no prepare")
}
func (lockWaitConn) Close() error                { return nil }
func (c lockWaitConn) Begin() (driver.Tx, error) { return c, nil }
func (lockWaitConn) Commit() error               { return nil }
func (lockWaitConn) Rollback() error             { return nil }
func (lockWaitConn) QueryContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (lockWaitConn) ExecContext(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// The Tx variants run every statement with the caller's context, so the paid-payment
// transaction's timeout also ends a lock wait inside them.
func TestTxVariantsHonourTheCallerTimeout(t *testing.T) {
	fake := sql.OpenDB(lockWaitDB{})
	defer fake.Close()
	for name, run := range map[string]func(context.Context, *sql.Tx) error{
		"ActivateSubscriptionWithPlanTx": func(ctx context.Context, tx *sql.Tx) error {
			return ActivateSubscriptionWithPlanTx(ctx, tx, "user-1", 3, 30, 300, "")
		},
		"CreditTenantWalletTx": func(ctx context.Context, tx *sql.Tx) error {
			paymentID := 7
			_, err := CreditTenantWalletTx(ctx, tx, 5, 100, "x", &paymentID)
			return err
		},
	} {
		tx, err := fake.Begin()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		done := make(chan error, 1)
		go func() { done <- run(ctx, tx) }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("%s: %v, want the caller's deadline", name, err)
			}
			_ = tx.Rollback()
		case <-time.After(3 * time.Second):
			// the statement ignores ctx and waits forever; leave it (Rollback would block too)
			t.Errorf("%s ignores the caller's timeout: still waiting on the lock after 3 s", name)
		}
		cancel()
	}
}
