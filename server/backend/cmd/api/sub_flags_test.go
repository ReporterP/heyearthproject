package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

func TestFlagRegistry(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range subFlags {
		if seen[f.Key] {
			t.Errorf("duplicate key %s", f.Key)
		}
		seen[f.Key] = true
		if f.Canary && !slices.Equal(f.States, []string{"off", "test", "on"}) {
			t.Errorf("%s: a canary flag must have exactly off, test, on — got %v", f.Key, f.States)
		}
		if !slices.Contains(watchedSettingKeys, f.Key) {
			t.Errorf("%s is not watched for changes outside the console (watchedSettingKeys)", f.Key)
		}
		if f.Label == "" {
			t.Errorf("%s has no label", f.Key)
		}
	}
	if f, ok := flagByKey("sub_json_dns"); !ok || !f.Canary {
		t.Errorf("flagByKey(sub_json_dns) = %+v, %v", f, ok)
	}
	if _, ok := flagByKey("nope"); ok {
		t.Error("an unknown key is found")
	}
}

type flagFakes struct {
	settings map[string]string
	bumps    int
	feed     []biz.OpsEvent
}

func useFlagFakes(t *testing.T) *flagFakes {
	t.Helper()
	f := &flagFakes{settings: map[string]string{"sub_json_dns": "test"}}
	prevGet, prevSet, prevBump, prevRecord := flagGet, flagSet, flagBump, recordOps
	t.Cleanup(func() { flagGet, flagSet, flagBump, recordOps = prevGet, prevSet, prevBump, prevRecord })
	flagGet = func(ctx context.Context, k string) (string, error) { return f.settings[k], nil }
	flagSet = func(ctx context.Context, k, v string) error { f.settings[k] = v; return nil }
	flagBump = func(ctx context.Context) error { f.bumps++; return nil }
	recordOps = func(e biz.OpsEvent) { f.feed = append(f.feed, e) }
	return f
}

func TestSetFlagState(t *testing.T) {
	f := useFlagFakes(t)
	ctx := context.Background()
	flag, _ := flagByKey("sub_json_dns")
	if err := setFlagState(ctx, flag, "all", "owner"); err == nil {
		t.Error("a state outside States must be refused")
	}
	if err := setFlagState(ctx, flag, "test", "owner"); err != nil || f.bumps != 0 || len(f.feed) != 0 {
		t.Errorf("the state already set: err %v, bumps %d, feed %v — want nothing", err, f.bumps, f.feed)
	}
	if err := setFlagState(ctx, flag, "on", "owner"); err != nil {
		t.Fatal(err)
	}
	if f.settings["sub_json_dns"] != "on" || f.bumps != 1 || len(f.feed) != 1 {
		t.Fatalf("after test → on: settings %v, bumps %d, feed %v", f.settings, f.bumps, f.feed)
	}
	e := f.feed[0]
	if e.Target != "flag:sub_json_dns" || e.Impact != biz.ImpactK0 || e.Actor != "owner" || !strings.Contains(e.Summary, "тест → всем") {
		t.Errorf("feed record %+v", e)
	}
	if st, err := flagState(ctx, flag); err != nil || st != "on" {
		t.Errorf("flagState = %q, %v", st, err)
	}
	// a failed write is reported and records nothing
	flagSet = func(context.Context, string, string) error { return errors.New("db down") }
	if err := setFlagState(ctx, flag, "off", "owner"); err == nil || len(f.feed) != 1 {
		t.Errorf("failed write: %v, feed %d", err, len(f.feed))
	}
}

func TestSetUrgentUpdate(t *testing.T) {
	f := useFlagFakes(t)
	ctx := context.Background()
	now := time.Now()
	if err := setUrgentUpdate(ctx, 24, "automation"); err != nil {
		t.Fatal(err)
	}
	until, err := time.Parse(time.RFC3339, f.settings[subUrgentKey])
	if err != nil || until.Sub(now) < 23*time.Hour || until.Sub(now) > 25*time.Hour || f.bumps != 1 || len(f.feed) != 1 {
		t.Fatalf("urgent 24 h: until %v (%v), bumps %d, feed %v", until, err, f.bumps, f.feed)
	}
	if err := setUrgentUpdate(ctx, 0, "owner"); err != nil || f.settings[subUrgentKey] != "" {
		t.Errorf("urgent 0 stops it: %v %q", err, f.settings[subUrgentKey])
	}
	if err := setUrgentUpdate(ctx, 49, "owner"); err == nil {
		t.Error("more than 48 hours must be refused")
	}
}
