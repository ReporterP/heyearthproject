package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notifierFixture returns three configs: base, base with an exit down (not
// client-facing) and base with another SNI (client-facing).
func notifierFixture(t *testing.T) (base, exitDown, newSNI []byte) {
	t.Helper()
	cfg := func(mod func(*xrayInputs)) []byte {
		in := baseInputs()
		if mod != nil {
			mod(&in)
		}
		j, _ := genConfig(t, in, quotaCutoff{})
		return j
	}
	return cfg(nil),
		cfg(func(in *xrayInputs) { in.exitNodes = in.exitNodes[:1] }),
		cfg(func(in *xrayInputs) { in.settings.realitySNI = "vk.com" })
}

func countingNotifier(path string) (*subscriptionNotifier, *int) {
	bumps := 0
	return &subscriptionNotifier{path: path, bump: func(context.Context) error { bumps++; return nil }}, &bumps
}

// A state file that can be neither read nor written (here: a directory) must
// not make every sync bump subscription_version (a banner every 5 minutes):
// it is "no stored digest" once, then the digest is known in memory. The WARN
// lines are logged once.
func TestSubscriptionNotifierUnreadableStateNeverBumpsPerSync(t *testing.T) {
	base, exitDown, newSNI := notifierFixture(t)
	logs := captureLog(t)
	n, bumps := countingNotifier(t.TempDir()) // reading a directory fails, not ENOENT

	// Restarts whose replaced config cannot be read: notify once to be safe,
	// never again for the same digest.
	for i := 0; i < 10; i++ {
		n.afterApply(base, nil, os.ErrNotExist, true)
	}
	if *bumps != 1 {
		t.Fatalf("unreadable state + unreadable old config: %d bumps over 10 syncs, want exactly 1", *bumps)
	}
	// Periodic syncs and exit failovers: nothing client-facing changed.
	for i := 0; i < 20; i++ {
		n.afterApply(base, base, nil, false)
		n.afterApply(exitDown, base, nil, true)
		n.afterApply(base, exitDown, nil, true)
	}
	if *bumps != 1 {
		t.Fatalf("unchanged client-facing config with an unreadable state file: %d bumps, want still 1", *bumps)
	}
	// A real change is still notified, once.
	for i := 0; i < 5; i++ {
		n.afterApply(newSNI, base, nil, true)
	}
	if *bumps != 2 {
		t.Fatalf("SNI change: %d bumps, want 2", *bumps)
	}
	out := logs.String()
	if c := strings.Count(out, "не читается"); c != 1 {
		t.Errorf("unreadable-state WARN logged %d times, want once:\n%s", c, out)
	}
	if c := strings.Count(out, "не записан"); c != 1 {
		t.Errorf("unwritable-state WARN logged %d times, want once:\n%s", c, out)
	}
}

// The first sync after a restart with an unreadable state file and an
// unchanged client-facing config stores the digest without notifying.
func TestSubscriptionNotifierUnreadableStateFirstSyncNoBump(t *testing.T) {
	base, exitDown, _ := notifierFixture(t)
	captureLog(t)
	n, bumps := countingNotifier(t.TempDir())
	for i := 0; i < 10; i++ {
		n.afterApply(exitDown, base, nil, true)
	}
	if *bumps != 0 {
		t.Fatalf("%d bumps, want 0", *bumps)
	}
}

// Corrupt content is "no stored digest" once and is overwritten with a valid
// digest; corruption appearing later falls back to the digest in memory.
func TestSubscriptionNotifierCorruptState(t *testing.T) {
	base, exitDown, _ := notifierFixture(t)
	logs := captureLog(t)
	path := filepath.Join(t.TempDir(), "xray-client-facing.sha256")
	if err := os.WriteFile(path, []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, bumps := countingNotifier(path)
	n.afterApply(exitDown, base, nil, true)
	if *bumps != 0 {
		t.Fatalf("corrupt state, unchanged client-facing config: %d bumps, want 0", *bumps)
	}
	b, _ := os.ReadFile(path)
	if !isClientFacingDigest(strings.TrimSpace(string(b))) {
		t.Fatalf("state not rewritten with a valid digest: %q", b)
	}
	_ = os.WriteFile(path, []byte("\x00\x01"), 0o600)
	for i := 0; i < 5; i++ {
		n.afterApply(base, exitDown, nil, true)
	}
	if *bumps != 0 {
		t.Fatalf("corruption mid-run: %d bumps, want 0 (the digest is known)", *bumps)
	}
	if !strings.Contains(logs.String(), "corrupt content") {
		t.Errorf("corrupt state not logged:\n%s", logs.String())
	}

	// A fresh process with a valid state file and a failed bump retries the
	// notification on the next sync, then stops.
	_, _, newSNI := notifierFixture(t)
	fails := 1
	retry := &subscriptionNotifier{path: path, bump: func(context.Context) error {
		if fails > 0 {
			fails--
			return errors.New("db down")
		}
		*bumps++
		return nil
	}}
	retry.afterApply(newSNI, base, nil, true)
	retry.afterApply(newSNI, newSNI, nil, false)
	retry.afterApply(newSNI, newSNI, nil, false)
	if *bumps != 1 {
		t.Fatalf("failed bump then retries: %d bumps, want 1", *bumps)
	}
}

func TestIsClientFacingDigest(t *testing.T) {
	good := strings.Repeat("ab", 32)
	for s, want := range map[string]bool{
		good:                     true,
		strings.ToUpper(good):    false,
		good[:63]:                false,
		strings.Repeat("zz", 32): false,
		"":                       false,
	} {
		if got := isClientFacingDigest(s); got != want {
			t.Errorf("isClientFacingDigest(%q) = %v, want %v", s, got, want)
		}
	}
}
