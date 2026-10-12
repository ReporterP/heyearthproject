package main

import (
	"testing"

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

func set(ids ...string) map[string]struct{} {
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
		{"armed, over quota: excluded", quotaCutoff{true, set("trial-user")}, trial, true},
		{"armed, other user over quota: kept", quotaCutoff{true, set("trial-user")}, paid, false},
		{"armed, nobody over quota: kept", quotaCutoff{true, set()}, trial, false},
		{"armed, nil set: kept", quotaCutoff{true, nil}, trial, false},
		{"disarmed, over quota: kept (cutoff disabled)", quotaCutoff{false, set("trial-user")}, trial, false},
		{"disarmed, nobody over quota: kept", quotaCutoff{false, set()}, trial, false},
		{"server admin entry is never in the set: kept", quotaCutoff{true, set("trial-user")}, server, false},
		{"empty UserID is never excluded even if the set holds an empty key", quotaCutoff{true, set("")}, noID, false},
		{"set keyed by VLESS UUID matches nobody (the old bug)", quotaCutoff{true, set(trial.UUID)}, trial, false},
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
		{"armed with an offender: sync now", quotaCutoff{true, set("a")}, true},
		{"armed, nobody over quota: nothing to change", quotaCutoff{true, set()}, false},
		{"disarmed with an offender: a sync would change nothing", quotaCutoff{false, set("a", "b")}, false},
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
			quotaCutoff{false, set("a", "b")},
			"[quota] 2 user(s) over quota, cutoff disabled (quota_cutoff_enabled=false)"},
		{"armed: no notice", quotaCutoff{true, set("a", "b")}, ""},
		{"disarmed, nobody over quota: no noise", quotaCutoff{false, set()}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.disabledNotice(); got != tt.want {
				t.Errorf("disabledNotice() = %q, want %q", got, tt.want)
			}
		})
	}
}
