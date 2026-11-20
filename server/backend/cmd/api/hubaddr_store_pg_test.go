package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/testpg"
)

func TestHubAddrStoreLifecycle(t *testing.T) {
	conn := testpg.Open(t)
	ctx := context.Background()

	id, err := createHubAddrChange(ctx, conn, hubAddrChange{OldIP: "192.0.2.10", NewIP: "198.51.100.20", Iface: "ens3", Gateway: "203.0.113.1", CreatedBy: "owner"})
	if err != nil || id == 0 {
		t.Fatalf("create: %d %v", id, err)
	}
	if _, err := createHubAddrChange(ctx, conn, hubAddrChange{OldIP: "192.0.2.10", NewIP: "198.51.100.21"}); !errors.Is(err, errHubAddrActive) {
		t.Fatalf("second active change: want errHubAddrActive, got %v", err)
	}
	c, ok, err := activeHubAddrChange(ctx, conn)
	if err != nil || !ok || c.ID != id || c.State != hubAddrDraft || c.NewIP != "198.51.100.20" {
		t.Fatalf("active: %+v %v %v", c, ok, err)
	}

	if err := setHubAddrState(ctx, conn, id, hubAddrDraft, hubAddrSwitching, nil); err == nil {
		t.Fatal("draft → switching must be refused")
	}
	if err := setHubAddrState(ctx, conn, id, hubAddrAdded, hubAddrChecked, nil); err == nil {
		t.Fatal("a stale from-state must be refused")
	}
	if err := setHubAddrState(ctx, conn, id, hubAddrDraft, hubAddrAdded, func(c *hubAddrChange) { c.PhoneSkipped = true }); err != nil {
		t.Fatalf("draft → added: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := saveHubAddrStep(ctx, conn, id, "add", hubAddrStep{Status: hubStepRunning, StartedAt: &now, Log: "ip addr add"}); err != nil {
		t.Fatal(err)
	}
	if n, err := markHubAddrInterrupted(ctx, conn); err != nil || n != 1 {
		t.Fatalf("interrupted: %d %v", n, err)
	}
	c, err = getHubAddrChange(ctx, conn, id)
	if err != nil || c.State != hubAddrAdded || !c.PhoneSkipped || c.Steps["add"].Status != hubStepInterrupted || c.Steps["add"].Log != "ip addr add" {
		t.Fatalf("after interrupt: %+v %v", c, err)
	}

	if err := setHubAddrState(ctx, conn, id, hubAddrAdded, hubAddrCancelled, nil); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, ok, _ := activeHubAddrChange(ctx, conn); ok {
		t.Fatal("a cancelled change is not active")
	}
	if _, err := createHubAddrChange(ctx, conn, hubAddrChange{OldIP: "192.0.2.10", NewIP: "198.51.100.21"}); err != nil {
		t.Fatalf("a new change after cancel: %v", err)
	}
}

func TestHubAddrMigrationExtras(t *testing.T) {
	conn := testpg.Open(t)
	var n int
	// super_admin itself is created at the first login (ensureSuperAdminRole, from the catalog), so
	// a fresh database has the permission row only; prod gets the grant from the migration.
	if err := conn.QueryRow(`SELECT count(*) FROM admin_permissions WHERE permission_key = 'hub.address'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("permission hub.address: %d %v", n, err)
	}
	if err := conn.QueryRow(`SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'users' AND column_name = 'subscription_fetched_at'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("users.subscription_fetched_at: %d %v", n, err)
	}
}

func TestHubAddrNext(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{hubAddrDraft, hubAddrAdded, true},
		{hubAddrChecked, hubAddrAdded, true}, // a failed re-check steps back
		{hubAddrDNSReady, hubAddrSwitching, true},
		{hubAddrSwitching, hubAddrTransition, true},
		{hubAddrSwitching, hubAddrReverting, true},
		{hubAddrTransition, hubAddrReverting, true},
		{hubAddrReverting, hubAddrReverted, true},
		{hubAddrTransition, hubAddrFinished, true},
		{hubAddrSwitching, hubAddrCancelled, false},
		{hubAddrDraft, hubAddrSwitching, false},
		{hubAddrFinished, hubAddrDraft, false},
	} {
		if got := hubAddrNext(tc.from, tc.to); got != tc.ok {
			t.Errorf("hubAddrNext(%s, %s) = %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}
