package biz

import (
	"context"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A7: one quota rule for every protocol. An over-quota user is in the xray cutoff set and
// refused at the Hysteria2 login while quota_cutoff_enabled is "true" (migration 000063
// sets it); promocode access (traffic_limit = 0) is unlimited on both.
func TestQuotaCutsEveryProtocol(t *testing.T) {
	conn := testpg.Open(t)
	var flag string
	if err := conn.QueryRow(`SELECT value FROM app_settings WHERE key = 'quota_cutoff_enabled'`).Scan(&flag); err != nil || flag != "true" {
		t.Fatalf("migration default: %q %v", flag, err)
	}
	var planID int
	if err := conn.QueryRow(`
		INSERT INTO plans (slug, name, description, price_rub, duration_days, traffic_limit_bytes, allowed_protocols, is_active, express_enabled)
		VALUES ('t-limited', 'L', '', 0, 30, $1, ARRAY['all','hysteria2'], TRUE, TRUE) RETURNING id`, int64(1<<30)).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	pw := func(c string) string { return strings.Repeat(c, 64) }
	mk := func(userID, password string, limit any) {
		id := testpg.NewUser(t, conn, userID)
		testpg.MustExec(t, conn, `UPDATE users SET hysteria2_password = $1 WHERE id = $2`, password, id)
		testpg.MustExec(t, conn, `INSERT INTO user_subscriptions (user_id, plan_id, valid_until, status, started_at, traffic_limit)
			VALUES ($1, $2, NOW() + INTERVAL '20 days', 'active', NOW() - INTERVAL '3 days', $3)`, id, planID, limit)
		// 2 GB used this cycle: over the plan's 1 GB.
		testpg.MustExec(t, conn, `INSERT INTO traffic_stats (user_id, upload_bytes, download_bytes, recorded_at) VALUES ($1, $2, $2, NOW() - INTERVAL '1 hour')`, userID, int64(1<<30))
	}
	mk("over", pw("a"), nil)
	mk("promo", pw("b"), int64(0))

	over, err := ListQuotaExceededUserIDsContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := over["over"]; !ok {
		t.Error("over-quota user is not in the xray cutoff set")
	}
	if _, ok := over["promo"]; ok {
		t.Error("promocode access (limit 0) is in the cutoff set")
	}
	if ValidateHysteria2Auth(pw("a")).OK {
		t.Error("over-quota user may log in to Hysteria2")
	}
	if !ValidateHysteria2Auth(pw("b")).OK {
		t.Error("promocode access refused at the Hysteria2 login")
	}

	// The owner switches the cutoff off: nobody is cut, on any protocol.
	testpg.MustExec(t, conn, `UPDATE app_settings SET value = 'false' WHERE key = 'quota_cutoff_enabled'`)
	if !ValidateHysteria2Auth(pw("a")).OK {
		t.Error("cutoff off, but the Hysteria2 login is still refused")
	}
}
