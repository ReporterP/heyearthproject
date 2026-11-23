package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBuildDigest(t *testing.T) {
	now := time.Date(2026, 11, 24, 6, 0, 0, 0, time.UTC) // 09:00 MSK
	text := buildDigest(now, digestInput{Alerts: 2, Autos: 1, Restarts: []string{"04:31 xray (ночь)"}, BuildSHA: "abc123", SynthPass: 140, SynthTotal: 144})
	for _, want := range []string{"2", "1", "04:31 xray (ночь)", "abc123", "140", "144", "24.11"} {
		if !strings.Contains(text, want) {
			t.Errorf("digest lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "@") || regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`).MatchString(text) {
		t.Errorf("the digest carries an address:\n%s", text)
	}
	// a quiet day says so in words, not with a row of zeros
	quiet := buildDigest(now, digestInput{BuildSHA: "abc123", SynthPass: 144, SynthTotal: 144})
	for _, want := range []string{"тревог не было", "VPN не перезапускался", "144 из 144"} {
		if !strings.Contains(quiet, want) {
			t.Errorf("a quiet day's digest lacks %q:\n%s", want, quiet)
		}
	}
	if !strings.Contains(buildDigest(now, digestInput{RebootRequired: true}), "перезагруз") {
		t.Error("a pending reboot is not mentioned")
	}
	if !strings.Contains(buildDigest(now, digestInput{SynthTotal: 0}), "не запускалась") {
		t.Error("no synthetic runs at all must be said, not shown as 0 из 0")
	}
}

func TestDigestOncePerDay(t *testing.T) {
	sent := 0
	last := ""
	d := &digestScheduler{
		build:   func(time.Time) (string, error) { return "сводка", nil },
		send:    func(title, text string) { sent++ },
		getLast: func() (string, error) { return last, nil },
		setLast: func(day string) error { last = day; return nil },
	}
	msk := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h-3, m, 0, 0, time.UTC) }
	d.tick(msk(24, 8, 59))
	if sent != 0 {
		t.Fatal("sent before 09:00 MSK")
	}
	d.tick(msk(24, 9, 0))
	d.tick(msk(24, 9, 5))
	d.tick(msk(24, 15, 0))
	if sent != 1 {
		t.Fatalf("%d digests on one day, want 1", sent)
	}
	d.tick(msk(25, 9, 1))
	if sent != 2 {
		t.Fatalf("%d digests after the next morning, want 2", sent)
	}
	// an API restart at 11:00 after the morning's digest: the day is remembered in the settings
	restarted := &digestScheduler{build: d.build, send: d.send, getLast: d.getLast, setLast: d.setLast}
	restarted.tick(msk(25, 11, 0))
	if sent != 2 {
		t.Fatalf("a restart sent the day's digest again: %d", sent)
	}
	// the API was down at 09:00: the digest goes out when it is back, the same day
	restarted.tick(msk(26, 13, 30))
	if sent != 3 {
		t.Fatalf("a late start did not send the day's digest: %d", sent)
	}
}
