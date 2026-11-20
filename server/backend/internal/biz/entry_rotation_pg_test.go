package biz

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// relays of the rotation tests: 11 all, 12 test, 13 off, 14 all but lifecycle failed, 15 online
// without an entry_state row (= all).
func seedRotation(t *testing.T) {
	t.Helper()
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('main_server_host', '203.0.113.10')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status, relay_tunnel) VALUES
		(11, 'a', '198.51.100.11', 'relay', 'online', 'online', FALSE),
		(12, 't', '198.51.100.12', 'relay', 'online', 'online', TRUE),
		(13, 'o', '198.51.100.13', 'relay', 'online', 'online', FALSE),
		(14, 'f', '198.51.100.14', 'relay', 'failed', 'offline', FALSE),
		(15, 'n', '198.51.100.15', 'relay', 'online', 'online', FALSE)`)
	testpg.MustExec(t, conn, `INSERT INTO entry_state (entry_id, rotation, rotation_reason) VALUES
		(11, 'all', ''), (12, 'test', 'новый вход'), (13, 'off', 'потери у провайдера'), (14, 'all', '')`)
}

func entryIDs(es []Entry) []int {
	var out []int
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestListEntriesByRotation(t *testing.T) {
	seedRotation(t)
	ctx := context.Background()
	check := func(name string, es []Entry, err error, want []int) {
		t.Helper()
		if err != nil || !slices.Equal(entryIDs(es), want) {
			t.Fatalf("%s: %v %v, want %v", name, entryIDs(es), err, want)
		}
	}
	es, err := ListRelayEntries(ctx)
	check("published", es, err, []int{11, 15})
	es, err = ListSubscriptionRelays(ctx, false)
	check("subscription", es, err, []int{11, 15})
	es, err = ListSubscriptionRelays(ctx, true)
	check("subscription of the test user", es, err, []int{11, 12, 15})
	es, err = ListMonitoredEntries(ctx)
	check("monitored", es, err, []int{0, 11, 12, 13, 15})
	es, err = ListAdminEntries(ctx)
	check("admin", es, err, []int{0, 11, 12, 13, 14, 15})
	off := es[slices.IndexFunc(es, func(e Entry) bool { return e.ID == 13 })]
	if off.Rotation != EntryRotationOff || off.RotationReason != "потери у провайдера" || off.Tunnel {
		t.Fatalf("entry 13: %+v", off)
	}
	if tst := es[slices.IndexFunc(es, func(e Entry) bool { return e.ID == 12 })]; tst.Rotation != EntryRotationTest || !tst.Tunnel {
		t.Fatalf("entry 12: %+v", tst)
	}
	if none := es[slices.IndexFunc(es, func(e Entry) bool { return e.ID == 15 })]; none.Rotation != EntryRotationAll {
		t.Fatalf("entry 15 without a row: %+v", none)
	}
}

func TestSetEntryRotationFrom(t *testing.T) {
	seedRotation(t)
	ctx := context.Background()
	if err := SetEntryRotation(ctx, 11, []string{EntryRotationAll, EntryRotationTest}, EntryRotationOff, "потери", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := SetEntryRotation(ctx, 11, []string{EntryRotationAll}, EntryRotationOff, "ещё раз", "owner"); !errors.Is(err, ErrRotationChanged) {
		t.Fatalf("a changed rotation: %v", err)
	}
	// A relay without a row is 'all'.
	if err := SetEntryRotation(ctx, 15, []string{EntryRotationAll}, EntryRotationOff, "x", "owner"); err != nil {
		t.Fatal(err)
	}
	es, _ := ListAdminEntries(ctx)
	for _, e := range es {
		if (e.ID == 11 || e.ID == 15) && (e.Rotation != EntryRotationOff || e.RotationAt == nil || e.RotationBy != "owner") {
			t.Fatalf("entry %d: %+v", e.ID, e)
		}
	}
	if err := SetEntryRotation(ctx, 0, []string{EntryRotationAll}, EntryRotationOff, "x", "owner"); err == nil {
		t.Fatal("the Hub has no rotation")
	}
	if err := SetEntryRotation(ctx, 11, []string{EntryRotationOff}, "maybe", "x", "owner"); err == nil {
		t.Fatal("unknown rotation accepted")
	}
}

// Entry 6, taken out of rotation by SQL on 20.11 (lifecycle offline), becomes online + off.
func TestMigration97Entry6(t *testing.T) {
	conn := testpg.Open(t)
	testpg.MustExec(t, conn, `INSERT INTO servers (id, name, ip_address, role, lifecycle_state, status) VALUES
		(6, 'Вход 6', '198.51.100.42', 'relay', 'offline', 'offline'), (5, 'Вход 5', '198.51.100.43', 'relay', 'online', 'online')`)
	up, err := os.ReadFile("../../migrations/000097_entry_rotation.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	testpg.MustExec(t, conn, string(up))
	var life, rot string
	if err := conn.QueryRow(`SELECT s.lifecycle_state, e.rotation FROM servers s JOIN entry_state e ON e.entry_id = s.id WHERE s.id = 6`).Scan(&life, &rot); err != nil {
		t.Fatal(err)
	}
	if life != "online" || rot != "off" {
		t.Fatalf("entry 6: %s %s", life, rot)
	}
	es, err := ListRelayEntries(context.Background())
	if err != nil || !slices.Equal(entryIDs(es), []int{5}) {
		t.Fatalf("published after the migration: %v %v", entryIDs(es), err)
	}
}
