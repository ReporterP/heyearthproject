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
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// rvPayment is a payments row for the revenue queries.
type rvPayment struct {
	status              string
	amount              float64
	test                bool
	confirmedAt, paidAt time.Time // zero = NULL
}

// rvDB evaluates the revenue queries over rvPayment rows, reading the status filter and
// the date column from the SQL text: a query that counts only 'completed' sees 0 ₽ of
// YooKassa money, exactly as on prod. COUNT(…) answers 0, anything else fails (the
// dashboard tolerates that for traffic, top users and server health).
type rvDB struct{ rows []rvPayment }

func (d *rvDB) Connect(context.Context) (driver.Conn, error) { return &rvConn{d}, nil }
func (d *rvDB) Driver() driver.Driver                        { return rvDriver{} }

type rvDriver struct{}

func (rvDriver) Open(string) (driver.Conn, error) { return nil, errors.New("rv driver: no DSN") }

type rvConn struct{ d *rvDB }

func (c *rvConn) Prepare(q string) (driver.Stmt, error) {
	return nil, fmt.Errorf("rv driver: no prepare: %s", q)
}
func (c *rvConn) Close() error              { return nil }
func (c *rvConn) Begin() (driver.Tx, error) { return nil, errors.New("rv driver: no tx") }

var (
	rvStatusIn = regexp.MustCompile(`status IN \(([^)]*)\)`)
	rvStatusEq = regexp.MustCompile(`status = '([a-z_]+)'`)
)

func (r rvPayment) paidPerQuery(q string) bool {
	switch {
	case rvStatusIn.MatchString(q):
		return strings.Contains(rvStatusIn.FindStringSubmatch(q)[1], "'"+r.status+"'")
	case rvStatusEq.MatchString(q):
		return rvStatusEq.FindStringSubmatch(q)[1] == r.status
	}
	return true
}

func (c *rvConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	q := strings.Join(strings.Fields(query), " ")
	one := func(v driver.Value) driver.Rows { return &listRows{cols: []string{"v"}, rows: [][]driver.Value{{v}}} }
	switch {
	case strings.Contains(q, "SELECT COUNT("):
		return one(int64(0)), nil
	case strings.Contains(q, "FROM payments") && strings.Contains(q, "date_trunc('month', NOW())"):
		month := time.Date(time.Now().Year(), time.Now().Month(), 1, 0, 0, 0, 0, time.Local)
		var sum float64
		for _, r := range c.d.rows {
			when := r.confirmedAt
			if strings.Contains(q, "COALESCE(confirmed_at, paid_at)") && when.IsZero() {
				when = r.paidAt
			}
			if r.paidPerQuery(q) && !r.test && !when.IsZero() && !when.Before(month) {
				sum += r.amount
			}
		}
		return one(sum), nil
	case strings.Contains(q, "SUM(p.amount)"):
		var sum float64
		for _, r := range c.d.rows {
			if r.paidPerQuery(q) && !r.test {
				sum += r.amount
			}
		}
		return one(sum), nil
	case strings.Contains(q, "FROM payments p") && strings.Contains(q, "ORDER BY"):
		return &listRows{cols: make([]string, 24)}, nil
	}
	return nil, fmt.Errorf("rv driver: %s", q)
}

func useRevenueDB(t *testing.T) {
	t.Helper()
	now := time.Now()
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).Add(-24 * time.Hour)
	d := &rvDB{rows: []rvPayment{
		{status: "succeeded", amount: 1000, confirmedAt: now},
		{status: "succeeded", amount: 500, paidAt: now}, // old auto-renew: no confirmed_at
		{status: "completed", amount: 200, confirmedAt: now},
		{status: "succeeded", amount: 900, confirmedAt: lastMonth},
		{status: "refunded", amount: 300, confirmedAt: now},
		{status: "canceled", amount: 400},
		{status: "pending", amount: 50},
		{status: "succeeded", amount: 700, test: true, confirmedAt: now},
	}}
	fake := sql.OpenDB(d)
	prev, prevLog := db.DB, log.Writer()
	db.DB = fake
	log.SetOutput(io.Discard)
	t.Cleanup(func() { db.DB = prev; log.SetOutput(prevLog); _ = fake.Close() })
}

// Revenue counts every paid payment — YooKassa's are 'succeeded' (prod: 37 succeeded,
// 0 completed, so the old 'completed'-only revenue showed 0 ₽) — never refunded,
// canceled, pending or test ones.
func TestRevenueCountsPaidPayments(t *testing.T) {
	useRevenueDB(t)
	t.Run("dashboard: this month", func(t *testing.T) {
		rec := httptest.NewRecorder()
		OverviewHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/dashboard/overview", nil))
		var out struct {
			RevenueMonth float64 `json:"revenue_month"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%d %s (%v)", rec.Code, rec.Body.String(), err)
		}
		if out.RevenueMonth != 1700 { // 1000 + 500 (paid_at only) + 200 (completed)
			t.Fatalf("revenue_month %v, want 1700", out.RevenueMonth)
		}
	})
	t.Run("payments list: real total", func(t *testing.T) {
		rec := httptest.NewRecorder()
		ListPaymentsHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/payments", nil))
		var out struct {
			RealTotalAmount float64 `json:"real_total_amount"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("%d %s (%v)", rec.Code, rec.Body.String(), err)
		}
		if out.RealTotalAmount != 2600 { // 1000 + 500 + 200 + 900 (last month)
			t.Fatalf("real_total_amount %v, want 2600", out.RealTotalAmount)
		}
	})
}
