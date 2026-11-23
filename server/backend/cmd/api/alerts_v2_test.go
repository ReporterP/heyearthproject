package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Review Focus 4: an alert whose cause is already reported is kept, not sent — and neither the
// cause nor its recovery is ever swallowed.

func TestRaiseInhibitedIsJournaledNotSent(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "оба туннеля мертвы")
	h.m.Raise("exit-path-down-3-hy", AlertWarn, "Путь Hysteria выхода 3 не работает", "d")
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "Выход 3 недоступен") {
		t.Fatalf("sent %q, want only the cause", h.sent)
	}
	if h.kinds() != "raise,inhibited" {
		t.Fatalf("journal kinds %q, want raise,inhibited", h.kinds())
	}
	child := h.m.state["exit-path-down-3-hy"]
	if child == nil || !child.active || child.notified {
		t.Fatalf("child state = %+v, want active and not notified", child)
	}
	// the monitor raises it again every minute: nothing more is journaled or sent
	for i := 0; i < 5; i++ {
		h.advance(time.Minute)
		h.m.Raise("exit-path-down-3-hy", AlertWarn, "Путь Hysteria выхода 3 не работает", "d")
	}
	if len(h.sent) != 1 || h.kinds() != "raise,inhibited" {
		t.Fatalf("after repeats: sent %d, kinds %q", len(h.sent), h.kinds())
	}
	// a muted alert that goes away by itself closes silently
	h.m.Resolve("exit-path-down-3-hy", "путь снова работает")
	if len(h.sent) != 1 || !strings.HasSuffix(h.kinds(), ",cleared") {
		t.Fatalf("a muted alert's recovery: sent %d, kinds %q; want it closed without a message", len(h.sent), h.kinds())
	}
	// an alert of another exit is not muted
	h.m.Raise("exit-path-down-4-hy", AlertWarn, "Путь Hysteria выхода 4 не работает", "d")
	if len(h.sent) != 2 {
		t.Fatalf("an unrelated alert was muted: sent %q", h.sent)
	}
}

func TestParentResolveReleasesChildren(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "d")
	h.m.Raise("exit-path-down-3-hy", AlertWarn, "Путь Hysteria выхода 3 не работает", "d")
	h.m.Raise("exit-path-down-3-vr", AlertWarn, "Запасной путь выхода 3 не работает", "d")
	h.advance(10 * time.Minute)
	h.m.Resolve("exit-path-down-3-hy", "снова работает") // this one recovered while muted
	h.advance(time.Minute)
	h.m.Resolve("exit-down-3", "Выход 3 снова отвечает")
	if len(h.sent) != 3 {
		t.Fatalf("sent %d messages %q, want the cause, its recovery and ONE message about what is left", len(h.sent), h.sent)
	}
	if !strings.Contains(h.sent[1], "ВОССТАНОВЛЕНО") || !strings.Contains(h.sent[1], "Выход 3 недоступен") {
		t.Errorf("the cause's recovery: %q", h.sent[1])
	}
	left := h.sent[2]
	if !strings.Contains(left, "Запасной путь выхода 3 не работает") || strings.Contains(left, "Путь Hysteria выхода 3") {
		t.Errorf("the message about what is left: %q, want only the alert still active", left)
	}
	if !strings.HasSuffix(h.kinds(), "resolve,notice") {
		t.Errorf("journal kinds %q, want …resolve,notice", h.kinds())
	}
	// the owner has been told about it now: raising it again sends nothing, its recovery is sent
	h.advance(time.Minute)
	h.m.Raise("exit-path-down-3-vr", AlertWarn, "Запасной путь выхода 3 не работает", "d")
	if len(h.sent) != 3 {
		t.Fatalf("the released alert was sent again: %q", h.sent[3:])
	}
	h.m.Resolve("exit-path-down-3-vr", "снова работает")
	if len(h.sent) != 4 || !strings.Contains(h.sent[3], "ВОССТАНОВЛЕНО") {
		t.Fatalf("the released alert's recovery: sent %q", h.sent)
	}
	// nothing left when the cause resolves: no extra message
	h2 := newAlertHarness()
	h2.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "d")
	h2.m.Raise("exit-path-down-3-hy", AlertWarn, "Путь", "d")
	h2.m.Resolve("exit-path-down-3-hy", "ok")
	h2.m.Resolve("exit-down-3", "ok")
	if len(h2.sent) != 2 {
		t.Fatalf("no alert was left, yet %d messages: %q", len(h2.sent), h2.sent)
	}
}

func TestParentRaiseAndResolveAlwaysSent(t *testing.T) {
	h := newAlertHarness()
	// children first, the cause after: the cause is sent whatever is active already
	h.m.Raise("exit-path-down-3-hy", AlertWarn, "Путь", "d")
	h.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "d")
	h.m.Raise("all-exits-down", AlertCritical, "Все выходы недоступны", "d")
	if len(h.sent) != 3 {
		t.Fatalf("sent %d, want the child (raised first), the exit and the critical cause: %q", len(h.sent), h.sent)
	}
	h.advance(5 * time.Minute)
	h.m.Resolve("all-exits-down", "есть живой выход")
	if n := len(h.sent); n != 4 || !strings.Contains(h.sent[3], "ВОССТАНОВЛЕНО") {
		t.Fatalf("the cause's recovery was not sent: %q", h.sent)
	}
	// db-unreachable mutes everything but is muted by nothing
	h = newAlertHarness()
	h.m.Raise("all-exits-down", AlertCritical, "Все выходы недоступны", "d")
	h.m.Raise("db-unreachable", AlertCritical, "База данных недоступна", "d")
	h.m.Raise("backup-stale", AlertWarn, "Бэкап устарел", "d")
	h.m.Raise("hysteria-auth-listener", AlertCritical, "Бамбук: вход не работает", "d")
	if len(h.sent) != 3 {
		t.Fatalf("sent %q: want all-exits-down, db-unreachable and hysteria-auth-listener, not backup-stale", h.sent)
	}
}

func TestFlapEscalationThroughManager(t *testing.T) {
	h := newAlertHarness()
	h.m.flap = newFlapTracker()
	for i := 0; i < 3; i++ {
		h.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "d")
		h.advance(10 * time.Minute)
		h.m.Resolve("exit-down-3", "ok")
		h.advance(35 * time.Minute) // past the 30-minute suppression of a key that just spoke
	}
	var unstable string
	for _, s := range h.sent {
		if strings.Contains(s, "exit-down-3~unstable") {
			unstable = s
		}
	}
	if unstable == "" || !strings.Contains(unstable, "Нестабильно") {
		t.Fatalf("three incidents in 2.5 hours: no «нестабильно» alert among %q", h.sent)
	}
	if st := h.m.state["exit-down-3~unstable"]; st == nil || !st.active {
		t.Fatal("the «нестабильно» alert is not active")
	}
	before := len(h.sent)
	h.advance(61 * time.Minute)
	h.m.flapSweep()
	if st := h.m.state["exit-down-3~unstable"]; st.active {
		t.Fatal("an hour of silence did not close the «нестабильно» alert")
	}
	if len(h.sent) != before+1 || !strings.Contains(h.sent[before], "ВОССТАНОВЛЕНО") {
		t.Fatalf("the «нестабильно» alert's recovery: %q", h.sent[before:])
	}
}

func TestEmailFallbackAfter15Min(t *testing.T) {
	h := newAlertHarness()
	var mails []string
	to := "owner@example.test"
	h.m.emailTo = func() string { return to }
	h.m.email = func(to, subject, body string) error {
		mails = append(mails, to+"|"+subject+"|"+body)
		return nil
	}
	h.failSends = 1000 // Telegram is down
	h.m.Raise("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи", "три проверки подряд")
	for min := 6; min <= 12; min += 6 { // retries at 6 and 12 minutes: not 15 yet
		h.advance(6 * time.Minute)
		h.m.Raise("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи", "d")
	}
	if len(mails) != 0 {
		t.Fatalf("a letter after 12 minutes: %q", mails)
	}
	h.advance(6 * time.Minute) // the next attempt the 5-minute backoff allows: 18 minutes after the first failure
	h.m.Raise("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи", "d")
	if len(mails) != 1 {
		t.Fatalf("%d letters after 18 minutes without Telegram, want 1", len(mails))
	}
	if !strings.HasPrefix(mails[0], "owner@example.test|") || !strings.Contains(mails[0], "Вход недоступен снаружи") || !strings.Contains(mails[0], "Telegram") {
		t.Errorf("letter = %q", mails[0])
	}
	for i := 0; i < 5; i++ { // it stays down: one letter per incident
		h.advance(6 * time.Minute)
		h.m.Raise("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи", "d")
	}
	if len(mails) != 1 {
		t.Fatalf("%d letters for one incident, want 1", len(mails))
	}
	// no address set: no letters
	to = ""
	h2 := newAlertHarness()
	h2.m.emailTo, h2.m.email = func() string { return to }, h.m.email
	h2.failSends = 1000
	for i := 0; i < 6; i++ {
		h2.m.Raise("k", AlertCritical, "T", "d")
		h2.advance(6 * time.Minute)
	}
	if len(mails) != 1 {
		t.Fatalf("a letter although alert_email is empty: %q", mails[1:])
	}
	// a letter that cannot be sent is tried again with the next attempt
	h3 := newAlertHarness()
	tries := 0
	h3.m.emailTo = func() string { return "owner@example.test" }
	h3.m.email = func(string, string, string) error { tries++; return errors.New("smtp down") }
	h3.failSends = 1000
	for i := 0; i < 6; i++ {
		h3.m.Raise("k", AlertCritical, "T", "d")
		h3.advance(6 * time.Minute)
	}
	if tries < 2 {
		t.Fatalf("a failed letter was tried %d time(s), want it retried", tries)
	}
}

func TestNotifyOpsJournaledOnce(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("disk-usage", AlertWarn, "Диск", "90%")
	states := len(h.m.state)
	h.m.Notify("Хаб перезагрузился", "Самопроверка: 16 проверок, все прошли.")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "Хаб перезагрузился") || !strings.Contains(h.sent[1], "Самопроверка") {
		t.Fatalf("sent %q", h.sent)
	}
	if h.kinds() != "raise,notice" {
		t.Fatalf("journal kinds %q, want raise,notice", h.kinds())
	}
	if len(h.m.state) != states {
		t.Fatalf("a notice created alert state: %d keys, was %d", len(h.m.state), states)
	}
	// a notice that cannot be delivered is journaled as such and not retried: it has no state
	h.failSends = 1
	h.m.Notify("Сводка", "…")
	if last := h.events[len(h.events)-1]; last.Kind != "notice" || last.Delivered || last.Error == "" {
		t.Fatalf("an undelivered notice: %+v", last)
	}
	// replay ignores notices and digests: they are not incidents
	st := replayAlertEvents(h.events)
	if len(st) != 1 || st["disk-usage"] == nil {
		t.Fatalf("replay built state from a notice: %v", st)
	}
}

func TestAlertMessageCarriesConsoleLink(t *testing.T) {
	h := newAlertHarness()
	h.m.link = func(key string) string { return "https://admin.example.test/operations?alert=" + url.QueryEscape(key) }
	h.m.Raise("synth-way:⚡ Авто/p-r5-tcp", AlertWarn, "Путь подписки не несёт данные", "d")
	want := "https://admin.example.test/operations?alert=" + url.QueryEscape("synth-way:⚡ Авто/p-r5-tcp")
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], want) {
		t.Fatalf("sent %q, want the console link %s", h.sent, want)
	}
	if strings.Contains(h.events[0].Details, "admin.example.test") {
		t.Error("the link belongs to the message, not to the journal's details")
	}
}

// An «inhibited» row in the journal is an open incident nobody was told about: after a
// restart the next raise decides again.
func TestReplayInhibited(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	st := replayAlertEvents([]alertEvent{
		{Key: "exit-path-down-3-hy", Level: AlertWarn, Kind: "inhibited", Title: "Путь", At: t0},
		{Key: "x", Level: AlertWarn, Kind: "digest", Title: "Сводка", At: t0},
	})
	if got := st["exit-path-down-3-hy"]; got == nil || !got.active || got.notified {
		t.Fatalf("replayed state = %+v, want active, not notified", got)
	}
	if _, ok := st["x"]; ok {
		t.Fatal("a digest row became alert state")
	}
}
