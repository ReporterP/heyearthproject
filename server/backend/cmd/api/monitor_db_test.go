package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type dbHealthHarness struct {
	now     time.Time
	pingErr error
	events  []string
	details map[string]string
	msgs    map[string]string
	m       *dbHealthMonitor
}

func newDBHealthHarness() *dbHealthHarness {
	h := &dbHealthHarness{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC), details: map[string]string{}, msgs: map[string]string{}}
	h.m = &dbHealthMonitor{
		now:  func() time.Time { return h.now },
		ping: func() error { return h.pingErr },
		raise: func(key string, level AlertLevel, title, details string) {
			h.events = append(h.events, fmt.Sprintf("raise %s %s", key, level))
			h.details[key] = details
		},
		resolve: func(key, msg string) {
			h.events = append(h.events, "resolve "+key)
			h.msgs[key] = msg
		},
		logf: func(string, ...any) {},
	}
	return h
}

func (h *dbHealthHarness) step(err error) []string {
	h.now = h.now.Add(time.Minute)
	h.pingErr = err
	h.events = nil
	h.m.run()
	return h.events
}

func TestDBHealthRaisesAfterTwoFailuresAndResolvesAfterTwoSuccesses(t *testing.T) {
	h := newDBHealthHarness()
	down := errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
	raise, resolve := "raise "+dbAlertKey+" critical", "resolve "+dbAlertKey

	if ev := h.step(nil); len(ev) != 0 {
		t.Fatalf("a healthy DB with nothing open: %v", ev)
	}
	if ev := h.step(down); len(ev) != 0 {
		t.Fatalf("one failed ping is not an outage: %v", ev)
	}
	if ev := h.step(down); fmt.Sprint(ev) != "["+raise+"]" {
		t.Fatalf("the second failure in a row must raise: %v", ev)
	}
	if d := h.details[dbAlertKey]; !strings.Contains(d, "connection refused") || !strings.Contains(d, "2 проверок подряд") {
		t.Fatalf("details: %s", d)
	}
	// flapping: ok, fail, ok — neither resolved nor re-raised below the thresholds
	for i, step := range []error{nil, down, nil} {
		if ev := h.step(step); len(ev) != 0 {
			t.Fatalf("flap step %d: %v", i, ev)
		}
	}
	if ev := h.step(nil); fmt.Sprint(ev) != "["+resolve+"]" {
		t.Fatalf("two successes in a row must resolve: %v", ev)
	}
	// down since the first failure (minute 2) until minute 7 — the single good ping inside does not reset it
	if msg := h.msgs[dbAlertKey]; !strings.Contains(msg, "около 5m0s") {
		t.Fatalf("recovery must tell how long the outage lasted: %q", msg)
	}
}

// End to end with the real alert manager: the db-unreachable alert goes out once, the recovery once.
func TestDBHealthWithAlertManager(t *testing.T) {
	ah := newAlertHarness()
	h := newDBHealthHarness()
	h.m.raise, h.m.resolve = ah.m.Raise, ah.m.Resolve
	ah.m.now = func() time.Time { return h.now }
	down := errors.New("pq: the database system is shutting down")
	for i := 0; i < 10; i++ {
		h.step(down)
	}
	h.step(nil)
	h.step(nil)
	h.step(nil) // resolving an already resolved key is a no-op in the manager
	if got := ah.kinds(); got != "raise,resolve" || len(ah.sent) != 2 {
		t.Fatalf("want one raise and one recovery: kinds=%s sent=%q", got, ah.sent)
	}
	if !strings.Contains(ah.sent[0], "КРИТИЧНО · База данных недоступна") {
		t.Fatalf("message: %q", ah.sent[0])
	}
}

func TestPingDBWithoutDB(t *testing.T) {
	if err := pingDB(); !errors.Is(err, errAlertsNoDB) {
		t.Fatalf("pingDB without a DB: %v", err)
	}
}

// dbBounded returns at the deadline even when the call ignores its context.
func TestDBBoundedReturnsAtDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	_, err := dbBounded(50*time.Millisecond, func(ctx context.Context) (int, error) {
		<-release // a driver stuck on a socket read
		return 1, nil
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want a deadline error, got %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("returned after %v", took)
	}
	v, err := dbBounded(time.Second, func(ctx context.Context) (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Fatalf("fast call: %v %v", v, err)
	}
}
