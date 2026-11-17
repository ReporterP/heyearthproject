package main

import "testing"

// The first read after the API starts has no previous snapshot. Diffing it
// against zero booked everything Hysteria had counted since it started: on
// 2026-11-12 each API restart charged one user 24 GB again.
func TestCumulativeDeltasFirstReadOnlyBaselines(t *testing.T) {
	cur := map[string]counterPair{"u1": {Up: 2e9, Down: 24e9}}
	if got := cumulativeDeltas(nil, cur, false); len(got) != 0 {
		t.Fatalf("first read must book nothing, got %v", got)
	}
}

func TestCumulativeDeltasGrowthNewAndReset(t *testing.T) {
	prev := map[string]counterPair{
		"grow":  {Up: 100, Down: 1000},
		"idle":  {Up: 5, Down: 5},
		"reset": {Up: 9000, Down: 9000},
	}
	cur := map[string]counterPair{
		"grow":  {Up: 150, Down: 1700},
		"idle":  {Up: 5, Down: 5},
		"reset": {Up: 10, Down: 20}, // the source restarted: counters went down
		"new":   {Up: 7, Down: 70},  // appeared after the last read: all of it is new
	}
	got := cumulativeDeltas(prev, cur, true)
	want := map[string]counterPair{
		"grow": {Up: 50, Down: 700},
		"new":  {Up: 7, Down: 70},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("%s: got %v, want %v", k, got[k], w)
		}
	}
}

// One direction going down is a restart too, even if the other grew.
func TestCumulativeDeltasPartialDecreaseIsReset(t *testing.T) {
	prev := map[string]counterPair{"u": {Up: 100, Down: 100}}
	cur := map[string]counterPair{"u": {Up: 150, Down: 50}}
	if got := cumulativeDeltas(prev, cur, true); len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}
