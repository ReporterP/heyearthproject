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
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// atRefund is a payment_refunds row of the payment.
type atRefund struct {
	kop    int64
	status string
}

// atState is one payment, its refunds, tenants and their wallet rows;
// tenant_wallet_transactions (payment_id) is unique like the 000060 index.
type atState struct {
	payStatus, payMeta string
	payAmount          float64
	refunds            []atRefund
	balance            map[int64]float64
	wallet             []int64 // payment ids credited
	commits            int
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
func (c *atConn) Commit() error {
	c.d.mu.Lock()
	c.d.st.commits++
	c.d.mu.Unlock()
	c.inTx, c.undo = false, nil
	return nil
}
func (c *atConn) Rollback() error {
	c.d.mu.Lock()
	for i := len(c.undo) - 1; i >= 0; i-- {
		c.undo[i]()
	}
	c.d.mu.Unlock()
	c.inTx, c.undo = false, nil
	return nil
}

var atFilter = regexp.MustCompile(`FILTER \(WHERE status = '(\w+)'\)`)

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
		return one([]string{"amount", "status", "meta"}, int64(math.Round(st.payAmount*100)), st.payStatus, st.payMeta), nil
	case strings.Contains(q, "FROM payment_refunds"): // sum, then count; their filters are read from the SQL
		m := atFilter.FindAllStringSubmatch(q, -1)
		if len(m) != 2 {
			return nil, fmt.Errorf("at driver: refunds query without a sum and a count filter: %s", q)
		}
		var sum, count int64
		for _, r := range st.refunds {
			if r.status == m[0][1] {
				sum += r.kop
			}
			if r.status == m[1][1] {
				count++
			}
		}
		return one([]string{"sum", "count"}, sum, count), nil
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

// useAttachFake routes db.DB to a fake holding st and records PaymentUnappliedResolvedFn
// calls (with the number of COMMITs seen at that moment).
func useAttachFake(t *testing.T, st atState) (*atDB, *[]string) {
	t.Helper()
	if st.balance == nil {
		st.balance = map[int64]float64{}
	}
	d := &atDB{st: st}
	fake := sql.OpenDB(d)
	prev, prevLog, prevResolved := db.DB, log.Writer(), PaymentUnappliedResolvedFn
	db.DB = fake
	log.SetOutput(io.Discard)
	var resolved []string
	PaymentUnappliedResolvedFn = func(paymentID int, message string) {
		d.mu.Lock()
		defer d.mu.Unlock()
		resolved = append(resolved, fmt.Sprintf("%d after %d commit(s): %s", paymentID, d.st.commits, message))
	}
	t.Cleanup(func() {
		db.DB, PaymentUnappliedResolvedFn = prev, prevResolved
		log.SetOutput(prevLog)
		_ = fake.Close()
	})
	return d, &resolved
}

func postAttach(tenant int) (int, map[string]any) {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/payments/12/attach-tenant", strings.NewReader(fmt.Sprintf(`{"tenant_id": %d}`, tenant)))
	req = req.WithContext(context.WithValue(req.Context(), adminClaimsContextKey, &Claims{AdminID: 1}))
	rec := httptest.NewRecorder()
	AttachPaymentToTenantHandler(rec, req)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec.Code, m
}

// Attaching an already credited payment again answers 409 «Этот платёж уже зачислен» and
// changes nothing: neither the payment's tenant_id nor any balance.
func TestAttachPaymentToTenantCreditsOnce(t *testing.T) {
	d, _ := useAttachFake(t, atState{payStatus: "succeeded", payMeta: `{"kind":"b2b_invoice"}`, payAmount: 1500})

	if code, m := postAttach(5); code != http.StatusOK || m["credited"] != true {
		t.Fatalf("first attach: %d %v", code, m)
	}
	if d.st.balance[5] != 1500 || len(d.st.wallet) != 1 || !strings.Contains(d.st.payMeta, `"tenant_id":5`) {
		t.Fatalf("after the first attach: %+v", d.st)
	}
	code, m := postAttach(6)
	if code != http.StatusConflict || m["error"] != "Этот платёж уже зачислен" {
		t.Fatalf("second attach: %d %v, want 409 «Этот платёж уже зачислен»", code, m)
	}
	if d.st.balance[5] != 1500 || d.st.balance[6] != 0 || len(d.st.wallet) != 1 || !strings.Contains(d.st.payMeta, `"tenant_id":5`) {
		t.Fatalf("the refused attach changed something: %+v", d.st)
	}

	d.st.payStatus, d.st.wallet = "pending", nil
	if code, m := postAttach(7); code != http.StatusOK || m["credited"] != false || len(d.st.wallet) != 0 {
		t.Fatalf("unpaid payment: %d %v, wallet %v", code, m, d.st.wallet)
	}
}

// Only a B2B payment can be attached; a paid one with a pending refund answers 409, otherwise
// it is credited with amount − succeeded refunds (failed/canceled do not count) and 409 when
// nothing is left; a paid-but-unapplied mark goes with the credit and its alert is resolved
// after COMMIT.
func TestAttachPaymentToTenantRules(t *testing.T) {
	const b2b = `{"kind":"b2b_invoice","inn":"7707083893"}`
	const marked = `{"kind":"tenant_topup","unapplied_reason":"в платеже tenant_topup нет tenant_id (привяжите его к компании)"}`
	tests := []struct {
		name         string
		st           atState
		wantCode     int
		wantErr      string
		wantBalance  float64
		wantAmount   any    // credited_amount in the answer
		wantMeta     string // substring of the metadata afterwards ("" = unchanged)
		wantResolved []string
	}{
		{name: "subscription payment", st: atState{payStatus: "succeeded", payMeta: `{"kind":"subscription","plan_id":3}`, payAmount: 300},
			wantCode: http.StatusConflict, wantErr: "Этот платёж нельзя зачислить компании"},
		{name: "payment without a kind", st: atState{payStatus: "succeeded", payMeta: `{}`, payAmount: 300},
			wantCode: http.StatusConflict, wantErr: "Этот платёж нельзя зачислить компании"},
		{name: "unpaid subscription payment is not bound either", st: atState{payStatus: "pending", payMeta: `{"kind":"subscription"}`, payAmount: 300},
			wantCode: http.StatusConflict, wantErr: "Этот платёж нельзя зачислить компании"},
		{name: "tenant_topup: credited in full", st: atState{payStatus: "succeeded", payMeta: `{"kind":"tenant_topup"}`, payAmount: 1500},
			wantCode: http.StatusOK, wantBalance: 1500, wantAmount: 1500.0, wantMeta: `"tenant_id":5`},
		{name: "partly refunded: amount − succeeded refunds", st: atState{payStatus: "succeeded", payMeta: b2b, payAmount: 1500,
			refunds: []atRefund{{50000, "succeeded"}, {30000, "failed"}, {10000, "canceled"}}},
			wantCode: http.StatusOK, wantBalance: 1000, wantAmount: 1000.0, wantMeta: `"tenant_id":5`},
		{name: "a pending refund: 409, nothing changed", st: atState{payStatus: "succeeded", payMeta: b2b, payAmount: 1500,
			refunds: []atRefund{{50000, "succeeded"}, {20000, "pending"}}},
			wantCode: http.StatusConflict, wantErr: "Дождитесь завершения возврата по этому платежу"},
		{name: "kopecks stay exact", st: atState{payStatus: "completed", payMeta: b2b, payAmount: 1000.10, refunds: []atRefund{{30, "succeeded"}}},
			wantCode: http.StatusOK, wantBalance: 999.80, wantAmount: 999.80, wantMeta: `"tenant_id":5`},
		{name: "succeeded refunds cover it: 409", st: atState{payStatus: "succeeded", payMeta: b2b, payAmount: 1500,
			refunds: []atRefund{{100000, "succeeded"}, {50000, "succeeded"}}},
			wantCode: http.StatusConflict, wantErr: "Зачислять нечего: платёж возвращён"},
		{name: "refunded payment: 409", st: atState{payStatus: "refunded", payMeta: b2b, payAmount: 1500, refunds: []atRefund{{150000, "succeeded"}}},
			wantCode: http.StatusConflict, wantErr: "Зачислять нечего"},
		{name: "paid but unapplied: credited, mark cleared, alert resolved after COMMIT", st: atState{payStatus: "succeeded", payMeta: marked, payAmount: 1500},
			wantCode: http.StatusOK, wantBalance: 1500, wantAmount: 1500.0, wantMeta: `"tenant_id":5`,
			wantResolved: []string{"12 after 1 commit(s): Платёж #12 вручную зачислен компании «Tenant 5» (#5): 1500.00 ₽."}},
		{name: "unpaid B2B payment: bound, nothing credited", st: atState{payStatus: "pending", payMeta: b2b, payAmount: 1500},
			wantCode: http.StatusOK, wantMeta: `"tenant_id":5`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, resolved := useAttachFake(t, tt.st)
			code, m := postAttach(5)
			if code != tt.wantCode {
				t.Fatalf("%d %v, want %d", code, m, tt.wantCode)
			}
			if tt.wantErr != "" {
				if msg, _ := m["error"].(string); !strings.Contains(msg, tt.wantErr) {
					t.Fatalf("error %q, want %q", msg, tt.wantErr)
				}
			}
			if got := d.st.balance[5]; math.Abs(got-tt.wantBalance) > 1e-9 {
				t.Fatalf("balance %v, want %v", got, tt.wantBalance)
			}
			if code != http.StatusOK && len(d.st.wallet) != 0 {
				t.Fatalf("a refused attach wrote wallet rows: %v", d.st.wallet)
			}
			if tt.wantAmount != nil && m["credited_amount"] != tt.wantAmount {
				t.Fatalf("credited_amount %v, want %v", m["credited_amount"], tt.wantAmount)
			}
			if tt.wantMeta == "" && d.st.payMeta != tt.st.payMeta {
				t.Fatalf("metadata changed: %s", d.st.payMeta)
			}
			if tt.wantMeta != "" && !strings.Contains(d.st.payMeta, tt.wantMeta) {
				t.Fatalf("metadata %s lacks %s", d.st.payMeta, tt.wantMeta)
			}
			if code == http.StatusOK && m["credited"] == true && strings.Contains(d.st.payMeta, "unapplied_reason") {
				t.Fatalf("credited but still marked unapplied: %s", d.st.payMeta)
			}
			if code == http.StatusOK && m["credited"] == false && strings.Contains(tt.st.payMeta, "unapplied_reason") != strings.Contains(d.st.payMeta, "unapplied_reason") {
				t.Fatalf("an unpaid attach touched the mark: %s", d.st.payMeta)
			}
			if strings.Join(*resolved, "\n") != strings.Join(tt.wantResolved, "\n") {
				t.Fatalf("resolved %q, want %q", *resolved, tt.wantResolved)
			}
		})
	}
}
