package main

import (
	"context"
	"testing"
	"time"

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

// The newest job of each kind per entry; Setup is the newer of provision / reprovision.
func TestPGLastEntryJobs(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node) VALUES
		(41, 'e', '198.51.100.41', 'relay', 'online', 'online', FALSE), (42, 'f', '198.51.100.42', 'relay', 'online', 'online', FALSE)`)
	testpg.MustExec(t, conn, `INSERT INTO provision_jobs (id, server_id, kind, status, finished_at) VALUES
		(101, 41, 'pathcheck', 'failed', NOW() - interval '2 hours'),
		(102, 41, 'provision', 'succeeded', NOW() - interval '90 minutes'),
		(103, 41, 'reprovision', 'succeeded', NOW() - interval '1 hour'),
		(104, 41, 'pathcheck', 'succeeded', NOW() - interval '10 minutes'),
		(105, 41, 'tunnelcheck', 'interrupted', NOW() - interval '5 minutes'),
		(106, 42, 'verify', 'succeeded', NOW())`)
	got, err := lastEntryJobs(context.Background(), []int{41, 42})
	if err != nil {
		t.Fatal(err)
	}
	c := got[41]
	if c.Path == nil || c.Path.ID != 104 || c.Setup == nil || c.Setup.ID != 103 || c.Tunnel == nil || c.Tunnel.Status != "interrupted" || c.Path.FinishedAt == nil {
		t.Fatalf("entry 41: %+v", c)
	}
	if c := got[42]; c.Path != nil || c.Setup != nil || c.Tunnel != nil {
		t.Fatalf("entry 42 has only a verify job: %+v", c)
	}
	c.judge(time.Now())
	if !c.PathFresh || c.TunnelFresh {
		t.Fatalf("fresh: %+v", c)
	}
}
