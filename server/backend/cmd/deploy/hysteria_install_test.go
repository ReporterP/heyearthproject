package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/config"
)

// Every download in the Hub script (hysteria binary, geoip.dat, the monthly geoip cron) must
// be bounded: a stalled GitHub connection must not hang the deploy or the cron forever.
func TestHysteria2HubScriptCurlTimeouts(t *testing.T) {
	n := 0
	for _, line := range strings.Split(hysteria2HubScript(), "\n") {
		if !strings.Contains(line, "curl -") {
			continue
		}
		n++
		if !strings.Contains(line, "--connect-timeout 15") || !strings.Contains(line, "--max-time") || !strings.Contains(line, "--retry 3") {
			t.Errorf("curl without timeouts/retries: %s", strings.TrimSpace(line))
		}
	}
	if n != 3 { // hysteria binary, geoip.dat, cron geoip.dat
		t.Errorf("found %d curl lines, want 3", n)
	}
	if c := strings.Count(hysteria2HubScript(), "--connect-timeout 15 --max-time 120 --retry 3 --retry-delay 2"); c != 2 {
		t.Errorf("geoip curl timeouts appear %d times, want 2 (setup + monthly cron)", c)
	}
}

// fakeHubHysteria mimics `hysteria version`; mode tells the systemctl stub how
// hysteria2-server behaves on this binary: "GOOD" runs steadily, "FLAPPY" is up at every
// instant but crash-loops (NRestarts grows between samples), "BAD" fails to start.
func fakeHubHysteria(version, mode string) string {
	return "#!/bin/sh\nprintf 'banner\\n\\nVersion:\\t" + version + "\\nLibVersion:\\tv0.4.5\\n'\n# " + mode + "\n"
}

// hubRun is one run of the Hub script as far as the rollback logic is concerned: step 1 left
// HY_REPLACED, then hysteria2HubMarkPending runs; an aborted run stops there (geoip download,
// SSH drop, ...), a full run continues with steps 8-9 (hysteria2HubStartScript).
type hubRun struct {
	replaced string
	abort    bool
}

// TestHysteria2HubStartScriptRollback runs the pending marker + steps 8-9 of the Hub script in
// a real bash with /usr/local/bin/ rewritten to a temp ./bin/ and systemctl/journalctl/ss/sleep
// stubbed. The systemctl stub serves `show -p NRestarts --value` and decides the service's
// fate from the mode baked into ./bin/hysteria (see fakeHubHysteria).
func TestHysteria2HubStartScriptRollback(t *testing.T) {
	if !haveBash() {
		t.Skip("no working bash")
	}
	const stubs = `systemctl() {
  echo "systemctl $*" >> ./systemctl.log
  case "$1" in
    enable) return 0 ;;
    restart)
      if grep -q -e GOOD -e FLAPPY ./bin/hysteria 2>/dev/null; then touch ./active; return 0; fi
      rm -f ./active; return 1 ;;
    is-active) [ -f ./active ] ;;
    show)
      [ "$*" = "show -p NRestarts --value hysteria2-server" ] || { echo "unexpected: systemctl $*" >&2; return 1; }
      n=$(cat ./nrestarts 2>/dev/null || echo 0); echo "$n"
      if grep -q FLAPPY ./bin/hysteria 2>/dev/null; then echo $((n + 1)) > ./nrestarts; fi ;;
    *) return 0 ;;
  esac
}
journalctl() { echo "journal: $*"; }
ss() { echo "UNCONN 0 0 *:50443 *:*"; }
sleep() { echo "sleep $*" >> ./sleep.log; }
`
	rewrite := func(s string) string {
		s = strings.ReplaceAll(s, "/usr/local/bin/", "./bin/")
		if strings.Contains(s, "/usr/local/bin") {
			t.Fatal("path rewrite incomplete; refusing to run against the real /usr/local/bin")
		}
		return s
	}
	mark, start := rewrite(hysteria2HubMarkPending), rewrite(hysteria2HubStartScript)
	want := config.HysteriaVersion
	newGood, newBad, newFlappy := fakeHubHysteria(want, "GOOD"), fakeHubHysteria(want, "BAD"), fakeHubHysteria(want, "FLAPPY")
	oldGood, oldBad := fakeHubHysteria("v2.6.1", "GOOD"), fakeHubHysteria("v2.6.1", "BAD")
	rolledBackOK := "[hysteria2-hub] ROLLED BACK to previous binary v2.6.1 (failed: " + want + "); hysteria2-server is active"

	tests := []struct {
		name         string
		bin, bak     string // bin/hysteria (as left by step 1), bin/hysteria.bak; "" = absent
		runs         []hubRun
		wantOK       bool // of the last run
		wantBin      string
		wantPending  bool
		wantRestarts int // over all runs
		wantRollback bool
		wantOut      string // in the last run's output
	}{
		{name: "healthy upgrade clears pending", bin: newGood, bak: oldGood, runs: []hubRun{{replaced: "1"}},
			wantOK: true, wantBin: newGood, wantRestarts: 1, wantOut: "hysteria2-server is active (NRestarts 0, stable)"},
		{name: "broken upgrade is rolled back", bin: newBad, bak: oldGood, runs: []hubRun{{replaced: "1"}},
			wantBin: oldGood, wantRestarts: 2, wantRollback: true, wantOut: rolledBackOK},
		{name: "dies after the first sample: rolled back", bin: newFlappy, bak: oldGood, runs: []hubRun{{replaced: "1"}},
			wantBin: oldGood, wantRestarts: 2, wantRollback: true, wantOut: "not running steadily (NRestarts 0 -> 1)"},
		{name: "rollback that does not help still fails", bin: newBad, bak: oldBad, runs: []hubRun{{replaced: "1"}},
			wantBin: oldBad, wantRestarts: 2, wantRollback: true, wantOut: "hysteria2-server is NOT active"},
		// I1: HY_REPLACED dies with the aborted run; the pending marker carries it to the retry.
		{name: "abort after replace, then retry: still rolled back", bin: newBad, bak: oldGood,
			runs:    []hubRun{{replaced: "1", abort: true}, {replaced: "0"}},
			wantBin: oldGood, wantRestarts: 2, wantRollback: true, wantOut: rolledBackOK},
		{name: "abort after replace, retry with a working binary clears pending", bin: newGood, bak: oldGood,
			runs:   []hubRun{{replaced: "1", abort: true}, {replaced: "0"}},
			wantOK: true, wantBin: newGood, wantRestarts: 1, wantOut: "stable"},
		{name: "nothing replaced or pending: no rollback", bin: newBad, bak: oldGood, runs: []hubRun{{replaced: "0"}},
			wantBin: newBad, wantRestarts: 1, wantOut: "ERROR: hysteria2-server is not running steadily"},
		{name: "no bak: no rollback, stays pending", bin: newBad, runs: []hubRun{{replaced: "1"}},
			wantBin: newBad, wantPending: true, wantRestarts: 1, wantOut: "ERROR: hysteria2-server is not running steadily"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, content := range map[string]string{"hysteria": tc.bin, "hysteria.bak": tc.bak} {
				if content != "" {
					if err := os.WriteFile(filepath.Join(bin, name), []byte(content), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			pending := filepath.Join(bin, "hysteria.pending")

			var got string
			var err error
			for i, r := range tc.runs {
				body := mark + start + "echo AFTER-START\n"
				if r.abort {
					body = mark + "exit 1\n"
				}
				cmd := exec.Command("bash", "-s")
				cmd.Dir = dir
				cmd.Stdin = strings.NewReader("set -e\nHY_WANT=\"" + want + "\"\nHY_REPLACED=" + r.replaced + "\n" + stubs + body)
				var out bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &out
				err, got = cmd.Run(), out.String()
				if r.abort {
					if _, statErr := os.Stat(pending); r.replaced == "1" && statErr != nil {
						t.Fatalf("run %d replaced the binary and aborted, but left no pending marker", i+1)
					}
				}
			}

			if ok := err == nil; ok != tc.wantOK {
				t.Fatalf("exit ok=%v, want %v (err %v)\n%s", ok, tc.wantOK, err, got)
			}
			if after := strings.Contains(got, "AFTER-START"); after != tc.wantOK {
				t.Errorf("script continued past the health check=%v, want %v\n%s", after, tc.wantOK, got)
			}
			if !strings.Contains(got, tc.wantOut) {
				t.Errorf("output lacks %q:\n%s", tc.wantOut, got)
			}
			if rolled := strings.Contains(got, "ROLLED BACK"); rolled != tc.wantRollback {
				t.Errorf("rolled back=%v, want %v\n%s", rolled, tc.wantRollback, got)
			}
			if b, _ := os.ReadFile(filepath.Join(bin, "hysteria")); string(b) != tc.wantBin {
				t.Errorf("bin/hysteria after run:\n%s\nwant:\n%s", b, tc.wantBin)
			}
			if b, _ := os.ReadFile(filepath.Join(bin, "hysteria.bak")); string(b) != tc.bak {
				t.Error("bin/hysteria.bak changed; the rollback must copy it, not move it")
			}
			if _, statErr := os.Stat(pending); (statErr == nil) != tc.wantPending {
				t.Errorf("hysteria.pending present=%v, want %v", statErr == nil, tc.wantPending)
			}
			if _, statErr := os.Stat(filepath.Join(bin, "hysteria.rollback.tmp")); statErr == nil {
				t.Error("bin/hysteria.rollback.tmp left behind")
			}
			log, _ := os.ReadFile(filepath.Join(dir, "systemctl.log"))
			if n := strings.Count(string(log), "systemctl restart hysteria2-server"); n != tc.wantRestarts {
				t.Errorf("restarts=%d, want %d\n%s", n, tc.wantRestarts, log)
			}
			// Two samples 3 s + 6 s after every restart.
			sleeps, _ := os.ReadFile(filepath.Join(dir, "sleep.log"))
			if s := strings.Repeat("sleep 3\nsleep 6\n", tc.wantRestarts); string(sleeps) != s {
				t.Errorf("sleep pattern %q, want %q", sleeps, s)
			}
		})
	}
}

func TestHysteria2HubSetupError(t *testing.T) {
	runErr := errors.New("set -e: Process exited with status 1")
	tests := []struct {
		name   string
		out    string
		runErr error
		wantOK bool
	}{
		{"clean", "[hysteria2-hub] hysteria2-server is active (NRestarts 0, stable)\n[hysteria2-hub] Setup complete.\n", nil, true},
		{"script failed", "[hysteria2-hub] ERROR: /etc/hysteria/server.yaml not generated by heyearth-api.\n", runErr, false},
		{"rolled back", "[hysteria2-hub] ROLLED BACK to previous binary v2.6.1 (failed: v2.13.0); hysteria2-server is active\n", runErr, false},
		{"rolled back, exit status lost", "[hysteria2-hub] ROLLED BACK to previous binary v2.6.1 (failed: v2.13.0); hysteria2-server is active\n", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := hysteria2HubSetupError(tc.out, tc.runErr)
			if (err == nil) != tc.wantOK {
				t.Fatalf("hysteria2HubSetupError = %v, want ok=%v", err, tc.wantOK)
			}
			if tc.runErr != nil && !errors.Is(err, tc.runErr) {
				t.Errorf("error %v does not wrap the script error", err)
			}
		})
	}
}

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
		{"hysteria2HubScript", hysteria2HubScript(), false, []string{
			"set -e\n\n# ── 1. Install / upgrade hysteria binary",
			// The pending marker follows the install directly, before any step that can abort.
			install + "\n# A replaced binary stays pending until step 9 passes, so a retry can still roll it back.\n" +
				hysteria2HubMarkPending + "\n# ── 2. System user",
			"chmod 640 /etc/hysteria/server.yaml\n\n" + hysteria2HubStartScript + "\n" + `echo "[hysteria2-hub] Setup complete."`,
		}},
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
			"if ! systemctl is-active --quiet hysteria2-client-5; then\n",
			"hysteria2-client-5 не активен после рестарта (см. journalctl -u hysteria2-client-5)",
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
			for _, bad := range []string{"v2.6.1", "%!", "%%2F", "__HYSTERIA_INSTALL__", "__VERSION__", "__START__", "__MARK_PENDING__"} {
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
