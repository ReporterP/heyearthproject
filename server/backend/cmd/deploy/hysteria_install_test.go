package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// Every install path must splice its hysteria script verbatim, exactly once, as a Sprintf
// argument / replacer value — never through a format string (that would turn its literal %2F
// into a fmt error marker or leave a double-escaped %%2F). Exit-side paths and the Hub's own
// --setup-hysteria2-hub upgrade (HysteriaInstallScript); the Hub-side step of exit
// provisioning must only fill in a missing binary (HysteriaEnsureScript), or it would upgrade
// the Hub's clients ahead of the other exits.
func TestHysteriaInstallSpliced(t *testing.T) {
	install, ensure := config.HysteriaInstallScript(), config.HysteriaEnsureScript()
	tests := []struct {
		name    string
		script  string
		hubSide bool     // must use ensure, not a bare install
		want    []string // proves the other Sprintf args kept their positions
	}{
		{"hysteria2HubScript", hysteria2HubScript(), false, []string{"set -e\n\n# ── 1. Install / upgrade hysteria binary"}},
		{"exitNodeSetupScript", exitNodeSetupScript("203.0.113.7"), false, []string{
			"set -e\n", `echo "IP: 203.0.113.7"`,
		}},
		{"hubHy2ClientScript", hubHy2ClientScript("203.0.113.7", "5", "s3cret"), true, []string{
			"set -e\n",
			"# Конфиг клиента для exit-ноды 203.0.113.7 (порт 1088+5)",
			"cat > /opt/heyearth/hysteria/client-5.yaml",
			"server: 203.0.113.7:50443",
			"auth: s3cret",
			"PORT=$((1088 + 5))",
			"/etc/systemd/system/hysteria2-client-5.service",
			"Description=Hysteria2 Client for 203.0.113.7 (HeYearth)",
			"systemctl restart hysteria2-client-5\n",
			`echo "Hysteria2 client for 203.0.113.7: port $PORT"`,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.script
			// The ensure script embeds the install script once, so install counts 1 either way.
			if n := strings.Count(s, install); n != 1 {
				t.Fatalf("install script appears %d times, want 1", n)
			}
			wantEnsure := 0
			if tc.hubSide {
				wantEnsure = 1
			}
			if n := strings.Count(s, ensure); n != wantEnsure {
				t.Fatalf("ensure script appears %d times, want %d (hub-side=%v)", n, wantEnsure, tc.hubSide)
			}
			for _, bad := range []string{"v2.6.1", "%!", "%%2F", "__HYSTERIA_INSTALL__", "__VERSION__"} {
				if strings.Contains(s, bad) {
					t.Errorf("script contains %q", bad)
				}
			}
			// The binary must be in place before any hysteria unit is (re)started.
			if i, j := strings.Index(s, install), strings.Index(s, "systemctl restart hysteria2"); j < 0 || i > j {
				t.Errorf("install at %d, first hysteria restart at %d: install must come first", i, j)
			}
			for _, w := range tc.want {
				if !strings.Contains(s, w) {
					t.Errorf("script lacks %q", w)
				}
			}
			if haveBash() {
				if out, code := runBashStdin(t, s, "-n"); code != 0 {
					t.Errorf("bash -n failed (%d): %s", code, out)
				}
			}
		})
	}
}

func TestExitNodeSetupError(t *testing.T) {
	ok := "[xray] Сервис запущен\n[hysteria] installed " + config.HysteriaVersion + " (previous: none -> /usr/local/bin/hysteria.bak)\n[hy2] Hysteria2 сервис запущен\n"
	runErr := errors.New("set -e: Process exited with status 1")
	tests := []struct {
		name   string
		out    string
		runErr error
		wantOK bool
	}{
		{"clean", ok, nil, true},
		{"already current", "[hysteria] " + config.HysteriaVersion + " already installed\n", nil, true},
		{"script failed after install", ok, runErr, false},
		{"stopped before hysteria", "[xray] Сервис запущен\n", runErr, false},
		{"no success line, exit 0", "[xray] Сервис запущен\n", nil, false},
		{"error line in stdout", ok + "[hysteria] ERROR: x\n", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := exitNodeSetupError(tc.out, tc.runErr)
			if (err == nil) != tc.wantOK {
				t.Fatalf("exitNodeSetupError = %v, want ok=%v", err, tc.wantOK)
			}
			if tc.runErr != nil && !errors.Is(err, tc.runErr) {
				t.Errorf("error %v does not wrap the script error", err)
			}
		})
	}
}
