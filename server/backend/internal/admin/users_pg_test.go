package admin

import (
	"database/sql"
	"database/sql/driver"
	"strconv"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// C2: a user who paid or moved money through the wallet is never deleted (54-ФЗ receipts, the
// ledger); a user without money is.
func TestDeleteUserKeepsMoney(t *testing.T) {
	conn := testpg.Open(t)
	prev := auditLogExec
	t.Cleanup(func() { auditLogExec = prev })
	auditLogExec = func(string, ...any) (sql.Result, error) { return driver.RowsAffected(1), nil }
	paid := testpg.NewUser(t, conn, "paid-user")
	walleted := testpg.NewUser(t, conn, "wallet-user")
	empty := testpg.NewUser(t, conn, "empty-user")
	testpg.MustExec(t, conn, `INSERT INTO payments (user_id, user_id_text, amount, currency, status, provider) VALUES ($1, 'paid-user', 250, 'RUB', 'succeeded', 'yookassa')`, paid)
	testpg.MustExec(t, conn, `INSERT INTO wallet_transactions (user_id, amount_kop, balance_after_kop, kind, idempotency_key) VALUES ($1, 10000, 10000, 'admin_credit', 'k1')`, walleted)
	del := func(id int) int {
		return callAs(DeleteUserHandler, "DELETE", "/api/admin/users/"+strconv.Itoa(id), "", "users.write").Code
	}
	if c := del(paid); c != 409 {
		t.Errorf("a paying user deleted: %d", c)
	}
	if c := del(walleted); c != 409 {
		t.Errorf("a user with wallet history deleted: %d", c)
	}
	if c := del(empty); c != 200 {
		t.Errorf("a user without money: %d", c)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM users WHERE id IN ($1, $2)`, paid, walleted).Scan(&n)
	if n != 2 {
		t.Errorf("refused deletions removed rows: %d left", n)
	}
}
