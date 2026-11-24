package main

import (
	"strings"
	"testing"
	"time"
)

func TestSilencedBy(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	list := []silence{
		{ID: 1, Pattern: "relay-5-*", Until: now.Add(time.Hour)},
		{ID: 2, Pattern: "synth-line:*", Until: now.Add(-time.Minute)}, // over
		{ID: 3, Pattern: "relay-*-unstable", Until: now.Add(time.Hour)},
		{ID: 4, Pattern: "backup-stale", Until: now.Add(time.Hour)},
	}
	cases := []struct {
		key  string
		want int64
	}{
		{"relay-5-unreachable", 1},
		{"relay-5-unstable", 1}, // the first that matches
		{"relay-6-unstable", 3},
		{"relay-6-unreachable", 0},
		{"synth-line:⚡ Авто//blocked", 0},
		{"backup-stale", 4},
		{"backup-stale~unstable", 0}, // an exact pattern is exact
	}
	for _, c := range cases {
		s, ok := silencedBy(c.key, list, now)
		if (c.want == 0) == ok || (ok && s.ID != c.want) {
			t.Errorf("%s: got %v %d, want %d", c.key, ok, s.ID, c.want)
		}
	}
}

func TestValidSilence(t *testing.T) {
	bad := []struct {
		p string
		d time.Duration
	}{
		{"*", time.Hour},
		{"re*", time.Hour},            // the literal start is shorter than 4
		{"*-unstable", time.Hour},     // no literal start at all
		{"relay-5-*", 25 * time.Hour}, // longer than a day
		{"relay-5-*", 0},
		{"", time.Hour},
	}
	for _, b := range bad {
		if validSilence(b.p, b.d) == nil {
			t.Errorf("%q for %v must be refused", b.p, b.d)
		}
	}
	for _, p := range []string{"relay-5-*", "backup-stale", "synth-line:*"} {
		if err := validSilence(p, time.Hour); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
}

func TestRaiseUnderSilence(t *testing.T) {
	h := newAlertHarness()
	until := h.now.Add(time.Hour)
	h.m.silenced = func(key string, now time.Time) (string, bool) {
		if strings.HasPrefix(key, "relay-5-") && now.Before(until) {
			return "relay-5-* до 13:00", true
		}
		return "", false
	}
	h.m.Raise("relay-5-unreachable", AlertCritical, "Вход 5 недоступен", "d1")
	if len(h.sent) != 0 || h.kinds() != "silenced" {
		t.Fatalf("under a silence: journaled «silenced», nothing sent: %q %q", h.kinds(), h.sent)
	}
	h.advance(40 * time.Minute)
	h.m.Raise("relay-5-unreachable", AlertCritical, "Вход 5 недоступен", "d2")
	if len(h.sent) != 0 || h.kinds() != "silenced" {
		t.Fatalf("once per incident: %q", h.kinds())
	}
	h.m.Resolve("relay-5-unreachable", "снова работает")
	if len(h.sent) != 0 || h.kinds() != "silenced,cleared" {
		t.Fatalf("the resolve of a silenced incident is silent: %q %q", h.kinds(), h.sent)
	}
	h.m.Raise("relay-6-unreachable", AlertCritical, "Вход 6 недоступен", "d")
	if len(h.sent) != 1 {
		t.Fatalf("another key is not silenced: %q", h.sent)
	}
	h.m.Raise("relay-5-unreachable", AlertCritical, "Вход 5 недоступен", "d3")
	h.advance(30 * time.Minute) // the silence is over
	h.m.Raise("relay-5-unreachable", AlertCritical, "Вход 5 недоступен", "d4")
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "Вход 5") {
		t.Fatalf("after the silence the next raise is sent: %q", h.sent)
	}
}
