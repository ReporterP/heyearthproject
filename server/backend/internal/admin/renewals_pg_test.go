package admin

import (
	"encoding/json"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// C10: «Не прошло продление» lists paid auto-renewing subscriptions the wallet cannot renew.
func TestFailingRenewals(t *testing.T) {
	conn := testpg.Open(t)
	var plan int
	if err := conn.QueryRow(`INSERT INTO plans (slug, name, price_rub, duration_days, is_active) VALUES ('t-paid', 'Тест', 250, 30, TRUE) RETURNING id`).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	poor := testpg.NewUser(t, conn, "poor")
	rich := testpg.NewUser(t, conn, "rich")
	ended := testpg.NewUser(t, conn, "ended")
	manual := testpg.NewUser(t, conn, "manual")
	later := testpg.NewUser(t, conn, "later")
	for _, c := range []struct {
		uid   int
		in    string
		renew bool
	}{{poor, "2 days", true}, {rich, "2 days", true}, {ended, "-3 days", true}, {manual, "2 days", false}, {later, "20 days", true}} {
		testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, auto_renew, status) VALUES ($1, $2, NOW() + $3::interval, $4, 'active')`, c.uid, plan, c.in, c.renew)
	}
	testpg.MustExec(t, conn, `INSERT INTO user_wallets (user_id, balance_kop) VALUES ($1, 10000), ($2, 30000), ($3, 0)
		ON CONFLICT (user_id) DO UPDATE SET balance_kop = EXCLUDED.balance_kop`, poor, rich, ended)
	rec := call(FailingRenewalsHandler, "GET", "/api/admin/renewals/failing", "")
	var out struct {
		Items []failingRenewal `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := map[string]failingRenewal{}
	for _, it := range out.Items {
		got[it.UserID] = it
	}
	if len(got) != 2 || got["poor"].ShortfallKop != 15000 || got["poor"].Ended || !got["ended"].Ended {
		t.Errorf("failing renewals %+v", out.Items)
	}
}
