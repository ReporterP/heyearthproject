package admin

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// Both runSetupExitNode install paths must splice their hysteria script verbatim, exactly
// once, before anything (re)starts a hysteria unit. The exit side upgrades
// (HysteriaInstallScript); the Hub side only fills in a missing binary (HysteriaEnsureScript),
// or it would upgrade the Hub's clients ahead of the other exits.
func TestRunSetupExitNodeHysteriaScripts(t *testing.T) {
	install, ensure := config.HysteriaInstallScript(), config.HysteriaEnsureScript()
	tests := []struct {
		name    string
		script  string
		hubSide bool
		want    []string
	}{
		{"exitHysteria2ServerScript", exitHysteria2ServerScript("203.0.113.5"), false, []string{
			"set -e\n" + install,
			"systemctl daemon-reload && systemctl enable hysteria2 && systemctl restart hysteria2",
		}},
		{"hubHy2ClientSetupScript", hubHy2ClientSetupScript("hysteria2-client-7"), true, []string{
			"set -e\n" + ensure,
			"systemctl enable hysteria2-client-7\nsystemctl restart hysteria2-client-7\n",
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
			for _, bad := range []string{"v2.6.1", "%!", "%%2F"} {
				if strings.Contains(s, bad) {
					t.Errorf("script contains %q", bad)
				}
			}
			if i, j := strings.Index(s, install), strings.Index(s, "systemctl restart hysteria2"); j < 0 || i > j {
				t.Errorf("install at %d, first hysteria restart at %d: install must come first", i, j)
			}
			for _, w := range tc.want {
				if !strings.Contains(s, w) {
					t.Errorf("script lacks %q", w)
				}
			}
			// Syntax check only (-n parses, never executes).
			if out, err := exec.Command("bash", "-c", "echo ok").Output(); err == nil && strings.TrimSpace(string(out)) == "ok" {
				cmd := exec.Command("bash", "-n")
				cmd.Stdin = strings.NewReader(s)
				var buf bytes.Buffer
				cmd.Stdout, cmd.Stderr = &buf, &buf
				if err := cmd.Run(); err != nil {
					t.Errorf("bash -n: %v: %s", err, buf.String())
				}
			}
		})
	}
}
