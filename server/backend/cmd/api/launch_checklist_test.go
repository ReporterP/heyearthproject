package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func launchItemOf(c launchChecklist, key string) launchItem {
	for _, it := range c.Items {
		if it.Key == key {
			return it
		}
	}
	return launchItem{}
}

func TestLaunchChecklist(t *testing.T) {
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	// Day one: the Hub and one exit, nothing else.
	f := launchFacts{Now: now, HubSampleAt: now.Add(-time.Minute), HubCPU: 23, ExitsWorking: 1, ExitsOnline: 1, Mode: entryModeAuto, SiteHost: "example.test"}
	c := buildLaunchChecklist(f)
	if c.Total != 6 || c.Done != 1 || c.Complete {
		t.Fatalf("day one: %+v", c)
	}
	if it := launchItemOf(c, "hub"); !it.Done || !strings.Contains(it.Detail, "23 %") {
		t.Errorf("hub: %+v", it)
	}
	if it := launchItemOf(c, "exits"); it.Done || it.Action == nil || it.Action.Href != "/servers?tab=exit&add=1" || !strings.Contains(it.Detail, "1 из 1") {
		t.Errorf("exits: %+v", it)
	}
	if it := launchItemOf(c, "entry"); it.Done || it.Action.Href != "/servers?tab=relay&add=1" {
		t.Errorf("entry: %+v", it)
	}
	if it := launchItemOf(c, "mobile"); it.Done || it.Action != nil {
		t.Errorf("mobile without an entry has no button: %+v", it)
	}
	if it := launchItemOf(c, "enable"); it.Done || it.Action != nil {
		t.Errorf("enable without an entry: %+v", it)
	}

	// A stale Hub sample is not «работает».
	f.HubSampleAt = now.Add(-10 * time.Minute)
	if launchItemOf(buildLaunchChecklist(f), "hub").Done {
		t.Error("stale hub sample counted")
	}

	// An entry, not checked from a phone, Hub mode, the domain still on the Hub.
	f.HubSampleAt = now
	f.ExitsWorking, f.ExitsOnline = 2, 2
	f.Relays = []launchRelay{{ID: 7, Name: "Москва", IP: "203.0.113.7", ExtHealthy: true}}
	f.DNS = map[string][]string{"example.test": {"203.0.113.1"}, "www.example.test": {"203.0.113.1"}}
	c = buildLaunchChecklist(f)
	if it := launchItemOf(c, "mobile"); it.Done || it.Action.Href != "/servers?tab=relay&phone=7" {
		t.Errorf("mobile: %+v", it)
	}
	if it := launchItemOf(c, "enable"); it.Done || it.Action.Href != "/servers?tab=relay" {
		t.Errorf("enable: %+v", it)
	}
	if it := launchItemOf(c, "dns"); it.Done || !strings.Contains(it.Detail, "example.test → 203.0.113.1") || !strings.Contains(it.Detail, "www.example.test") {
		t.Errorf("dns on the hub: %+v", it)
	}

	// Everything done: the phone got through, pinned to the entry, the names on it (admin.
	// does not resolve: not required).
	f.Relays[0].MobileOK = now.Add(-time.Hour)
	f.Mode, f.PinnedTarget = entryModeRelay, 7
	f.DNS = map[string][]string{"example.test": {"203.0.113.7"}, "www.example.test": {"203.0.113.7"}}
	c = buildLaunchChecklist(f)
	if !c.Complete || c.Done != 6 {
		t.Fatalf("all done: %+v", c)
	}
	if it := launchItemOf(c, "enable"); !strings.Contains(it.Detail, "Москва") {
		t.Errorf("enable detail: %q", it.Detail)
	}
	if it := launchItemOf(c, "mobile"); !strings.Contains(it.Detail, "13.11 14:00") {
		t.Errorf("mobile detail (Moscow time): %q", it.Detail)
	}

	// A mixed record (entry + the old Hub one) is not done; the main name must resolve.
	f.DNS = map[string][]string{"example.test": {"203.0.113.7", "203.0.113.1"}}
	if it := launchItemOf(buildLaunchChecklist(f), "dns"); it.Done || !strings.Contains(it.Detail, "203.0.113.1") || strings.Contains(it.Detail, "203.0.113.7") {
		t.Errorf("mixed records: %+v", it)
	}
	f.DNS = map[string][]string{}
	if it := launchItemOf(buildLaunchChecklist(f), "dns"); it.Done || !strings.Contains(it.Detail, "не находится") {
		t.Errorf("unresolved main name: %+v", it)
	}
	f.DNSErr = "timeout"
	if it := launchItemOf(buildLaunchChecklist(f), "dns"); it.Done || !strings.Contains(it.Detail, "timeout") {
		t.Errorf("dns error: %+v", it)
	}
}

func TestResolveSiteNames(t *testing.T) {
	prev := launchLookup
	t.Cleanup(func() { launchLookup = prev })
	launchLookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "example.test":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}, {IP: net.ParseIP("2001:db8::1")}}, nil
		case "www.example.test":
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		return nil, errors.New("boom")
	}
	got, hard := resolveSiteNames(context.Background(), "example.test")
	if hard != "" || len(got["example.test"]) != 1 || got["example.test"][0] != "203.0.113.7" || got["www.example.test"] != nil {
		t.Fatalf("%v %q", got, hard)
	}
	launchLookup = func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("resolver down") }
	if _, hard := resolveSiteNames(context.Background(), "example.test"); hard == "" {
		t.Error("a broken resolver must be reported, not read as «не находится»")
	}
}
