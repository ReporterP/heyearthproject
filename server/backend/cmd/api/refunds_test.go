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
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─── Pure rules ───────────────────────────────────────────────────────────

func TestParseRefundAmount(t *testing.T) {
	tests := []struct {
		rub     float64
		want    int64
		wantErr string
	}{
		{0, 0, ""},
		{249.5, 24950, ""},
		{0.29, 29, ""}, // 0.29*100 = 28.999999999999996
		{1e10, 1e12, ""},
		{1e300, 1<<63 - 1, ""}, // absurd amounts become "too much", never an overflowed int64
		{-1, 0, "отрицательной"},
		{10.005, 0, "до копейки"},
	}
	for _, tt := range tests {
		got, refusal := parseRefundAmount(tt.rub)
		switch {
		case tt.wantErr != "" && (refusal == nil || refusal.status != http.StatusBadRequest || !strings.Contains(refusal.msg, tt.wantErr)):
			t.Errorf("parseRefundAmount(%v) = %d, %v; want 400 %q", tt.rub, got, refusal, tt.wantErr)
		case tt.wantErr == "" && (refusal != nil || got != tt.want):
			t.Errorf("parseRefundAmount(%v) = %d, %v; want %d", tt.rub, got, refusal, tt.want)
		}
	}
}

func TestParseYKAmount(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"249.50", 24950, true}, {"249.5", 24950, true}, {"249", 24900, true}, {"0.01", 1, true},
		{"", 0, false}, {"1.234", 0, false}, {"-1.00", 0, false}, {"abc", 0, false}, {"1.x", 0, false}, {".50", 0, false},
	}
	for _, tt := range tests {
		got, err := parseYKAmount(tt.in)
		if (err == nil) != tt.ok || got != tt.want {
			t.Errorf("parseYKAmount(%q) = %d, %v", tt.in, got, err)
		}
	}
}

func TestDecideRefund(t *testing.T) {
	paid := refundState{YookassaID: "pay-1", Status: "succeeded", AmountKop: 30000}
	with := func(f func(*refundState)) refundState { s := paid; f(&s); return s }
	open := &refundRow{ID: 4, AmountKop: 10000, Description: "first", Key: "refund-7-4-1"}
	const left = "(остаток с учётом незавершённых возвратов)"
	const inFlight = "Предыдущий возврат по этому платежу ещё обрабатывается — проверьте статус"

	tests := []struct {
		name       string
		st         refundState
		request    int64
		wantAmount int64
		wantResume *refundRow
		wantStatus int
		wantErr    string
	}{
		{"full refund of an untouched payment", paid, 0, 30000, nil, 0, ""},
		{"partial refund", paid, 10000, 10000, nil, 0, ""},
		{"the whole amount explicitly", paid, 30000, 30000, nil, 0, ""},
		{"completed is paid too", with(func(s *refundState) { s.Status = "completed" }), 0, 30000, nil, 0, ""},
		{"a pending refund reduces what is left", with(func(s *refundState) { s.HeldKop = 10000 }), 25000, 0, nil,
			http.StatusBadRequest, "Можно вернуть не больше 200,00 ₽ " + left},
		{"exactly what is left after a pending refund", with(func(s *refundState) { s.HeldKop = 10000 }), 20000, 20000, nil, 0, ""},
		{"amount 0 means what is left, not the payment", with(func(s *refundState) { s.HeldKop = 10050 }), 0, 19950, nil, 0, ""},
		{"more than the payment", paid, 30001, 0, nil, http.StatusBadRequest, "Можно вернуть не больше 300,00 ₽ " + left},
		{"nothing left, amount 0", with(func(s *refundState) { s.HeldKop = 30000 }), 0, 0, nil, http.StatusBadRequest, "Можно вернуть не больше 0,00 ₽ " + left},
		{"over-held payment never shows a negative amount", with(func(s *refundState) { s.HeldKop = 40000 }), 1, 0, nil, http.StatusBadRequest, "не больше 0,00 ₽"},
		{"pending payment", with(func(s *refundState) { s.Status = "pending" }), 0, 0, nil, http.StatusBadRequest, "Вернуть можно только оплаченный платёж (статус платежа: pending)"},
		{"waiting_for_capture", with(func(s *refundState) { s.Status = "waiting_for_capture" }), 100, 0, nil, http.StatusBadRequest, "только оплаченный"},
		{"canceled payment", with(func(s *refundState) { s.Status = "canceled" }), 100, 0, nil, http.StatusBadRequest, "только оплаченный"},
		{"already refunded", with(func(s *refundState) { s.Status = "refunded" }), 0, 0, nil, http.StatusBadRequest, "только оплаченный"},
		{"no YooKassa id", with(func(s *refundState) { s.YookassaID = "" }), 0, 0, nil, http.StatusBadRequest, "нет идентификатора ЮKassa"},
		{"open reservation, same amount: repeat it", with(func(s *refundState) { s.HeldKop, s.Open = 10000, open }), 10000, 0, open, 0, ""},
		{"open reservation, other amount", with(func(s *refundState) { s.HeldKop, s.Open = 10000, open }), 5000, 0, nil, http.StatusConflict, inFlight},
		{"open reservation, amount 0", with(func(s *refundState) { s.HeldKop, s.Open = 10000, open }), 0, 0, nil, http.StatusConflict, inFlight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, resume, refusal := decideRefund(tt.st, tt.request)
			if tt.wantStatus != 0 {
				if refusal == nil || refusal.status != tt.wantStatus || !strings.Contains(refusal.msg, tt.wantErr) {
					t.Fatalf("got %d %v %v; want %d %q", amount, resume, refusal, tt.wantStatus, tt.wantErr)
				}
				return
			}
			if refusal != nil || amount != tt.wantAmount || resume != tt.wantResume {
				t.Fatalf("got %d %v %v; want %d %v", amount, resume, refusal, tt.wantAmount, tt.wantResume)
			}
		})
	}
}

func TestRefundIdempotenceKey(t *testing.T) {
	base := refundIdempotenceKey(7, 3, 1760000000)
	tests := []struct {
		name       string
		other      string
		wantEquals bool
	}{
		{"the same attempt again", refundIdempotenceKey(7, 3, 1760000000), true},
		{"the next attempt of the payment", refundIdempotenceKey(7, 4, 1760000100), false},
		{"the same row id reused after a restore", refundIdempotenceKey(7, 3, 1760500000), false},
		{"another payment", refundIdempotenceKey(8, 3, 1760000000), false},
	}
	for _, tt := range tests {
		if (base == tt.other) != tt.wantEquals {
			t.Errorf("%s: %q vs %q", tt.name, base, tt.other)
		}
	}
	if base != "refund-7-3-1760000000" {
		t.Errorf("key = %q", base)
	}
	if long := refundIdempotenceKey(2147483647, 9223372036854775807, 9999999999); len(long) > 64 || strings.Trim(long, "refund-0123456789") != "" {
		t.Errorf("longest key %q (%d chars) must be ≤64 ASCII", long, len(long))
	}
}

func TestClassifyRefundCall(t *testing.T) {
	invalid := &yookassa.APIError{HTTPStatus: 400, Code: "invalid_request"}
	tests := []struct {
		name   string
		ref    *yookassa.Refund
		err    error
		repeat bool
		want   refundCallOutcome
	}{
		{"refund returned", &yookassa.Refund{ID: "rf-1"}, nil, false, refundCreated},
		{"repeat: refund returned", &yookassa.Refund{ID: "rf-1"}, nil, true, refundCreated},
		{"202 accepted, no refund yet", &yookassa.Refund{}, nil, false, refundUnknown},
		{"new: 400 invalid_request", nil, invalid, false, refundRejected},
		{"new: wrapped 401", nil, fmt.Errorf("x: %w", &yookassa.APIError{HTTPStatus: 401, Code: "invalid_credentials"}), false, refundRejected},
		{"new: 429 too many requests", nil, &yookassa.APIError{HTTPStatus: 429, Code: "too_many_requests"}, false, refundUnknown},
		{"repeat: 429", nil, &yookassa.APIError{HTTPStatus: 429, Code: "too_many_requests"}, true, refundUnknown},
		{"repeat: 400 invalid_request", nil, invalid, true, refundRejected},
		{"repeat: 403 forbidden", nil, &yookassa.APIError{HTTPStatus: 403, Code: "forbidden"}, true, refundUnknown},
		{"repeat: 404 not_found", nil, &yookassa.APIError{HTTPStatus: 404, Code: "not_found"}, true, refundUnknown},
		{"repeat: 400 without a code", nil, &yookassa.APIError{HTTPStatus: 400}, true, refundUnknown},
		{"500 internal", nil, &yookassa.APIError{HTTPStatus: 500}, false, refundUnknown},
		{"timeout", nil, errors.New("http do: context deadline exceeded (Client.Timeout exceeded)"), false, refundUnknown},
	}
	for _, tt := range tests {
		if got := classifyRefundCall(tt.ref, tt.err, tt.repeat); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestMergeRefundStatus(t *testing.T) {
	tests := []struct{ a, b, want string }{
		{"pending", "succeeded", "succeeded"}, {"succeeded", "pending", "succeeded"}, // a stale answer never undoes success
		{"failed", "pending", "pending"}, {"failed", "succeeded", "succeeded"}, // YooKassa has the refund: not failed
		{"pending", "canceled", "canceled"}, {"canceled", "pending", "canceled"}, {"pending", "pending", "pending"},
	}
	for _, tt := range tests {
		if got := mergeRefundStatus(tt.a, tt.b); got != tt.want {
			t.Errorf("merge(%s, %s) = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestRefundSQL(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for name, q := range map[string]string{"by id": refundLockPaymentSQL, "by YooKassa id": refundLockPaymentByYookassaSQL} {
		if !strings.HasSuffix(norm(q), "FOR UPDATE") {
			t.Errorf("payment lock %s: %s", name, norm(q))
		}
	}
	if !strings.Contains(norm(refundHeldSQL), "status IN ('pending', 'succeeded')") {
		t.Errorf("held refunds: %s", norm(refundHeldSQL))
	}
	if !strings.HasSuffix(norm(refundLinkSQL), "WHERE id = $1 AND (yookassa_refund_id IS NULL OR yookassa_refund_id = $2)") {
		t.Errorf("link: %s", norm(refundLinkSQL))
	}
	// Rows without a refund id are reservations only with an idempotence key.
	if !strings.HasSuffix(norm(refundMarkFailedSQL), "AND yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL") {
		t.Errorf("mark failed: %s", norm(refundMarkFailedSQL))
	}
	if !strings.Contains(norm(refundSweepPaymentsSQL), "(yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL AND created_at < NOW() - INTERVAL '10 minutes')") {
		t.Errorf("sweep: %s", norm(refundSweepPaymentsSQL))
	}
}

// ─── Fakes ────────────────────────────────────────────────────────────────

// rfRow is a payment_refunds row.
type rfRow struct {
	id        int64
	refundID  string
	status    string
	amountKop int64
	desc      string
	key       string
	created   time.Time
	updated   time.Time
}

// rfDB is PostgreSQL for one payment (id 7, YooKassa id pay-1) and its refunds: SELECT …
// FOR UPDATE inside a transaction takes the payment row lock until COMMIT/ROLLBACK,
// yookassa_refund_id is unique, ROLLBACK undoes the transaction's writes.
type rfDB struct {
	missing     bool
	ykPayment   string
	status      string
	amountKop   int64
	refundedKop int64

	payLock sync.Mutex

	mu        sync.Mutex
	rows      []rfRow
	nextID    int64
	journal   []string
	openTx    int
	failOn    string        // statement label that fails
	readPause time.Duration // after the open-reservation read: widens the race window
	clock     time.Time     // created_at of new rows (zero: now)
}

func newRFDB() *rfDB { return &rfDB{ykPayment: "pay-1", status: "succeeded", amountKop: 30000} }

func (d *rfDB) Connect(context.Context) (driver.Conn, error) { return &rfConn{d: d}, nil }
func (d *rfDB) Driver() driver.Driver                        { return rfDriver{} }

type rfDriver struct{}

func (rfDriver) Open(string) (driver.Conn, error) { return nil, errors.New("rf driver: no DSN") }

type rfSnapshot struct {
	rows                []rfRow
	status              string
	refundedKop, nextID int64
}

type rfConn struct {
	d      *rfDB
	inTx   bool
	locked bool
	snap   *rfSnapshot
}

func (c *rfConn) Prepare(q string) (driver.Stmt, error) {
	return nil, fmt.Errorf("rf driver: prepare not supported: %s", q)
}
func (c *rfConn) Close() error { return nil }
func (c *rfConn) Begin() (driver.Tx, error) {
	c.d.mu.Lock()
	c.inTx, c.snap = true, nil
	c.d.openTx++
	c.d.journal = append(c.d.journal, "BEGIN")
	c.d.mu.Unlock()
	return c, nil
}
func (c *rfConn) Commit() error { c.end("COMMIT", false); return nil }
func (c *rfConn) Rollback() error {
	c.end("ROLLBACK", true)
	return nil
}

func (c *rfConn) end(what string, undo bool) {
	d := c.d
	d.mu.Lock()
	if undo && c.snap != nil {
		d.rows, d.status, d.refundedKop, d.nextID = c.snap.rows, c.snap.status, c.snap.refundedKop, c.snap.nextID
	}
	c.inTx, c.snap = false, nil
	d.openTx--
	d.journal = append(d.journal, what)
	d.mu.Unlock()
	if c.locked {
		c.locked = false
		d.payLock.Unlock()
	}
}

// write snapshots the state before the transaction's first write; caller holds d.mu.
func (c *rfConn) write() {
	if c.inTx && c.snap == nil {
		d := c.d
		c.snap = &rfSnapshot{rows: append([]rfRow(nil), d.rows...), status: d.status, refundedKop: d.refundedKop, nextID: d.nextID}
	}
}

func (c *rfConn) note(label string) error {
	e := label
	if c.inTx {
		e = "tx:" + label
	}
	c.d.journal = append(c.d.journal, e)
	if c.d.failOn == label {
		return fmt.Errorf("rf driver: injected failure at %s", label)
	}
	return nil
}

func (d *rfDB) now() time.Time {
	if !d.clock.IsZero() {
		return d.clock
	}
	return time.Now()
}

func (d *rfDB) find(id int64) *rfRow {
	for i := range d.rows {
		if d.rows[i].id == id {
			return &d.rows[i]
		}
	}
	return nil
}

func (d *rfDB) refundTaken(refundID string, except int64) bool {
	for _, r := range d.rows {
		if r.refundID != "" && r.refundID == refundID && r.id != except {
			return true
		}
	}
	return false
}

func rowValues(r rfRow) []driver.Value {
	return []driver.Value{r.id, r.refundID, r.status, r.amountKop, r.desc, r.key, r.created}
}

var rfRowCols = []string{"id", "refund", "status", "amount", "desc", "key", "created"}

func (c *rfConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	if strings.Contains(q, "FOR UPDATE") {
		if !c.inTx {
			return nil, errors.New("rf driver: FOR UPDATE outside a transaction locks nothing")
		}
		if !c.locked {
			d.payLock.Lock() // waits while another transaction holds the payment
			c.locked = true
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	one := func(cols []string, vals ...driver.Value) *rfRows {
		return &rfRows{cols: cols, rows: [][]driver.Value{vals}}
	}
	none := func(cols []string) *rfRows { return &rfRows{cols: cols} }
	switch {
	case strings.Contains(q, "FROM payments WHERE id = $1 FOR UPDATE"):
		if err := c.note("lock"); err != nil {
			return nil, err
		}
		if d.missing {
			return none([]string{"yk", "status", "amount"}), nil
		}
		return one([]string{"yk", "status", "amount"}, d.ykPayment, d.status, d.amountKop), nil
	case strings.Contains(q, "FROM payments WHERE yookassa_payment_id = $1 FOR UPDATE"):
		if err := c.note("lock-by-yookassa"); err != nil {
			return nil, err
		}
		if d.missing || args[0].Value != d.ykPayment {
			return none([]string{"id"}), nil
		}
		return one([]string{"id"}, int64(7)), nil
	case strings.HasPrefix(q, "SELECT COALESCE(yookassa_payment_id, '') FROM payments WHERE id = $1"):
		if err := c.note("payment"); err != nil {
			return nil, err
		}
		return one([]string{"yk"}, d.ykPayment), nil
	case strings.Contains(q, "SUM(ROUND(amount * 100))"):
		if err := c.note("held"); err != nil {
			return nil, err
		}
		m := regexp.MustCompile(`status IN \(([^)]*)\)`).FindStringSubmatch(q)
		if m == nil {
			return nil, fmt.Errorf("rf driver: held query without a status list: %s", q)
		}
		var sum int64
		for _, r := range d.rows {
			if strings.Contains(m[1], "'"+r.status+"'") {
				sum += r.amountKop
			}
		}
		return one([]string{"sum"}, sum), nil
	case strings.HasPrefix(q, "INSERT INTO payment_refunds") && strings.Contains(q, "RETURNING id"):
		if err := c.note("reserve"); err != nil {
			return nil, err
		}
		c.write()
		d.nextID++
		now := d.now()
		d.rows = append(d.rows, rfRow{id: d.nextID, amountKop: mustKop(args[1].Value), status: "pending", desc: args[2].Value.(string), created: now, updated: now})
		return one([]string{"id", "epoch"}, d.nextID, now.Unix()), nil
	case strings.Contains(q, "SELECT DISTINCT payment_id FROM payment_refunds"):
		if err := c.note("sweep"); err != nil {
			return nil, err
		}
		for _, r := range d.rows {
			keyOK := r.key != "" || !strings.Contains(q, "yookassa_refund_id IS NULL AND idempotence_key IS NOT NULL")
			if r.status == "pending" && ((r.refundID != "" && time.Since(r.updated) > 3*time.Minute) || (r.refundID == "" && keyOK && time.Since(r.created) > 10*time.Minute)) {
				return one([]string{"payment_id"}, int64(7)), nil
			}
		}
		return none([]string{"payment_id"}), nil
	case strings.Contains(q, "FROM payment_refunds WHERE id = $1"):
		if err := c.note("row-by-id"); err != nil {
			return nil, err
		}
		if r := d.find(args[0].Value.(int64)); r != nil {
			return one(rfRowCols, rowValues(*r)...), nil
		}
		return none(rfRowCols), nil
	case strings.Contains(q, "FROM payment_refunds WHERE yookassa_refund_id = $1"):
		if err := c.note("row-by-refund-id"); err != nil {
			return nil, err
		}
		for _, r := range d.rows {
			if r.refundID != "" && r.refundID == args[0].Value.(string) {
				return one(rfRowCols, rowValues(r)...), nil
			}
		}
		return none(rfRowCols), nil
	case strings.Contains(q, "INTERVAL '23 hours'") && strings.Contains(q, "LIMIT 1"):
		if err := c.note("open-reservation"); err != nil {
			return nil, err
		}
		if d.readPause > 0 {
			defer func() { d.mu.Unlock(); time.Sleep(d.readPause); d.mu.Lock() }()
		}
		for _, r := range d.rows {
			if r.status == "pending" && r.refundID == "" && r.key != "" && time.Since(r.created) < 23*time.Hour {
				return one(rfRowCols, rowValues(r)...), nil
			}
		}
		return none(rfRowCols), nil
	case strings.Contains(q, "ROUND(amount * 100)::bigint = $2"):
		if err := c.note("match-reservation"); err != nil {
			return nil, err
		}
		for _, r := range d.rows {
			if r.status == "pending" && r.refundID == "" && r.key != "" && r.amountKop == args[1].Value.(int64) {
				return one(rfRowCols, rowValues(r)...), nil
			}
		}
		return none(rfRowCols), nil
	case strings.Contains(q, "WHERE payment_id = $1 AND status = 'pending' ORDER BY id"):
		if err := c.note("pending-rows"); err != nil {
			return nil, err
		}
		out := none(rfRowCols)
		for _, r := range d.rows {
			if r.status == "pending" {
				out.rows = append(out.rows, rowValues(r))
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("rf driver: unscripted query %s", q)
}

func mustKop(v any) int64 {
	k, err := parseYKAmount(v.(string))
	if err != nil {
		panic(err)
	}
	return k
}

func (c *rfConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	id := func() int64 { v, _ := args[0].Value.(int64); return v }
	label := ""
	switch {
	case strings.HasPrefix(q, "UPDATE payment_refunds SET idempotence_key"):
		label = "set-key"
	case strings.HasPrefix(q, "INSERT INTO payment_refunds"):
		label = "insert-row"
	case strings.HasPrefix(q, "UPDATE payment_refunds SET yookassa_refund_id = $2"):
		label = "link"
	case strings.HasPrefix(q, "UPDATE payment_refunds SET yookassa_refund_id = NULL"):
		label = "unlink"
	case strings.HasPrefix(q, "UPDATE payment_refunds SET status = 'failed'"):
		label = "failed"
	case strings.HasPrefix(q, "UPDATE payment_refunds SET status = $2"):
		label = "set-status"
	case strings.HasPrefix(q, "DELETE FROM payment_refunds"):
		label = "delete-webhook-row"
	case strings.HasPrefix(q, "UPDATE payments SET refunded_amount"):
		label = "totals"
	default:
		return nil, fmt.Errorf("rf driver: unscripted statement %s", q)
	}
	if err := c.note(label); err != nil {
		return nil, err
	}
	c.write()
	now := d.now()
	affected := int64(0)
	switch label {
	case "set-key":
		if r := d.find(id()); r != nil {
			r.key, affected = args[1].Value.(string), 1
		}
	case "insert-row":
		refundID := args[1].Value.(string)
		if d.refundTaken(refundID, 0) {
			return nil, fmt.Errorf("pq: duplicate key value violates unique constraint (yookassa_refund_id=%s)", refundID)
		}
		d.nextID++
		d.rows = append(d.rows, rfRow{id: d.nextID, refundID: refundID, amountKop: mustKop(args[2].Value),
			status: args[4].Value.(string), desc: args[5].Value.(string), created: now, updated: now})
		affected = 1
	case "link":
		refundID := args[1].Value.(string)
		r := d.find(id())
		if r == nil || (r.refundID != "" && r.refundID != refundID) {
			break
		}
		if d.refundTaken(refundID, r.id) {
			return nil, fmt.Errorf("pq: duplicate key value violates unique constraint (yookassa_refund_id=%s)", refundID)
		}
		r.refundID, r.amountKop, r.status, r.updated, affected = refundID, mustKop(args[2].Value), args[3].Value.(string), now, 1
	case "unlink":
		if r := d.find(id()); r != nil {
			r.refundID, r.status, r.updated, affected = "", "pending", now, 1
		}
	case "failed":
		keyOK := !strings.Contains(q, "idempotence_key IS NOT NULL")
		if r := d.find(id()); r != nil && r.status == "pending" && r.refundID == "" && (keyOK || r.key != "") {
			r.status, r.updated, affected = "failed", now, 1
		}
	case "set-status":
		if r := d.find(id()); r != nil {
			r.status, r.updated, affected = args[1].Value.(string), now, 1
		}
	case "delete-webhook-row":
		for i, r := range d.rows {
			if r.id == id() && r.key == "" {
				d.rows, affected = append(d.rows[:i:i], d.rows[i+1:]...), 1
				break
			}
		}
	case "totals":
		var sum int64
		for _, r := range d.rows {
			if r.status == "succeeded" {
				sum += r.amountKop
			}
		}
		d.refundedKop = sum
		if args[1].Value == "succeeded" && sum >= d.amountKop {
			d.status = "refunded"
		}
		affected = 1
	}
	return driver.RowsAffected(affected), nil
}

type rfRows struct {
	cols []string
	rows [][]driver.Value
	pos  int
}

func (r *rfRows) Columns() []string { return r.cols }
func (*rfRows) Close() error        { return nil }
func (r *rfRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.pos])
	r.pos++
	return nil
}

func (d *rfDB) summary() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, r := range d.rows {
		kind := "reservation"
		if r.key == "" {
			kind = "webhook"
		}
		out = append(out, fmt.Sprintf("%d:%s:%s:%s:%s", r.id, ykAmount(r.amountKop), r.status, r.refundID, kind))
	}
	return out
}

func (d *rfDB) keyOf(id int64) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r := d.find(id); r != nil {
		return r.key
	}
	return ""
}

// rfYooKassa is YooKassa's refund API: a key it has seen returns the refund it created
// (same body) or 400 (other body). behaviour is consumed per create call: ok, ok-pending,
// lost (refund created or found, answer lost), reject (400 invalid_request); down, 5xx,
// 429, 403 and accepted (202) never reach the refund logic. during runs while the call is in flight, after the refund
// exists — e.g. the refund webhook arriving before the answer.
type rfYooKassa struct {
	mu           sync.Mutex
	behaviour    []string
	byKey        map[string]rfCreated
	refunds      map[string]*yookassa.Refund
	order        []string
	calls        []string // "key=amount description"
	listErr      error
	listDeadline time.Time
	getCalls     int
	listCalls    int
	onCall       func(string)
	during       func(ref yookassa.Refund)
}

type rfCreated struct {
	req yookassa.CreateRefundRequest
	id  string
}

// add makes a refund exist in YooKassa (made elsewhere, e.g. in the dashboard, when key == "").
func (y *rfYooKassa) add(amount, status string, created time.Time) yookassa.Refund {
	if y.refunds == nil {
		y.refunds = map[string]*yookassa.Refund{}
	}
	ref := &yookassa.Refund{ID: fmt.Sprintf("rf-%d", len(y.order)+1), Status: status, PaymentID: "pay-1",
		Amount: yookassa.Amount{Value: amount, Currency: "RUB"}, CreatedAt: created.UTC().Format(time.RFC3339Nano)}
	y.refunds[ref.ID] = ref
	y.order = append(y.order, ref.ID)
	return *ref
}

func (y *rfYooKassa) setStatus(id, status string) {
	y.mu.Lock()
	y.refunds[id].Status = status
	y.mu.Unlock()
}

func (y *rfYooKassa) CreateRefundWithKey(req yookassa.CreateRefundRequest, key string) (*yookassa.Refund, error) {
	ref, err, during := y.create(req, key)
	if during != nil && ref != nil {
		during(*ref)
	}
	if err != nil {
		return nil, err
	}
	return ref, nil
}

func (y *rfYooKassa) create(req yookassa.CreateRefundRequest, key string) (*yookassa.Refund, error, func(yookassa.Refund)) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.calls = append(y.calls, key+"="+req.Amount.Value+" "+req.Description)
	if y.onCall != nil {
		y.onCall(key)
	}
	b := "ok"
	if len(y.behaviour) > 0 {
		b, y.behaviour = y.behaviour[0], y.behaviour[1:]
	}
	switch b { // the request did not get through
	case "down":
		return nil, errors.New("http do: context deadline exceeded (Client.Timeout exceeded while awaiting headers)"), nil
	case "5xx":
		return nil, &yookassa.APIError{HTTPStatus: 500, Code: "internal_server_error"}, nil
	case "429":
		return nil, &yookassa.APIError{HTTPStatus: 429, Code: "too_many_requests"}, nil
	case "403":
		return nil, &yookassa.APIError{HTTPStatus: 403, Code: "forbidden"}, nil
	case "accepted":
		return &yookassa.Refund{}, nil, nil
	}
	lost := errors.New("read body: connection reset by peer")
	if c, ok := y.byKey[key]; ok {
		if !reflect.DeepEqual(c.req, req) {
			return nil, &yookassa.APIError{HTTPStatus: 400, Code: "invalid_request", Description: "Idempotence key reused with another request"}, nil
		}
		ref := *y.refunds[c.id]
		if b == "lost" {
			return &ref, lost, nil
		}
		return &ref, nil, nil
	}
	if b == "reject" {
		return nil, &yookassa.APIError{HTTPStatus: 400, Code: "invalid_request", Description: "Refund amount is invalid"}, nil
	}
	status := yookassa.RefundStatusSucceeded
	if b == "ok-pending" {
		status = yookassa.RefundStatusPending
	}
	ref := y.add(req.Amount.Value, status, time.Now())
	if y.byKey == nil {
		y.byKey = map[string]rfCreated{}
	}
	y.byKey[key] = rfCreated{req: req, id: ref.ID}
	if b == "lost" {
		return &ref, lost, y.during
	}
	return &ref, nil, y.during
}

func (y *rfYooKassa) GetRefund(id string) (*yookassa.Refund, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.getCalls++
	r, ok := y.refunds[id]
	if !ok {
		return nil, &yookassa.APIError{HTTPStatus: 404, Code: "not_found"}
	}
	cp := *r
	return &cp, nil
}

func (y *rfYooKassa) ListRefunds(paymentID string, deadline time.Time) ([]yookassa.Refund, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.listCalls++
	y.listDeadline = deadline
	if y.listErr != nil {
		return nil, y.listErr
	}
	var out []yookassa.Refund
	for _, id := range y.order {
		if r := y.refunds[id]; r.PaymentID == paymentID {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (y *rfYooKassa) created() int { y.mu.Lock(); defer y.mu.Unlock(); return len(y.order) }

// useRefundFakes routes db.DB, the YooKassa client and the log to the fakes. The journal
// records every YooKassa call with the number of transactions open at that moment.
func useRefundFakes(t *testing.T, d *rfDB, y *rfYooKassa) {
	t.Helper()
	fake := sql.OpenDB(d)
	prevDB, prevClient, prevLog := db.DB, newRefundClient, log.Writer()
	db.DB = fake
	newRefundClient = func() (refundAPI, error) { return y, nil }
	log.SetOutput(io.Discard)
	y.onCall = func(key string) {
		d.mu.Lock()
		d.journal = append(d.journal, fmt.Sprintf("yookassa %s open-tx=%d", key, d.openTx))
		d.mu.Unlock()
	}
	t.Cleanup(func() {
		db.DB, newRefundClient = prevDB, prevClient
		log.SetOutput(prevLog)
		_ = fake.Close()
	})
}

func postRefund(t *testing.T, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	handleAdminPaymentCancel(rec, httptest.NewRequest(http.MethodPost, "/api/admin/payments/7/cancel", strings.NewReader(body)))
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("non-JSON answer %d %q", rec.Code, rec.Body.String())
	}
	return rec.Code, m
}

// noDeadlock fails the test instead of hanging when fn blocks on a lock.
func noDeadlock(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: blocked for 5 s on the payment row lock")
	}
}

// ─── Admin refund flows ───────────────────────────────────────────────────

type rfStep struct {
	before   func(d *rfDB, y *rfYooKassa)
	body     string
	yk       []string
	wantCode int
	wantText string   // substring of "error", or of "message" on 200/202
	wantSent []string // "key=amount description" sent to YooKassa in this step; #N = key of row N
}

func TestAdminRefundFlows(t *testing.T) {
	webhook := func(id string) func(d *rfDB, y *rfYooKassa) {
		return func(d *rfDB, y *rfYooKassa) {
			if code := refundNotificationStatus(id, y.GetRefund); code != http.StatusOK {
				panic(fmt.Sprintf("webhook %s: %d", id, code))
			}
		}
	}
	long := strings.Repeat("я", 251)
	tests := []struct {
		name        string
		status      string
		missing     bool
		rows        []rfRow
		during      func(t *testing.T, y *rfYooKassa) func(yookassa.Refund)
		steps       []rfStep
		wantCreated int      // refunds that exist in YooKassa at the end
		wantRows    []string // payment_refunds at the end: id:amount:status:refund id:kind
		wantStatus  string   // payments.status at the end
	}{
		{
			name: "a pending refund reduces what is left; amount 0 refunds the rest",
			steps: []rfStep{
				{body: `{"amount":100,"reason":"part"}`, yk: []string{"ok-pending"}, wantCode: 200, wantText: "Возврат создан", wantSent: []string{"#1=100.00 part"}},
				{body: `{"amount":250}`, wantCode: 400, wantText: "Можно вернуть не больше 200,00 ₽ (остаток с учётом незавершённых возвратов)"},
				{body: `{"amount":0,"reason":"rest"}`, wantCode: 200, wantSent: []string{"#2=200.00 rest"}},
				{body: `{}`, wantCode: 400, wantText: "Можно вернуть не больше 0,00 ₽"},
			},
			wantCreated: 2,
			wantRows:    []string{"1:100.00:pending:rf-1:reservation", "2:200.00:succeeded:rf-2:reservation"},
			wantStatus:  "succeeded",
		},
		{
			name:        "no body is a full refund",
			steps:       []rfStep{{body: ``, wantCode: 200, wantSent: []string{"#1=300.00 Admin refund"}}},
			wantCreated: 1, wantRows: []string{"1:300.00:succeeded:rf-1:reservation"}, wantStatus: "refunded",
		},
		{
			name: "completed is paid", status: "completed",
			steps:       []rfStep{{body: `{"amount":300}`, wantCode: 200, wantSent: []string{"#1=300.00 Admin refund"}}},
			wantCreated: 1, wantRows: []string{"1:300.00:succeeded:rf-1:reservation"}, wantStatus: "refunded",
		},
		{
			name: "unpaid payment", status: "pending",
			steps:      []rfStep{{body: `{"amount":100}`, wantCode: 400, wantText: "Вернуть можно только оплаченный платёж (статус платежа: pending)"}},
			wantStatus: "pending",
		},
		{name: "payment not found", missing: true, steps: []rfStep{{body: `{"amount":100}`, wantCode: 404, wantText: "Платёж не найден"}}},
		{
			name: "a body that does not parse, a negative amount, a too long reason: 400, nothing reserved",
			steps: []rfStep{
				{body: `{"amount":"100"}`, wantCode: 400, wantText: "Некорректный запрос"},
				{body: `{"amount":-5}`, wantCode: 400, wantText: "отрицательной"},
				{body: `{"amount":100,"reason":"` + long + `"}`, wantCode: 400, wantText: "длиннее 250 символов"},
			},
			wantStatus: "succeeded",
		},
		{
			name: "250 characters of Cyrillic are fine (runes, not bytes)",
			steps: []rfStep{{body: `{"amount":100,"reason":"` + long[2:] + `"}`, wantCode: 200,
				wantSent: []string{"#1=100.00 " + long[2:]}}},
			wantCreated: 1, wantRows: []string{"1:100.00:succeeded:rf-1:reservation"}, wantStatus: "succeeded",
		},
		{
			name: "lost answer: one call, 202 pending; the next click repeats it with the same key and body",
			steps: []rfStep{
				{body: `{"amount":100,"reason":"first"}`, yk: []string{"lost"}, wantCode: 202, wantText: refundPendingMessage, wantSent: []string{"#1=100.00 first"}},
				{body: `{"amount":200}`, wantCode: 409, wantText: "Предыдущий возврат по этому платежу ещё обрабатывается — проверьте статус"},
				{body: `{"amount":100,"reason":"typed again"}`, wantCode: 200, wantText: "повтор попытки", wantSent: []string{"#1=100.00 first"}},
				{body: `{"amount":100,"reason":"second"}`, wantCode: 200, wantText: "Возврат создан", wantSent: []string{"#2=100.00 second"}},
			},
			wantCreated: 2,
			wantRows:    []string{"1:100.00:succeeded:rf-1:reservation", "2:100.00:succeeded:rf-2:reservation"},
			wantStatus:  "succeeded",
		},
		{
			name: "timeout, 5xx and 202 are no answer: the amount stays reserved",
			steps: []rfStep{
				{body: `{"amount":100}`, yk: []string{"down"}, wantCode: 202, wantText: refundPendingMessage, wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":100}`, yk: []string{"5xx"}, wantCode: 202, wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":100}`, yk: []string{"accepted"}, wantCode: 202, wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":250}`, wantCode: 409, wantText: "ещё обрабатывается"},
			},
			wantRows:   []string{"1:100.00:pending::reservation"},
			wantStatus: "succeeded",
		},
		{
			name: "429 is no answer: the reservation stays, the next click repeats it",
			steps: []rfStep{
				{body: `{"amount":100}`, yk: []string{"429"}, wantCode: 202, wantText: refundPendingMessage, wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":100}`, wantCode: 200, wantText: "повтор попытки", wantSent: []string{"#1=100.00 Admin refund"}},
			},
			wantCreated: 1, wantRows: []string{"1:100.00:succeeded:rf-1:reservation"}, wantStatus: "succeeded",
		},
		{
			name: "a repeat answered 403 keeps the reservation; a repeat answered invalid_request frees it",
			steps: []rfStep{
				{body: `{"amount":100}`, yk: []string{"down"}, wantCode: 202, wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":100}`, yk: []string{"403"}, wantCode: 202, wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":250}`, wantCode: 409, wantText: "ещё обрабатывается"},
				{body: `{"amount":100}`, yk: []string{"reject"}, wantCode: 502, wantText: "ЮKassa отклонила возврат", wantSent: []string{"#1=100.00 Admin refund"}},
				{body: `{"amount":300.01}`, wantCode: 400, wantText: "не больше 300,00 ₽"}, // the 100 are free again
			},
			wantRows:   []string{"1:100.00:failed::reservation"},
			wantStatus: "succeeded",
		},
		{
			name: "a rejected request frees the amount, the next attempt gets a new key",
			steps: []rfStep{
				{body: `{"amount":300}`, yk: []string{"reject"}, wantCode: 502, wantText: "ЮKassa отклонила возврат", wantSent: []string{"#1=300.00 Admin refund"}},
				{body: `{"amount":300}`, wantCode: 200, wantSent: []string{"#2=300.00 Admin refund"}},
			},
			wantCreated: 1,
			wantRows:    []string{"1:300.00:failed::reservation", "2:300.00:succeeded:rf-1:reservation"},
			wantStatus:  "refunded",
		},
		{
			name: "the webhook arrives while the call is in flight: it takes the reservation, no deadlock",
			during: func(t *testing.T, y *rfYooKassa) func(yookassa.Refund) {
				return func(ref yookassa.Refund) {
					if code := refundNotificationStatus(ref.ID, y.GetRefund); code != http.StatusOK {
						t.Errorf("webhook during the call: %d", code)
					}
				}
			},
			steps:       []rfStep{{body: `{"amount":100}`, wantCode: 200, wantSent: []string{"#1=100.00 Admin refund"}}},
			wantCreated: 1, wantRows: []string{"1:100.00:succeeded:rf-1:reservation"}, wantStatus: "succeeded",
		},
		{
			name: "lost answer, then the webhook: counted once",
			steps: []rfStep{
				{body: `{"amount":100}`, yk: []string{"lost"}, wantCode: 202, wantSent: []string{"#1=100.00 Admin refund"}},
				{before: webhook("rf-1"), body: `{"amount":250}`, wantCode: 400, wantText: "не больше 200,00 ₽"},
			},
			wantCreated: 1, wantRows: []string{"1:100.00:succeeded:rf-1:reservation"}, wantStatus: "succeeded",
		},
		{
			name: "a pending refund later succeeds through the webhook",
			steps: []rfStep{
				{body: `{"amount":300}`, yk: []string{"ok-pending"}, wantCode: 200, wantSent: []string{"#1=300.00 Admin refund"}},
				{before: func(d *rfDB, y *rfYooKassa) { y.setStatus("rf-1", "succeeded"); webhook("rf-1")(d, y) },
					body: `{"amount":1}`, wantCode: 400, wantText: "только оплаченный платёж (статус платежа: refunded)"},
			},
			wantCreated: 1, wantRows: []string{"1:300.00:succeeded:rf-1:reservation"}, wantStatus: "refunded",
		},
		{
			// During our call a 100 ₽ dashboard refund D is made: its webhook takes our
			// reservation by amount. Then the webhook of our own refund X finds no
			// reservation and inserts a row. Our answer: X merges into the reservation, D
			// gets a row of its own.
			name: "webhooks matched the wrong refund to the reservation: sorted out on the answer",
			during: func(t *testing.T, y *rfYooKassa) func(yookassa.Refund) {
				return func(x yookassa.Refund) {
					y.mu.Lock()
					d := y.add("100.00", "succeeded", time.Now())
					y.mu.Unlock()
					for _, id := range []string{d.ID, x.ID} {
						if code := refundNotificationStatus(id, y.GetRefund); code != http.StatusOK {
							t.Errorf("webhook %s: %d", id, code)
						}
					}
				}
			},
			steps:       []rfStep{{body: `{"amount":100}`, wantCode: 200, wantSent: []string{"#1=100.00 Admin refund"}}},
			wantCreated: 2,
			wantRows:    []string{"1:100.00:succeeded:rf-1:reservation", "3:100.00:succeeded:rf-2:webhook"},
			wantStatus:  "succeeded",
		},
		{
			// An old unanswered reservation of the same amount is still open (past the
			// 23 h window); the webhook of the new refund takes it (oldest first). The
			// answer moves the refund to its own reservation and gives the old one back.
			name: "the webhook took an older reservation of the same amount: it gets it back",
			rows: []rfRow{{id: 1, amountKop: 10000, status: "pending", desc: "old", key: "refund-7-1-1", created: time.Now().Add(-30 * time.Hour)}},
			during: func(t *testing.T, y *rfYooKassa) func(yookassa.Refund) {
				return func(x yookassa.Refund) {
					if code := refundNotificationStatus(x.ID, y.GetRefund); code != http.StatusOK {
						t.Errorf("webhook: %d", code)
					}
				}
			},
			steps: []rfStep{
				{body: `{"amount":100}`, wantCode: 200, wantSent: []string{"#2=100.00 Admin refund"}},
				{body: `{"amount":150}`, wantCode: 400, wantText: "не больше 100,00 ₽"}, // the old one still holds 100
			},
			wantCreated: 1,
			wantRows:    []string{"1:100.00:pending::reservation", "2:100.00:succeeded:rf-1:reservation"},
			wantStatus:  "succeeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newRFDB()
			d.missing = tt.missing
			d.rows = append([]rfRow(nil), tt.rows...)
			for _, r := range tt.rows {
				d.nextID = max(d.nextID, r.id)
			}
			if tt.status != "" {
				d.status = tt.status
			}
			y := &rfYooKassa{}
			useRefundFakes(t, d, y)
			if tt.during != nil {
				y.during = tt.during(t, y)
			}
			for i, st := range tt.steps {
				if st.before != nil {
					st.before(d, y)
				}
				y.mu.Lock()
				y.behaviour, y.calls = st.yk, nil
				y.mu.Unlock()
				var code int
				var m map[string]any
				noDeadlock(t, func() { code, m = postRefund(t, st.body) })
				text, _ := m["error"].(string)
				if code == http.StatusOK || code == http.StatusAccepted {
					text, _ = m["message"].(string)
				}
				if code != st.wantCode || !strings.Contains(text, st.wantText) {
					t.Fatalf("step %d: %d %v; want %d %q", i+1, code, m, st.wantCode, st.wantText)
				}
				if code == http.StatusAccepted && (m["status"] != "pending" || len(m) != 2) {
					t.Fatalf("step %d: 202 body %v, want exactly status=pending and message", i+1, m)
				}
				var want []string
				for _, s := range st.wantSent {
					if strings.HasPrefix(s, "#") {
						var n int64
						fmt.Sscanf(s, "#%d=", &n)
						s = d.keyOf(n) + s[strings.Index(s, "="):]
					}
					want = append(want, s)
				}
				y.mu.Lock()
				sent := y.calls
				y.mu.Unlock()
				if !reflect.DeepEqual(sent, want) {
					t.Fatalf("step %d: sent to YooKassa %q, want %q", i+1, sent, want)
				}
			}
			if got := y.created(); got != tt.wantCreated {
				t.Errorf("refunds in YooKassa: %d, want %d", got, tt.wantCreated)
			}
			if got := d.summary(); !reflect.DeepEqual(got, tt.wantRows) {
				t.Errorf("payment_refunds\n got %q\nwant %q", got, tt.wantRows)
			}
			if tt.wantStatus != "" && d.status != tt.wantStatus {
				t.Errorf("payments.status %q, want %q", d.status, tt.wantStatus)
			}
			if d.openTx != 0 {
				t.Errorf("%d transactions left open", d.openTx)
			}
		})
	}
}

// The reservation is one locked transaction; the YooKassa call runs with NO transaction
// open; the answer is recorded in a second short locked transaction; the webhook locks the
// payment before touching payment_refunds.
func TestRefundTransactions(t *testing.T) {
	d := newRFDB()
	d.clock = time.Unix(1760000000, 0)
	y := &rfYooKassa{}
	useRefundFakes(t, d, y)
	if code, m := postRefund(t, `{"amount":100}`); code != 200 {
		t.Fatalf("%d %v", code, m)
	}
	y.setStatus("rf-1", "succeeded")
	if code := refundNotificationStatus("rf-1", y.GetRefund); code != http.StatusOK {
		t.Fatalf("webhook %d", code)
	}
	want := []string{
		"BEGIN", "tx:lock", "tx:held", "tx:open-reservation", "tx:reserve", "tx:set-key", "COMMIT",
		"yookassa refund-7-1-1760000000 open-tx=0",
		"BEGIN", "tx:lock", "tx:row-by-refund-id", "tx:row-by-id", "tx:link", "tx:totals", "COMMIT",
		"BEGIN", "tx:lock-by-yookassa", "tx:row-by-refund-id", "tx:set-status", "tx:totals", "COMMIT",
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !reflect.DeepEqual(d.journal, want) {
		t.Fatalf("journal\n got %q\nwant %q", d.journal, want)
	}
}

// Concurrent refunds of one payment: 300 ₽ paid, ten admins ask for 200 ₽ at once. The
// reservation serializes them; the ones that see the open reservation repeat it with its
// key, so YooKassa deduplicates: exactly one refund.
func TestAdminRefundConcurrent(t *testing.T) {
	d := newRFDB()
	d.readPause = 5 * time.Millisecond
	y := &rfYooKassa{}
	useRefundFakes(t, d, y)
	const n = 10
	type answer struct {
		code int
		body map[string]any
	}
	answers := make(chan answer, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handleAdminPaymentCancel(rec, httptest.NewRequest(http.MethodPost, "/api/admin/payments/7/cancel", strings.NewReader(`{"amount":200}`)))
			var m map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &m)
			answers <- answer{rec.Code, m}
		}()
	}
	wg.Wait()
	close(answers)
	count := map[int]int{}
	for a := range answers {
		count[a.code]++
		if a.code == http.StatusOK && a.body["refund_id"] != "rf-1" {
			t.Errorf("a 200 for another refund: %v", a.body)
		}
	}
	if y.created() != 1 || count[http.StatusOK] < 1 || count[http.StatusOK]+count[http.StatusBadRequest] != n {
		t.Fatalf("answers %v, refunds created %d; want one refund, the rest 200 (same refund) or 400", count, y.created())
	}
	if got := d.summary(); !reflect.DeepEqual(got, []string{"1:200.00:succeeded:rf-1:reservation"}) {
		t.Fatalf("payment_refunds %q", got)
	}
}

// ─── Webhook ──────────────────────────────────────────────────────────────

func TestRefundWebhookStatus(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		verify   error
		missing  bool
		failOn   string
		wantCode int
		wantRows []string
	}{
		{"recorded", "rf-1", nil, false, "", 200, []string{"1:100.00:succeeded:rf-1:webhook"}},
		{"no refund id", "", nil, false, "", 400, nil},
		{"verification fails", "rf-1", errors.New("timeout"), false, "", 502, nil},
		{"payment is not ours: acknowledged", "rf-1", nil, true, "", 200, nil},
		{"database down: 500 so YooKassa repeats it", "rf-1", nil, false, "lock-by-yookassa", 500, nil},
		{"failure mid-way rolls back: 500", "rf-1", nil, false, "totals", 500, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newRFDB()
			d.missing, d.failOn = tt.missing, tt.failOn
			y := &rfYooKassa{}
			useRefundFakes(t, d, y)
			y.add("100.00", "succeeded", time.Now())
			verify := y.GetRefund
			if tt.verify != nil {
				verify = func(string) (*yookassa.Refund, error) { return nil, tt.verify }
			}
			if code := refundNotificationStatus(tt.id, verify); code != tt.wantCode {
				t.Fatalf("status %d, want %d", code, tt.wantCode)
			}
			if got := d.summary(); !reflect.DeepEqual(got, tt.wantRows) {
				t.Fatalf("payment_refunds %q, want %q", got, tt.wantRows)
			}
			if d.openTx != 0 {
				t.Fatalf("%d transactions left open", d.openTx)
			}
		})
	}
}

// ─── Reconcile ────────────────────────────────────────────────────────────

func TestReconcileRefunds(t *testing.T) {
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }
	reservation := func(id int64, age time.Duration) rfRow {
		return rfRow{id: id, amountKop: 10000, status: "pending", key: fmt.Sprintf("refund-7-%d-1", id), created: ago(age), updated: ago(age)}
	}
	tests := []struct {
		name       string
		rows       []rfRow
		yk         func(y *rfYooKassa)
		listErr    error
		minAge     time.Duration
		budget     time.Time
		wantRows   []string
		wantGets   int
		wantLists  int
		wantErr    bool
		wantStatus string
	}{
		{
			name:     "pending refund succeeded",
			rows:     []rfRow{{id: 1, refundID: "rf-1", amountKop: 30000, status: "pending", key: "k", created: ago(time.Hour), updated: ago(time.Hour)}},
			yk:       func(y *rfYooKassa) { y.add("300.00", "succeeded", ago(time.Hour)) },
			wantRows: []string{"1:300.00:succeeded:rf-1:reservation"}, wantGets: 1, wantStatus: "refunded",
		},
		{
			name:     "pending refund canceled: frees the amount",
			rows:     []rfRow{{id: 1, refundID: "rf-1", amountKop: 10000, status: "pending", key: "k", created: ago(time.Hour), updated: ago(time.Hour)}},
			yk:       func(y *rfYooKassa) { y.add("100.00", "canceled", ago(time.Hour)) },
			wantRows: []string{"1:100.00:canceled:rf-1:reservation"}, wantGets: 1, wantStatus: "succeeded",
		},
		{
			name:     "unanswered reservation: the matching refund is found by the list",
			rows:     []rfRow{reservation(1, 15*time.Minute)},
			yk:       func(y *rfYooKassa) { y.add("100.00", "succeeded", ago(15*time.Minute)) },
			wantRows: []string{"1:100.00:succeeded:rf-1:reservation"}, wantLists: 1,
		},
		{
			name:     "another amount does not match",
			rows:     []rfRow{reservation(1, 15*time.Minute)},
			yk:       func(y *rfYooKassa) { y.add("50.00", "succeeded", ago(15*time.Minute)) },
			wantRows: []string{"1:100.00:pending::reservation"}, wantLists: 1,
		},
		{
			name:     "a refund made before the reservation does not match",
			rows:     []rfRow{reservation(1, 15*time.Minute)},
			yk:       func(y *rfYooKassa) { y.add("100.00", "succeeded", ago(2*time.Hour)) },
			wantRows: []string{"1:100.00:pending::reservation"}, wantLists: 1,
		},
		{
			name:     "a refund already recorded elsewhere does not match",
			rows:     []rfRow{reservation(1, 15*time.Minute), {id: 2, refundID: "rf-1", amountKop: 10000, status: "succeeded", created: ago(10 * time.Minute)}},
			yk:       func(y *rfYooKassa) { y.add("100.00", "succeeded", ago(14*time.Minute)) },
			wantRows: []string{"1:100.00:pending::reservation", "2:100.00:succeeded:rf-1:webhook"}, wantLists: 1,
		},
		{
			name:     "nothing in YooKassa after 23 h: failed",
			rows:     []rfRow{reservation(1, 24*time.Hour)},
			wantRows: []string{"1:100.00:failed::reservation"}, wantLists: 1,
		},
		{
			name:     "list fails: never marked failed without looking",
			rows:     []rfRow{reservation(1, 24*time.Hour)},
			listErr:  errors.New("timeout"),
			wantRows: []string{"1:100.00:pending::reservation"}, wantLists: 1, wantErr: true,
		},
		{
			name:     "a reservation younger than the minimum age is left alone",
			rows:     []rfRow{reservation(1, 5*time.Minute)},
			minAge:   10 * time.Minute,
			wantRows: []string{"1:100.00:pending::reservation"},
		},
		{
			name:     "a row without idempotence key is never marked failed",
			rows:     []rfRow{{id: 1, amountKop: 10000, status: "pending", created: ago(24 * time.Hour)}},
			wantRows: []string{"1:100.00:pending::webhook"},
		},
		{
			name:     "within the budget: the list gets the budget as its per-page deadline",
			rows:     []rfRow{reservation(1, 15*time.Minute)},
			yk:       func(y *rfYooKassa) { y.add("100.00", "succeeded", ago(15*time.Minute)) },
			budget:   time.Now().Add(time.Hour),
			wantRows: []string{"1:100.00:succeeded:rf-1:reservation"}, wantLists: 1,
		},
		{
			name:     "no HTTP call after the budget",
			rows:     []rfRow{reservation(1, 24*time.Hour), {id: 2, refundID: "rf-1", amountKop: 10000, status: "pending", key: "k", created: ago(time.Hour)}},
			budget:   ago(time.Second),
			wantRows: []string{"1:100.00:pending::reservation", "2:100.00:pending:rf-1:reservation"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newRFDB()
			d.rows = tt.rows
			for _, r := range tt.rows {
				d.nextID = max(d.nextID, r.id)
			}
			y := &rfYooKassa{listErr: tt.listErr}
			useRefundFakes(t, d, y)
			if tt.yk != nil {
				tt.yk(y)
			}
			minAge := tt.minAge
			if minAge == 0 {
				minAge = refundListMinAgeSweep
			}
			err := reconcileRefunds(context.Background(), y, 7, minAge, tt.budget)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err %v, want error %v", err, tt.wantErr)
			}
			if got := d.summary(); !reflect.DeepEqual(got, tt.wantRows) {
				t.Errorf("payment_refunds\n got %q\nwant %q", got, tt.wantRows)
			}
			if y.getCalls != tt.wantGets || y.listCalls != tt.wantLists {
				t.Errorf("GetRefund %d, ListRefunds %d; want %d, %d", y.getCalls, y.listCalls, tt.wantGets, tt.wantLists)
			}
			if tt.wantStatus != "" && d.status != tt.wantStatus {
				t.Errorf("payments.status %q, want %q", d.status, tt.wantStatus)
			}
			if y.listCalls > 0 && !y.listDeadline.Equal(tt.budget) {
				t.Errorf("ListRefunds deadline %v, want the call budget %v (checked per page)", y.listDeadline, tt.budget)
			}
			if d.openTx != 0 {
				t.Errorf("%d transactions left open", d.openTx)
			}
		})
	}
}

// The sweep (payment sync cron) settles what the webhook did not: a pending refund and an
// unanswered reservation, and leaves fresh ones to the webhook.
func TestReconcilePendingRefundsSweep(t *testing.T) {
	d := newRFDB()
	old := time.Now().Add(-20 * time.Minute)
	d.rows = []rfRow{
		{id: 1, refundID: "rf-1", amountKop: 5000, status: "pending", key: "k1", created: old, updated: old},
		{id: 2, amountKop: 10000, status: "pending", key: "k2", created: old, updated: old},
	}
	d.nextID = 2
	y := &rfYooKassa{}
	useRefundFakes(t, d, y)
	y.add("50.00", "succeeded", old)
	y.add("100.00", "succeeded", old.Add(time.Second))
	reconcilePendingRefunds()
	want := []string{"1:50.00:succeeded:rf-1:reservation", "2:100.00:succeeded:rf-2:reservation"}
	if got := d.summary(); !reflect.DeepEqual(got, want) {
		t.Fatalf("payment_refunds\n got %q\nwant %q", got, want)
	}
	// Nothing left to do: the sweep selects no payment and calls nothing. A pending row
	// without a refund id AND without an idempotence key is not a reservation: ignored.
	d.mu.Lock()
	d.rows = append(d.rows, rfRow{id: 3, amountKop: 700, status: "pending", created: old, updated: old})
	d.mu.Unlock()
	y.getCalls, y.listCalls = 0, 0
	d.mu.Lock()
	d.journal = nil
	d.mu.Unlock()
	reconcilePendingRefunds()
	if y.getCalls+y.listCalls != 0 {
		t.Fatalf("second sweep called YooKassa %d+%d times", y.getCalls, y.listCalls)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !reflect.DeepEqual(d.journal, []string{"sweep"}) {
		t.Fatalf("second sweep selected the payment again: %q", d.journal)
	}
}

// «Проверить статус» starts no YooKassa call after its budget; budget + one call + the
// lock wait fit nginx's 60 s.
func TestCheckStatusTiming(t *testing.T) {
	if checkStatusBudget+refundCallTimeout+refundDBTimeout > 60*time.Second || refundCallTimeout > 25*time.Second || refundDBTimeout > 10*time.Second {
		t.Fatalf("budget %v + call %v + lock wait %v must stay under 60 s", checkStatusBudget, refundCallTimeout, refundDBTimeout)
	}
}
