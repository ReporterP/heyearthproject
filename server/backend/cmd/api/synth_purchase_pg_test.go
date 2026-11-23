package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/testpg"
)

// Review Focus 4: the synthetic purchase leaves no row, no receipt, no sync — when it passes and
// when it fails at any step.
func TestSyntheticPurchaseLeavesNothing(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()
	if _, err := conn.Exec(`INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes, allowed_protocols, is_active, auto_renew_default)
		VALUES ('t-paid', 't-paid', '', 300, 30, NULL, ARRAY['all'], TRUE, TRUE), ('t-free', 't-free', '', 0, 7, NULL, ARRAY['all'], TRUE, FALSE)`); err != nil {
		t.Fatal(err)
	}
	testpg.NewUser(t, conn, "synth-user")
	if _, err := conn.Exec(`INSERT INTO app_settings (key, value) VALUES ('sub_auto_test_user', 'synth-user') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`); err != nil {
		t.Fatal(err)
	}
	var syncs atomic.Int32
	prevX, prevH := biz.XraySyncFn, biz.Hysteria2SyncFn
	t.Cleanup(func() { biz.XraySyncFn, biz.Hysteria2SyncFn = prevX, prevH })
	biz.XraySyncFn = func() { syncs.Add(1) }
	biz.Hysteria2SyncFn = func() { syncs.Add(1) }

	tables := []string{"wallet_transactions", "user_wallets", "user_subscriptions", "payments", "wallet_receipts", "email_log", "tg_outbox"}
	counts := func() map[string]int {
		out := map[string]int{}
		for _, tbl := range tables {
			var n int
			if err := db.DB.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil {
				n = -1 // a table this schema does not have
			}
			out[tbl] = n
		}
		var bal int64
		_ = db.DB.QueryRow(`SELECT COALESCE(sum(balance_kop), 0) FROM user_wallets`).Scan(&bal)
		out["balance"] = int(bal)
		return out
	}
	before := counts()
	same := func(name string) {
		t.Helper()
		after := counts()
		for k, v := range before {
			if after[k] != v {
				t.Errorf("%s: %s %d → %d", name, k, v, after[k])
			}
		}
	}

	d := purchaseDeps{
		plans: func(context.Context) (int, []byte, error) {
			return 200, []byte(`[{"id":1,"price_rub":300,"is_active":true}]`), nil
		},
		provider: func(context.Context) error { return nil },
		testUser: func(context.Context) (string, error) { return "synth-user", nil },
	}
	checks := syntheticPurchaseWith(ctx, d)
	for _, c := range checks {
		if !c.OK || c.Blind {
			t.Fatalf("a good run failed: %+v", checks)
		}
	}
	same("a passing run")

	for name, fail := range map[string]func(*purchaseDeps){
		"plans":    func(d *purchaseDeps) { d.plans = func(context.Context) (int, []byte, error) { return 502, nil, nil } },
		"provider": func(d *purchaseDeps) { d.provider = func(context.Context) error { return errors.New("timeout") } },
		"inside the transaction": func(d *purchaseDeps) {
			d.afterPurchase = func() error { return errors.New("injected after the purchase") }
		},
	} {
		dd := d
		fail(&dd)
		checks := syntheticPurchaseWith(ctx, dd)
		failed := 0
		for _, c := range checks {
			if !c.OK {
				failed++
			}
		}
		if failed == 0 {
			t.Errorf("%s: nothing failed: %+v", name, checks)
		}
		same("failure at " + name)
	}
	time.Sleep(2500 * time.Millisecond) // biz.RequestSync fires after 2 s of quiet
	if n := syncs.Load(); n != 0 {
		t.Errorf("a sync was requested %d times", n)
	}
}
