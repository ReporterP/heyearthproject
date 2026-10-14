package admin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// atState is one payment, tenants and their wallet rows; tenant_wallet_transactions
// (payment_id) is unique like the 000060 index.
type atState struct {
	payStatus, payMeta string
	payAmount          float64
	balance            map[int64]float64
	wallet             []int64 // payment ids credited
}

type atDB struct {
	mu sync.Mutex
	st atState
}

func (d *atDB) Connect(context.Context) (driver.Conn, error) { return &atConn{d: d}, nil }
func (d *atDB) Driver() driver.Driver                        { return atDriver{} }

type atDriver struct{}

func (atDriver) Open(string) (driver.Conn, error) { return nil, errors.New("at driver: no DSN") }

// atConn undoes only its own transaction's writes on ROLLBACK (a write through another
// connection, outside the transaction, stays — as in PostgreSQL).
type atConn struct {
	d    *atDB
	inTx bool
	undo []func()
}

// remember records how to undo a write; caller holds d.mu.
func (c *atConn) remember(f func()) {
	if c.inTx {
		c.undo = append(c.undo, f)
	}
}

func (c *atConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("at driver: no prepare")
}
func (c *atConn) Close() error              { return nil }
func (c *atConn) Begin() (driver.Tx, error) { c.inTx, c.undo = true, nil; return c, nil }
func (c *atConn) Commit() error             { c.inTx, c.undo = false, nil; return nil }
func (c *atConn) Rollback() error {
	c.d.mu.Lock()
	for i := len(c.undo) - 1; i >= 0; i-- {
		c.undo[i]()
	}
	c.d.mu.Unlock()
	c.inTx, c.undo = false, nil
	return nil
}

func (c *atConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(query), " ")
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	st := &c.d.st
	one := func(cols []string, v ...driver.Value) driver.Rows {
		return &listRows{cols: cols, rows: [][]driver.Value{v}}
	}
	switch {
	case strings.Contains(q, "FROM admin_users WHERE id = $1"):
		return one([]string{"ids", "null"}, []byte("{}"), true), nil // super admin
	case strings.HasPrefix(q, "SELECT name FROM tenants WHERE id = $1"):
		return one([]string{"name"}, fmt.Sprintf("Tenant %d", args[0].Value.(int64))), nil
	case strings.Contains(q, "FROM payments WHERE id = $1 FOR UPDATE"):
		return one([]string{"amount", "status", "meta"}, st.payAmount, st.payStatus, st.payMeta), nil
	case strings.HasPrefix(q, "SELECT status FROM tenants WHERE id = $1 FOR UPDATE"):
		return one([]string{"status"}, "active"), nil
	case strings.HasPrefix(q, "UPDATE tenants SET balance = balance + $1"):
		tenant := args[1].Value.(int64)
		before := st.balance[tenant]
		c.remember(func() { st.balance[tenant] = before })
		st.balance[tenant] += args[0].Value.(float64)
		return one([]string{"balance"}, st.balance[tenant]), nil
	}
	return nil, fmt.Errorf("at driver: unscripted %s", q)
}

func (c *atConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := strings.Join(strings.Fields(query), " ")
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	st := &c.d.st
	switch {
	case strings.HasPrefix(q, "UPDATE payments SET metadata = $1::jsonb"):
		before := st.payMeta
		c.remember(func() { st.payMeta = before })
		st.payMeta = args[0].Value.(string)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "INSERT INTO tenant_wallet_transactions"):
		if pid, ok := args[3].Value.(int64); ok {
			for _, have := range st.wallet {
				if have == pid {
					return nil, &pq.Error{Code: "23505", Message: `duplicate key value violates unique constraint "uq_tenant_wallet_transactions_payment_id"`}
				}
			}
			n := len(st.wallet)
			c.remember(func() { st.wallet = st.wallet[:n] })
			st.wallet = append(st.wallet, pid)
		}
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("at driver: unscripted %s", q)
}

// Attaching an already credited payment again answers 409 «Этот платёж уже зачислен» and
// changes nothing: neither the payment's tenant_id nor any balance.
func TestAttachPaymentToTenantCreditsOnce(t *testing.T) {
	d := &atDB{st: atState{payStatus: "succeeded", payMeta: "{}", payAmount: 1500, balance: map[int64]float64{}}}
	fake := sql.OpenDB(d)
	prev, prevLog := db.DB, log.Writer()
	db.DB = fake
	log.SetOutput(io.Discard)
	t.Cleanup(func() { db.DB = prev; log.SetOutput(prevLog); _ = fake.Close() })

	attach := func(tenant int) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/admin/payments/12/attach-tenant", strings.NewReader(fmt.Sprintf(`{"tenant_id": %d}`, tenant)))
		req = req.WithContext(context.WithValue(req.Context(), adminClaimsContextKey, &Claims{AdminID: 1}))
		rec := httptest.NewRecorder()
		AttachPaymentToTenantHandler(rec, req)
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}

	if code, m := attach(5); code != http.StatusOK || m["credited"] != true {
		t.Fatalf("first attach: %d %v", code, m)
	}
	if d.st.balance[5] != 1500 || len(d.st.wallet) != 1 || !strings.Contains(d.st.payMeta, `"tenant_id":5`) {
		t.Fatalf("after the first attach: %+v", d.st)
	}
	code, m := attach(6)
	if code != http.StatusConflict || m["error"] != "Этот платёж уже зачислен" {
		t.Fatalf("second attach: %d %v, want 409 «Этот платёж уже зачислен»", code, m)
	}
	if d.st.balance[5] != 1500 || d.st.balance[6] != 0 || len(d.st.wallet) != 1 || !strings.Contains(d.st.payMeta, `"tenant_id":5`) {
		t.Fatalf("the refused attach changed something: %+v", d.st)
	}

	d.st.payStatus, d.st.wallet = "pending", nil
	if code, m := attach(7); code != http.StatusOK || m["credited"] != false || len(d.st.wallet) != 0 {
		t.Fatalf("unpaid payment: %d %v, wallet %v", code, m, d.st.wallet)
	}
}
