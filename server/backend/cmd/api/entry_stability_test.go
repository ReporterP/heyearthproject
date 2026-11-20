package main

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

func withSamples(e *entryEnv) *[]string {
	saved := &[]string{}
	e.m.tlsFlow = func(_ int, host string, _ int, _ string) error { return e.tcp[host] }
	e.m.udpFlow = func(_ int, host string, _ []byte) error { return e.udp[host] }
	e.m.saveSamples = func(_ context.Context, id int, _ time.Time, s []biz.EntrySample) error {
		for _, x := range s {
			*saved = append(*saved, fmt.Sprintf("%d %s %d/%d", id, x.Kind, x.OK, x.Total))
		}
		return nil
	}
	return saved
}

func TestEntryMonitorWritesSamples(t *testing.T) {
	e := newEntryEnv()
	e.settings["hysteria2_obfs_password"] = "psk"
	saved := withSamples(e)
	e.step(nil)
	want := []string{"0 tcp443 8/8", "5 tcp2443 8/8", "5 tcp443 8/8", "5 udp 4/4"} // sorted
	slices.Sort(*saved)
	if !slices.Equal(*saved, want) {
		t.Fatalf("samples %v, want %v", *saved, want)
	}
	// A blocked entry: its flows fail, the samples say so.
	*saved = nil
	e.tcp[tR5] = errBlocked
	e.step(nil)
	if !slices.Contains(*saved, "5 tcp443 0/8") || !slices.Contains(*saved, "5 tcp2443 0/8") {
		t.Fatalf("blocked: %v", *saved)
	}
}

func TestEntryMonitorSkipWritesNoSamples(t *testing.T) {
	e := newEntryEnv()
	saved := withSamples(e)
	e.controlErr = errBlocked // no exit passes its control: the run is a skip
	e.step(nil)
	if len(*saved) != 0 {
		t.Fatalf("samples on a skipped run: %v", *saved)
	}
}

func win(ok, total, checks int) biz.StabilityWindow {
	return biz.StabilityWindow{OK: ok, Total: total, Checks: checks}
}

func TestUnstableVerdict(t *testing.T) {
	for _, c := range []struct {
		w              map[string]biz.StabilityWindow
		raised         bool
		raise, resolve bool
	}{
		{map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(949, 1000, 30)}, false, true, false},
		{map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(949, 1000, 29)}, false, false, false}, // too few checks
		{map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(950, 1000, 60)}, false, false, false},
		{map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(979, 1000, 60)}, true, false, false}, // held until 98 %
		{map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(980, 1000, 60), biz.EntrySampleUDP: win(240, 240, 60)}, true, false, true},
		{map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(980, 1000, 60), biz.EntrySampleUDP: win(200, 240, 60)}, true, false, false},
		{nil, true, false, false}, // no data: keep what was raised
	} {
		raise, resolve := unstableVerdict(c.w, c.raised)
		if raise != c.raise || resolve != c.resolve {
			t.Errorf("%v raised=%v: raise %v resolve %v, want %v %v", c.w, c.raised, raise, resolve, c.raise, c.resolve)
		}
	}
}

func TestUnstableAlertOnlyAllTest(t *testing.T) {
	e := newEntryEnv()
	bad := map[int]biz.EntryStability{5: {H1: map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(400, 480, 60)}}}
	e.m.stability = func(context.Context) (map[int]biz.EntryStability, error) { return bad, nil }
	e.step(nil)
	if !e.has("raise relay-5-unstable warn") {
		t.Fatalf("in rotation: %v", e.events)
	}
	e.events = nil
	e.entries[1].Rotation = biz.EntryRotationTest // the canary: the owner wants to hear about it
	e.m.unstable = nil
	e.step(nil)
	if !e.has("raise relay-5-unstable warn") {
		t.Fatalf("on test: %v", e.events)
	}
	e.events = nil
	e.entries[1].Rotation = biz.EntryRotationOff
	e.m.unstable = nil
	e.step(nil)
	if e.has("raise relay-5-unstable warn") {
		t.Fatalf("out of rotation: %v", e.events)
	}
	// Recovered: resolved once.
	e.entries[1].Rotation = biz.EntryRotationAll
	e.step(nil)
	bad[5] = biz.EntryStability{H1: map[string]biz.StabilityWindow{biz.EntrySampleTCP2443: win(480, 480, 60)}}
	e.events = nil
	e.step(nil)
	if !e.has("resolve relay-5-unstable") {
		t.Fatalf("recovered: %v", e.events)
	}
}
