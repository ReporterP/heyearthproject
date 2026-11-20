package main

import (
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// An entry on the canary (rotation 'test') reaches only the «Тестовый пользователь» of «Входы»;
// an entry out of rotation reaches nobody.
func TestPGSubscriptionTestEntryOnlyForTestUser(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node) VALUES
		(31, 'all', '198.51.100.31', 'relay', 'online', 'online', FALSE),
		(32, 'test', '198.51.100.32', 'relay', 'online', 'online', FALSE),
		(33, 'off', '198.51.100.33', 'relay', 'online', 'online', FALSE)`)
	testpg.MustExec(t, conn, `INSERT INTO entry_state (entry_id, rotation, ext_status) VALUES
		(31, 'all', 'healthy'), (32, 'test', 'healthy'), (33, 'off', 'healthy')`)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('entry_failover_test_user', 'canary-user')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	ids := func(user string) []int {
		t.Helper()
		p, err := loadEntryPlan(user, "203.0.113.10")
		if err != nil {
			t.Fatal(err)
		}
		var out []int
		for _, r := range p.reserves {
			out = append(out, r.id)
		}
		return out
	}
	if got := ids("someone-else"); len(got) != 1 || got[0] != 31 {
		t.Fatalf("a user's reserves: %v", got)
	}
	if got := ids("canary-user"); len(got) != 2 || got[0] != 31 || got[1] != 32 {
		t.Fatalf("the test user's reserves: %v", got)
	}
	if got := ids(""); len(got) != 1 {
		t.Fatalf("no user: %v", got)
	}
}
