package main

import (
	"strings"
	"testing"
)

func TestApplyHubTransition(t *testing.T) {
	p := entryPlan{interval: 4}
	if got := applyHubTransition(p, true).interval; got != 1 {
		t.Errorf("transition: interval %d, want 1", got)
	}
	if got := applyHubTransition(p, false).interval; got != 4 {
		t.Errorf("no transition: interval %d, want 4", got)
	}
	pinned := entryPlan{interval: 6, pinned: true}
	if got := applyHubTransition(pinned, true).interval; got != 1 {
		t.Errorf("pinned entry during the transition: interval %d, want 1", got)
	}
}

func TestSubscriptionHourlyDuringHubTransition(t *testing.T) {
	s := goldenScript()
	s.settings["hub_addr_transition_since"] = "2026-11-20T10:00:00Z"
	_, h := entriesRequest(t, s, "Happ/3.1 Android", "")
	if got := h.Get("Profile-Update-Interval"); got != "1" {
		t.Fatalf("Profile-Update-Interval = %q during the transition, want 1", got)
	}
	touched := false
	for _, w := range s.writes {
		if strings.Contains(w, "subscription_fetched_at") {
			touched = true
		}
	}
	if !touched {
		t.Fatalf("the fetch time was not recorded: %v", s.writes)
	}

	_, h = entriesRequest(t, goldenScript(), "Happ/3.1 Android", "")
	if got := h.Get("Profile-Update-Interval"); got != "4" {
		t.Fatalf("Profile-Update-Interval = %q without a transition, want 4", got)
	}
}
