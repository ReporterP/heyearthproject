package main

import (
	"testing"
	"time"
)

func TestSlidingWindowCounter(t *testing.T) {
	c := &slidingWindowCounter{window: time.Hour}
	t0 := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 11; i++ {
		if n := c.Add(t0.Add(time.Duration(i) * time.Minute)); n != i+1 {
			t.Fatalf("add #%d: count %d, want %d", i, n, i+1)
		}
	}
	if n := c.Count(t0.Add(time.Hour)); n != 10 {
		t.Fatalf("event exactly 1h old must expire: count %d, want 10", n)
	}
	if n := c.Count(t0.Add(70*time.Minute + time.Second)); n != 0 {
		t.Fatalf("all events must expire: count %d", n)
	}
}
