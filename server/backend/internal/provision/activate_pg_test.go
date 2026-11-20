package provision

import (
	"context"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A relay that a setup job brings online starts on the canary (rotation 'test'); one the owner
// took out of rotation stays 'off'; an exit gets no entry_state row.
func TestPGActivateEntryRotation(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, is_exit_node) VALUES
		(21, 'new', '198.51.100.21', 'relay', 'provisioning', 'offline', FALSE),
		(22, 'off', '198.51.100.22', 'relay', 'provisioning', 'offline', FALSE),
		(23, 'back', '198.51.100.23', 'relay', 'provisioning', 'offline', FALSE),
		(24, 'exit', '192.0.2.24', 'exit', 'provisioning', 'offline', TRUE)`)
	testpg.MustExec(t, conn, `INSERT INTO entry_state (entry_id, rotation, rotation_reason) VALUES (22, 'off', 'потери'), (23, 'all', '')`)
	h := (&SQLStore{DB: conn}).Hooks(nil, nil)
	for _, id := range []int{21, 22, 23, 24} {
		if err := h.Activate(context.Background(), id); err != nil {
			t.Fatalf("activate %d: %v", id, err)
		}
	}
	want := map[int]string{21: "test", 22: "off", 23: "test"}
	for id, rot := range want {
		var got, reason, life string
		if err := conn.QueryRow(`SELECT e.rotation, e.rotation_reason, s.lifecycle_state FROM entry_state e JOIN servers s ON s.id = e.entry_id WHERE e.entry_id = $1`, id).
			Scan(&got, &reason, &life); err != nil {
			t.Fatalf("entry %d: %v", id, err)
		}
		if got != rot || life != "online" || (id == 22 && reason != "потери") {
			t.Errorf("entry %d: rotation %s reason %q lifecycle %s, want %s", id, got, reason, life, rot)
		}
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM entry_state WHERE entry_id = 24`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("an exit got an entry_state row: %d %v", n, err)
	}
	if err := h.Activate(context.Background(), 21); err == nil {
		t.Fatal("activating an online node twice must fail")
	}
}
