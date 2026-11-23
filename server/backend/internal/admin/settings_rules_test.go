package admin

import "testing"

// C9: the settings page writes only known keys, each value checked.
func TestNormalizeSetting(t *testing.T) {
	ok := []struct{ key, in, out string }{
		{"quota_cutoff_enabled", "true", "true"},
		{"wallet_min_topup_rub", " 150 ", "150"},
		{"early_renewal_days_threshold", "0", "0"},
		{"receipt_fallback_email", "owner@example.test", "owner@example.test"},
		{"receipt_fallback_email", "", ""},
		{"hub_main_server_host", "example.test", "example.test"},
		{"hub_main_server_host", "203.0.113.1", "203.0.113.1"},
		{"legal_inn", "1234567890", "1234567890"},
		{"legal_inn", "123456789012", "123456789012"},
		{"referral_reward_friend_rub", "0", "0"},
		{"smtp_password", " p@ss ", " p@ss "},
		{"b2b_member_plan", " All-Inclusive ", "all-inclusive"},
		{"b2b_member_plan", "", ""},
		{"xray_revoke_restart", " Night ", "night"},
		{"xray_revoke_restart", "off", "off"},
		{"xray_revoke_restart", "now", "now"},
		{"bamboo_optimise", "off", "off"},
		{"bamboo_optimise", "night", "night"},
		{"alert_email", "owner@example.test", "owner@example.test"},
		{"alert_email", "", ""}, // empty: the second channel is off
	}
	for _, c := range ok {
		got, err := normalizeSetting(c.key, c.in)
		if err != nil || got != c.out {
			t.Errorf("%s=%q: %q %v", c.key, c.in, got, err)
		}
	}
	bad := []struct{ key, in string }{
		{"price_premium", "100"},          // removed
		{"mtproto_secret", "ee00"},        // its own page
		{"alert_telegram_bot_token", "x"}, // its own page
		{"quota_cutoff_enabled", "yes"},
		{"wallet_min_topup_rub", "0"},
		{"wallet_min_topup_rub", "1e3"},
		{"early_renewal_days_threshold", "31"},
		{"receipt_fallback_email", "not an email"},
		{"receipt_fallback_email", "Owner <owner@example.test>"},
		{"hub_main_server_host", "https://example.test"},
		{"hub_main_server_host", "example.test:443"},
		{"legal_inn", "12345"},
		{"smtp_port", "70000"},
		{"smtp_password", "  "},
		{"b2b_member_plan", "полный"},
		{"b2b_member_plan", "all inclusive"},
		{"xray_revoke_restart", ""},
		{"xray_revoke_restart", "always"},
		{"bamboo_optimise", "now"}, // an optimisation never runs by day
		{"alert_email", "not an address"},
	}
	for _, c := range bad {
		if v, err := normalizeSetting(c.key, c.in); err == nil {
			t.Errorf("%s=%q accepted as %q", c.key, c.in, v)
		}
	}
}

// The watchdog's addresses and its bot token are written, never read back: whoever reads the
// settings learns only that they are set.
func TestWatchdogSettingsWriteOnly(t *testing.T) {
	plain, set := redactSettings(map[string]string{
		"watchdog_api_pulse_url": "https://pulse.example.test/ping/0f0f0f0f",
		"watchdog_bot_token":     "123456:AAfake_watchdog_token",
		"support_contact":        "@support",
	})
	for _, key := range []string{"watchdog_api_pulse_url", "watchdog_bot_token"} {
		if _, ok := plain[key]; ok {
			t.Errorf("%s is readable through the settings API", key)
		}
		if !set[key] {
			t.Errorf("%s: secrets_set must say it is set", key)
		}
	}
	if plain["support_contact"] != "@support" {
		t.Errorf("ordinary settings must stay readable: %v", plain)
	}
	if _, set := redactSettings(map[string]string{}); set["watchdog_api_pulse_url"] || set["watchdog_bot_token"] {
		t.Error("absent settings reported as set")
	}
	if v, err := normalizeSetting("watchdog_api_pulse_url", " https://pulse.example.test/ping/0f0f0f0f "); err != nil || v != "https://pulse.example.test/ping/0f0f0f0f" {
		t.Errorf("a good pulse address: %q, %v", v, err)
	}
	for _, bad := range []string{"", "http://pulse.example.test/ping/x", "pulse.example.test", "https://pulse.example.test/ping/a b", "https://pulse.example.test/'x"} {
		if v, err := normalizeSetting("watchdog_api_pulse_url", bad); err == nil {
			t.Errorf("pulse address %q accepted as %q", bad, v)
		}
	}
	if _, err := normalizeSetting("watchdog_bot_token", "123456:AAfake_watchdog_token"); err != nil {
		t.Errorf("bot token: %v", err)
	}
}
