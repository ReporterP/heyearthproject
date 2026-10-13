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
	"strconv"
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

func TestDecideRefund(t *testing.T) {
	paid := refundState{YookassaID: "pay-1", Status: "succeeded", AmountKop: 30000}
	with := func(f func(*refundState)) refundState { s := paid; f(&s); return s }
	unanswered := &refundReservation{ID: 4, AmountKop: 10000, Reason: "first"}
	const left = "(остаток с учётом незавершённых возвратов)"

	tests := []struct {
		name       string
		st         refundState
		request    int64
		wantAmount int64
		wantResume *refundReservation
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
		{"nothing left, amount 0", with(func(s *refundState) { s.HeldKop = 30000 }), 0, 0, nil,
			http.StatusBadRequest, "Можно вернуть не больше 0,00 ₽ " + left},
		{"nothing left, explicit amount", with(func(s *refundState) { s.HeldKop = 30000 }), 1, 0, nil,
			http.StatusBadRequest, "Можно вернуть не больше 0,00 ₽"},
		{"over-held payment never shows a negative amount", with(func(s *refundState) { s.HeldKop = 40000 }), 0, 0, nil,
			http.StatusBadRequest, "Можно вернуть не больше 0,00 ₽"},
		{"pending payment", with(func(s *refundState) { s.Status = "pending" }), 0, 0, nil, http.StatusBadRequest, "Вернуть можно только оплаченный платёж (статус платежа: pending)"},
		{"waiting_for_capture", with(func(s *refundState) { s.Status = "waiting_for_capture" }), 100, 0, nil, http.StatusBadRequest, "только оплаченный"},
		{"canceled payment", with(func(s *refundState) { s.Status = "canceled" }), 100, 0, nil, http.StatusBadRequest, "только оплаченный"},
		{"already refunded", with(func(s *refundState) { s.Status = "refunded" }), 0, 0, nil, http.StatusBadRequest, "только оплаченный"},
		{"no YooKassa id", with(func(s *refundState) { s.YookassaID = "" }), 0, 0, nil, http.StatusBadRequest, "нет идентификатора ЮKassa"},
		{"unanswered attempt, same amount: repeat it", with(func(s *refundState) { s.HeldKop, s.Unanswered = 10000, unanswered }), 10000, 0, unanswered, 0, ""},
		{"unanswered attempt, other amount", with(func(s *refundState) { s.HeldKop, s.Unanswered = 10000, unanswered }), 5000, 0, nil,
			http.StatusConflict, "Предыдущий возврат на 100,00 ₽ остался без ответа ЮKassa"},
		{"unanswered attempt, amount 0", with(func(s *refundState) { s.HeldKop, s.Unanswered = 10000, unanswered }), 0, 0, nil,
			http.StatusConflict, "на ту же сумму"},
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
	tests := []struct {
		name       string
		a, b       [2]int64 // {payment id, reservation row id}
		wantEquals bool
	}{
		{"same attempt, same key", [2]int64{7, 3}, [2]int64{7, 3}, true},
		{"next attempt of the payment, new key", [2]int64{7, 3}, [2]int64{7, 4}, false},
		{"other payment, new key", [2]int64{7, 3}, [2]int64{8, 3}, false},
	}
	for _, tt := range tests {
		ka := refundIdempotenceKey(int(tt.a[0]), tt.a[1])
		kb := refundIdempotenceKey(int(tt.b[0]), tt.b[1])
		if (ka == kb) != tt.wantEquals || len(ka) > 64 || len(kb) > 64 {
			t.Errorf("%s: %q vs %q", tt.name, ka, kb)
		}
	}
	if got := refundIdempotenceKey(7, 3); got != "refund-7-3" {
		t.Errorf("key = %q, want refund-7-3", got)
	}
}

func TestClassifyRefundCall(t *testing.T) {
	ok := &yookassa.Refund{ID: "rf-1", Status: "succeeded"}
	tests := []struct {
		name string
		ref  *yookassa.Refund
		err  error
		want refundCallOutcome
	}{
		{"refund returned", ok, nil, refundCreated},
		{"202 accepted, no refund yet", &yookassa.Refund{}, nil, refundUnknown},
		{"400 invalid_request", nil, &yookassa.APIError{HTTPStatus: 400, Code: "invalid_request"}, refundRejected},
		{"429 too many requests", nil, &yookassa.APIError{HTTPStatus: 429}, refundRejected},
		{"wrapped 401", nil, fmt.Errorf("x: %w", &yookassa.APIError{HTTPStatus: 401}), refundRejected},
		{"500 internal", nil, &yookassa.APIError{HTTPStatus: 500}, refundUnknown},
		{"transport error", nil, errors.New("http do: i/o timeout"), refundUnknown},
	}
	for _, tt := range tests {
		if got := classifyRefundCall(tt.ref, tt.err); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

// The checks and the reservation happen under the payment row lock; held money is every
// pending or succeeded refund.
func TestRefundSQL(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if !strings.HasSuffix(norm(refundLockPaymentSQL), "FROM payments WHERE id = $1 FOR UPDATE") {
		t.Errorf("payment lock: %s", norm(refundLockPaymentSQL))
	}
	if !strings.Contains(norm(refundHeldSQL), "status IN ('pending', 'succeeded')") {
		t.Errorf("held refunds: %s", norm(refundHeldSQL))
	}
	if !strings.Contains(norm(refundUnansweredSQL), "status = 'pending' AND yookassa_refund_id IS NULL") {
		t.Errorf("unanswered refunds: %s", norm(refundUnansweredSQL))
	}
}

// ─── Fakes: PostgreSQL with a payment row lock, YooKassa with idempotence ──

// rfRow is a payment_refunds row.
type rfRow struct {
	id        int64
	amountKop int64
	status    string
	ykID      string
	desc      string
	stale     bool // created more than 23 h ago
}

// rfDB is one payment (id 7) and its refunds. A SELECT … FOR UPDATE inside a transaction
// takes the payment lock until COMMIT/ROLLBACK, like PostgreSQL's row lock.
type rfDB struct {
	missing     bool
	ykPayment   string
	status      string
	amountKop   int64
	refundedKop int64

	payLock sync.Mutex // the payment row lock

	mu      sync.Mutex
	rows    []rfRow
	nextID  int64
	journal []string
	// readPause widens the window between reading the refunds and reserving, so a
	// missing lock shows up as a double reservation in the concurrency test.
	readPause time.Duration
}

func (d *rfDB) Connect(context.Context) (driver.Conn, error) { return &rfConn{d: d}, nil }
func (d *rfDB) Driver() driver.Driver                        { return rfDriver{} }

type rfDriver struct{}

func (rfDriver) Open(string) (driver.Conn, error) { return nil, errors.New("rf driver: no DSN") }

type rfConn struct {
	d      *rfDB
	inTx   bool
	locked bool
	undo   []func()
}

func (c *rfConn) Prepare(q string) (driver.Stmt, error) {
	return nil, fmt.Errorf("rf driver: prepare not supported: %s", q)
}
func (c *rfConn) Close() error { return nil }
func (c *rfConn) Begin() (driver.Tx, error) {
	c.inTx, c.undo = true, nil
	c.d.add("BEGIN")
	return c, nil
}
func (c *rfConn) Commit() error { c.end("COMMIT"); return nil }
func (c *rfConn) Rollback() error {
	c.d.mu.Lock()
	for i := len(c.undo) - 1; i >= 0; i-- {
		c.undo[i]()
	}
	c.d.mu.Unlock()
	c.end("ROLLBACK")
	return nil
}

func (c *rfConn) end(what string) {
	c.inTx, c.undo = false, nil
	c.d.add(what)
	if c.locked {
		c.locked = false
		c.d.payLock.Unlock()
	}
}

func (d *rfDB) add(e string) { d.mu.Lock(); d.journal = append(d.journal, e); d.mu.Unlock() }

// note journals a statement; caller holds d.mu.
func (c *rfConn) note(e string) {
	if c.inTx {
		e = "tx:" + e
	}
	c.d.journal = append(c.d.journal, e)
}

var rfStatusList = regexp.MustCompile(`status IN \(([^)]*)\)`)

func kopecks(v any) int64 {
	s, _ := v.(string)
	f, _ := strconv.ParseFloat(s, 64)
	return int64(f*100 + 0.5)
}

func (c *rfConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	if strings.Contains(q, "FOR UPDATE") {
		if !c.inTx {
			return nil, errors.New("rf driver: FOR UPDATE outside a transaction locks nothing")
		}
		if !c.locked {
			d.payLock.Lock() // blocks while another transaction holds the payment
			c.locked = true
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case strings.Contains(q, "FROM payments WHERE id = $1 FOR UPDATE"):
		c.note("lock")
		if d.missing {
			return &rfRows{cols: []string{"yk", "status", "amount"}}, nil
		}
		return &rfRows{cols: []string{"yk", "status", "amount"}, rows: [][]driver.Value{{d.ykPayment, d.status, d.amountKop}}}, nil
	case strings.Contains(q, "SUM(ROUND(amount * 100))"):
		c.note("held")
		m := rfStatusList.FindStringSubmatch(q)
		if m == nil {
			return nil, fmt.Errorf("rf driver: held query without a status list: %s", q)
		}
		var sum int64
		for _, r := range d.rows {
			if strings.Contains(m[1], "'"+r.status+"'") {
				sum += r.amountKop
			}
		}
		return &rfRows{cols: []string{"sum"}, rows: [][]driver.Value{{sum}}}, nil
	case strings.Contains(q, "yookassa_refund_id IS NULL") && strings.Contains(q, "ORDER BY id LIMIT 1"):
		c.note("unanswered")
		if d.readPause > 0 {
			defer func() { d.mu.Unlock(); time.Sleep(d.readPause); d.mu.Lock() }()
		}
		for _, r := range d.rows {
			if r.status == "pending" && r.ykID == "" && !r.stale {
				return &rfRows{cols: []string{"id", "amount", "desc"}, rows: [][]driver.Value{{r.id, r.amountKop, r.desc}}}, nil
			}
		}
		return &rfRows{cols: []string{"id", "amount", "desc"}}, nil
	case strings.HasPrefix(q, "INSERT INTO payment_refunds") && strings.Contains(q, "RETURNING id"):
		c.note("reserve")
		d.nextID++
		row := rfRow{id: d.nextID, amountKop: kopecks(args[1].Value), status: "pending", desc: args[2].Value.(string)}
		d.rows = append(d.rows, row)
		c.undo = append(c.undo, func() { d.rows = d.rows[:len(d.rows)-1] })
		return &rfRows{cols: []string{"id"}, rows: [][]driver.Value{{row.id}}}, nil
	case strings.HasPrefix(q, "SELECT status, COALESCE(yookassa_refund_id, '') FROM payment_refunds WHERE id = $1"):
		c.note("row-state")
		for _, r := range d.rows {
			if r.id == args[0].Value.(int64) {
				return &rfRows{cols: []string{"status", "yk"}, rows: [][]driver.Value{{r.status, r.ykID}}}, nil
			}
		}
		return &rfRows{cols: []string{"status", "yk"}}, nil
	case strings.HasPrefix(q, "SELECT id FROM payment_refunds WHERE yookassa_refund_id = $1"):
		c.note("find-by-yookassa-id")
		for _, r := range d.rows {
			if r.ykID != "" && r.ykID == args[0].Value.(string) {
				return &rfRows{cols: []string{"id"}, rows: [][]driver.Value{{r.id}}}, nil
			}
		}
		return &rfRows{cols: []string{"id"}}, nil
	}
	return nil, fmt.Errorf("rf driver: unscripted query %s", q)
}

func (c *rfConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	q := strings.Join(strings.Fields(query), " ")
	d := c.d
	d.mu.Lock()
	defer d.mu.Unlock()
	find := func() *rfRow {
		for i := range d.rows {
			if d.rows[i].id == args[0].Value.(int64) {
				return &d.rows[i]
			}
		}
		return nil
	}
	switch {
	case strings.HasPrefix(q, "UPDATE payment_refunds SET yookassa_refund_id = $2"):
		c.note("link")
		r := find()
		if r == nil {
			return driver.RowsAffected(0), nil
		}
		r.ykID, r.amountKop, r.status = args[1].Value.(string), kopecks(args[2].Value), args[3].Value.(string)
		return driver.RowsAffected(1), nil
	case strings.HasPrefix(q, "DELETE FROM payment_refunds WHERE id = $1 AND yookassa_refund_id IS NULL"):
		c.note("drop-reservation")
		for i, r := range d.rows {
			if r.id == args[0].Value.(int64) && r.ykID == "" {
				d.rows = append(d.rows[:i:i], d.rows[i+1:]...)
				return driver.RowsAffected(1), nil
			}
		}
		return driver.RowsAffected(0), nil
	case strings.HasPrefix(q, "UPDATE payment_refunds SET status = 'failed'"):
		c.note("failed")
		if r := find(); r != nil && r.status == "pending" && r.ykID == "" {
			r.status = "failed"
			return driver.RowsAffected(1), nil
		}
		return driver.RowsAffected(0), nil
	case strings.HasPrefix(q, "UPDATE payments SET refunded_amount"):
		c.note("totals")
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
		return driver.RowsAffected(1), nil
	}
	return nil, fmt.Errorf("rf driver: unscripted statement %s", q)
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

// rfYooKassa is YooKassa's refund API with idempotence: a key it has seen returns the
// refund it created (same body) or 400 (other body). behaviour is consumed per call:
// ok, ok-pending, lost (refund created or found, answer lost), reject (400); down, 5xx and
// accepted (HTTP 202) never reach the refund logic.
type rfYooKassa struct {
	mu        sync.Mutex
	behaviour []string
	byKey     map[string]rfCreated
	created   int
	calls     []string // "key=amount description"
	onCall    func(string)
}

type rfCreated struct {
	req yookassa.CreateRefundRequest
	ref yookassa.Refund
}

func (y *rfYooKassa) CreateRefundWithKey(req yookassa.CreateRefundRequest, key string) (*yookassa.Refund, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.calls = append(y.calls, key+"="+req.Amount.Value+" "+req.Description)
	if y.onCall != nil {
		y.onCall("yookassa " + key)
	}
	b := "ok"
	if len(y.behaviour) > 0 {
		b, y.behaviour = y.behaviour[0], y.behaviour[1:]
	}
	switch b { // the request did not get through
	case "down":
		return nil, errors.New("http do: dial tcp: i/o timeout")
	case "5xx":
		return nil, &yookassa.APIError{HTTPStatus: 500, Code: "internal_server_error", Description: "Internal error"}
	case "accepted":
		return &yookassa.Refund{}, nil
	}
	lost := errors.New("read body: connection reset by peer")
	if c, ok := y.byKey[key]; ok {
		if !reflect.DeepEqual(c.req, req) {
			return nil, &yookassa.APIError{HTTPStatus: 400, Code: "invalid_request", Description: "Idempotence key reused with another request"}
		}
		if b == "lost" {
			return nil, lost
		}
		ref := c.ref
		return &ref, nil
	}
	if b == "reject" {
		return nil, &yookassa.APIError{HTTPStatus: 400, Code: "invalid_request", Description: "Refund amount is invalid"}
	}
	y.created++
	status := yookassa.RefundStatusSucceeded
	if b == "ok-pending" {
		status = yookassa.RefundStatusPending
	}
	ref := yookassa.Refund{ID: fmt.Sprintf("rf-%d", y.created), Status: status, Amount: req.Amount, PaymentID: req.PaymentID}
	if y.byKey == nil {
		y.byKey = map[string]rfCreated{}
	}
	y.byKey[key] = rfCreated{req: req, ref: ref}
	if b == "lost" {
		return nil, lost
	}
	return &ref, nil
}

// useRefundFakes routes db.DB, the YooKassa client and the log to the fakes.
// Tests using it must not run in parallel.
func useRefundFakes(t *testing.T, d *rfDB, y *rfYooKassa) {
	t.Helper()
	fake := sql.OpenDB(d)
	prevDB, prevClient, prevDelay, prevLog := db.DB, newRefundClient, refundRetryDelay, log.Writer()
	db.DB = fake
	newRefundClient = func() (refundCreator, error) { return y, nil }
	refundRetryDelay = 0
	log.SetOutput(io.Discard)
	y.onCall = d.add
	t.Cleanup(func() {
		db.DB, newRefundClient, refundRetryDelay = prevDB, prevClient, prevDelay
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

func (d *rfDB) summary() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, r := range d.rows {
		out = append(out, fmt.Sprintf("%d:%s:%s:%s", r.id, ykAmount(r.amountKop), r.status, r.ykID))
	}
	return out
}

// ─── Handler flows ────────────────────────────────────────────────────────

type rfStep struct {
	before   func(d *rfDB) // e.g. the refund webhook arriving between two requests
	body     string
	yk       []string // YooKassa behaviour for this step's calls
	wantCode int
	wantText string   // substring of "error" (or "message" on 200)
	wantSent []string // "key=amount description" sent to YooKassa in this step
}

func TestAdminRefundFlows(t *testing.T) {
	webhookRecords := func(ykID string, kop int64) func(d *rfDB) {
		return func(d *rfDB) {
			d.mu.Lock()
			d.nextID++
			d.rows = append(d.rows, rfRow{id: d.nextID, amountKop: kop, status: "succeeded", ykID: ykID})
			d.mu.Unlock()
		}
	}
	tests := []struct {
		name        string
		status      string
		missing     bool
		rows        []rfRow
		steps       []rfStep
		wantCreated int      // refunds that exist in YooKassa at the end
		wantRows    []string // payment_refunds at the end
		wantStatus  string   // payments.status at the end
	}{
		{
			name: "a pending refund reduces what is left; amount 0 refunds the rest",
			steps: []rfStep{
				{body: `{"amount":100,"reason":"part"}`, yk: []string{"ok-pending"}, wantCode: 200, wantText: "Возврат создан",
					wantSent: []string{"refund-7-1=100.00 part"}},
				{body: `{"amount":250}`, wantCode: 400, wantText: "Можно вернуть не больше 200,00 ₽ (остаток с учётом незавершённых возвратов)"},
				{body: `{"amount":0,"reason":"rest"}`, wantCode: 200, wantSent: []string{"refund-7-2=200.00 rest"}},
				{body: `{}`, wantCode: 400, wantText: "Можно вернуть не больше 0,00 ₽ (остаток с учётом незавершённых возвратов)"},
			},
			wantCreated: 2,
			wantRows:    []string{"1:100.00:pending:rf-1", "2:200.00:succeeded:rf-2"},
			wantStatus:  "succeeded", // the pending 100 ₽ is not refunded yet
		},
		{
			name: "no body is a full refund",
			steps: []rfStep{
				{body: ``, wantCode: 200, wantSent: []string{"refund-7-1=300.00 Admin refund"}},
			},
			wantCreated: 1, wantRows: []string{"1:300.00:succeeded:rf-1"}, wantStatus: "refunded",
		},
		{
			name:   "completed payments are paid",
			status: "completed",
			steps: []rfStep{
				{body: `{"amount":300}`, wantCode: 200, wantSent: []string{"refund-7-1=300.00 Admin refund"}},
			},
			wantCreated: 1, wantRows: []string{"1:300.00:succeeded:rf-1"}, wantStatus: "refunded",
		},
		{
			name:   "unpaid payment",
			status: "pending",
			steps: []rfStep{
				{body: `{"amount":100}`, wantCode: 400, wantText: "Вернуть можно только оплаченный платёж (статус платежа: pending)"},
			},
			wantStatus: "pending",
		},
		{
			name:    "payment not found",
			missing: true,
			steps:   []rfStep{{body: `{"amount":100}`, wantCode: 404, wantText: "Платёж не найден"}},
		},
		{
			name: "a body that does not parse refunds nothing",
			steps: []rfStep{
				{body: `{"amount":"100"}`, wantCode: 400, wantText: "Некорректный запрос"},
				{body: `{"amount":-5}`, wantCode: 400, wantText: "отрицательной"},
			},
			wantStatus: "succeeded",
		},
		{
			name: "lost answer: the same attempt is repeated with the same key and body",
			steps: []rfStep{
				{body: `{"amount":100,"reason":"first"}`, yk: []string{"lost", "down"}, wantCode: 504,
					wantText: "100,00 ₽ зарезервированы: повторите возврат на ту же сумму",
					wantSent: []string{"refund-7-1=100.00 first", "refund-7-1=100.00 first"}},
				{body: `{"amount":200}`, wantCode: 409, wantText: "Предыдущий возврат на 100,00 ₽ остался без ответа ЮKassa"},
				{body: `{"amount":100,"reason":"typed again"}`, wantCode: 200, wantText: "повтор попытки",
					wantSent: []string{"refund-7-1=100.00 first"}},
				{body: `{"amount":100,"reason":"second"}`, wantCode: 200, wantText: "Возврат создан",
					wantSent: []string{"refund-7-2=100.00 second"}},
			},
			wantCreated: 2,
			wantRows:    []string{"1:100.00:succeeded:rf-1", "2:100.00:succeeded:rf-2"},
			wantStatus:  "succeeded",
		},
		{
			name: "5xx and 202 are no answer: the amount stays reserved",
			steps: []rfStep{
				{body: `{"amount":100}`, yk: []string{"5xx", "accepted"}, wantCode: 504, wantText: "неизвестно, создан ли возврат",
					wantSent: []string{"refund-7-1=100.00 Admin refund", "refund-7-1=100.00 Admin refund"}},
				{body: `{"amount":250}`, wantCode: 409, wantText: "на ту же сумму"},
			},
			wantRows:   []string{"1:100.00:pending:"},
			wantStatus: "succeeded",
		},
		{
			name: "a rejected request frees the amount, the next attempt gets a new key",
			steps: []rfStep{
				{body: `{"amount":300}`, yk: []string{"reject"}, wantCode: 502, wantText: "ЮKassa отклонила возврат",
					wantSent: []string{"refund-7-1=300.00 Admin refund"}},
				{body: `{"amount":300}`, wantCode: 200, wantSent: []string{"refund-7-2=300.00 Admin refund"}},
			},
			wantCreated: 1,
			wantRows:    []string{"1:300.00:failed:", "2:300.00:succeeded:rf-1"},
			wantStatus:  "refunded",
		},
		{
			name: "the webhook recorded the refund first: it is counted once",
			steps: []rfStep{
				{body: `{"amount":100}`, yk: []string{"lost", "down"}, wantCode: 504,
					wantSent: []string{"refund-7-1=100.00 Admin refund", "refund-7-1=100.00 Admin refund"}},
				{before: webhookRecords("rf-1", 10000), body: `{"amount":100}`, wantCode: 200,
					wantSent: []string{"refund-7-1=100.00 Admin refund"}},
				{body: `{"amount":250}`, wantCode: 400, wantText: "не больше 200,00 ₽"},
			},
			wantCreated: 1,
			wantRows:    []string{"2:100.00:succeeded:rf-1"},
			wantStatus:  "succeeded",
		},
		{
			name: "an unanswered attempt older than the key window is not repeated but stays reserved",
			rows: []rfRow{{id: 1, amountKop: 10000, status: "pending", desc: "old", stale: true}},
			steps: []rfStep{
				{body: `{"amount":100}`, wantCode: 200, wantSent: []string{"refund-7-2=100.00 Admin refund"}},
				{body: `{"amount":150}`, wantCode: 400, wantText: "не больше 100,00 ₽"},
			},
			wantCreated: 1,
			wantRows:    []string{"1:100.00:pending:", "2:100.00:succeeded:rf-1"},
			wantStatus:  "succeeded",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &rfDB{missing: tt.missing, ykPayment: "pay-1", status: "succeeded", amountKop: 30000, rows: append([]rfRow(nil), tt.rows...)}
			if tt.status != "" {
				d.status = tt.status
			}
			for _, r := range tt.rows {
				d.nextID = max(d.nextID, r.id)
			}
			y := &rfYooKassa{}
			useRefundFakes(t, d, y)
			for i, st := range tt.steps {
				if st.before != nil {
					st.before(d)
				}
				y.mu.Lock()
				y.behaviour, y.calls = st.yk, nil
				y.mu.Unlock()
				code, m := postRefund(t, st.body)
				text, _ := m["error"].(string)
				if code == http.StatusOK {
					text, _ = m["message"].(string)
				}
				if code != st.wantCode || !strings.Contains(text, st.wantText) {
					t.Fatalf("step %d: %d %v; want %d %q", i+1, code, m, st.wantCode, st.wantText)
				}
				y.mu.Lock()
				sent := y.calls
				y.mu.Unlock()
				if !reflect.DeepEqual(sent, st.wantSent) {
					t.Fatalf("step %d: sent to YooKassa %q, want %q", i+1, sent, st.wantSent)
				}
			}
			if y.created != tt.wantCreated {
				t.Errorf("refunds created in YooKassa: %d, want %d", y.created, tt.wantCreated)
			}
			if got := d.summary(); !reflect.DeepEqual(got, tt.wantRows) {
				t.Errorf("payment_refunds %q, want %q", got, tt.wantRows)
			}
			if tt.wantStatus != "" && d.status != tt.wantStatus {
				t.Errorf("payments.status %q, want %q", d.status, tt.wantStatus)
			}
		})
	}
}

// The check and the reservation are one transaction behind the payment lock, and the
// YooKassa call runs while the payment is locked again.
func TestAdminRefundTransactions(t *testing.T) {
	d := &rfDB{ykPayment: "pay-1", status: "succeeded", amountKop: 30000}
	useRefundFakes(t, d, &rfYooKassa{})
	if code, m := postRefund(t, `{"amount":100}`); code != 200 {
		t.Fatalf("%d %v", code, m)
	}
	want := []string{
		"BEGIN", "tx:lock", "tx:held", "tx:unanswered", "tx:reserve", "COMMIT",
		"BEGIN", "tx:lock", "tx:row-state", "yookassa refund-7-1", "tx:find-by-yookassa-id", "tx:link", "tx:totals", "COMMIT",
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !reflect.DeepEqual(d.journal, want) {
		t.Fatalf("journal\n got %q\nwant %q", d.journal, want)
	}
}

// Concurrent refunds of the same payment serialize on the payment lock: 300 ₽ paid, ten
// admins ask for 200 ₽ at once, exactly one refund is created.
func TestAdminRefundConcurrent(t *testing.T) {
	d := &rfDB{ykPayment: "pay-1", status: "succeeded", amountKop: 30000, readPause: 5 * time.Millisecond}
	y := &rfYooKassa{}
	useRefundFakes(t, d, y)
	const n = 10
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handleAdminPaymentCancel(rec, httptest.NewRequest(http.MethodPost, "/api/admin/payments/7/cancel", strings.NewReader(`{"amount":200}`)))
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	count := map[int]int{}
	for c := range codes {
		count[c]++
	}
	// The others get 400 (nothing left) or 409 (they joined the in-flight attempt, which
	// the first request completed).
	if count[http.StatusOK] != 1 || count[http.StatusBadRequest]+count[http.StatusConflict] != n-1 || y.created != 1 || len(y.calls) != 1 {
		t.Fatalf("answers %v, refunds created %d, YooKassa calls %q; want one 200 and one refund", count, y.created, y.calls)
	}
}
