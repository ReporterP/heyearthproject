package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// captureOps replaces the feed writer for one test.
func captureOps(t *testing.T) *[]biz.OpsEvent {
	t.Helper()
	var got []biz.OpsEvent
	old := recordOps
	recordOps = func(e biz.OpsEvent) { got = append(got, e) }
	t.Cleanup(func() { recordOps = old })
	return &got
}

// Every xray restart drops every user's connections: it goes to the feed with its cause.
func TestXrayRestartGoesToFeed(t *testing.T) {
	events := captureOps(t)
	old := xrayRestarts
	xrayRestarts = &slidingWindowCounter{window: xrayRestartWindow}
	t.Cleanup(func() { xrayRestarts = old })

	recordXrayRestart("revoked(quota)")
	if len(*events) != 1 {
		t.Fatalf("%d events, want 1", len(*events))
	}
	e := (*events)[0]
	if e.Kind != "restart" || e.Target != "xray" || e.Impact != biz.ImpactK3 || !e.OK || e.Actor != "system" {
		t.Errorf("event = %+v", e)
	}
	if !strings.Contains(e.Summary, "xray перезапущен") || !strings.Contains(e.Summary, "revoked(quota)") || e.Details["cause"] != "revoked(quota)" {
		t.Errorf("event = %+v, want the cause in the summary and the details", e)
	}
}

// A restart of hysteria2-server drops every Bamboo session: it goes to the feed with the
// reason — a new exit or a changed config. No restart, no event.
func TestBambooRestartGoesToFeed(t *testing.T) {
	render := func(port int) string { return "yaml" }
	commit := func(biz.ExpressChoice) error { return nil }
	restarted := func(string) (bool, error) { return true, nil }
	unchanged := func(string) (bool, error) { return false, nil }
	failed := func(string) (bool, error) { return false, errors.New("restart failed") }
	captureLog(t)

	events := captureOps(t)
	moved := biz.ExpressChoice{ExitID: 3, SocksPort: 1091, Switched: true, Optimisation: true, Reason: "exit #3 быстрее на 45% (ночное окно)"}
	if _, err := applyBambooChoice(moved, render, restarted, commit); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 {
		t.Fatalf("%d events after a restart for a new exit, want 1", len(*events))
	}
	e := (*events)[0]
	if e.Kind != "restart" || e.Target != "bamboo" || e.Impact != biz.ImpactK2 || !e.OK {
		t.Errorf("event = %+v", e)
	}
	if !strings.Contains(e.Summary, "выход #3") || !strings.Contains(e.Summary, "быстрее на 45%") || e.Details["optimisation"] != true {
		t.Errorf("event = %+v, want the exit, the reason and optimisation=true", e)
	}

	*events = nil
	same := biz.ExpressChoice{ExitID: 3, SocksPort: 1091}
	if _, err := applyBambooChoice(same, render, restarted, commit); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 || !strings.Contains((*events)[0].Summary, "изменился конфиг") || (*events)[0].Details["optimisation"] != false {
		t.Errorf("a restart on the same exit: %+v, want one «изменился конфиг» event", *events)
	}

	*events = nil
	if _, err := applyBambooChoice(moved, render, unchanged, commit); err != nil {
		t.Fatal(err)
	}
	if _, err := applyBambooChoice(moved, render, failed, commit); err == nil {
		t.Fatal("want the install error")
	}
	if len(*events) != 0 {
		t.Errorf("no restart happened, but the feed got %+v", *events)
	}
}

func TestStartEvents(t *testing.T) {
	// an ordinary deploy: the Hub has been up for days
	ev := startEvents("abc123def456", "2026-11-23T12:00:00Z", "999888777666", 72*time.Hour, true)
	if len(ev) != 1 {
		t.Fatalf("%d events, want only «start»: %+v", len(ev), ev)
	}
	s := ev[0]
	if s.Kind != "start" || s.Impact != biz.ImpactK1 || !s.OK || s.Target != "api" {
		t.Errorf("start event = %+v", s)
	}
	if s.Summary != "запущена сборка abc123def456, была 999888777666" || s.Details["sha"] != "abc123def456" || s.Details["prev"] != "999888777666" {
		t.Errorf("start event = %+v", s)
	}
	// the same build restarted (a crash, a manual restart)
	if s := startEvents("abc123def456", "", "abc123def456", time.Hour, true)[0]; s.Summary != "перезапущена сборка abc123def456" {
		t.Errorf("same build: %q", s.Summary)
	}
	// the first start that knows about the feed
	if s := startEvents("abc123def456", "", "", time.Hour, true)[0]; s.Summary != "запущена сборка abc123def456" {
		t.Errorf("no previous build: %q", s.Summary)
	}
	// the Hub itself came up a few minutes ago: a boot — every connection was dropped
	ev = startEvents("abc123def456", "", "abc123def456", 4*time.Minute, true)
	if len(ev) != 2 || ev[1].Kind != "boot" || ev[1].Target != "hub" || ev[1].Impact != biz.ImpactK3 {
		t.Fatalf("a Hub up for 4 minutes: %+v, want start + boot", ev)
	}
	// uptime unknown (not Linux, /proc unreadable): no guess
	if ev := startEvents("abc123def456", "", "", 0, false); len(ev) != 1 {
		t.Errorf("unknown uptime: %+v, want only «start»", ev)
	}
}

func TestParseProcUptime(t *testing.T) {
	if d, err := parseProcUptime("350735.47 234388.90\n"); err != nil || d != 350735*time.Second+470*time.Millisecond {
		t.Errorf("uptime = %v, %v", d, err)
	}
	for _, bad := range []string{"", "abc 1", "-5 1"} {
		if _, err := parseProcUptime(bad); err == nil {
			t.Errorf("parseProcUptime(%q): want an error", bad)
		}
	}
}
