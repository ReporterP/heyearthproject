package main

import (
	"encoding/json"
	"errors"
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

func TestAlertFlappingSuppressedByCooldown(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("k", AlertCritical, "T", "") // +0m  sent
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +1m  sent (recovery)
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +2m  suppressed
	h.advance(time.Minute)
	h.m.Resolve("k", "up") // +3m  cleared silently
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "") // +4m  suppressed
	if len(h.sent) != 2 {
		t.Fatalf("flapping inside cooldown must be silent, got %d: %q", len(h.sent), h.sent)
	}
	h.advance(28 * time.Minute) // +32m, 31m after the last message
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 3 {
		t.Fatalf("still-active alert must go out once cooldown expires, got %d", len(h.sent))
	}
	if got := h.kinds(); got != "raise,resolve,suppressed,cleared,suppressed,raise" {
		t.Fatalf("event kinds: %s", got)
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
	h.advance(time.Second)
	h.m.Raise("k", AlertCritical, "T", "d") // next poll of the monitor: must retry, not wait out the cooldown
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
	h.advance(time.Second)
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
	// nothing was delivered → no flapping cooldown either: a new outage is reported at once
	h.m.Raise("k", AlertCritical, "T", "")
	if len(h.sent) != 1 {
		t.Fatalf("new outage after a silent incident must be sent, got %d", len(h.sent))
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
	if err := sendTelegramAlert("x"); !errors.Is(err, errAlertsNoDB) {
		t.Fatalf("sendTelegramAlert without DB: %v", err)
	}
	recordAlertEvent(alertEvent{Key: "k", Level: AlertWarn, Kind: "raise", At: time.Now()}) // must not panic
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
