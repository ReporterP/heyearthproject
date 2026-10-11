package main

import (
	"sync"
	"testing"
	"time"
)

func TestSlidingWindowCounter(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	now := t0
	c := &slidingWindowCounter{window: time.Hour, clock: func() time.Time { return now }}
	for i := 0; i < 11; i++ {
		now = t0.Add(time.Duration(i) * time.Minute)
		if n := c.Add(1); n != i+1 {
			t.Fatalf("add #%d: count %d, want %d", i, n, i+1)
		}
	}
	now = t0.Add(time.Hour)
	if n := c.Count(); n != 10 {
		t.Fatalf("event exactly 1h old must expire: count %d, want 10", n)
	}
	now = t0.Add(70*time.Minute + time.Second)
	if n := c.Count(); n != 0 {
		t.Fatalf("all events must expire: count %d", n)
	}
	// A burst (several systemd auto-restarts seen at once) counts as that many events.
	if n := c.Add(3); n != 3 {
		t.Fatalf("Add(3): count %d, want 3", n)
	}
	if n := c.Add(0); n != 3 {
		t.Fatalf("Add(0) must not change the count: %d, want 3", n)
	}
}

// The timestamp must be taken under the lock, otherwise concurrent callers can append
// events out of order and pruning (front-only) silently keeps stale ones.
func TestSlidingWindowCounterConcurrentAddKeepsOrder(t *testing.T) {
	c := &slidingWindowCounter{window: time.Hour}
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c.Add(1)
			}
		}()
	}
	wg.Wait()
	if len(c.events) != 32*200 {
		t.Fatalf("events %d, want %d", len(c.events), 32*200)
	}
	for i := 1; i < len(c.events); i++ {
		if c.events[i].Before(c.events[i-1]) {
			t.Fatalf("events out of order at %d: %v before %v", i, c.events[i], c.events[i-1])
		}
	}
}

func TestXrayNRestartsDelta(t *testing.T) {
	cases := []struct {
		name      string
		last, cur int64
		haveLast  bool
		wantDelta int
	}{
		{"first reading is only a baseline", 0, 7, false, 0},
		{"no change", 4, 4, true, 0},
		{"growth", 4, 6, true, 2},
		{"systemctl restart reset the counter, nothing since", 6, 0, true, 0},
		{"reset and crashed again before the next poll", 6, 3, true, 3},
	}
	for _, tc := range cases {
		if got := xrayNRestartsDelta(tc.last, tc.cur, tc.haveLast); got != tc.wantDelta {
			t.Fatalf("%s: delta %d, want %d", tc.name, got, tc.wantDelta)
		}
	}
}

func TestParseNRestarts(t *testing.T) {
	if n, err := parseNRestarts("12\n"); err != nil || n != 12 {
		t.Fatalf("parse 12: %d, %v", n, err)
	}
	for _, bad := range []string{"", "NRestarts=", "abc", "-1"} {
		if _, err := parseNRestarts(bad); err == nil {
			t.Fatalf("%q must be an error", bad)
		}
	}
}
