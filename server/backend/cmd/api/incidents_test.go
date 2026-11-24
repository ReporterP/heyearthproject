package main

import (
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

var incT0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func span(key string, level AlertLevel, from, to time.Duration) alertSpan {
	s := alertSpan{Key: key, Level: level, From: incT0.Add(from), Title: key}
	if to > 0 {
		s.To = incT0.Add(to)
	}
	return s
}

func TestGroupIncidents(t *testing.T) {
	now := incT0.Add(6 * time.Hour)
	// two alerts 5 minutes apart → one incident
	got := groupIncidents([]alertSpan{
		span("relay-6-unstable", AlertCritical, 0, 20*time.Minute),
		span("synth-way:Бамбук", AlertCritical, 25*time.Minute, 50*time.Minute),
	}, now)
	if len(got) != 1 || !got[0].From.Equal(incT0) || !got[0].To.Equal(incT0.Add(50*time.Minute)) || len(got[0].Keys) != 2 {
		t.Fatalf("5 min apart → one: %+v", got)
	}
	// 20 minutes apart → two
	got = groupIncidents([]alertSpan{
		span("a", AlertCritical, 0, 20*time.Minute),
		span("b", AlertCritical, 40*time.Minute, 50*time.Minute),
	}, now)
	if len(got) != 2 {
		t.Fatalf("20 min apart → two: %+v", got)
	}
	// a warning for 10 minutes is no incident; for 40 minutes it is
	got = groupIncidents([]alertSpan{span("w", AlertWarn, 0, 10*time.Minute)}, now)
	if len(got) != 0 {
		t.Fatalf("a short warning is no incident: %+v", got)
	}
	got = groupIncidents([]alertSpan{span("w", AlertWarn, 0, 40*time.Minute)}, now)
	if len(got) != 1 || got[0].Level != AlertWarn {
		t.Fatalf("a long warning is: %+v", got)
	}
	// an open critical one is an incident without an end
	got = groupIncidents([]alertSpan{span("db-unreachable", AlertCritical, time.Hour, 0)}, now)
	if len(got) != 1 || !got[0].To.IsZero() {
		t.Fatalf("an open critical alert: %+v", got)
	}
	// the level of a mixed incident is its worst
	got = groupIncidents([]alertSpan{span("w", AlertWarn, 0, 40*time.Minute), span("c", AlertCritical, 30*time.Minute, 35*time.Minute)}, now)
	if len(got) != 1 || got[0].Level != AlertCritical || got[0].Title != "c" {
		t.Fatalf("the worst level and its title: %+v", got)
	}
}

func TestSpansFromJournal(t *testing.T) {
	ev := []alertEvent{
		{Key: "a", Level: AlertCritical, Kind: "raise", Title: "A", At: incT0},
		{Key: "a", Level: AlertCritical, Kind: "repeat", At: incT0.Add(30 * time.Minute)},
		{Key: "a", Level: AlertCritical, Kind: "resolve", At: incT0.Add(40 * time.Minute)},
		{Key: "b", Level: AlertWarn, Kind: "inhibited", Title: "B", At: incT0.Add(time.Hour)},
		{Key: "c", Level: AlertCritical, Kind: "silenced", Title: "C", At: incT0.Add(2 * time.Hour)},
		{Key: "c", Level: AlertCritical, Kind: "cleared", At: incT0.Add(3 * time.Hour)},
	}
	got := spansFromJournal(ev)
	if len(got) != 3 || !got[0].To.Equal(incT0.Add(40*time.Minute)) || !got[1].To.IsZero() || !got[2].To.Equal(incT0.Add(3*time.Hour)) {
		t.Fatalf("spans: %+v", got)
	}
}

func TestIncidentMarkdown(t *testing.T) {
	inc := incident{ID: incT0.Unix(), From: incT0, To: incT0.Add(50 * time.Minute), Level: AlertCritical, Title: "Вход 6 теряет потоки",
		Keys: []string{"relay-6-unstable"}, Notes: "провайдер"}
	alerts := []alertEvent{
		{Key: "relay-6-unstable", Kind: "resolve", Title: "Вход 6", Details: "снова 99 %", At: incT0.Add(50 * time.Minute)},
		{Key: "relay-6-unstable", Kind: "raise", Title: "Вход 6", Details: "TLS 60 % на 203.0.113.9, пользователь 5f0c1a2e-0000-4000-8000-0000000000ab", At: incT0},
	}
	feed := []biz.FeedItem{{At: incT0.Add(20 * time.Minute), Source: "ops", Kind: "action", Actor: "owner", Summary: "вход 6 вне ротации"}}
	md := incidentMarkdown(inc, alerts, feed)
	if strings.Contains(md, "203.0.113.9") || strings.Contains(md, "5f0c1a2e-0000-4000-8000-0000000000ab") {
		t.Fatalf("no addresses, no full ids:\n%s", md)
	}
	for _, want := range []string{"# ", "## Хронология", "## Причина", "## Что сделано", "## Выводы", "провайдер"} {
		if !strings.Contains(md, want) {
			t.Fatalf("missing %q:\n%s", want, md)
		}
	}
	a, b, c := strings.Index(md, "снова 99"), strings.Index(md, "вне ротации"), strings.Index(md, "TLS 60")
	if !(c < b && b < a) {
		t.Fatalf("events in time order:\n%s", md)
	}
}
