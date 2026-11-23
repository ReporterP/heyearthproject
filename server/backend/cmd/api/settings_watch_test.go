package main

import (
	"reflect"
	"testing"
)

func TestDetectOutside(t *testing.T) {
	explained := func(keys ...string) func(string) bool {
		return func(k string) bool {
			for _, x := range keys {
				if x == k {
					return true
				}
			}
			return false
		}
	}
	prev := map[string]string{"sub_json_dns": "h1", "bamboo_optimise": "h2"}
	if got := detectOutside(prev, map[string]string{"sub_json_dns": "h1", "bamboo_optimise": "h2"}, explained()); len(got) != 0 {
		t.Errorf("same values: %v", got)
	}
	cur := map[string]string{"sub_json_dns": "h9", "bamboo_optimise": "h8"}
	got := detectOutside(prev, cur, explained("sub_json_dns"))
	want := []settingChange{{Key: "bamboo_optimise", Explained: false}, {Key: "sub_json_dns", Explained: true}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("changes %v, want %v", got, want)
	}
	// a key seen for the first time is seeded, not a change
	if got := detectOutside(prev, map[string]string{"sub_json_dns": "h1", "bamboo_optimise": "h2", "alert_email": "h3"}, explained()); len(got) != 0 {
		t.Errorf("first sight: %v", got)
	}
}

func TestWatchedKeysExplainedByActions(t *testing.T) {
	for action, key := range map[string]string{
		"update_entry_mode":     "entry_failover_enabled",
		"hub_address.switch":    "main_server_host",
		"update_alert_settings": "alert_chat_id",
	} {
		if !auditActionExplains(action, key) {
			t.Errorf("%s does not explain %s", action, key)
		}
	}
	if auditActionExplains("update_entry_mode", "bamboo_optimise") {
		t.Error("an unrelated action explains a key")
	}
}
