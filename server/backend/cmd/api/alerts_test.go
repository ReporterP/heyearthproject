package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type alertHarness struct {
	now       time.Time
	sent      []string // messages that reached "Telegram"
	attempts  int      // send attempts, failed ones included
	failSends int      // the next N send attempts fail
	events    []alertEvent
	m         *alertManager
}

// newAlertHarness builds a synchronous manager (queue == nil) with a fake clock,
// fake sender and in-memory event store.
func newAlertHarness() *alertHarness {
	h := &alertHarness{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	h.m = &alertManager{
		now: func() time.Time { return h.now },
		send: func(text string) error {
			h.attempts++
			if h.failSends > 0 {
				h.failSends--
				return errors.New("telegram down")
			}
			h.sent = append(h.sent, text)
			return nil
		},
		record:   func(ev alertEvent) { h.events = append(h.events, ev) },
		cooldown: 30 * time.Minute,
		state:    map[string]*alertState{},
		loaded:   true,
	}
	return h
}

func (h *alertHarness) advance(d time.Duration) { h.now = h.now.Add(d) }

func (h *alertHarness) kinds() string {
	var kinds []string
	for _, ev := range h.events {
		kinds = append(kinds, ev.Kind)
	}
	return strings.Join(kinds, ",")
}

func TestAlertDedupAndCriticalRepeat(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "Вход недоступен", "d1")
	h.advance(5 * time.Minute)
	h.m.Raise("k", AlertCritical, "Вход недоступен", "d2")
	if len(h.sent) != 1 {
		t.Fatalf("dedup: want 1 message, got %d", len(h.sent))
	}
	h.advance(26 * time.Minute) // 31 min after the first message
	h.m.Raise("k", AlertCritical, "Вход недоступен", "d3")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "ПОВТОР") {
		t.Fatalf("critical must repeat after cooldown: %q", h.sent)
	}
}

func TestAlertWarnNotRepeated(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("disk", AlertWarn, "Диск", "90%")
	h.advance(3 * time.Hour)
	h.m.Raise("disk", AlertWarn, "Диск", "91%")
	if len(h.sent) != 1 {
		t.Fatalf("warn must not repeat while active, got %d", len(h.sent))
	}
}

func TestAlertResolveOnlyIfRaised(t *testing.T) {
	h := newAlertHarness()
	h.m.Resolve("never", "ok")
	if len(h.sent) != 0 || len(h.events) != 0 {
		t.Fatalf("resolve of an unknown key must be a no-op: %q %v", h.sent, h.events)
	}
	h.m.Raise("k", AlertWarn, "Бэкап", "старый")
	h.m.Resolve("k", "свежий")
	h.m.Resolve("k", "свежий")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "ВОССТАНОВЛЕНО") {
		t.Fatalf("want raise + exactly one recovery: %q", h.sent)
	}
}

// A warn key keeps the full cooldown after its recovery (critical: see the next test).
func TestAlertFlappingSuppressedByCooldown(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertWarn, "T", "") // +0m  sent
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +1m  sent (recovery)
	h.advance(time.Minute)
	h.m.Raise("k", AlertWarn, "T", "") // +2m  suppressed
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +3m  cleared silently
	h.advance(time.Minute)
	h.m.Raise("k", AlertWarn, "T", "") // +4m  suppressed
	h.advance(10 * time.Minute)
	h.m.Raise("k", AlertWarn, "T", "") // +14m still inside the warn cooldown
	if len(h.sent) != 2 {
		t.Fatalf("warn flapping inside cooldown must be silent, got %d: %q", len(h.sent), h.sent)
	}
	h.advance(18 * time.Minute) // +32m, 31m after the last message
	h.m.Raise("k", AlertWarn, "T", "")
	if len(h.sent) != 3 {
		t.Fatalf("still-active alert must go out once cooldown expires, got %d", len(h.sent))
	}
	if got := h.kinds(); got != "raise,resolve,suppressed,cleared,suppressed,raise" {
		t.Fatalf("event kinds: %s", got)
	}
}

// A critical key that comes back after a delivered recovery is held back only
// alertFlapSuppress, not the whole cooldown: the owner was just told "all clear".
func TestAlertCriticalAfterRecoveryUsesShortSuppression(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "T", "") // +0m  sent
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +1m  recovery delivered
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +2m  suppressed (1 min after the recovery)
	h.advance(2 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +4m  still inside alertFlapSuppress
	if len(h.sent) != 2 {
		t.Fatalf("inside the short window: want 2 messages, got %q", h.sent)
	}
	h.advance(2 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +6m  5 min after the recovery: sent
	if len(h.sent) != 3 || !strings.Contains(h.sent[2], "КРИТИЧНО") || strings.Contains(h.sent[2], "ПОВТОР") {
		t.Fatalf("a critical outage 5 min after the recovery must be sent, not wait 30 min: %q", h.sent)
	}
	if got := h.kinds(); got != "raise,resolve,suppressed,raise" {
		t.Fatalf("event kinds: %s", got)
	}
	// once the new raise is out, the normal 30 min repeat cooldown applies again
	h.advance(10 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 3 {
		t.Fatalf("no repeat 10 min after the raise: %q", h.sent)
	}
}

// The short window needs a recovery that really reached Telegram.
func TestAlertCriticalAfterUndeliveredRecoveryKeepsCooldown(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "T", "") // delivered
	h.advance(time.Minute)
	h.failSends = 1
	h.m.Resolve("k", "up") // recovery attempted, failed
	h.advance(6 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 {
		t.Fatalf("the owner never got the recovery: the full cooldown applies, got %q", h.sent)
	}
	h.advance(24 * time.Minute) // 30 min after the failed recovery
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 2 {
		t.Fatalf("after the cooldown the raise goes out: %q", h.sent)
	}
}

// ── failed delivery ──

func TestAlertFailedSendIsRetriedOnNextRaise(t *testing.T) {
	h := newAlertHarness()
	h.failSends = 1
	h.m.Raise("k", AlertCritical, "T", "d")
	if len(h.sent) != 0 || len(h.events) != 1 || h.events[0].Delivered || h.events[0].Error == "" {
		t.Fatalf("first attempt must fail and be journaled as undelivered: sent=%q events=%+v", h.sent, h.events)
	}
	h.advance(alertRetryBackoff + time.Second)
	h.m.Raise("k", AlertCritical, "T", "d") // first poll after the backoff: must retry, not wait out the 30 min cooldown
	if len(h.sent) != 1 {
		t.Fatalf("failed raise must be re-sent by the next Raise, got %d sent", len(h.sent))
	}
	h.m.Raise("k", AlertCritical, "T", "d")
	if len(h.sent) != 1 || h.attempts != 2 {
		t.Fatalf("delivered raise must dedup again: sent=%d attempts=%d", len(h.sent), h.attempts)
	}
	if got := h.kinds(); got != "raise,raise" {
		t.Fatalf("event kinds: %s", got)
	}
}

func TestAlertFailedRepeatIsRetried(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "T", "")
	h.advance(31 * time.Minute)
	h.failSends = 1
	h.m.Raise("k", AlertCritical, "T", "") // repeat fails
	h.advance(alertRetryBackoff + time.Second)
	h.m.Raise("k", AlertCritical, "T", "") // retried
	if len(h.sent) != 2 || h.attempts != 3 {
		t.Fatalf("failed repeat must be retried: sent=%d attempts=%d", len(h.sent), h.attempts)
	}
}

func TestAlertResolveAfterOnlyFailedRaisesSendsNothing(t *testing.T) {
	h := newAlertHarness()
	h.failSends = 1
	h.m.Raise("k", AlertCritical, "T", "")
	h.m.Resolve("k", "up")
	if len(h.sent) != 0 || h.attempts != 1 {
		t.Fatalf("nobody was told about this incident, recovery must not be sent: sent=%q attempts=%d", h.sent, h.attempts)
	}
	if got := h.kinds(); got != "raise,cleared" {
		t.Fatalf("event kinds: %s", got)
	}
	// nothing was delivered → no flapping cooldown, only the retry backoff of the failed delivery
	h.m.Raise("k", AlertCritical, "T", "")
	if h.attempts != 1 {
		t.Fatalf("a new outage inside the retry backoff must not hit Telegram again, attempts=%d", h.attempts)
	}
	h.advance(alertRetryBackoff + time.Second)
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 {
		t.Fatalf("new outage after the backoff must be sent, got %d", len(h.sent))
	}
}

func TestAlertResolveAfterFailedRepeatStillSendsRecovery(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "T", "") // delivered
	h.advance(31 * time.Minute)
	h.failSends = 1
	h.m.Raise("k", AlertCritical, "T", "") // repeat fails
	h.m.Resolve("k", "up")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "ВОССТАНОВЛЕНО") {
		t.Fatalf("the first raise was delivered, so recovery is due: %q", h.sent)
	}
}

func TestAlertResolveWaitsForRaiseOutcome(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failRaise bool
		wantSent  int
		wantKinds string
	}{
		{"raise delivered", false, 2, "raise,resolve"},
		{"raise failed", true, 0, "raise,cleared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			var mu sync.Mutex
			var sent, kinds []string
			var calls atomic.Int32
			m := &alertManager{
				now: time.Now,
				send: func(text string) error {
					if calls.Add(1) == 1 { // the raise: held in flight until released
						<-release
						if tc.failRaise {
							return errors.New("telegram down")
						}
					}
					mu.Lock()
					sent = append(sent, text)
					mu.Unlock()
					return nil
				},
				record: func(ev alertEvent) {
					mu.Lock()
					kinds = append(kinds, ev.Kind)
					mu.Unlock()
				},
				cooldown: 30 * time.Minute,
				state:    map[string]*alertState{},
				loaded:   true,
			}
			m.startWorker(16)
			m.Raise("k", AlertWarn, "T", "")
			m.Resolve("k", "up") // decided while the raise is still in flight
			close(release)
			m.flush()
			mu.Lock()
			defer mu.Unlock()
			if len(sent) != tc.wantSent || strings.Join(kinds, ",") != tc.wantKinds {
				t.Fatalf("sent=%q kinds=%v, want %d messages and kinds %s", sent, kinds, tc.wantSent, tc.wantKinds)
			}
		})
	}
}

func TestAlertFailedSendBacksOffBeforeRetry(t *testing.T) {
	h := newAlertHarness()
	h.failSends = 1
	h.m.Raise("k", AlertCritical, "T", "d") // t0: delivery fails
	if h.attempts != 1 || len(h.events) != 1 {
		t.Fatalf("setup: attempts=%d events=%d", h.attempts, len(h.events))
	}
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "d") // t0+1m: inside the backoff
	if h.attempts != 1 || len(h.events) != 1 {
		t.Fatalf("inside the backoff there must be no delivery attempt and no journal row: attempts=%d events=%d", h.attempts, len(h.events))
	}
	h.advance(4*time.Minute + time.Second)
	h.m.Raise("k", AlertCritical, "T", "d") // t0+5m+1s: retried
	if h.attempts != 2 || len(h.sent) != 1 || len(h.events) != 2 {
		t.Fatalf("after the backoff the raise must be retried: attempts=%d sent=%d events=%d", h.attempts, len(h.sent), len(h.events))
	}
}

// Until the owner creates the bot there is no token/chat_id: every delivery fails with
// errAlertsNotConfigured. A monitor polling every minute must neither retry every 5 min
// nor write a journal row per attempt: one row per incident, the rest is counted.
func TestAlertNotConfiguredIsJournaledOncePerIncident(t *testing.T) {
	h := newAlertHarness()
	notConfigured := true
	h.m.send = func(text string) error {
		h.attempts++
		if notConfigured {
			return errAlertsNotConfigured
		}
		h.sent = append(h.sent, text)
		return nil
	}
	start := h.now
	for minute := 0; minute < 120; minute++ {
		h.m.Raise("k", AlertCritical, "T", "")
		h.m.Raise("w", AlertWarn, "W", "")
		h.advance(time.Minute)
	}
	if h.attempts != 8 { // minutes 0, 30, 60 and 90 for each of the two keys
		t.Fatalf("want one attempt per %s per key, got %d", alertNotConfiguredBackoff, h.attempts)
	}
	if got := h.kinds(); got != "raise,raise" {
		t.Fatalf("one journal row per incident, got %s", got)
	}
	for _, ev := range h.events {
		if ev.Delivered || ev.Error != errAlertsNotConfigured.Error() {
			t.Fatalf("journal row must carry the delivery error: %+v", ev)
		}
	}
	und := h.m.undelivered()
	if len(und) != 2 || und[0].Key != "k" || und[1].Key != "w" {
		t.Fatalf("both incidents are undelivered: %+v", und)
	}
	if u := und[0]; !u.NotConfigured || u.Attempts != 4 || u.Level != AlertCritical || u.Error != errAlertsNotConfigured.Error() ||
		!u.FirstFailedAt.Equal(start) || !u.LastAttemptAt.Equal(start.Add(90*time.Minute)) ||
		!u.NextRetryAt.Equal(start.Add(120*time.Minute)) {
		t.Fatalf("undelivered summary: %+v", u)
	}

	// the owner saves the settings: retryNow lifts the 30 min backoff, the next poll delivers
	notConfigured = false
	h.m.retryNow()
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 || h.kinds() != "raise,raise,raise" || !h.events[2].Delivered {
		t.Fatalf("after retryNow the raise must go out and be journaled: sent=%d kinds=%s", len(h.sent), h.kinds())
	}
	if und := h.m.undelivered(); len(und) != 1 || und[0].Key != "w" {
		t.Fatalf("k was delivered, only w is left: %+v", und)
	}
}

// A different error, or an escalation, is news: it is journaled even inside one incident.
func TestAlertFailedDeliveryJournalsChangedErrorOrLevel(t *testing.T) {
	h := newAlertHarness()
	unauthorized := &telegramAPIError{Status: 401, Description: "Unauthorized", FromTelegram: true}
	errs := []error{errAlertsNotConfigured, errAlertsNotConfigured, unauthorized, unauthorized}
	h.m.send = func(string) error {
		err := errs[h.attempts]
		h.attempts++
		return err
	}
	h.m.Raise("k", AlertWarn, "T", "") // not configured: row
	h.advance(alertNotConfiguredBackoff)
	h.m.Raise("k", AlertWarn, "T", "") // same error: no row
	h.advance(alertNotConfiguredBackoff)
	h.m.Raise("k", AlertCritical, "T", "") // new error and level: row
	h.advance(alertRetryBackoff)           // a real Telegram error keeps the 5 min backoff
	h.m.Raise("k", AlertCritical, "T", "") // same error and level: no row
	if h.attempts != 4 || len(h.events) != 2 {
		t.Fatalf("attempts=%d events=%d (%s), want 4 attempts and 2 rows", h.attempts, len(h.events), h.kinds())
	}
	if h.events[1].Level != AlertCritical || !strings.Contains(h.events[1].Error, "401") {
		t.Fatalf("second row: %+v", h.events[1])
	}
}

func TestAlertSuccessfulDeliveryKeepsCooldownNotBackoff(t *testing.T) {
	h := newAlertHarness()
	h.failSends = 1
	h.m.Raise("k", AlertCritical, "T", "") // fails
	h.advance(alertRetryBackoff + time.Second)
	h.m.Raise("k", AlertCritical, "T", "") // delivered
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // inside the 30 min cooldown: silent, as before
	h.advance(31 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // cooldown over: critical repeat
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "ПОВТОР") || h.attempts != 3 {
		t.Fatalf("a delivered alert follows the normal cooldown rules: sent=%q attempts=%d", h.sent, h.attempts)
	}
}

// ── ordering across goroutines ──

// Raise/Resolve of one key from many goroutines: the worker must see them in the order the
// state machine decided them, so a recovery never overtakes its raise. Run with -race on Linux.
func TestAlertConcurrentRaiseResolveKeepsOrder(t *testing.T) {
	const workers, rounds = 8, 300
	var mu sync.Mutex
	var texts, kinds []string
	m := &alertManager{
		now: time.Now,
		send: func(text string) error {
			mu.Lock()
			texts = append(texts, text)
			mu.Unlock()
			return nil
		},
		record: func(ev alertEvent) {
			mu.Lock()
			kinds = append(kinds, ev.Kind)
			mu.Unlock()
		},
		cooldown: 0, // every Raise after a Resolve is a fresh outage: maximal interleaving
		state:    map[string]*alertState{},
		loaded:   true,
	}
	m.startWorker(2 * workers * rounds)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				m.Raise("k", AlertWarn, "T", "")
				runtime.Gosched()
				m.Resolve("k", "up")
			}
		}()
	}
	wg.Wait()
	m.flush()

	mu.Lock()
	defer mu.Unlock()
	if len(kinds) == 0 || len(kinds) != len(texts) {
		t.Fatalf("journal/messages mismatch: %d kinds, %d messages", len(kinds), len(texts))
	}
	for i, k := range kinds {
		want, wantText := "raise", "ВНИМАНИЕ"
		if i%2 == 1 {
			want, wantText = "resolve", "ВОССТАНОВЛЕНО"
		}
		if k != want || !strings.Contains(texts[i], wantText) {
			t.Fatalf("event %d: got %s / %q, want %s (resolve before raise or lost event)", i, k, texts[i], want)
		}
	}
}

func TestAlertQueueOverflowRollsBackState(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	m := &alertManager{
		now:      func() time.Time { return t0 },
		queue:    make(chan alertOutgoing, 1), // no worker: stays full
		cooldown: 30 * time.Minute,
		state:    map[string]*alertState{},
		loaded:   true,
	}
	drain := func() alertOutgoing {
		o := <-m.queue
		m.pending.Done()
		return o
	}

	m.Raise("a", AlertWarn, "A", "") // fills the queue
	m.Raise("b", AlertWarn, "B", "") // dropped
	if st := m.state["b"]; st == nil || st.active || st.notified || !st.lastSent.IsZero() {
		t.Fatalf("a dropped raise must leave no trace in the state: %+v", st)
	}
	drain()
	m.Raise("b", AlertWarn, "B", "") // retried, not swallowed by dedup
	if o := drain(); o.ev.Key != "b" || o.ev.Kind != "raise" {
		t.Fatalf("raise of b must be queued after the drop: %+v", o.ev)
	}

	// a dropped resolve keeps the incident open so that the next ResolveAlert retries it
	m.Raise("a2", AlertWarn, "A2", "")
	m.Resolve("b", "up") // dropped
	if st := m.state["b"]; st == nil || !st.active || !st.notified {
		t.Fatalf("a dropped resolve must keep b active: %+v", st)
	}
	drain()
	m.Resolve("b", "up")
	if o := drain(); o.ev.Key != "b" || o.ev.Kind != "resolve" {
		t.Fatalf("resolve of b must be queued after the drop: %+v", o.ev)
	}
}

// ── levels ──

func TestAlertEscalationWarnToCriticalIsImmediate(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertWarn, "Диск", "90%")
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "Диск", "99%")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "КРИТИЧНО") || strings.Contains(h.sent[1], "ПОВТОР") {
		t.Fatalf("escalation must go out at once as a plain critical alert: %q", h.sent)
	}
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "Диск", "99%")
	h.m.Raise("k", AlertWarn, "Диск", "95%") // no downgrade, no extra message
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "Диск", "99%")
	if len(h.sent) != 2 {
		t.Fatalf("no extra messages inside the cooldown, got %d: %q", len(h.sent), h.sent)
	}
	if got := h.kinds(); got != "raise,raise" {
		t.Fatalf("event kinds: %s", got)
	}
}

// ── journal replay ──

func TestReplayAlertEventsRestoresState(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	state := replayAlertEvents([]alertEvent{
		{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", Delivered: true, At: t0},
		{Key: "b", Level: AlertWarn, Kind: "raise", Title: "B", Delivered: true, At: t0},
		{Key: "b", Level: AlertWarn, Kind: "resolve", Title: "B", Delivered: true, At: t0.Add(time.Minute)},
		{Key: "test", Level: AlertWarn, Kind: "test", At: t0},
	})
	if a := state["a"]; a == nil || !a.active || !a.notified || !a.lastSent.Equal(t0) {
		t.Fatalf("a must be active+notified: %+v", a)
	}
	if b := state["b"]; b == nil || b.active {
		t.Fatalf("b must be resolved: %+v", b)
	}
	if _, ok := state["test"]; ok {
		t.Fatal("test events must not create alert state")
	}

	h := newAlertHarness()
	h.now = t0.Add(10 * time.Minute)
	h.m.state = state
	h.m.Raise("a", AlertCritical, "A", "") // restart inside cooldown: no duplicate
	h.m.Resolve("a", "ok")
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "ВОССТАНОВЛЕНО") {
		t.Fatalf("after restart want only the recovery: %q", h.sent)
	}
}

func TestReplayAlertEventsCountsOnlyDeliveredRaises(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	state := replayAlertEvents([]alertEvent{
		{Key: "failed", Level: AlertCritical, Kind: "raise", Title: "F", Delivered: false, Error: "down", At: t0},
		{Key: "okfail", Level: AlertCritical, Kind: "raise", Title: "O", Delivered: true, At: t0},
		{Key: "okfail", Level: AlertCritical, Kind: "repeat", Title: "O", Delivered: false, Error: "down", At: t0.Add(31 * time.Minute)},
	})
	if f := state["failed"]; f == nil || !f.active || f.notified || !f.lastSent.IsZero() {
		t.Fatalf("an undelivered raise must not count as notified: %+v", f)
	}

	h := newAlertHarness()
	h.now = t0.Add(time.Hour)
	h.m.state = state
	h.m.Resolve("failed", "ok") // nobody was told → silent
	h.m.Resolve("okfail", "ok") // the first raise was delivered → recovery
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "ВОССТАНОВЛЕНО") || h.events[0].Kind != "cleared" || h.events[1].Kind != "resolve" {
		t.Fatalf("sent=%q kinds=%s", h.sent, h.kinds())
	}

	// the undelivered raise is retried by the next Raise after a restart
	h2 := newAlertHarness()
	h2.now = t0.Add(time.Hour)
	h2.m.state = replayAlertEvents([]alertEvent{{Key: "failed", Level: AlertCritical, Kind: "raise", Title: "F", At: t0}})
	h2.m.Raise("failed", AlertCritical, "F", "")
	if len(h2.sent) != 1 {
		t.Fatalf("undelivered raise must be re-sent after a restart: %q", h2.sent)
	}
}

func TestReplayAlertEventsRestoresRetryBackoff(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	events := []alertEvent{{Key: "k", Level: AlertCritical, Kind: "raise", Title: "T", Delivered: false, Error: "not configured", At: t0}}

	h := newAlertHarness()
	h.now = t0.Add(time.Minute) // API restarted (deploy) right after the failed attempt
	h.m.state = replayAlertEvents(events)
	h.m.Raise("k", AlertCritical, "T", "")
	if h.attempts != 0 || len(h.events) != 0 {
		t.Fatalf("a restart must not skip the retry backoff: attempts=%d events=%d", h.attempts, len(h.events))
	}
	h.advance(alertRetryBackoff)
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 {
		t.Fatalf("after the backoff the raise must be retried: sent=%d", len(h.sent))
	}
}

// M-2: a critical incident whose later rows are warn (a re-sent raise after a failed delivery
// carries the caller's level) restores as critical, so the next critical poll after a restart
// is not taken for an escalation.
func TestReplayAlertEventsRestoresMaxLevelOfOpenIncident(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	state := replayAlertEvents([]alertEvent{
		{ID: 1, Key: "k", Level: AlertCritical, Kind: "raise", Title: "T", Delivered: true, At: t0},
		{ID: 2, Key: "k", Level: AlertCritical, Kind: "repeat", Title: "T", Error: "down", At: t0.Add(30 * time.Minute)},
		// the retry after the backoff came from a warn-level poll and got through
		{ID: 3, Key: "k", Level: AlertWarn, Kind: "raise", Title: "T", Delivered: true, At: t0.Add(36 * time.Minute)},
		// an older, closed critical incident must not leak into a new warn one
		{ID: 4, Key: "w", Level: AlertCritical, Kind: "raise", Title: "W", Delivered: true, At: t0},
		{ID: 5, Key: "w", Level: AlertCritical, Kind: "resolve", Title: "W", Delivered: true, At: t0.Add(time.Minute)},
		{ID: 6, Key: "w", Level: AlertWarn, Kind: "raise", Title: "W", Delivered: true, At: t0.Add(2 * time.Hour)},
	})
	if k := state["k"]; k == nil || k.level != AlertCritical || !k.active || !k.notified {
		t.Fatalf("open incident must keep its highest level: %+v", k)
	}
	if w := state["w"]; w == nil || w.level != AlertWarn {
		t.Fatalf("a new incident starts from its own level: %+v", w)
	}

	h := newAlertHarness()
	h.now = t0.Add(40 * time.Minute) // API restarted 4 min after the last message
	h.m.state = state
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 0 {
		t.Fatalf("the next critical poll after a restart is not an escalation: %q", h.sent)
	}
	h.advance(30 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "ПОВТОР") {
		t.Fatalf("after the cooldown the critical incident is repeated: %q", h.sent)
	}
}

// M-1: the extra rows loaded beyond the 7-day / 50-row window are merged into the window by id.
func TestMergeAlertEvents(t *testing.T) {
	ev := func(id int64) alertEvent { return alertEvent{ID: id, Key: "k"} }
	got := mergeAlertEvents([]alertEvent{ev(5), ev(7), ev(9)}, []alertEvent{ev(1), ev(7), ev(3), ev(9)})
	var ids []int64
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	if fmt.Sprint(ids) != "[1 3 5 7 9]" {
		t.Fatalf("merged ids %v", ids)
	}
}

// M-1: an active warn delivered long ago (only its raise left after the window) and a
// delivered raise pushed out of the window by later failed attempts both restore correctly.
func TestReplayAlertEventsWithAnchorsBeyondWindow(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	window := []alertEvent{}
	for i := 0; i < 3; i++ { // the newest rows of "c": failed attempts
		window = append(window, alertEvent{ID: int64(100 + i), Key: "c", Level: AlertCritical, Kind: "raise", Title: "C",
			Error: "Telegram HTTP 502: Bad Gateway", At: t0.Add(time.Duration(10+i) * 24 * time.Hour)})
	}
	anchors := []alertEvent{
		{ID: 1, Key: "disk", Level: AlertWarn, Kind: "raise", Title: "D", Delivered: true, At: t0},                                      // 8+ days old
		{ID: 2, Key: "c", Level: AlertCritical, Kind: "raise", Title: "C", Delivered: true, At: t0},                                     // the delivered raise
		{ID: 102, Key: "c", Level: AlertCritical, Kind: "raise", Title: "C", Error: "Telegram HTTP 502: Bad Gateway", At: window[2].At}, // newest row, duplicate
	}
	h := newAlertHarness()
	h.now = t0.Add(13 * 24 * time.Hour)
	h.m.state = replayAlertEvents(mergeAlertEvents(window, anchors))
	h.m.Raise("disk", AlertWarn, "D", "")
	if len(h.sent) != 0 || len(h.events) != 0 {
		t.Fatalf("an active warn must not be re-sent after a restart: sent=%q kinds=%s", h.sent, h.kinds())
	}
	h.m.Resolve("c", "up")
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "ВОССТАНОВЛЕНО") {
		t.Fatalf("the raise of c was delivered: the recovery is due: sent=%q kinds=%s", h.sent, h.kinds())
	}
}

// After a restart a repeated "not configured" failure of the same incident is not journaled
// again, and the restored retry backoff is the long one.
func TestReplayAlertEventsRestoresFailureDedup(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	h := newAlertHarness()
	h.m.send = func(string) error { h.attempts++; return errAlertsNotConfigured }
	h.now = t0.Add(10 * time.Minute)
	h.m.state = replayAlertEvents([]alertEvent{
		{ID: 1, Key: "k", Level: AlertCritical, Kind: "raise", Title: "T", Error: errAlertsNotConfigured.Error(), At: t0},
	})
	h.m.Raise("k", AlertCritical, "T", "")
	if h.attempts != 0 {
		t.Fatalf("inside the restored 30 min backoff: attempts=%d", h.attempts)
	}
	h.advance(20 * time.Minute)
	h.m.Raise("k", AlertCritical, "T", "")
	if h.attempts != 1 || len(h.events) != 0 {
		t.Fatalf("retried after the backoff, but no new row for the same failure: attempts=%d kinds=%s", h.attempts, h.kinds())
	}
	if und := h.m.undelivered(); len(und) != 1 || !und[0].NotConfigured || und[0].Attempts != 2 {
		t.Fatalf("undelivered after replay: %+v", und)
	}
}

// The short post-recovery window survives a restart: the resolve row restores the anchor.
func TestReplayAlertEventsRestoresRecoveryAnchor(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	h := newAlertHarness()
	h.now = t0.Add(6 * time.Minute)
	h.m.state = replayAlertEvents([]alertEvent{
		{ID: 1, Key: "k", Level: AlertCritical, Kind: "raise", Title: "T", Delivered: true, At: t0},
		{ID: 2, Key: "k", Level: AlertCritical, Kind: "resolve", Title: "T", Delivered: true, At: t0.Add(time.Minute)},
	})
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 {
		t.Fatalf("5 min after a delivered recovery a critical raise goes out, also after a restart: %q", h.sent)
	}
}

func TestAlertInitReplaysWithoutHoldingTheLock(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	m := &alertManager{state: map[string]*alertState{}}
	m.load = func() ([]alertEvent, error) {
		if !m.mu.TryLock() {
			t.Error("journal replay must not run under the manager mutex")
		} else {
			m.mu.Unlock()
		}
		return []alertEvent{{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", Delivered: true, At: t0}}, nil
	}
	m.Init()
	if a := m.state["a"]; !m.loaded || a == nil || !a.active || !a.notified {
		t.Fatalf("Init must restore a: loaded=%v state=%+v", m.loaded, a)
	}
}

func TestAlertInitRetriesAfterLoadFailure(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	m := &alertManager{state: map[string]*alertState{}, initRetry: 5 * time.Millisecond}
	m.load = func() ([]alertEvent, error) {
		if calls.Add(1) < 3 {
			return nil, errors.New("db down")
		}
		return []alertEvent{{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", Delivered: true, At: t0}}, nil
	}
	m.Init() // first attempt fails → retried in the background
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		loaded := m.loaded
		m.mu.Unlock()
		if loaded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("replay was not retried (load calls: %d)", calls.Load())
		}
		time.Sleep(2 * time.Millisecond)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a := m.state["a"]; calls.Load() != 3 || a == nil || !a.notified {
		t.Fatalf("state must be restored by the retry: calls=%d state=%+v", calls.Load(), a)
	}
}

func TestAlertLateReplayKeepsLiveState(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	h := newAlertHarness()
	h.now = t0
	fail := true
	h.m.loaded = false
	h.m.load = func() ([]alertEvent, error) {
		if fail {
			return nil, errors.New("db down")
		}
		return []alertEvent{
			{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", Delivered: true, At: t0.Add(-time.Hour)},
			{Key: "a", Level: AlertCritical, Kind: "resolve", Title: "A", Delivered: true, At: t0.Add(-50 * time.Minute)},
			{Key: "b", Level: AlertCritical, Kind: "raise", Title: "B", Delivered: true, At: t0.Add(-5 * time.Minute)},
		}, nil
	}
	if err := h.m.replay(); err == nil {
		t.Fatal("replay must report the DB error")
	}
	h.m.Raise("a", AlertCritical, "A", "") // raised while the journal was unavailable
	fail = false
	if err := h.m.replay(); err != nil || !h.m.loaded {
		t.Fatalf("replay must succeed once the DB is back: %v loaded=%v", err, h.m.loaded)
	}
	if st := h.m.state["a"]; st == nil || !st.active {
		t.Fatalf("late replay must not overwrite the live state of a: %+v", st)
	}
	if st := h.m.state["b"]; st == nil || !st.active || !st.notified {
		t.Fatalf("late replay must restore b: %+v", st)
	}
}

func TestAlertDBHelpersSurviveNilDB(t *testing.T) {
	if _, err := loadRecentAlertEvents(); !errors.Is(err, errAlertsNoDB) {
		t.Fatalf("loadRecentAlertEvents without DB: %v", err)
	}
	if _, err := purgeOldAlertEvents(); !errors.Is(err, errAlertsNoDB) {
		t.Fatalf("purgeOldAlertEvents without DB: %v", err)
	}
	if _, err := listAlertEvents(10); !errors.Is(err, errAlertsNoDB) {
		t.Fatalf("listAlertEvents without DB: %v", err)
	}
	old := alertTelegram
	defer func() { alertTelegram = old }()
	alertTelegram = newAlertTelegramSource(func(key string) (string, error) { return appSettingBounded(key, time.Second) })
	if err := sendTelegramAlert("x"); !errors.Is(err, errAlertsNoDB) || errors.Is(err, errAlertsNotConfigured) {
		t.Fatalf("sendTelegramAlert without DB and without a cache: %v", err)
	}
	recordAlertEvent(alertEvent{Key: "k", Level: AlertWarn, Kind: "raise", At: time.Now()}) // must not panic
}

// ── Telegram credentials cache (I-1) ──

// fakeSettings is an app_settings stand-in: down=true makes every read fail like a DB outage.
type fakeSettings struct {
	mu     sync.Mutex
	values map[string]string
	down   bool
	reads  int
}

func (f *fakeSettings) get(key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	if f.down {
		return "", errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
	}
	v, ok := f.values[key]
	if !ok {
		return "", sql.ErrNoRows
	}
	return v, nil
}

func (f *fakeSettings) set(key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return errors.New("dial tcp 127.0.0.1:5432: connect: connection refused")
	}
	f.values[key] = value
	return nil
}

// withFakeTelegram swaps the credentials source and the Telegram route for the test.
func withFakeTelegram(t *testing.T, fs *fakeSettings) *[]string {
	t.Helper()
	oldSrc, oldRoute := alertTelegram, telegram
	t.Cleanup(func() { alertTelegram, telegram = oldSrc, oldRoute })
	alertTelegram = newAlertTelegramSource(fs.get)
	var calls []string
	telegram = &telegramRoute{
		now:     time.Now,
		direct:  func(token, chatID, text string) error { calls = append(calls, token+"|"+chatID); return nil },
		viaExit: func(token, chatID, text string) error { return errNoExitRoute },
	}
	return &calls
}

const fakeBotToken = "123456789:AAFakeTokenFakeTokenFakeTokenFake12"

func TestSendTelegramAlertUsesCacheWhileDBIsDown(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-100"}}
	calls := withFakeTelegram(t, fs)

	if err := sendTelegramAlert("one"); err != nil {
		t.Fatalf("DB up: %v", err)
	}
	fs.down = true
	if err := sendTelegramAlert("two"); err != nil {
		t.Fatalf("DB down: delivery must use the cached credentials, got %v", err)
	}
	if len(*calls) != 2 || (*calls)[1] != fakeBotToken+"|-100" {
		t.Fatalf("both messages must reach Telegram with the cached token: %v", *calls)
	}
	// the DB is back with new values: they win over the cache
	fs.down = false
	fs.values[alertChatIDSetting] = "-200"
	if err := sendTelegramAlert("three"); err != nil || (*calls)[2] != fakeBotToken+"|-200" {
		t.Fatalf("a fresh read must refresh the cache: %v %v", err, *calls)
	}
}

func TestSendTelegramAlertDBDownWithoutCacheIsNotNotConfigured(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{}, down: true}
	calls := withFakeTelegram(t, fs)
	err := sendTelegramAlert("x")
	if !errors.Is(err, errAlertsNoDB) || errors.Is(err, errAlertsNotConfigured) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("a DB outage must be reported as such, not as Telegram not configured: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("nothing to send with: %v", *calls)
	}
	// a known "not configured" (empty values read successfully) is cached as such
	fs.down = false
	fs.values[alertTokenSetting] = ""
	if err := sendTelegramAlert("x"); !errors.Is(err, errAlertsNotConfigured) {
		t.Fatalf("empty settings: %v", err)
	}
	fs.down = true
	if err := sendTelegramAlert("x"); !errors.Is(err, errAlertsNotConfigured) {
		t.Fatalf("cached empty settings while the DB is down: %v", err)
	}
}

func TestAlertTelegramAfterWritePatchesCacheWhenReadFails(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-100"}}
	src := newAlertTelegramSource(fs.get)
	if _, err := src.fresh(); err != nil {
		t.Fatal(err)
	}
	fs.down = true // the write went through, the re-read right after it failed
	src.afterWrite(alertSettingsUpdate{chatID: "-300", setChatID: true})
	cfg, err := src.load()
	if err != nil || cfg.chatID != "-300" || cfg.token != fakeBotToken {
		t.Fatalf("the written chat_id must replace the cached one: %+v %v", cfg, err)
	}
	src.afterWrite(alertSettingsUpdate{token: "", setToken: true}) // token deleted
	if cfg, _ := src.load(); cfg.configured() {
		t.Fatalf("a deleted token must not stay usable from the cache: %+v", cfg)
	}
}

// ── Telegram transport ──

func TestTelegramSendMessageErrorsHideToken(t *testing.T) {
	const token = "123456789:AAFakeTokenFakeTokenFakeTokenFake12"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bot"+token+"/sendMessage" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()
	old := telegramAPIBase
	defer func() { telegramAPIBase = old }()

	telegramAPIBase = srv.URL
	err := telegramSendMessage(srv.Client(), token, "-100", "hi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("want Telegram description in error, got %v", err)
	}
	if !telegramFailureIsFinal(err) {
		t.Fatalf("a Telegram 400 is final, the exit tunnel cannot help: %v", err)
	}

	telegramAPIBase = "http://127.0.0.1:1"
	err = telegramSendMessage(&http.Client{Timeout: 3 * time.Second}, token, "-100", "hi")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("transport error must not contain the token, got %v", err)
	}
	if telegramFailureIsFinal(err) {
		t.Fatalf("a transport error must be retried through the exit: %v", err)
	}
}

func TestTelegramSendMessageMiddleboxAnswerIsNotFinal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html>blocked by provider</html>`))
	}))
	defer srv.Close()
	old := telegramAPIBase
	defer func() { telegramAPIBase = old }()
	telegramAPIBase = srv.URL

	err := telegramSendMessage(srv.Client(), "123456789:AAFakeTokenFakeTokenFakeTokenFake12", "-100", "hi")
	if err == nil || telegramFailureIsFinal(err) {
		t.Fatalf("a non-Telegram 403 (provider block page) must fall back to the exit: %v", err)
	}
}

type routeHarness struct {
	now         time.Time
	directCalls int
	exitCalls   int
	directErr   error
	exitErr     error
	r           *telegramRoute
}

func newRouteHarness() *routeHarness {
	h := &routeHarness{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	h.r = &telegramRoute{
		now:     func() time.Time { return h.now },
		direct:  func(token, chatID, text string) error { h.directCalls++; return h.directErr },
		viaExit: func(token, chatID, text string) error { h.exitCalls++; return h.exitErr },
	}
	return h
}

func TestTelegramRouteFallsBackOnTransportErrorAndRemembersIt(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("Post \"https://api.telegram.org/bot<token>/sendMessage\": context deadline exceeded")

	if err := h.r.send("t", "c", "x"); err != nil || h.directCalls != 1 || h.exitCalls != 1 {
		t.Fatalf("first send: err=%v direct=%d exit=%d", err, h.directCalls, h.exitCalls)
	}
	h.now = h.now.Add(time.Minute)
	if err := h.r.send("t", "c", "x"); err != nil || h.directCalls != 1 || h.exitCalls != 2 {
		t.Fatalf("direct path must be skipped for 10 min: err=%v direct=%d exit=%d", err, h.directCalls, h.exitCalls)
	}
	h.now = h.now.Add(10 * time.Minute) // 11 min after the failure
	if err := h.r.send("t", "c", "x"); err != nil || h.directCalls != 2 || h.exitCalls != 3 {
		t.Fatalf("direct path must be probed again: err=%v direct=%d exit=%d", err, h.directCalls, h.exitCalls)
	}
}

func TestTelegramRouteNoFallbackOnTelegram4xx(t *testing.T) {
	h := newRouteHarness()
	h.directErr = &telegramAPIError{Status: 400, Description: "Bad Request: chat not found", FromTelegram: true}

	err := h.r.send("t", "c", "x")
	if err == nil || !strings.Contains(err.Error(), "chat not found") || h.exitCalls != 0 {
		t.Fatalf("Telegram 4xx must be returned as is, without the exit: err=%v exit=%d", err, h.exitCalls)
	}
	_ = h.r.send("t", "c", "x")
	if h.directCalls != 2 || h.exitCalls != 0 {
		t.Fatalf("a Telegram verdict must not mark the direct path as down: direct=%d exit=%d", h.directCalls, h.exitCalls)
	}
}

func TestTelegramRouteWithoutExitReturnsDirectError(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("dial tcp: i/o timeout")
	h.exitErr = errNoExitRoute

	err := h.r.send("t", "c", "x")
	if err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("want the direct error when there is no exit: %v", err)
	}
	// direct is "down", exit is unavailable: the direct path is still tried
	_ = h.r.send("t", "c", "x")
	if h.directCalls != 2 {
		t.Fatalf("with no exit the direct path must keep being tried: direct=%d", h.directCalls)
	}
}

func TestTelegramRouteDirectRecoversWhileExitFails(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("dial tcp: i/o timeout")
	if err := h.r.send("t", "c", "x"); err != nil { // exit ok → direct marked down
		t.Fatal(err)
	}
	h.exitErr = errors.New("exit socks: connection refused")
	h.directErr = nil
	h.now = h.now.Add(time.Minute)
	if err := h.r.send("t", "c", "x"); err != nil {
		t.Fatalf("direct recovered, the message must go out: %v", err)
	}
	h.exitErr = nil
	h.now = h.now.Add(time.Minute)
	callsBefore := h.exitCalls
	if err := h.r.send("t", "c", "x"); err != nil || h.exitCalls != callsBefore {
		t.Fatalf("a recovered direct path must be used again: err=%v exit calls %d→%d", err, callsBefore, h.exitCalls)
	}
}

func TestTelegramRouteExit4xxIsFinal(t *testing.T) {
	h := newRouteHarness()
	h.directErr = errors.New("dial tcp: i/o timeout")
	h.exitErr = &telegramAPIError{Status: 401, Description: "Unauthorized", FromTelegram: true}
	err := h.r.send("t", "c", "x")
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("a Telegram verdict through the exit must be reported: %v", err)
	}
}

// ── admin settings ──

func TestAlertSettingsRequestValidate(t *testing.T) {
	const goodToken = "123456789:AAFakeTokenFakeTokenFakeTokenFake12"
	parse := func(body string) alertSettingsRequest {
		var req alertSettingsRequest
		if err := json.Unmarshal([]byte(body), &req); err != nil {
			t.Fatal(err)
		}
		return req
	}

	if upd, msg := parse(`{"bot_token":"` + goodToken + `"}`).validate(); msg != "" || !upd.setToken || upd.setChatID {
		t.Fatalf("a request without chat_id must not touch the stored chat_id: %+v %q", upd, msg)
	}
	if upd, msg := parse(`{"chat_id":""}`).validate(); msg != "" || !upd.setChatID || upd.chatID != "" || upd.setToken {
		t.Fatalf("an explicit empty chat_id clears it and keeps the token: %+v %q", upd, msg)
	}
	if upd, msg := parse(`{"chat_id":" -1001234567890 ","bot_token":""}`).validate(); msg != "" || upd.chatID != "-1001234567890" || upd.setToken {
		t.Fatalf("chat_id is trimmed, an empty token keeps the stored one: %+v %q", upd, msg)
	}
	if upd, msg := parse(`{"clear_token":true,"chat_id":"@alerts_ch"}`).validate(); msg != "" || !upd.setToken || upd.token != "" || upd.chatID != "@alerts_ch" {
		t.Fatalf("clear_token removes the token: %+v %q", upd, msg)
	}
	if _, msg := parse(`{"bot_token":"nope"}`).validate(); msg == "" {
		t.Fatal("a malformed token must be rejected")
	}
	if _, msg := parse(`{"chat_id":"not a chat"}`).validate(); msg == "" {
		t.Fatal("a malformed chat_id must be rejected")
	}
	if upd, msg := parse(`{}`).validate(); msg != "" || upd.setToken || upd.setChatID {
		t.Fatalf("an empty request changes nothing: %+v %q", upd, msg)
	}
}
