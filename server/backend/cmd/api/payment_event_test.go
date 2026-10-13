package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"log"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// peDB is the payments row (id 7, YooKassa id pay-1) as processPaymentEvent sees it. It
// applies the WHERE guard and the paid_at expression it finds in the SQL text, so a
// statement without the guard behaves like one in PostgreSQL would.
type peDB struct {
	mu          sync.Mutex
	status      string
	ykStatus    string
	paidAt      time.Time // zero = NULL
	confirmedAt time.Time
	kind        string
	journal     []string
}

func (d *peDB) Connect(context.Context) (driver.Conn, error) { return &peConn{d}, nil }
func (d *peDB) Driver() driver.Driver                        { return peDriver{} }

type peDriver struct{}

func (peDriver) Open(string) (driver.Conn, error) { return nil, errors.New("pe driver: no DSN") }

type peConn struct{ d *peDB }

func (c *peConn) Prepare(q string) (driver.Stmt, error) {
	return nil, fmt.Errorf("pe driver: prepare not supported: %s", q)
}
func (c *peConn) Close() error              { return nil }
func (c *peConn) Begin() (driver.Tx, error) { return nil, errors.New("pe driver: no transactions") }

var (
	peNotIn        = regexp.MustCompile(`status NOT IN \(([^)]*)\)`)
	peStatusAssign = regexp.MustCompile(`(SET|,) status = `)
)

// guarded reports whether the statement's "status NOT IN (…)" (if any) excludes the row.
func (d *peDB) guarded(q string) bool {
	m := peNotIn.FindStringSubmatch(q)
	return m != nil && strings.Contains(m[1], "'"+d.status+"'")
}

func peSetTime(q, col string, t *time.Time) {
	switch {
	case strings.Contains(q, col+" = COALESCE("+col+", NOW())"):
		if t.IsZero() {
			*t = time.Now()
		}
	case strings.Contains(q, col+" = NOW()") || strings.Contains(q, "THEN NOW() ELSE "+col):
		*t = time.Now()
	}
}

func (c *peConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.Contains(q, "FROM payments p WHERE p.yookassa_payment_id = $1"):
		d.journal = append(d.journal, "find")
		meta := `{"plan_id": 3, "kind": "` + d.kind + `"}`
		return &rfRows{cols: []string{"id", "user", "meta", "kind"}, rows: [][]driver.Value{{int64(7), "user-1", meta, d.kind}}}, nil
	case strings.HasPrefix(q, "UPDATE payments SET status = 'succeeded'"):
		d.journal = append(d.journal, "claim")
		if d.guarded(q) {
			return &rfRows{cols: []string{"id"}}, nil
		}
		d.status = "succeeded"
		peSetTime(q, "paid_at", &d.paidAt)
		peSetTime(q, "confirmed_at", &d.confirmedAt)
		return &rfRows{cols: []string{"id"}, rows: [][]driver.Value{{int64(7)}}}, nil
	}
	return nil, fmt.Errorf("pe driver: unscripted query %s", q)
}

func (c *peConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.HasPrefix(q, "UPDATE payments SET yookassa_status = $1::text"):
		d.journal = append(d.journal, "details")
		if peStatusAssign.MatchString(q) {
			return nil, fmt.Errorf("pe driver: details statement touches status: %s", q)
		}
		d.ykStatus = args[0].Value.(string)
		peSetTime(q, "paid_at", &d.paidAt)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE payments SET status = $2::text"):
		d.journal = append(d.journal, "unpaid-status")
		if d.guarded(q) {
			return driver.RowsAffected(0), nil
		}
		d.status = args[1].Value.(string)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "UPDATE payments SET status = 'pending'") && strings.Contains(q, "AND status = 'succeeded'"):
		d.journal = append(d.journal, "undo")
		if d.status == "succeeded" {
			d.status = "pending"
		}
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("pe driver: unscripted statement %s", q)
}

// usePaymentEventFakes routes db.DB, the paid-payment effects and the log to fakes; effects
// counts successful effect runs, fail makes the next n runs fail.
func usePaymentEventFakes(t *testing.T, d *peDB) (effects func() int, failNext func(n int)) {
	t.Helper()
	fake := sql.OpenDB(d)
	prevDB, prevEffects, prevLog := db.DB, paidPaymentEffects, log.Writer()
	db.DB = fake
	log.SetOutput(io.Discard)
	var mu sync.Mutex
	runs, fails := 0, 0
	paidPaymentEffects = func(*yookassa.Payment, int, string, string, string, string) error {
		mu.Lock()
		defer mu.Unlock()
		if fails > 0 {
			fails--
			return errors.New("activation: database is down")
		}
		runs++
		return nil
	}
	t.Cleanup(func() {
		db.DB, paidPaymentEffects = prevDB, prevEffects
		log.SetOutput(prevLog)
		_ = fake.Close()
	})
	return func() int { mu.Lock(); defer mu.Unlock(); return runs },
		func(n int) { mu.Lock(); fails = n; mu.Unlock() }
}

func pePayment(status string) *yookassa.Payment {
	return &yookassa.Payment{ID: "pay-1", Status: status, Amount: yookassa.Amount{Value: "300.00", Currency: "RUB"}}
}

// A paid payment activates (extends the subscription, credits the tenant, saves the card)
// exactly once, whoever reports it: «Проверить статус», the webhook (also duplicated or
// concurrent), the sync cron or the dunning charge. paid_at keeps its first value, a paid
// or refunded payment never goes back to an older status.
func TestPaymentEventActivatesOnce(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		status      string
		paidAt      time.Time
		kind        string
		failEffects int
		events      []string // YooKassa statuses reported one after another
		concurrent  int      // >0: report "succeeded" that many times at once instead
		dunning     bool     // the dunning charge marks it paid before the webhook
		wantRuns    int
		wantStatus  string
		wantPaidAt  string // "t0", "set", "null"
		wantErrs    int
	}{
		{name: "«Проверить статус» twice: one activation", status: "pending",
			events: []string{"succeeded", "succeeded"}, wantRuns: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "duplicate payment.succeeded webhooks: one activation", status: "waiting_for_capture",
			events: []string{"succeeded", "succeeded", "succeeded"}, wantRuns: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "concurrent duplicate webhooks: one activation", status: "pending",
			concurrent: 8, wantRuns: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "refunded stays refunded, paid_at untouched, nothing activated", status: "refunded", paidAt: t0,
			events: []string{"succeeded"}, wantRuns: 0, wantStatus: "refunded", wantPaidAt: "t0"},
		{name: "a paid payment keeps paid_at and activates nothing", status: "succeeded", paidAt: t0,
			events: []string{"succeeded"}, wantRuns: 0, wantStatus: "succeeded", wantPaidAt: "t0"},
		{name: "completed counts as paid", status: "completed", paidAt: t0,
			events: []string{"succeeded"}, wantRuns: 0, wantStatus: "completed", wantPaidAt: "t0"},
		{name: "a stale pending/canceled never downgrades a paid payment", status: "succeeded", paidAt: t0,
			events: []string{"pending", "canceled"}, wantRuns: 0, wantStatus: "succeeded", wantPaidAt: "t0"},
		{name: "refunded is not downgraded either", status: "refunded", paidAt: t0,
			events: []string{"canceled"}, wantRuns: 0, wantStatus: "refunded", wantPaidAt: "t0"},
		{name: "unpaid statuses are still recorded", status: "pending",
			events: []string{"waiting_for_capture", "canceled"}, wantRuns: 0, wantStatus: "canceled", wantPaidAt: "null"},
		{name: "a handed-back payment keeps its first paid_at", status: "pending", paidAt: t0,
			events: []string{"succeeded"}, wantRuns: 1, wantStatus: "succeeded", wantPaidAt: "t0"},
		{name: "failed activation is handed back and done by the next report", status: "pending", failEffects: 1,
			events: []string{"succeeded", "succeeded", "succeeded"}, wantRuns: 1, wantStatus: "succeeded", wantPaidAt: "set", wantErrs: 1},
		{name: "B2B top-up credits the wallet once", status: "pending", kind: "tenant_topup",
			events: []string{"succeeded", "succeeded"}, wantRuns: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "dunning charge, then its webhook: one activation", status: "pending", dunning: true,
			events: []string{"succeeded"}, wantRuns: 0, wantStatus: "succeeded", wantPaidAt: "set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &peDB{status: tt.status, paidAt: tt.paidAt, kind: tt.kind}
			effects, failNext := usePaymentEventFakes(t, d)
			failNext(tt.failEffects)
			dunningRuns := 0
			if tt.dunning {
				applied, err := onPaidTransition(7, func() error { dunningRuns++; return nil })
				if err != nil || !applied {
					t.Fatalf("dunning charge: applied=%v err=%v", applied, err)
				}
			}
			errs := 0
			if tt.concurrent > 0 {
				var wg sync.WaitGroup
				var emu sync.Mutex
				for i := 0; i < tt.concurrent; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if err := processPaymentEvent(pePayment("succeeded")); err != nil {
							emu.Lock()
							errs++
							emu.Unlock()
						}
					}()
				}
				wg.Wait()
			}
			for _, st := range tt.events {
				if err := processPaymentEvent(pePayment(st)); err != nil {
					errs++
				}
			}
			if got := effects(); got != tt.wantRuns {
				t.Errorf("activations %d, want %d (journal %q)", got, tt.wantRuns, d.journal)
			}
			if tt.dunning && dunningRuns != 1 {
				t.Errorf("dunning activations %d, want 1", dunningRuns)
			}
			if errs != tt.wantErrs {
				t.Errorf("errors %d, want %d", errs, tt.wantErrs)
			}
			if d.status != tt.wantStatus {
				t.Errorf("status %q, want %q", d.status, tt.wantStatus)
			}
			switch tt.wantPaidAt {
			case "t0":
				if !d.paidAt.Equal(t0) {
					t.Errorf("paid_at %v, want it untouched (%v)", d.paidAt, t0)
				}
			case "set":
				if d.paidAt.IsZero() {
					t.Errorf("paid_at not set")
				}
			case "null":
				if !d.paidAt.IsZero() {
					t.Errorf("paid_at %v, want NULL", d.paidAt)
				}
			}
		})
	}
}

// paid_at is written once: the second «Проверить статус» does not move it.
func TestPaymentEventPaidAtStable(t *testing.T) {
	d := &peDB{status: "pending"}
	usePaymentEventFakes(t, d)
	if err := processPaymentEvent(pePayment("succeeded")); err != nil {
		t.Fatal(err)
	}
	first := d.paidAt
	time.Sleep(5 * time.Millisecond)
	if err := processPaymentEvent(pePayment("succeeded")); err != nil {
		t.Fatal(err)
	}
	if first.IsZero() || !d.paidAt.Equal(first) {
		t.Fatalf("paid_at %v then %v, want it set once", first, d.paidAt)
	}
}
