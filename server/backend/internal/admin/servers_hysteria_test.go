package admin

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// Both runSetupExitNode install paths must splice config.HysteriaInstallScript() verbatim,
// exactly once, before anything (re)starts a hysteria unit.
func TestRunSetupExitNodeHysteriaScripts(t *testing.T) {
	install := config.HysteriaInstallScript()
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{"exitHysteria2ServerScript", exitHysteria2ServerScript(), []string{
			"set -e\n" + install,
			"systemctl daemon-reload && systemctl enable hysteria2 && systemctl restart hysteria2",
		}},
		{"hubHy2ClientSetupScript", hubHy2ClientSetupScript("hysteria2-client-7"), []string{
			"set -e\n" + install,
			"systemctl enable hysteria2-client-7\nsystemctl restart hysteria2-client-7\n",
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.script
			if n := strings.Count(s, install); n != 1 {
				t.Fatalf("install script appears %d times, want 1", n)
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
