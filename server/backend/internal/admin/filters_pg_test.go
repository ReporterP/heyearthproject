package admin

import (
	"encoding/json"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// C4: the «с … по …» and «администратор» filters of «Платежи» and «Журнал аудита» work on the
// server (whole Moscow days, both ends included).
func TestListDateFilters(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO payments (user_id_text, amount, currency, status, provider, created_at) VALUES
		('a', 100, 'RUB', 'succeeded', 'yookassa', '2026-11-09 23:30:00+03'),
		('b', 200, 'RUB', 'succeeded', 'yookassa', '2026-11-10 00:10:00+03'),
		('c', 300, 'RUB', 'succeeded', 'yookassa', '2026-11-11 23:59:00+03'),
		('d', 400, 'RUB', 'succeeded', 'yookassa', '2026-11-12 00:00:00+03')`)
	testpg.MustExec(t, conn, `INSERT INTO admin_audit_log (admin_id, admin_username, action, target_type, target_id, created_at) VALUES
		(NULL, 'owner', 'update_user', 'user', '1', '2026-11-10 12:00:00+03'),
		(NULL, 'helper', 'update_user', 'user', '2', '2026-11-10 13:00:00+03'),
		(NULL, 'helper', 'create_payment', 'payment', '3', '2026-11-12 13:00:00+03')`)
	get := func(path string, isAudit bool) (int, int) {
		h := ListPaymentsHandler
		if isAudit {
			h = ListAuditLogHandler
		}
		rec := call(h, "GET", path, "")
		var out struct {
			Total int `json:"total"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Total
	}
	if code, n := get("/api/admin/payments?from=2026-11-10&to=2026-11-11", false); code != 200 || n != 2 {
		t.Errorf("payments 10..11 Nov: %d %d (want the 00:10 and the 23:59 ones)", code, n)
	}
	if _, n := get("/api/admin/payments?from=2026-11-12", false); n != 1 {
		t.Errorf("payments from 12 Nov: %d", n)
	}
	if code, _ := get("/api/admin/payments?from=10.11.2026", false); code != 400 {
		t.Errorf("a malformed date: %d", code)
	}
	if _, n := get("/api/admin/audit-log?admin=help&from=2026-11-10&to=2026-11-10", true); n != 1 {
		t.Errorf("audit: helper on 10 Nov: %d", n)
	}
	if _, n := get("/api/admin/audit-log?admin=help", true); n != 2 {
		t.Errorf("audit: helper: %d", n)
	}
}
