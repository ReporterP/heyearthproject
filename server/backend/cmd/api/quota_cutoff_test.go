package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

func TestParseQuotaCutoffEnabled(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false}, // key absent: GetSettingOrDefault returns the "false" default, but "" must be off too
		{"false", false},
		{"FALSE", false},
		{"true", true},
		{"TRUE", true},
		{" true\n", true},
		{"1", false}, // only the literal "true" arms the cutoff, like the other app_settings flags
		{"yes", false},
		{"on", false},
		{"enabled", false},
	}
	for _, tt := range tests {
		if got := parseQuotaCutoffEnabled(tt.in); got != tt.want {
			t.Errorf("parseQuotaCutoffEnabled(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func userSet(ids ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	return m
}

func TestQuotaCutoffExcludes(t *testing.T) {
	// A client has a VLESS UUID AND a user_id; the quota set is keyed by user_id.
	trial := biz.ActiveVlessClient{UUID: "11111111-1111-1111-1111-111111111111", UserID: "trial-user"}
	paid := biz.ActiveVlessClient{UUID: "22222222-2222-2222-2222-222222222222", UserID: "paid-user"}
	server := biz.ActiveVlessClient{UUID: "33333333-3333-3333-3333-333333333333", UserID: "server@heyearth"}
	noID := biz.ActiveVlessClient{UUID: "44444444-4444-4444-4444-444444444444"}

	tests := []struct {
		name   string
		q      quotaCutoff
		client biz.ActiveVlessClient
		want   bool
	}{
		{"armed, over quota: excluded", quotaCutoff{true, userSet("trial-user")}, trial, true},
		{"armed, other user over quota: kept", quotaCutoff{true, userSet("trial-user")}, paid, false},
		{"armed, nobody over quota: kept", quotaCutoff{true, userSet()}, trial, false},
		{"armed, nil set: kept", quotaCutoff{true, nil}, trial, false},
		{"disarmed, over quota: kept (cutoff disabled)", quotaCutoff{false, userSet("trial-user")}, trial, false},
		{"disarmed, nobody over quota: kept", quotaCutoff{false, userSet()}, trial, false},
		{"server admin entry is never in the set: kept", quotaCutoff{true, userSet("trial-user")}, server, false},
		{"empty UserID is never excluded even if the set holds an empty key", quotaCutoff{true, userSet("")}, noID, false},
		{"set keyed by VLESS UUID matches nobody (the old bug)", quotaCutoff{true, userSet(trial.UUID)}, trial, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.excludes(tt.client); got != tt.want {
				t.Errorf("excludes(%+v) = %v, want %v", tt.client, got, tt.want)
			}
		})
	}
}

func TestQuotaCutoffNeedsSync(t *testing.T) {
	tests := []struct {
		name string
		q    quotaCutoff
		want bool
	}{
		{"armed with an offender: sync now", quotaCutoff{true, userSet("a")}, true},
		{"armed, nobody over quota: nothing to change", quotaCutoff{true, userSet()}, false},
		{"disarmed with an offender: a sync would change nothing", quotaCutoff{false, userSet("a", "b")}, false},
		{"disarmed, nobody over quota", quotaCutoff{false, nil}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.needsSync(); got != tt.want {
				t.Errorf("needsSync() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuotaCutoffDisabledNotice(t *testing.T) {
	tests := []struct {
		name string
		q    quotaCutoff
		want string
	}{
		{"disarmed with offenders: one summary line",
			quotaCutoff{false, userSet("a", "b")},
			"[quota] 2 user(s) over quota, cutoff disabled (quota_cutoff_enabled=false)"},
		{"armed: no notice", quotaCutoff{true, userSet("a", "b")}, ""},
		{"disarmed, nobody over quota: no noise", quotaCutoff{false, userSet()}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.disabledNotice(); got != tt.want {
				t.Errorf("disabledNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuotaFlagWatch(t *testing.T) {
	var w quotaFlagWatch
	steps := []struct {
		name    string
		enabled bool
		changed bool
	}{
		{"first observation is a baseline, not a change (off)", false, false},
		{"still off", false, false},
		{"flipped on: request a sync", true, true},
		{"still on", true, false},
		{"flipped off: users must be re-added, request a sync", false, true},
		{"still off again", false, false},
	}
	for _, st := range steps {
		if got := w.observe(st.enabled); got != st.changed {
			t.Errorf("%s: observe(%v) = %v, want %v", st.name, st.enabled, got, st.changed)
		}
	}

	var w2 quotaFlagWatch
	if w2.observe(true) {
		t.Error("first observation (on) must be a baseline, not a change")
	}
	if !w2.observe(false) {
		t.Error("on -> off must be reported as a change")
	}
}

func TestQuotaWarnThrottle(t *testing.T) {
	t0 := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	every := time.Hour
	var th quotaWarnThrottle

	steps := []struct {
		name string
		at   time.Duration
		over map[string]struct{}
		want []string
	}{
		{"first sight: everyone is logged, sorted", 0, userSet("bob", "alice"), []string{"alice", "bob"}},
		{"next minute: silent", time.Minute, userSet("bob", "alice"), nil},
		{"set changed: only the newcomer is logged at once", 30 * time.Minute, userSet("bob", "alice", "carol"), []string{"carol"}},
		{"59 minutes: still silent for everyone", 59 * time.Minute, userSet("bob", "alice", "carol"), nil},
		{"an hour after the first line: alice and bob again (carol not yet)", time.Hour, userSet("bob", "alice", "carol"), []string{"alice", "bob"}},
		{"bob left the set: forgotten", time.Hour + time.Minute, userSet("alice", "carol"), nil},
		{"bob is back within the hour: logged again, he was forgotten", time.Hour + 2*time.Minute, userSet("alice", "carol", "bob"), []string{"bob"}},
		{"nobody over quota: everything forgotten", 2 * time.Hour, userSet(), nil},
		{"alice returns: logged again", 2*time.Hour + time.Minute, userSet("alice"), []string{"alice"}},
	}
	for _, st := range steps {
		got := th.due(st.over, t0.Add(st.at), every)
		if !reflect.DeepEqual(got, st.want) {
			t.Errorf("%s: due() = %v, want %v", st.name, got, st.want)
		}
	}
}
