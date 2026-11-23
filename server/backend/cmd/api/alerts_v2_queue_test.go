package main

import (
	"errors"
	"testing"
	"time"
)

// What the alert manager does while its queue is full, and what it holds its lock over.

func queueAlertManager() *alertManager {
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	return &alertManager{
		now:      func() time.Time { return t0 },
		queue:    make(chan alertOutgoing, 1), // no worker: what is put there stays
		cooldown: 30 * time.Minute,
		state:    map[string]*alertState{},
		loaded:   true,
	}
}

// mutedPair raises a parent and the alert it mutes, and empties the queue.
func mutedPair(t *testing.T, m *alertManager) (parent, child string) {
	t.Helper()
	parent, child = "exit-down-3", "exit-path-down-3-hy"
	m.Raise(parent, AlertWarn, "Выход 3 недоступен", "")
	<-m.queue
	m.Raise(child, AlertWarn, "Путь к выходу 3", "")
	if o := <-m.queue; o.ev.Kind != "inhibited" {
		t.Fatalf("the child was queued as %q, want inhibited", o.ev.Kind)
	}
	if st := m.state[child]; st.inhibitedBy != parent || st.notified {
		t.Fatalf("child = %+v, want it muted by %s", st, parent)
	}
	return parent, child
}

// A resolve the full queue dropped is rolled back — and so must be the release of the alerts
// the parent muted: nobody was told about them, and the parent that mutes them is still open.
func TestDroppedResolveKeepsChildrenMuted(t *testing.T) {
	captureLog(t)
	m := queueAlertManager()
	parent, child := mutedPair(t, m)
	m.Raise("disk-usage", AlertWarn, "Диск", "90%") // fills the queue
	m.Resolve(parent, "снова отвечает")             // dropped
	if st := m.state[parent]; !st.active {
		t.Fatal("a dropped resolve must keep the parent open")
	}
	st := m.state[child]
	if st.inhibitedBy != parent || st.notified || (st.inc != nil && st.inc.delivered) {
		t.Fatalf("child = %+v (incident %+v): the resolve was dropped, yet the child is marked as told and no longer muted", st, st.inc)
	}
}

// The resolve took the queue's last place and the notice «остаются активны» did not fit: nobody
// was told, so the alerts must not be marked as told — each reports itself at its next raise.
func TestDroppedNoticeLeavesChildrenUntold(t *testing.T) {
	captureLog(t)
	m := queueAlertManager()
	parent, child := mutedPair(t, m)
	m.Resolve(parent, "снова отвечает") // queued; the notice is dropped
	if o := <-m.queue; o.ev.Key != parent || o.ev.Kind != "resolve" {
		t.Fatalf("queued %+v, want the parent's resolve", o.ev)
	}
	st := m.state[child]
	if !st.active || st.inhibitedBy != "" {
		t.Fatalf("child = %+v, want it active and no longer muted", st)
	}
	if st.notified || (st.inc != nil && st.inc.delivered) {
		t.Fatalf("child = %+v (incident %+v): its notice was dropped, yet it is marked as told", st, st.inc)
	}
	m.Raise(child, AlertWarn, "Путь к выходу 3", "")
	select {
	case o := <-m.queue:
		if o.ev.Key != child || o.ev.Kind != "raise" || !o.send {
			t.Fatalf("queued %+v (send %v), want the child's own raise", o.ev, o.send)
		}
	default:
		t.Fatal("the child's next raise was swallowed: the owner never hears of it")
	}
}

// The address of the second channel lives in the database. It is never read under the
// manager's lock: a slow database would stop every Raise and Resolve of every monitor.
func TestEmailAddressIsReadOutsideTheLock(t *testing.T) {
	h := newAlertHarness()
	held, mails := false, 0
	h.m.emailTo = func() string {
		if h.m.mu.TryLock() {
			h.m.mu.Unlock()
		} else {
			held = true
		}
		return "owner@example.test"
	}
	h.m.email = func(string, string, string) error { mails++; return nil }
	h.failSends = 1000
	for i := 0; i < 4; i++ { // attempts at 0, 6, 12 and 18 minutes
		h.m.Raise("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи", "d")
		h.advance(6 * time.Minute)
	}
	if mails != 1 {
		t.Fatalf("%d letters, want 1", mails)
	}
	if held {
		t.Error("alert_email was read with the manager's lock held")
	}
}

// With a delivery worker the letter goes beside it: a mail server that does not answer must
// not hold the alerts waiting in the queue.
func TestFallbackEmailDoesNotHoldTheWorker(t *testing.T) {
	captureLog(t)
	m := queueAlertManager()
	m.send = func(string) error { return errors.New("telegram down") }
	m.emailTo = func() string { return "owner@example.test" }
	started, release := make(chan struct{}), make(chan struct{})
	m.email = func(string, string, string) error {
		close(started)
		<-release
		return nil
	}
	now := m.now()
	inc := &alertIncident{failures: 1, firstFailAt: now.Add(-20 * time.Minute)}
	m.state["k"] = &alertState{active: true, level: AlertCritical, title: "T", inc: inc}
	done := make(chan struct{})
	go func() {
		m.deliver(alertOutgoing{ev: alertEvent{Key: "k", Level: AlertCritical, Kind: "raise", Title: "T", At: now}, send: true, inc: inc})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("the delivery worker waits for the mail server")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the letter was never sent")
	}
	close(release)
}
