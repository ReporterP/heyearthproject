package main

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
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─── Fake payments table ──────────────────────────────────────────────────

// peRow is a payments row; activations counts what the effects seam wrote inside the
// claim transaction (it rolls back with it).
type peRow struct {
	id                  int64
	status, ykID, user  string
	ykStatus, meta      string
	amount              string    // payments.amount::text; "" = 300.00
	paidAt, confirmedAt time.Time // zero = NULL
	created             time.Time
	failures            int64
	activations         int64
}

// peDB is the payments table. A write inside a transaction locks its row until
// COMMIT/ROLLBACK (PostgreSQL row lock: a concurrent writer waits, then sees the committed
// row); ROLLBACK undoes the transaction's writes. yookassa_payment_id is unique. WHERE
// guards, paid_at expressions, status assignments and ORDER BY are read from the SQL
// text, so a statement without its guard behaves like it would in PostgreSQL.
type peDB struct {
	mu      sync.Mutex
	rows    []*peRow
	nextID  int64
	locks   map[int64]*sync.Mutex
	journal []string
	failOn  string // statement label that fails
}

func newPEDB(rows ...*peRow) *peDB {
	d := &peDB{locks: map[int64]*sync.Mutex{}}
	for _, r := range rows {
		if r.created.IsZero() {
			r.created = time.Now().Add(-time.Hour)
		}
		if r.meta == "" {
			r.meta = "{}"
		}
		if r.amount == "" {
			r.amount = "300.00"
		}
		d.rows = append(d.rows, r)
		d.nextID = max(d.nextID, r.id)
	}
	return d
}

func (d *peDB) Connect(context.Context) (driver.Conn, error) { return &peConn{d: d}, nil }
func (d *peDB) Driver() driver.Driver                        { return peDriver{} }

type peDriver struct{}

func (peDriver) Open(string) (driver.Conn, error) { return nil, errors.New("pe driver: no DSN") }

type peConn struct {
	d      *peDB
	inTx   bool
	held   []*sync.Mutex
	heldID map[int64]bool
	undo   []func()
}

func (c *peConn) Prepare(q string) (driver.Stmt, error) {
	return nil, fmt.Errorf("pe driver: prepare not supported: %s", q)
}
func (c *peConn) Close() error { return nil }
func (c *peConn) Begin() (driver.Tx, error) {
	c.inTx, c.heldID, c.undo = true, map[int64]bool{}, nil
	c.d.note(c, "BEGIN")
	return c, nil
}
func (c *peConn) Commit() error   { c.end("COMMIT", false); return nil }
func (c *peConn) Rollback() error { c.end("ROLLBACK", true); return nil }

func (c *peConn) end(what string, rollback bool) {
	d := c.d
	d.mu.Lock()
	if rollback {
		for i := len(c.undo) - 1; i >= 0; i-- {
			c.undo[i]()
		}
	}
	d.journal = append(d.journal, what)
	d.mu.Unlock()
	for _, l := range c.held {
		l.Unlock()
	}
	c.inTx, c.held, c.heldID, c.undo = false, nil, nil, nil
}

func (d *peDB) note(c *peConn, label string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := label
	if c.inTx && label != "BEGIN" {
		e = "tx:" + label
	}
	d.journal = append(d.journal, e)
	if d.failOn == label {
		return fmt.Errorf("pe driver: injected failure at %s", label)
	}
	return nil
}

// lock takes the row lock for a write: until the end of the transaction, or just for the
// statement outside one. Call without d.mu.
func (c *peConn) lock(id int64) (release func()) {
	c.d.mu.Lock()
	l := c.d.locks[id]
	if l == nil {
		l = &sync.Mutex{}
		c.d.locks[id] = l
	}
	c.d.mu.Unlock()
	if c.inTx {
		if !c.heldID[id] {
			l.Lock()
			c.held, c.heldID[id] = append(c.held, l), true
		}
		return func() {}
	}
	l.Lock()
	return l.Unlock
}

// write records how to undo a change of r; caller holds d.mu.
func (c *peConn) write(r *peRow) {
	if c.inTx {
		before := *r
		c.undo = append(c.undo, func() { *r = before })
	}
}

func (d *peDB) byID(id int64) *peRow {
	for _, r := range d.rows {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (d *peDB) ykTaken(yk string, except int64) bool {
	for _, r := range d.rows {
		if yk != "" && r.ykID == yk && r.id != except {
			return true
		}
	}
	return false
}

var (
	peNotIn        = regexp.MustCompile(`status NOT IN \(([^)]*)\)`)
	peStatusIn     = regexp.MustCompile(`AND status IN \(([^)]*)\)`)
	peStatusAssign = regexp.MustCompile(`(SET|,) status = \$(\d)`)
	peAnyStatusIn  = regexp.MustCompile(`status IN \(([^)]*)\)`)
)

// peMeta decodes a row's metadata.
func peMeta(r *peRow) map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal([]byte(r.meta), &m)
	return m
}

func (r *peRow) excludedBy(q string) bool {
	if m := peNotIn.FindStringSubmatch(q); m != nil && strings.Contains(m[1], "'"+r.status+"'") {
		return true
	}
	if m := peStatusIn.FindStringSubmatch(q); m != nil && !strings.Contains(m[1], "'"+r.status+"'") {
		return true
	}
	return strings.Contains(q, "paid_at IS NULL") && !r.paidAt.IsZero()
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

// assignStatus applies a "status = $N" a statement may carry (the old SQL did).
func assignStatus(q string, r *peRow, args []driver.NamedValue) {
	if m := peStatusAssign.FindStringSubmatch(q); m != nil {
		n := int(m[2][0] - '1')
		r.status, _ = args[n].Value.(string)
		peSetTime(q, "paid_at", &r.paidAt)
	}
}

var pePaymentCols = []string{"id", "user", "meta", "yk"}

func (c *peConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	label := ""
	switch {
	case strings.Contains(q, "FROM payments p WHERE p.id = $1"):
		label = "find-by-id"
	case strings.Contains(q, "FROM payments p WHERE p.yookassa_payment_id = $1"):
		label = "find-by-yookassa"
	case strings.HasPrefix(q, "INSERT INTO payments ("):
		label = "orphan-insert"
	case strings.HasPrefix(q, "UPDATE payments SET status = 'succeeded'"):
		label = "claim"
	case strings.HasPrefix(q, "UPDATE payments SET activation_failures = activation_failures + 1"):
		label = "failure"
	case strings.Contains(q, "FROM payments WHERE yookassa_payment_id IS NOT NULL"):
		label = "sync-select"
	case strings.HasPrefix(q, "SELECT COALESCE(yookassa_payment_id, ''), COALESCE(status, '') FROM payments WHERE id = $1"):
		label = "check-read"
	case strings.Contains(q, "metadata ? 'unapplied_reason'"):
		label = "unapplied-select"
	default:
		return nil, fmt.Errorf("pe driver: unscripted query %s", q)
	}
	if err := d.note(c, label); err != nil {
		return nil, err
	}
	var target int64
	if label == "claim" || label == "failure" {
		target = args[0].Value.(int64)
		defer c.lock(target)()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch label {
	case "check-read":
		if r := d.byID(args[0].Value.(int64)); r != nil {
			return &rfRows{cols: []string{"yk", "status"}, rows: [][]driver.Value{{r.ykID, r.status}}}, nil
		}
		return &rfRows{cols: []string{"yk", "status"}}, nil
	case "find-by-id", "find-by-yookassa":
		for _, r := range d.rows {
			if (label == "find-by-id" && r.id == args[0].Value.(int64)) || (label == "find-by-yookassa" && r.ykID != "" && r.ykID == args[0].Value.(string)) {
				return &rfRows{cols: pePaymentCols, rows: [][]driver.Value{{r.id, r.user, r.meta, r.ykID}}}, nil
			}
		}
		return &rfRows{cols: pePaymentCols}, nil
	case "orphan-insert":
		yk := args[4].Value.(string)
		if d.ykTaken(yk, 0) {
			return nil, errors.New("pq: duplicate key value violates unique constraint uq_payments_yookassa_payment_id")
		}
		d.nextID++
		r := &peRow{id: d.nextID, user: args[0].Value.(string), status: args[3].Value.(string), ykID: yk, meta: args[6].Value.(string), created: time.Now()}
		d.rows = append(d.rows, r)
		return &rfRows{cols: []string{"id"}, rows: [][]driver.Value{{r.id}}}, nil
	case "claim":
		r := d.byID(target)
		if r == nil || r.excludedBy(q) {
			return &rfRows{cols: []string{"failures"}}, nil
		}
		c.write(r)
		r.status = "succeeded"
		peSetTime(q, "paid_at", &r.paidAt)
		peSetTime(q, "confirmed_at", &r.confirmedAt)
		return &rfRows{cols: []string{"failures"}, rows: [][]driver.Value{{r.failures}}}, nil
	case "failure":
		r := d.byID(target)
		c.write(r)
		r.failures++
		return &rfRows{cols: []string{"failures"}, rows: [][]driver.Value{{r.failures}}}, nil
	case "sync-select":
		var out []*peRow
		for _, r := range d.rows {
			if r.ykID == "" || (r.status != "pending" && r.status != "waiting_for_capture") {
				continue
			}
			if strings.Contains(q, "activation_failures < $3") && r.failures >= args[2].Value.(int64) {
				continue
			}
			out = append(out, r)
		}
		byFailures := strings.Contains(q, "ORDER BY activation_failures ASC, created_at ASC")
		sort.SliceStable(out, func(i, j int) bool {
			if byFailures && out[i].failures != out[j].failures {
				return out[i].failures < out[j].failures
			}
			return out[i].created.Before(out[j].created)
		})
		res := &rfRows{cols: []string{"id", "yk", "created"}}
		for i, r := range out {
			if int64(i) >= args[1].Value.(int64) {
				break
			}
			res.rows = append(res.rows, []driver.Value{r.id, r.ykID, r.created})
		}
		return res, nil
	case "unapplied-select": // the status filter is read from the SQL
		m := peAnyStatusIn.FindStringSubmatch(q)
		var out []*peRow
		for _, r := range d.rows {
			if _, marked := peMeta(r)["unapplied_reason"]; marked && (m == nil || strings.Contains(m[1], "'"+r.status+"'")) {
				out = append(out, r)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
		res := &rfRows{cols: []string{"id", "user", "meta", "amount"}}
		for _, r := range out {
			res.rows = append(res.rows, []driver.Value{r.id, r.user, r.meta, r.amount})
		}
		return res, nil
	}
	return nil, errors.New("unreachable")
}

func (c *peConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	label, idArg := "", 0
	switch {
	case strings.HasPrefix(q, "UPDATE payments SET yookassa_payment_id = $2"):
		label = "link-by-db-id"
	case strings.HasPrefix(q, "UPDATE payments SET yookassa_payment_id = $1::text"):
		label, idArg = "recurring-link", 4
	case strings.HasPrefix(q, "UPDATE payments SET yookassa_status = $1::text"):
		label, idArg = "details", 6
	case strings.HasPrefix(q, "UPDATE payments SET status = $2::text"):
		label = "unpaid-status"
	case strings.HasPrefix(q, "UPDATE payments SET status = 'canceled'"):
		label = "stale-cancel"
	case strings.HasPrefix(q, "UPDATE users SET fake_activation"):
		label = "activation"
	case strings.HasPrefix(q, "UPDATE payments SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('unapplied_reason', $2::text)"):
		label = "mark-unapplied"
	default:
		return nil, fmt.Errorf("pe driver: unscripted statement %s", q)
	}
	if err := d.note(c, label); err != nil {
		return nil, err
	}
	id := args[idArg].Value.(int64)
	defer c.lock(id)()
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.byID(id)
	if r == nil {
		return driver.RowsAffected(0), nil
	}
	switch label {
	case "link-by-db-id":
		yk := args[1].Value.(string)
		if r.ykID != "" {
			return driver.RowsAffected(0), nil
		}
		if d.ykTaken(yk, r.id) {
			return nil, errors.New("pq: duplicate key value violates unique constraint uq_payments_yookassa_payment_id")
		}
		c.write(r)
		r.ykID = yk
	case "recurring-link":
		yk := args[0].Value.(string)
		if d.ykTaken(yk, r.id) {
			return nil, errors.New("pq: duplicate key value violates unique constraint uq_payments_yookassa_payment_id")
		}
		c.write(r)
		r.ykID, r.ykStatus, r.meta = yk, args[1].Value.(string), args[3].Value.(string)
		assignStatus(q, r, args)
	case "details":
		c.write(r)
		r.ykStatus = args[0].Value.(string)
		assignStatus(q, r, args)
	case "unpaid-status":
		if r.excludedBy(q) {
			return driver.RowsAffected(0), nil
		}
		c.write(r)
		r.status = args[1].Value.(string)
	case "stale-cancel":
		if r.excludedBy(q) {
			return driver.RowsAffected(0), nil
		}
		c.write(r)
		r.status = "canceled"
	case "activation":
		c.write(r)
		r.activations++
	case "mark-unapplied":
		meta := peMeta(r)
		meta["unapplied_reason"] = args[1].Value.(string)
		b, _ := json.Marshal(meta)
		c.write(r)
		r.meta = string(b)
	}
	return driver.RowsAffected(1), nil
}

func (d *peDB) row(id int64) peRow {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := d.byID(id); r != nil {
		return *r
	}
	return peRow{}
}

// peEffects replaces the real activation: it writes inside the claim transaction (so a
// rollback undoes it) and fails after writing while failing > 0.
type peEffects struct {
	mu      sync.Mutex
	failing int
	after   int
	seen    []paymentRow
	alerts  []string
	resolve []string
	open    []string // what openUnappliedAlerts answers
}

func usePaymentFakes(t *testing.T, d *peDB) *peEffects {
	t.Helper()
	fake := sql.OpenDB(d)
	e := &peEffects{}
	prevDB, prevEffects, prevRaise, prevResolve, prevLog := db.DB, paidPaymentEffects, raisePaymentAlert, resolvePaymentAlert, log.Writer()
	prevUnapplied, prevOpen := raiseUnappliedAlert, openUnappliedAlerts
	db.DB = fake
	log.SetOutput(io.Discard)
	paidPaymentEffects = func(ctx context.Context, tx *sql.Tx, p *yookassa.Payment, row paymentRow) (func(), error) {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET fake_activation = fake_activation + 1 WHERE payment = $1`, row.ID); err != nil {
			return nil, err
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		e.seen = append(e.seen, row)
		if e.failing > 0 {
			e.failing--
			return nil, errors.New("activation: database is down")
		}
		return func() { e.mu.Lock(); e.after++; e.mu.Unlock() }, nil
	}
	raisePaymentAlert = func(key string, level AlertLevel, title, details string) {
		e.mu.Lock()
		e.alerts = append(e.alerts, key+" | "+string(level)+" | "+title+" | "+details)
		e.mu.Unlock()
	}
	raiseUnappliedAlert = raisePaymentAlert
	resolvePaymentAlert = func(key, msg string) { e.mu.Lock(); e.resolve = append(e.resolve, key); e.mu.Unlock() }
	openUnappliedAlerts = func() []string { e.mu.Lock(); defer e.mu.Unlock(); return append([]string(nil), e.open...) }
	t.Cleanup(func() {
		db.DB, paidPaymentEffects, raisePaymentAlert, resolvePaymentAlert = prevDB, prevEffects, prevRaise, prevResolve
		raiseUnappliedAlert, openUnappliedAlerts = prevUnapplied, prevOpen
		log.SetOutput(prevLog)
		_ = fake.Close()
	})
	return e
}

func pePayment(status string, meta map[string]string) *yookassa.Payment {
	return &yookassa.Payment{ID: "pay-1", Status: status, Amount: yookassa.Amount{Value: "300.00", Currency: "RUB"}, Metadata: meta}
}

// ─── Applied once ─────────────────────────────────────────────────────────

// A paid payment is applied (subscription extended, wallet credited) exactly once,
// whoever reports it — «Проверить статус», the webhook (duplicated, concurrent), the sync
// cron — and only together with the move to 'succeeded'. paid_at keeps its first value,
// a paid or refunded payment never goes back to an older status, a failed activation rolls
// the move back and the next report applies it.
func TestPaymentAppliedOnce(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		row        peRow
		failing    int
		events     []string
		concurrent int
		wantActs   int64 // activations committed in the claim transaction
		wantAfter  int   // post-commit side effects run
		wantStatus string
		wantPaidAt string // "t0", "set", "null"
		wantErrs   int
		wantFails  int64
	}{
		{name: "«Проверить статус» twice", row: peRow{status: "pending"},
			events: []string{"succeeded", "succeeded"}, wantActs: 1, wantAfter: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "duplicate webhooks", row: peRow{status: "waiting_for_capture"},
			events: []string{"succeeded", "succeeded", "succeeded"}, wantActs: 1, wantAfter: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "concurrent duplicate webhooks", row: peRow{status: "pending"},
			concurrent: 8, wantActs: 1, wantAfter: 1, wantStatus: "succeeded", wantPaidAt: "set"},
		{name: "refunded stays refunded", row: peRow{status: "refunded", paidAt: t0},
			events: []string{"succeeded", "canceled"}, wantStatus: "refunded", wantPaidAt: "t0"},
		{name: "a paid payment is not applied again", row: peRow{status: "succeeded", paidAt: t0},
			events: []string{"succeeded", "pending", "canceled"}, wantStatus: "succeeded", wantPaidAt: "t0"},
		{name: "completed counts as paid", row: peRow{status: "completed", paidAt: t0},
			events: []string{"succeeded"}, wantStatus: "completed", wantPaidAt: "t0"},
		{name: "unpaid statuses are recorded", row: peRow{status: "pending"},
			events: []string{"waiting_for_capture", "canceled"}, wantStatus: "canceled", wantPaidAt: "null"},
		{name: "a failed activation rolls the move back; the next report applies it", row: peRow{status: "pending"}, failing: 1,
			events: []string{"succeeded", "succeeded", "succeeded"}, wantActs: 1, wantAfter: 1, wantStatus: "succeeded", wantPaidAt: "set", wantErrs: 1, wantFails: 1},
		{name: "a payment paid before keeps its first paid_at", row: peRow{status: "pending", paidAt: t0},
			events: []string{"succeeded"}, wantActs: 1, wantAfter: 1, wantStatus: "succeeded", wantPaidAt: "t0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.row
			r.id, r.ykID, r.user = 7, "pay-1", "user-1"
			d := newPEDB(&r)
			e := usePaymentFakes(t, d)
			e.failing = tt.failing
			var mu sync.Mutex
			errs := 0
			process := func(st string) {
				if err := processPaymentEvent(pePayment(st, nil)); err != nil {
					mu.Lock()
					errs++
					mu.Unlock()
				}
			}
			var wg sync.WaitGroup
			for i := 0; i < tt.concurrent; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); process("succeeded") }()
			}
			wg.Wait()
			for i, st := range tt.events {
				process(st)
				if tt.failing > 0 && i == 0 {
					if got := d.row(7); got.status != "pending" || got.activations != 0 || got.paidAt.IsZero() != tt.row.paidAt.IsZero() {
						t.Fatalf("after the failed activation: status %q, activations %d, paid_at %v — want everything rolled back",
							got.status, got.activations, got.paidAt)
					}
				}
			}
			got := d.row(7)
			if got.activations != tt.wantActs || e.after != tt.wantAfter || got.status != tt.wantStatus || errs != tt.wantErrs || got.failures != tt.wantFails {
				t.Fatalf("activations %d (want %d), after %d (want %d), status %q (want %q), errors %d (want %d), failures %d (want %d)",
					got.activations, tt.wantActs, e.after, tt.wantAfter, got.status, tt.wantStatus, errs, tt.wantErrs, got.failures, tt.wantFails)
			}
			switch {
			case tt.wantPaidAt == "t0" && !got.paidAt.Equal(t0),
				tt.wantPaidAt == "set" && got.paidAt.IsZero(),
				tt.wantPaidAt == "null" && !got.paidAt.IsZero():
				t.Fatalf("paid_at %v, want %s", got.paidAt, tt.wantPaidAt)
			}
		})
	}
}

// ─── Finding the row ──────────────────────────────────────────────────────

func TestPaymentFindsItsRow(t *testing.T) {
	ykMeta := map[string]string{"payment_db_id": "7", "plan_id": "3", "user_id": "user-1", "kind": "subscription"}
	tests := []struct {
		name     string
		rows     []*peRow
		meta     map[string]string
		wantErr  error
		wantRow  int64 // the row that was applied
		wantRows int
		check    func(t *testing.T, d *peDB, e *peEffects)
	}{
		{
			name: "by payment_db_id before the creator wrote the YooKassa id and metadata",
			rows: []*peRow{{id: 7, status: "pending", user: "user-1"}},
			meta: ykMeta, wantRow: 7, wantRows: 1,
			check: func(t *testing.T, d *peDB, e *peEffects) {
				if d.row(7).ykID != "pay-1" {
					t.Errorf("row 7 not linked to pay-1: %q", d.row(7).ykID)
				}
				if m := e.seen[0]; m.kind() != "subscription" || m.metaInt("plan_id") != 3 {
					t.Errorf("activation saw kind %q plan %d; want the YooKassa metadata", m.kind(), m.metaInt("plan_id"))
				}
			},
		},
		{
			name: "the row's own metadata wins over YooKassa's",
			rows: []*peRow{{id: 7, status: "pending", user: "user-1", ykID: "pay-1", meta: `{"plan_id": 5, "kind": "subscription"}`}},
			meta: ykMeta, wantRow: 7, wantRows: 1,
			check: func(t *testing.T, d *peDB, e *peEffects) {
				if e.seen[0].metaInt("plan_id") != 5 {
					t.Errorf("plan %d, want the row's 5", e.seen[0].metaInt("plan_id"))
				}
			},
		},
		{
			name: "a payment_db_id of another YooKassa payment is not trusted",
			rows: []*peRow{{id: 7, status: "pending", user: "user-2", ykID: "pay-other"}},
			meta: ykMeta, wantRow: 8, wantRows: 2,
		},
		{
			name:    "orphan: recovered as pending, then applied once",
			meta:    map[string]string{"user_id": "user-1", "plan_id": "3", "kind": "subscription"},
			wantRow: 1, wantRows: 1,
		},
		{
			name:    "orphan without a user: not ours",
			meta:    map[string]string{"kind": "subscription"},
			wantErr: errOrphanNotOurs,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newPEDB(tt.rows...)
			e := usePaymentFakes(t, d)
			for i := 0; i < 2; i++ { // the second report finds the row by YooKassa id and applies nothing
				err := processPaymentEvent(pePayment("succeeded", tt.meta))
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("report %d: err %v, want %v", i+1, err, tt.wantErr)
				}
			}
			if tt.wantErr != nil {
				return
			}
			d.mu.Lock()
			n := len(d.rows)
			d.mu.Unlock()
			got := d.row(tt.wantRow)
			if got.activations != 1 || got.status != "succeeded" || n != tt.wantRows || e.after != 1 {
				t.Fatalf("row %d: activations %d, status %q, after %d; %d rows (want %d)", tt.wantRow, got.activations, got.status, e.after, n, tt.wantRows)
			}
			if tt.check != nil {
				tt.check(t, d, e)
			}
		})
	}
}

func TestPaymentWebhookStatus(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		verify   error
		meta     map[string]string
		failing  int
		failOn   string
		wantCode int
		wantActs int64
	}{
		{name: "applied", id: "pay-1", wantCode: 200, wantActs: 1},
		{name: "no payment id", id: "", wantCode: 400},
		{name: "verification fails", id: "pay-1", verify: errors.New("timeout"), wantCode: 502},
		{name: "not ours: acknowledged", id: "pay-1", meta: map[string]string{"kind": "subscription"}, wantCode: 200},
		{name: "activation fails: 500, YooKassa repeats", id: "pay-1", failing: 1, wantCode: 500},
		{name: "database down: 500", id: "pay-1", failOn: "find-by-yookassa", wantCode: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := []*peRow{{id: 7, status: "pending", user: "user-1", ykID: "pay-1"}}
			if tt.meta != nil {
				rows = nil // a payment we do not have
			}
			d := newPEDB(rows...)
			d.failOn = tt.failOn
			e := usePaymentFakes(t, d)
			e.failing = tt.failing
			verify := func(id string) (*yookassa.Payment, error) {
				if tt.verify != nil {
					return nil, tt.verify
				}
				return pePayment("succeeded", tt.meta), nil
			}
			if code := paymentNotificationStatus(tt.id, verify); code != tt.wantCode {
				t.Fatalf("status %d, want %d", code, tt.wantCode)
			}
			if got := d.row(7).activations; got != tt.wantActs {
				t.Fatalf("activations %d, want %d", got, tt.wantActs)
			}
		})
	}
}

// ─── Failure cap, alert, sync ─────────────────────────────────────────────

// Five failed activations raise one alert (Russian, with payment and user id); the sync
// cron then leaves the payment alone; a later successful apply resolves the alert.
func TestActivationFailuresAlertAndCap(t *testing.T) {
	d := newPEDB(&peRow{id: 7, status: "pending", user: "user-1", ykID: "pay-1"})
	e := usePaymentFakes(t, d)
	e.failing = paymentActivationMaxFailures
	for i := 0; i < paymentActivationMaxFailures; i++ {
		if err := processPaymentEvent(pePayment("succeeded", nil)); err == nil {
			t.Fatalf("attempt %d: want an error", i+1)
		}
		if i < paymentActivationMaxFailures-1 && len(e.alerts) != 0 {
			t.Fatalf("alert after %d failures: %q", i+1, e.alerts)
		}
	}
	if len(e.alerts) != 1 || !strings.HasPrefix(e.alerts[0], "payment-activation-failed-7 | critical | Оплата не активирована | Платёж #7 (пользователь user-1)") {
		t.Fatalf("alerts %q", e.alerts)
	}
	if got := d.row(7); got.failures != paymentActivationMaxFailures || got.status != "pending" || got.activations != 0 {
		t.Fatalf("row %+v", got)
	}

	var synced []string
	prevClient, prevProcess := newPaymentSyncClient, syncProcessPayment
	t.Cleanup(func() { newPaymentSyncClient, syncProcessPayment = prevClient, prevProcess })
	newPaymentSyncClient = func() (paymentGetter, error) { return fakeGetter{}, nil }
	syncProcessPayment = func(p *yookassa.Payment) error { synced = append(synced, p.ID); return nil }
	syncPendingPayments()
	if len(synced) != 0 {
		t.Fatalf("sync retried a capped payment: %q", synced)
	}

	if err := processPaymentEvent(pePayment("succeeded", nil)); err != nil { // e.g. «Проверить статус» after the fix
		t.Fatal(err)
	}
	if got := d.row(7); got.activations != 1 || len(e.resolve) != 1 || e.resolve[0] != "payment-activation-failed-7" {
		t.Fatalf("activations %d, resolved %q", got.activations, e.resolve)
	}
}

type fakeGetter struct{ gone map[string]bool }

func (g fakeGetter) GetPayment(id string) (*yookassa.Payment, error) {
	if g.gone[id] {
		return nil, &yookassa.APIError{HTTPStatus: 404, Code: "not_found"}
	}
	return &yookassa.Payment{ID: id, Status: "succeeded"}, nil
}

// The sync re-checks fresh payments first, the ones that failed before after them, never
// the capped ones; a payment YooKassa forgot is canceled only if it was never paid.
func TestPaymentSyncOrderAndStaleCancel(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour)
	d := newPEDB(
		&peRow{id: 1, ykID: "capped", status: "pending", failures: 5, created: old.Add(-time.Hour)},
		&peRow{id: 2, ykID: "failed-twice", status: "pending", failures: 2, created: old.Add(-30 * time.Minute)},
		&peRow{id: 3, ykID: "fresh-newer", status: "pending", created: old.Add(time.Minute)},
		&peRow{id: 4, ykID: "fresh-older", status: "waiting_for_capture", created: old},
		&peRow{id: 5, ykID: "paid", status: "succeeded", created: old},
		&peRow{id: 6, ykID: "gone-paid", status: "pending", paidAt: old, created: old},
		&peRow{id: 7, ykID: "gone-unpaid", status: "pending", created: old},
	)
	usePaymentFakes(t, d)
	var synced []string
	prevClient, prevProcess := newPaymentSyncClient, syncProcessPayment
	t.Cleanup(func() { newPaymentSyncClient, syncProcessPayment = prevClient, prevProcess })
	newPaymentSyncClient = func() (paymentGetter, error) {
		return fakeGetter{gone: map[string]bool{"gone-paid": true, "gone-unpaid": true}}, nil
	}
	syncProcessPayment = func(p *yookassa.Payment) error { synced = append(synced, p.ID); return nil }
	syncPendingPayments()
	want := []string{"fresh-older", "fresh-newer", "failed-twice"}
	if strings.Join(synced, ",") != strings.Join(want, ",") {
		t.Fatalf("synced %q, want %q", synced, want)
	}
	if d.row(6).status != "pending" || d.row(7).status != "canceled" {
		t.Fatalf("stale cancel: paid one %q (want pending), unpaid one %q (want canceled)", d.row(6).status, d.row(7).status)
	}
}

func TestPaymentSQLGuards(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for name, tc := range map[string]struct{ q, want string }{
		"claim":        {paymentClaimPaidSQL, "WHERE id = $1 AND status NOT IN ('succeeded', 'completed', 'refunded') RETURNING activation_failures"},
		"unpaid":       {paymentSetUnpaidStatusSQL, "WHERE id = $1 AND status NOT IN ('succeeded', 'completed', 'refunded')"},
		"stale cancel": {paymentStaleCancelSQL, "WHERE id = $1 AND status IN ('pending', 'waiting_for_capture') AND paid_at IS NULL"},
		"sync":         {paymentSyncSelectSQL, "AND activation_failures < $3 ORDER BY activation_failures ASC, created_at ASC LIMIT $2"},
	} {
		if !strings.HasSuffix(norm(tc.q), tc.want) {
			t.Errorf("%s: %s", name, norm(tc.q))
		}
	}
	for name, q := range map[string]string{"details": paymentDetailsSQL} {
		if peStatusAssign.MatchString(norm(q)) || strings.Contains(q, "paid_at") {
			t.Errorf("%s writes status or paid_at: %s", name, norm(q))
		}
	}
}

// Migration 000060 (one Exec, idempotent) carries the columns and the "applied once"
// backstop indexes this code relies on; down removes them.
func TestMigration000060(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("../../migrations/000060_payment_refunds_idempotence_key." + name + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(b)), " ")
	}
	up, down := read("up"), read("down")
	for _, want := range []string{
		"ALTER TABLE payment_refunds ADD COLUMN IF NOT EXISTS idempotence_key TEXT;",
		"ALTER TABLE payments ADD COLUMN IF NOT EXISTS activation_failures INT NOT NULL DEFAULT 0;",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_wallet_transactions_payment_id ON tenant_wallet_transactions (payment_id) WHERE payment_id IS NOT NULL;",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_payments_yookassa_payment_id ON payments (yookassa_payment_id) WHERE yookassa_payment_id IS NOT NULL AND yookassa_payment_id <> '';",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("000060 up lacks %q", want)
		}
	}
	for _, want := range []string{
		"DROP INDEX IF EXISTS uq_payments_yookassa_payment_id;", "DROP INDEX IF EXISTS uq_tenant_wallet_transactions_payment_id;",
		"ALTER TABLE payments DROP COLUMN IF EXISTS activation_failures;", "ALTER TABLE payment_refunds DROP COLUMN IF EXISTS idempotence_key;",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("000060 down lacks %q", want)
		}
	}
}

// ─── Paid but not applied ─────────────────────────────────────────────────

// A paid payment that changes nothing (no plan/user, plan deleted, downgrade refused,
// B2B without tenant or with a bad amount) stays paid, keeps the reason in its metadata —
// written in the claim transaction — and, after COMMIT, raises payment-unapplied-<id> with
// the payment, who paid, why, and what to do. The payment sync cron re-raises exactly that
// alert from the row. A failed mark fails the claim: nothing is paid, nothing raised.
func TestPaidButNotAppliedAlerts(t *testing.T) {
	basic := &biz.Plan{ID: 3, Name: "SOCKS Basic", DurationDays: 30}
	tests := []struct {
		name        string
		user, meta  string
		amount      string
		plan        *biz.Plan
		planErr     error
		activateErr error
		failOn      string
		wantReason  string   // metadata.unapplied_reason; "" = no mark
		wantAlert   []string // substrings; nil = no alert
		wantActs    int
		wantErr     bool
	}{
		{name: "no plan in the payment", user: "user-1", meta: `{"kind": "subscription"}`,
			wantReason: "в платеже нет тарифа или пользователя",
			wantAlert:  []string{"Платёж #7 (пользователь user-1, 300.00 ₽)", "в платеже нет тарифа или пользователя", "подписка не изменена"}},
		{name: "no user", meta: `{"kind": "subscription", "plan_id": 3}`,
			wantReason: "в платеже нет тарифа или пользователя", wantAlert: []string{"пользователь не указан", "нет тарифа или пользователя"}},
		{name: "plan deleted", user: "user-1", meta: `{"plan_id": 3}`, planErr: sql.ErrNoRows,
			wantReason: "тариф #3 больше не существует", wantAlert: []string{"тариф #3 больше не существует", "подписка не изменена"}},
		{name: "downgrade refused", user: "user-1", meta: `{"plan_id": 3}`, plan: basic, activateErr: biz.ErrDowngradeNotAllowed,
			wantReason: "тариф «SOCKS Basic» дешевле текущего — понижение тарифа не применяется",
			wantAlert:  []string{"тариф «SOCKS Basic» дешевле текущего", "подписка не изменена"}, wantActs: 1},
		{name: "B2B without tenant_id", meta: `{"kind": "tenant_topup"}`,
			wantReason: "в платеже tenant_topup нет tenant_id (привяжите его к компании)",
			wantAlert:  []string{"компания не указана", "нет tenant_id", "баланс компании не изменён"}},
		{name: "B2B with a bad amount", meta: `{"kind": "b2b_invoice", "tenant_id": 5}`, amount: "abc",
			wantReason: `некорректная сумма "abc"`, wantAlert: []string{"компания #5", "некорректная сумма", "баланс компании не изменён"}},
		{name: "applied: no alert", user: "user-1", meta: `{"plan_id": 3}`, plan: basic, wantActs: 1},
		{name: "donation: nothing to apply, no alert", user: "user-1", meta: `{"kind": "donation"}`},
		{name: "the mark fails: the claim rolls back", user: "user-1", meta: `{"kind": "subscription"}`, failOn: "mark-unapplied", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newPEDB(&peRow{id: 7, status: "pending", user: tt.user, ykID: "pay-1", meta: tt.meta})
			d.failOn = tt.failOn
			e := usePaymentFakes(t, d)
			prev := []any{paidPaymentEffects, getPlanByID, activatePlanTx, subscriptionActivated}
			t.Cleanup(func() {
				paidPaymentEffects = prev[0].(func(context.Context, *sql.Tx, *yookassa.Payment, paymentRow) (func(), error))
				getPlanByID = prev[1].(func(int) (*biz.Plan, error))
				activatePlanTx = prev[2].(func(context.Context, *sql.Tx, string, int, int, int, string) error)
				subscriptionActivated = prev[3].(func(paymentRow, *biz.Plan, int, bool, string))
			})
			paidPaymentEffects = applyPaidPaymentEffects
			getPlanByID = func(int) (*biz.Plan, error) { return tt.plan, tt.planErr }
			acts, notified := 0, 0
			activatePlanTx = func(context.Context, *sql.Tx, string, int, int, int, string) error { acts++; return tt.activateErr }
			subscriptionActivated = func(paymentRow, *biz.Plan, int, bool, string) { notified++ }
			var journalAtAlert []string
			raise := raiseUnappliedAlert
			raiseUnappliedAlert = func(key string, level AlertLevel, title, details string) {
				d.mu.Lock()
				journalAtAlert = append([]string(nil), d.journal...)
				d.mu.Unlock()
				raise(key, level, title, details)
			}

			pay := pePayment("succeeded", nil)
			if tt.amount != "" {
				pay.Amount.Value = tt.amount
			}
			err := processPaymentEvent(pay)
			if tt.wantErr {
				got := d.row(7)
				if err == nil || got.status != "pending" || len(e.alerts) != 0 || got.failures != 1 || strings.Contains(got.meta, "unapplied_reason") {
					t.Fatalf("err %v, row %+v, alerts %q; want an error, the claim rolled back, no alert", err, got, e.alerts)
				}
				return
			}
			if err != nil {
				t.Fatalf("processPaymentEvent: %v", err)
			}
			if got := d.row(7).status; got != "succeeded" {
				t.Fatalf("status %q, want succeeded (money received)", got)
			}
			if acts != tt.wantActs {
				t.Fatalf("activation calls %d, want %d", acts, tt.wantActs)
			}
			row := d.row(7)
			if reason, _ := peMeta(&row)["unapplied_reason"].(string); reason != tt.wantReason {
				t.Fatalf("unapplied_reason %q, want %q", reason, tt.wantReason)
			}
			if tt.wantAlert == nil {
				if len(e.alerts) != 0 {
					t.Fatalf("unexpected alert %q", e.alerts)
				}
				if tt.plan != nil && notified != 1 {
					t.Fatalf("activation notifications %d, want 1", notified)
				}
				return
			}
			if len(e.alerts) != 1 || !strings.HasPrefix(e.alerts[0], "payment-unapplied-7 | critical | Оплата получена, но не применена | ") {
				t.Fatalf("alerts %q", e.alerts)
			}
			for _, want := range append(tt.wantAlert, "Деньги получены", "решите вручную: вернуть или применить") {
				if !strings.Contains(e.alerts[0], want) {
					t.Errorf("alert lacks %q: %s", want, e.alerts[0])
				}
			}
			n := len(journalAtAlert)
			if n == 0 || journalAtAlert[n-1] != "COMMIT" {
				t.Errorf("alert raised before COMMIT (journal %q)", journalAtAlert)
			}
			if i := slices.Index(journalAtAlert, "tx:mark-unapplied"); i < 0 || i > slices.Index(journalAtAlert, "COMMIT") {
				t.Errorf("mark not written inside the claim transaction (journal %q)", journalAtAlert)
			}

			// The cron raises the same alert again from what the row keeps.
			syncUnappliedPaymentAlerts()
			if tt.amount == "" && (len(e.alerts) != 2 || e.alerts[1] != e.alerts[0]) {
				t.Fatalf("cron re-raise differs from the claim's alert:\n%q", e.alerts)
			}
		})
	}
}

// The payment sync cron follows the mark: every paid payment that carries it raises its
// alert again (whatever the Telegram outcome of the first raise was); an open alert whose
// payment lost the mark or is no longer paid is resolved; the open alerts are read before
// the payments; a failed read changes nothing.
func TestUnappliedAlertsFollowTheMark(t *testing.T) {
	rows := func() []*peRow {
		return []*peRow{
			{id: 7, status: "succeeded", user: "user-1", meta: `{"kind": "subscription", "plan_id": 3, "unapplied_reason": "тариф #3 больше не существует"}`},
			{id: 8, status: "completed", amount: "1500.00", meta: `{"kind": "b2b_invoice", "tenant_id": 5, "unapplied_reason": "некорректная сумма"}`},
			{id: 9, status: "refunded", user: "user-2", meta: `{"plan_id": 3, "unapplied_reason": "тариф #3 больше не существует"}`},
			{id: 10, status: "succeeded", user: "user-3", meta: `{"plan_id": 3}`},
			{id: 11, status: "pending", user: "user-4", meta: `{"unapplied_reason": "x"}`},
		}
	}
	open := []string{"payment-unapplied-10", "payment-unapplied-12", "payment-unapplied-7", "payment-unapplied-9"}

	t.Run("raise the marked, resolve the rest", func(t *testing.T) {
		d := newPEDB(rows()...)
		e := usePaymentFakes(t, d)
		var journalAtOpen []string
		openUnappliedAlerts = func() []string {
			d.mu.Lock()
			journalAtOpen = append([]string(nil), d.journal...)
			d.mu.Unlock()
			return open
		}
		syncUnappliedPaymentAlerts()
		want := []string{
			"payment-unapplied-7 | critical | Оплата получена, но не применена | Платёж #7 (пользователь user-1, 300.00 ₽): тариф #3 больше не существует. " +
				"Деньги получены, подписка не изменена — решите вручную: вернуть или применить.",
			"payment-unapplied-8 | critical | Оплата получена, но не применена | Платёж #8 (компания #5, 1500.00 ₽): некорректная сумма. " +
				"Деньги получены, баланс компании не изменён — решите вручную: вернуть или применить.",
		}
		if !slices.Equal(e.alerts, want) {
			t.Errorf("raised\n%q\nwant\n%q", e.alerts, want)
		}
		if wantResolved := []string{"payment-unapplied-10", "payment-unapplied-12", "payment-unapplied-9"}; !slices.Equal(e.resolve, wantResolved) {
			t.Errorf("resolved %q, want %q", e.resolve, wantResolved)
		}
		if slices.Contains(journalAtOpen, "unapplied-select") {
			t.Errorf("open alerts read after the payments (journal %q)", journalAtOpen)
		}
	})

	t.Run("a failed read raises and resolves nothing", func(t *testing.T) {
		d := newPEDB(rows()...)
		d.failOn = "unapplied-select"
		e := usePaymentFakes(t, d)
		e.open = open
		syncUnappliedPaymentAlerts()
		if len(e.alerts) != 0 || len(e.resolve) != 0 {
			t.Fatalf("alerts %q, resolved %q; want nothing", e.alerts, e.resolve)
		}
	})
}

// ─── «Проверить статус» ───────────────────────────────────────────────────

// «Проверить статус» shows an apply failure to the admin instead of a cheerful 200.
func TestCheckStatusSurfacesApplyError(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failing  int
		wantCode int
		wantText string
	}{
		{"apply fails: 500 with the reason", 1, http.StatusInternalServerError, "записать или применить платёж не удалось: apply paid payment 7"},
		{"applied: 200", 0, http.StatusOK, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newPEDB(&peRow{id: 7, status: "pending", user: "user-1", ykID: "pay-1"})
			e := usePaymentFakes(t, d)
			e.failing = tc.failing
			prevStatus, prevRefund := newPaymentStatusClient, newRefundClient
			t.Cleanup(func() { newPaymentStatusClient, newRefundClient = prevStatus, prevRefund })
			newPaymentStatusClient = func() (paymentGetter, error) { return fakeGetter{}, nil }
			newRefundClient = func() (refundAPI, error) { return nil, errors.New("refunds not under test") }

			rec := httptest.NewRecorder()
			handleAdminPaymentCheckStatus(rec, httptest.NewRequest(http.MethodPost, "/api/admin/payments/7/check-status", nil))
			var m map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &m)
			msg, _ := m["error"].(string)
			if rec.Code != tc.wantCode || !strings.Contains(msg, tc.wantText) {
				t.Fatalf("%d %v; want %d %q", rec.Code, m, tc.wantCode, tc.wantText)
			}
			if tc.wantCode == http.StatusOK && (m["yookassa_status"] != "succeeded" || d.row(7).activations != 1) {
				t.Fatalf("200 body %v, activations %d", m, d.row(7).activations)
			}
		})
	}
}
