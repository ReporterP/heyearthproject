package main

import (
	"reflect"
	"testing"
	"time"
)

func TestFlapEscalatesOnThird(t *testing.T) {
	f := newFlapTracker()
	t0 := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	if f.onRaise("exit-down-3", at(0)) || f.onRaise("exit-down-3", at(40)) {
		t.Fatal("escalated before the third raise")
	}
	if !f.onRaise("exit-down-3", at(90)) {
		t.Fatal("three raises in 90 minutes: want the escalation")
	}
	if f.onRaise("exit-down-3", at(100)) {
		t.Fatal("the fourth raise escalated again (it is already raised)")
	}
	// another key has its own count
	if f.onRaise("exit-down-4", at(100)) {
		t.Fatal("another key escalated on its first raise")
	}
	// the escalation itself is never tracked
	for i := 0; i < 5; i++ {
		if f.onRaise("exit-down-3~unstable", at(100+i)) {
			t.Fatal("the «нестабильно» alert escalated itself")
		}
	}
}

func TestFlapWindow(t *testing.T) {
	f := newFlapTracker()
	t0 := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	if f.onRaise("k", t0) || f.onRaise("k", t0.Add(3*time.Hour)) || f.onRaise("k", t0.Add(7*time.Hour)) {
		t.Fatal("raises at 0, 3 h and 7 h escalated: only two fall into any 6 hours")
	}
	if !f.onRaise("k", t0.Add(8*time.Hour)) {
		t.Fatal("raises at 3 h, 7 h and 8 h: want the escalation")
	}
}

func TestFlapQuiet(t *testing.T) {
	f := newFlapTracker()
	t0 := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	for _, m := range []int{0, 10, 20} {
		f.onRaise("k", t0.Add(time.Duration(m)*time.Minute))
	}
	if got := f.quiet(t0.Add(79 * time.Minute)); len(got) != 0 {
		t.Fatalf("59 minutes after the last raise: quiet = %v, want none yet", got)
	}
	if got := f.quiet(t0.Add(81 * time.Minute)); !reflect.DeepEqual(got, []string{"k"}) {
		t.Fatalf("an hour without raises: quiet = %v, want [k]", got)
	}
	if got := f.quiet(t0.Add(82 * time.Minute)); len(got) != 0 {
		t.Fatalf("quiet reported the same key twice: %v", got)
	}
	// after the quiet hour it may escalate anew
	t1 := t0.Add(3 * time.Hour)
	if f.onRaise("k", t1) || f.onRaise("k", t1.Add(time.Minute)) {
		t.Fatal("escalated too early after the quiet hour")
	}
}
