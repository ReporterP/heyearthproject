package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type alertHarness struct {
	now    time.Time
	sent   []string
	events []alertEvent
	m      *alertManager
}

// newAlertHarness builds a synchronous manager (queue == nil) with a fake clock,
// fake sender and in-memory event store.
func newAlertHarness() *alertHarness {
	h := &alertHarness{now: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	h.m = &alertManager{
		now:      func() time.Time { return h.now },
		send:     func(text string) error { h.sent = append(h.sent, text); return nil },
		record:   func(ev alertEvent) { h.events = append(h.events, ev) },
		cooldown: 30 * time.Minute,
		state:    map[string]*alertState{},
		loaded:   true,
	}
	return h
}

func (h *alertHarness) advance(d time.Duration) { h.now = h.now.Add(d) }

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
	var kinds []string
	for _, ev := range h.events {
		kinds = append(kinds, ev.Kind)
	}
	if got := strings.Join(kinds, ","); got != "raise,resolve,suppressed,cleared,suppressed,raise" {
		t.Fatalf("event kinds: %s", got)
	}
}

func TestReplayAlertEventsRestoresState(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	state := replayAlertEvents([]alertEvent{
		{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", At: t0},
		{Key: "b", Level: AlertWarn, Kind: "raise", Title: "B", At: t0},
		{Key: "b", Level: AlertWarn, Kind: "resolve", Title: "B", At: t0.Add(time.Minute)},
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

	telegramAPIBase = "http://127.0.0.1:1"
	err = telegramSendMessage(&http.Client{Timeout: 3 * time.Second}, token, "-100", "hi")
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("transport error must not contain the token, got %v", err)
	}
}
