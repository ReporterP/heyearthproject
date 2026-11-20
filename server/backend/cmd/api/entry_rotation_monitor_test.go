package main

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
)

// Entries on the canary ('test') or out of rotation ('off') are checked, never a failover target.
func TestFailoverIgnoresNonAllEntries(t *testing.T) {
	e := newEntryEnv()
	e.entries[1].Rotation = biz.EntryRotationTest
	e.entries = append(e.entries, biz.Entry{ID: 7, Name: "Выведен", Kind: biz.EntryKindRelay, Address: tR7, Lifecycle: "online",
		Rotation: biz.EntryRotationOff, Health: biz.EntryHealth{External: biz.EntryHealthy, UDP: biz.EntryHealthy}})
	for range 4 {
		e.step(errBlocked)
	}
	if e.fo.Active {
		t.Fatalf("failover to an entry not in rotation: %+v (%v)", e.fo, e.events)
	}
	for _, ev := range e.events {
		if strings.HasPrefix(ev, "raise entry-failover-") {
			t.Fatalf("failover alert: %v", e.events)
		}
	}
}

// The failover target taken out of rotation: the monitor moves to another reserve, or gives up.
func TestFailoverLeavesOffEntry(t *testing.T) {
	e := newEntryEnv()
	e.entries = append(e.entries, biz.Entry{ID: 7, Name: "Второй", Kind: biz.EntryKindRelay, Address: tR7, Lifecycle: "online",
		Health: biz.EntryHealth{External: biz.EntryHealthy, UDP: biz.EntryHealthy}})
	for range 4 {
		e.step(errBlocked)
	}
	if !e.fo.Active || e.fo.Target != 5 {
		t.Fatalf("failover: %+v", e.fo)
	}
	e.entries[1].Rotation = biz.EntryRotationOff
	e.step(errBlocked)
	if !e.fo.Active || e.fo.Target != 7 {
		t.Fatalf("after 5 left the rotation: %+v", e.fo)
	}
	e.entries[2].Rotation = biz.EntryRotationOff
	e.step(errBlocked)
	if e.fo.Active {
		t.Fatalf("no reserve in rotation, still %+v", e.fo)
	}
}

// An entry out of rotation raises no alerts; taking it out closes its open ones, once.
func TestEntryMonitorOffNoAlerts(t *testing.T) {
	e := newEntryEnv()
	e.settings["hysteria2_obfs_password"] = "psk"
	e.tcp[tR5], e.udp[tR5] = errBlocked, errBlocked
	for range 3 {
		e.step(nil)
	}
	if !e.has("raise relay-5-unreachable-external warn") {
		t.Fatalf("in rotation: %v", e.events)
	}
	e.entries[1].Rotation = biz.EntryRotationOff
	e.events = nil
	for range 3 {
		e.step(nil)
	}
	n := 0
	for _, ev := range e.events {
		if strings.HasPrefix(ev, "raise relay-5-") {
			t.Fatalf("alert for an entry out of rotation: %v", e.events)
		}
		if ev == "resolve relay-5-unreachable-external" {
			n++
		}
	}
	if n != 1 || !e.has("resolve relay-5-bamboo") || !e.has("resolve relay-5-unstable") {
		t.Fatalf("taking it out closes its alerts once: %v", e.events)
	}
}

// «Все через резервный вход» goes only to an entry in rotation: not to the canary, not to one
// taken out, not to one that is not online.
func TestEntryModeRelayNeedsAllRotation(t *testing.T) {
	useEntriesFake(t)
	for _, c := range []struct{ rotation, lifecycle string }{{biz.EntryRotationTest, "online"}, {biz.EntryRotationOff, "online"}, {"all", "failed"}} {
		entriesList = func(context.Context) ([]biz.Entry, error) {
			return []biz.Entry{{ID: 0, Kind: biz.EntryKindHub}, {ID: 9, Kind: biz.EntryKindRelay, Rotation: c.rotation, Lifecycle: c.lifecycle,
				Health: biz.EntryHealth{External: biz.EntryHealthy}}}, nil
		}
		rec := callEntries(handleAdminEntryMode, http.MethodPut, "/api/admin/entries/mode", `{"mode":"relay","target":9}`)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ротации") {
			t.Fatalf("%s/%s: %d %s", c.rotation, c.lifecycle, rec.Code, rec.Body)
		}
	}
}
