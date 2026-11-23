package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

func TestAlertInfoPrefix(t *testing.T) {
	info := alertInfoFor("relay-6-unreachable-external")
	if info.Meaning == "" || info.Usual == "" {
		t.Fatalf("relay-6-unreachable-external: %+v, want a meaning and a usual cause", info)
	}
	if !slices.Contains(info.Actions, "entry.pathcheck") {
		t.Errorf("actions = %v, want entry.pathcheck among them", info.Actions)
	}
	// the longest prefix wins: a relay's Bamboo alert is not the relay's TCP alert
	if alertInfoFor("relay-6-bamboo").Meaning == info.Meaning {
		t.Error("relay-6-bamboo got the text of relay-6-unreachable-external")
	}
	// exit-path-down-3-hy is about a path, exit-down-3 about the whole exit
	if alertInfoFor("exit-path-down-3-hy").Meaning == alertInfoFor("exit-down-3").Meaning {
		t.Error("exit-path-down-* and exit-down-* share a text")
	}
	if got := alertInfoFor("nobody-knows-this-key"); got.Meaning != "" || len(got.Actions) != 0 {
		t.Errorf("an unknown key: %+v, want the empty info", got)
	}
	// «нестабильно» is the same alert, flapping: its text builds on the alert's own
	base, unstable := alertInfoFor("exit-down-3"), alertInfoFor("exit-down-3~unstable")
	if unstable.Meaning == "" || !strings.Contains(unstable.Meaning, base.Meaning) || unstable.Meaning == base.Meaning {
		t.Errorf("exit-down-3~unstable: %q", unstable.Meaning)
	}
}

var (
	alertLiteralRe = regexp.MustCompile(`(?:RaiseAlert|RaiseAlertNoRepeat|quotaRaiseAlert|\.raise|\.Alert)\(\s*(?:fmt\.Sprintf\()?"([a-z][a-z0-9:~%-]*)"`)
	alertConstRe   = regexp.MustCompile(`(?:Alert(?:Key|Prefix)?|alertKey)\s*=\s*"([a-z][a-z0-9:~-]*)"`)
	alertFuncRe    = regexp.MustCompile(`AlertKey\([^)]*\) string\s*\{\s*return (?:fmt\.Sprintf\()?"([a-z][a-z0-9:~%-]*)"`)
)

// alertKeysInCode collects every alert key (or key prefix, for keys built with an id) the
// code of this package can raise.
func alertKeysInCode(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{alertLiteralRe, alertConstRe, alertFuncRe} {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				key := m[1]
				if i := strings.Index(key, "%"); i >= 0 {
					key = key[:i]
				}
				if len(key) >= 4 {
					seen[key] = true
				}
			}
		}
	}
	var keys []string
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Every alert the code can raise has an explanation. A new key without an entry in the
// catalogue fails this test: an alert nobody can read is noise.
func TestAlertInfoCoversAllKeys(t *testing.T) {
	keys := alertKeysInCode(t)
	for _, must := range []string{"xray-restart-storm", "backup-stale", "tls-cert-expiry", "capacity-buy-more", "ddos-signals",
		"quota-cutoff-failopen", "revoked-still-online", "synthetic-blind", "synth-all-ways", "db-unreachable",
		"exit-down-", "exit-path-down-", "relay-", "entry-failover-", "payment-unapplied-", "entry-unreachable-external"} {
		if !slices.Contains(keys, must) {
			t.Errorf("the scan of the code did not find %q — the scan itself is broken (found %d keys: %v)", must, len(keys), keys)
		}
	}
	for _, key := range keys {
		probe := key
		if strings.HasSuffix(key, "-") || strings.HasSuffix(key, ":") {
			probe = key + "1" // a key built with an id
		}
		if alertInfoFor(probe).Meaning == "" {
			t.Errorf("alert key %q has no entry in the catalogue (alert_catalog.go)", key)
		}
	}
	// the keys the dynamic parts of relayAlertKey can form
	for _, key := range []string{"relay-5-unreachable-external", "relay-5-bamboo", "relay-5-unstable", "relay-5-unreachable-ru",
		"synth-way:⚡ Авто/p-r5-tcp", "synth-line:⚡ Авто/blocked", "site-unreachable-ru", "subdomain-unreachable-ru", "entry-unreachable-ru"} {
		if alertInfoFor(key).Meaning == "" {
			t.Errorf("alert key %q has no entry in the catalogue", key)
		}
	}
}

func TestInhibitedBy(t *testing.T) {
	active := func(keys ...string) func(string) bool {
		return func(k string) bool { return slices.Contains(keys, k) }
	}
	for _, c := range []struct {
		key    string
		active []string
		want   string
	}{
		{"exit-path-down-3-hy", []string{"exit-down-3"}, "exit-down-3"},
		{"exit-path-down-4-hy", []string{"exit-down-3"}, ""},
		{"exit-path-down-31-hy", []string{"exit-down-3"}, ""}, // 31 is not 3
		// the database mutes what cannot work without it, and nothing else (PRD, part 5)
		{"backup-stale", []string{"db-unreachable"}, "db-unreachable"},
		{"xray-sync-failed", []string{"db-unreachable"}, "db-unreachable"},
		{"synthetic-blind", []string{"db-unreachable"}, "db-unreachable"},
		{"payment-activation-failed-11111111", []string{"db-unreachable"}, "db-unreachable"},
		{"wallet-receipt-failed-42", []string{"db-unreachable"}, "db-unreachable"},
		{"disk-usage", []string{"db-unreachable"}, ""},
		{"tls-cert-expiry", []string{"db-unreachable"}, ""},
		{"domain-expiry", []string{"db-unreachable"}, ""},
		{"all-exits-down", []string{"db-unreachable"}, ""},
		{"exit-down-3", []string{"db-unreachable"}, ""},
		{"entry-unreachable-external", []string{"db-unreachable"}, ""},
		{"relay-5-unreachable-external", []string{"db-unreachable"}, ""},
		{"bamboo-unreachable-external", []string{"db-unreachable"}, ""},
		{"exit-path-down-3-hy", []string{"db-unreachable", "exit-down-3"}, "exit-down-3"},
		{"db-unreachable", []string{"db-unreachable"}, ""},
		{"hysteria-auth-listener", []string{"db-unreachable"}, ""},
		{"exit-down-3", []string{"all-exits-down"}, "all-exits-down"},
		{"exit-path-down-3-hy", []string{"all-exits-down"}, "all-exits-down"},
		{"synth-way:⚡ Авто/p-hub-tcp", []string{"all-exits-down"}, "all-exits-down"},
		{"bamboo-unreachable-external", []string{"all-exits-down"}, "all-exits-down"},
		{"tg-webproxy-readyz", []string{"all-exits-down"}, "all-exits-down"},
		{"bamboo-unreachable-external", []string{"entry-unreachable-external"}, "entry-unreachable-external"},
		{"relay-5-unreachable-external", []string{"entry-unreachable-external"}, "entry-unreachable-external"},
		{"synth-line:⚡ Авто/blocked", []string{"entry-unreachable-external"}, "entry-unreachable-external"},
		{"synth-way:⚡ Авто/p-hub-tcp", []string{"synth-all-ways"}, "synth-all-ways"},
		{"synth-all-ways", []string{"synth-all-ways"}, ""},
		{"synthetic-blind", []string{"synth-all-ways"}, ""}, // «the client is blind» is not about a way
		{"disk-usage", []string{"all-exits-down", "exit-down-3", "entry-unreachable-external"}, ""},
		{"exit-down-3", nil, ""},
		// the «нестабильно» twin of a muted alert is muted too
		{"exit-path-down-3-hy~unstable", []string{"exit-down-3"}, "exit-down-3"},
	} {
		if got := inhibitedBy(c.key, active(c.active...)); got != c.want {
			t.Errorf("inhibitedBy(%q) with %v active = %q, want %q", c.key, c.active, got, c.want)
		}
	}
}
