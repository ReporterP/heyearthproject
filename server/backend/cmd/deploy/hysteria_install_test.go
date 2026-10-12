package main

import (
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// Every install path must splice config.HysteriaInstallScript() verbatim, exactly once,
// as a Sprintf argument / replacer value — never through a format string (that would turn
// its literal %2F into a fmt error marker or leave a double-escaped %%2F).
func TestHysteriaInstallSpliced(t *testing.T) {
	install := config.HysteriaInstallScript()
	tests := []struct {
		name   string
		script string
		want   []string // proves the other Sprintf args kept their positions
	}{
		{"hysteria2HubScript", hysteria2HubScript(), []string{"set -e\n\n# ── 1. Install / upgrade hysteria binary"}},
		{"exitNodeSetupScript", exitNodeSetupScript("203.0.113.7"), []string{
			"set -e\n", `echo "IP: 203.0.113.7"`,
		}},
		{"hubHy2ClientScript", hubHy2ClientScript("203.0.113.7", "5", "s3cret"), []string{
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
			if n := strings.Count(s, install); n != 1 {
				t.Fatalf("install script appears %d times, want 1", n)
			}
			for _, bad := range []string{"v2.6.1", "%!", "%%2F", "__HYSTERIA_INSTALL__", "__VERSION__"} {
				if strings.Contains(s, bad) {
					t.Errorf("script contains %q", bad)
				}
			}
			// The binary must be installed before any hysteria unit is (re)started.
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
