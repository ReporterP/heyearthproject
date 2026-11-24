package main

import (
	"testing"
	"time"
)

var onlineT0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func TestOnlineNowCountsOnlyTheLastTwoMinutes(t *testing.T) {
	o := newOnlineTracker()
	o.note("xray", []string{"5f0c1a2e-0000-4000-8000-000000000001"}, onlineT0.Add(-5*time.Minute))
	o.note("xray", []string{"5f0c1a2e-0000-4000-8000-000000000002"}, onlineT0.Add(-time.Minute))
	o.note("bamboo", nil, onlineT0.Add(-time.Minute))
	got := o.count(onlineT0)
	if got.Xray != 1 || got.Total != 1 {
		t.Fatalf("a minute ago counts, five minutes ago does not: %+v", got)
	}
	if got.Stale {
		t.Fatalf("both collectors read a minute ago — not stale: %+v", got)
	}
}

func TestOnlineNowOneUserOnBothCountsOnce(t *testing.T) {
	o := newOnlineTracker()
	u := "5f0c1a2e-0000-4000-8000-000000000003"
	o.note("xray", []string{u}, onlineT0.Add(-30*time.Second))
	o.note("bamboo", []string{u, "5f0c1a2e-0000-4000-8000-000000000004"}, onlineT0.Add(-20*time.Second))
	got := o.count(onlineT0)
	if got.Xray != 1 || got.Bamboo != 2 || got.Total != 2 {
		t.Fatalf("one user on xray and Bamboo is one person in Total: %+v", got)
	}
}

func TestOnlineNowWithoutDataIsStale(t *testing.T) {
	o := newOnlineTracker()
	if got := o.count(onlineT0); !got.Stale || !got.XrayStale || !got.BambooStale {
		t.Fatalf("no reads at all → stale: %+v", got)
	}
	// xray read 4 minutes ago (longer than 3), Bamboo fresh: the xray number is unknown.
	o.note("xray", nil, onlineT0.Add(-4*time.Minute))
	o.note("bamboo", nil, onlineT0.Add(-time.Minute))
	got := o.count(onlineT0)
	if !got.Stale || !got.XrayStale || got.BambooStale {
		t.Fatalf("xray data older than 3 minutes → stale for xray only: %+v", got)
	}
}

func TestOnlineNowEmptyFreshReadIsZeroNotStale(t *testing.T) {
	o := newOnlineTracker()
	o.note("xray", nil, onlineT0.Add(-40*time.Second))
	o.note("bamboo", []string{}, onlineT0.Add(-40*time.Second))
	got := o.count(onlineT0)
	if got.Stale || got.Total != 0 {
		t.Fatalf("a fresh read with nobody transferring is a known zero: %+v", got)
	}
	if !got.At.Equal(onlineT0.Add(-40 * time.Second)) {
		t.Fatalf("At is the older of the two reads: %v", got.At)
	}
}
