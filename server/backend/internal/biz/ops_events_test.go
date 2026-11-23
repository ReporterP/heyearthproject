package biz

import (
	"context"
	"strings"
	"testing"
)

func TestScrubOpsText(t *testing.T) {
	in := "exit 203.0.113.7 user 11111111-2222-3333-4444-555555555555 bot123:AAE"
	got := ScrubOpsText(in)
	for _, secret := range []string{"203.0.113.7", "11111111-2222-3333-4444-555555555555", "2222-3333", "bot123:AAE", "AAE"} {
		if strings.Contains(got, secret) {
			t.Errorf("ScrubOpsText(%q) = %q, still carries %q", in, got, secret)
		}
	}
	// a user stays recognisable by the first 8 characters of the identifier
	if !strings.Contains(got, "11111111…") {
		t.Errorf("ScrubOpsText(%q) = %q, want the user as 11111111…", in, got)
	}
	for in, want := range map[string]string{
		"xray перезапущен: structural (inbounds)":            "xray перезапущен: structural (inbounds)",
		"сборка abc123def456, Xray 26.3.27":                  "сборка abc123def456, Xray 26.3.27", // three numbers are a version, not an address
		"вход 198.51.100.20:443 не отвечает":                 "вход <адрес>:443 не отвечает",
		"token 123456789:AAfake_Fake-fake_FAKE-fake_fake01234": "token <токен>",
		"https://api.telegram.org/bot42:XyZ_-9/sendMessage":  "https://api.telegram.org/<токен>/sendMessage",
		"": "",
	} {
		if got := ScrubOpsText(in); got != want {
			t.Errorf("ScrubOpsText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecordOpsEventRejectsBadImpact(t *testing.T) {
	err := RecordOpsEvent(context.Background(), OpsEvent{Kind: "restart", Actor: "system", Target: "xray", Summary: "x", Impact: "K9", OK: true})
	if err == nil || !strings.Contains(err.Error(), "impact") {
		t.Fatalf("Impact K9: err = %v, want an impact error", err)
	}
}

func TestRecordOpsEventValidates(t *testing.T) {
	for name, e := range map[string]OpsEvent{
		"unknown kind":  {Kind: "reboot", Summary: "x"},
		"empty kind":    {Summary: "x"},
		"empty summary": {Kind: "note", Summary: "  "},
	} {
		if err := RecordOpsEvent(context.Background(), e); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
