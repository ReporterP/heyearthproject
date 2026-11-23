package main

// Stability phase 0 («Гигиена», docs/plan/stability-operations.md): journal retention, the
// Bamboo log level, the weekly restore drill and Restart=on-failure. Every step is impact
// class К0: none of these scripts may restart or reload a VPN service.

import (
	"regexp"
	"strings"
	"testing"
)

// vpnRestartRe matches a command that would drop users' connections.
var vpnRestartRe = regexp.MustCompile(`systemctl\s+(restart|try-restart|reload|reload-or-restart|stop|kill)\s+[^\n]*(xray|hysteria2-server|hysteria2-client|heyearth-stls|heyearth-tunnel|nginx|heyearth-api)`)

func assertNoVPNRestart(t *testing.T, name, script string) {
	t.Helper()
	if m := vpnRestartRe.FindString(script); m != "" {
		t.Errorf("%s: a К0 script must not touch a VPN service: %q", name, m)
	}
	if strings.Contains(script, "reboot") || strings.Contains(script, "shutdown") {
		t.Errorf("%s: reboot/shutdown in a К0 script", name)
	}
}

// 0.4 — the journal keeps 45 days within 4 GB (it kept ~6 days within 1 GB).
func TestJournaldRetention(t *testing.T) {
	if !strings.Contains(journaldConf, "[Journal]\nSystemMaxUse=4G\nMaxRetentionSec=45day\n") {
		t.Errorf("journald drop-in: %q", journaldConf)
	}
	s := renderJournaldScript()
	if got := payloadFrom(t, s, "/etc/systemd/journald.conf.d/heyearth.conf"); got != journaldConf {
		t.Errorf("journald payload does not round-trip: %q", got)
	}
	for _, want := range []string{
		`if [ "$PUT_CHANGED" = 1 ]; then systemctl restart systemd-journald; fi`,
		"journalctl --vacuum-size=4G --vacuum-time=45d",
		"journalctl --disk-usage",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("journald script lacks %q", want)
		}
	}
	if strings.Contains(s, "vacuum-size=1G") {
		t.Error("the old 1G vacuum would throw away what the new limit keeps")
	}
	if strings.Contains(s, "\nsystemctl restart systemd-journald") {
		t.Error("journald must restart only when its drop-in changed")
	}
	assertNoVPNRestart(t, "journald", s)
	// the full host hygiene step (setup-ops) carries the same journald part
	if !strings.Contains(renderHostHygieneScript(22, ""), "journalctl --vacuum-size=4G --vacuum-time=45d") {
		t.Error("renderHostHygieneScript must use the same journald settings")
	}
}

// 0.5 — hysteria2-server logs errors only (it wrote ~100 000 lines a day with user ids and
// destinations). The unit carries it for a fresh install; the live Hub gets a drop-in that
// takes effect at the next restart — the script itself never restarts the server (К2).
func TestBambooLogLevel(t *testing.T) {
	unit := hysteria2HubScript()
	service := unit[strings.Index(unit, "[Service]"):]
	service = service[:strings.Index(service, "[Install]")]
	if !strings.Contains(service, "\nEnvironment=HYSTERIA_LOG_LEVEL=error\n") {
		t.Errorf("hysteria2-server.service [Service] lacks the log level:\n%s", service)
	}
	if bambooLogLevelDropIn != "# Installed by deploy --setup-hygiene (cmd/deploy/hysteria2_hub.go)\n[Service]\nEnvironment=HYSTERIA_LOG_LEVEL=error\n" {
		t.Errorf("drop-in: %q", bambooLogLevelDropIn)
	}
	s := renderBambooLogLevelScript()
	if got := payloadFrom(t, s, "/etc/systemd/system/hysteria2-server.service.d/10-loglevel.conf"); got != bambooLogLevelDropIn {
		t.Errorf("drop-in payload does not round-trip: %q", got)
	}
	for _, want := range []string{
		`if [ "$PUT_CHANGED" = 1 ]; then systemctl daemon-reload; fi`,
		"после следующего перезапуска hysteria2-server",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("log level script lacks %q", want)
		}
	}
	assertNoVPNRestart(t, "bamboo log level", s)
}

// 0.8 — the restore drill runs by itself every Sunday at 05:00 MSK and leaves its outcome
// where the API can read it.
func TestBackupVerifyTimer(t *testing.T) {
	if backupVerifyOnCalendar != "Sun *-*-* 05:00:00 Europe/Moscow" {
		t.Errorf("calendar: %q", backupVerifyOnCalendar)
	}
	if backupVerifyTimerUnit != "[Unit]\nDescription=Weekly HiEarth Hub backup restore drill, Sunday 05:00 MSK\n\n[Timer]\nOnCalendar=Sun *-*-* 05:00:00 Europe/Moscow\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n" {
		t.Errorf("timer unit: %q", backupVerifyTimerUnit)
	}
	for _, want := range []string{
		"Type=oneshot\n",
		"ExecStart=/opt/heyearth/bin/heyearth-backup-verify.sh\n",
		// %% is a literal % in a unit file; SERVICE_RESULT is set by systemd for ExecStopPost
		`ExecStopPost=/bin/sh -c 'echo "$(date +%%s) $SERVICE_RESULT" > /opt/heyearth/state/backup-verify.status'` + "\n",
		"Nice=10\n", "IOSchedulingClass=idle\n", "TimeoutStartSec=30min\n",
	} {
		if !strings.Contains(backupVerifyServiceUnit, want) {
			t.Errorf("service unit lacks %q:\n%s", want, backupVerifyServiceUnit)
		}
	}
	s := renderBackupVerifyTimerScript()
	for path, content := range map[string]string{
		"/etc/systemd/system/heyearth-backup-verify.service": backupVerifyServiceUnit,
		"/etc/systemd/system/heyearth-backup-verify.timer":   backupVerifyTimerUnit,
	} {
		if got := payloadFrom(t, s, path); got != content {
			t.Errorf("payload of %s does not round-trip", path)
		}
	}
	// what can fail is checked before the first write: a timer that cannot work is not installed
	first := strings.Index(s, "put_file /etc/systemd/system/heyearth-backup-verify.service")
	for _, marker := range []string{`systemd-analyze calendar "` + backupVerifyOnCalendar + `"`, "/opt/heyearth/bin/heyearth-backup-verify.sh"} {
		if i := strings.Index(s, marker); i < 0 || i > first {
			t.Errorf("%q must be checked before the first put_file", marker)
		}
	}
	for _, want := range []string{"systemctl daemon-reload", "systemctl enable --now heyearth-backup-verify.timer", "systemctl list-timers heyearth-backup-verify.timer"} {
		if !strings.Contains(s, want) {
			t.Errorf("install script lacks %q", want)
		}
	}
	// installing the timer does not run the drill (the first run is a separate, announced step)
	if strings.Contains(s, "systemctl start heyearth-backup-verify") {
		t.Error("the install must not start the drill")
	}
	assertNoVPNRestart(t, "backup verify timer", s)
	// the full backup install (setup-ops) installs the same two units
	full := renderBackupInstallScript(backupConfig{port: 22})
	for _, path := range []string{"/etc/systemd/system/heyearth-backup-verify.service", "/etc/systemd/system/heyearth-backup-verify.timer"} {
		payloadFrom(t, full, path)
	}
	if !strings.Contains(full, "systemctl enable --now heyearth-backup-verify.timer") {
		t.Error("setup-ops must enable the verify timer too")
	}
}

// 0.10 — a fallen service is raised by systemd (PRD A4). Only a unit without any restart
// policy gets the drop-in; the services themselves are never restarted.
func TestRestartPolicyScript(t *testing.T) {
	if restartPolicyDropIn != "# Installed by deploy --setup-hygiene (cmd/deploy/ops_hygiene.go)\n[Service]\nRestart=on-failure\nRestartSec=3s\n" {
		t.Errorf("drop-in: %q", restartPolicyDropIn)
	}
	// Review finding: a start limit counts manual restarts too — four `systemctl restart nginx`
	// in ten minutes (a deploy retried over a dropping SSH) would leave nginx down.
	if strings.Contains(restartPolicyDropIn, "StartLimit") {
		t.Error("the drop-in must not set a start limit")
	}
	s := renderRestartPolicyScript()
	for _, want := range []string{
		"xray.service", "hysteria2-server.service", "nginx.service", "heyearth-api.service",
		"'hysteria2-client-*.service'", "'heyearth-stls-client-*.service'", "'heyearth-tunnel*.service'",
		"UNIT_ROOT=/etc/systemd/system\n",
		`if [ "$RP_CHANGED" = 1 ]; then systemctl daemon-reload; fi`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("restart policy script lacks %q", want)
		}
	}
	assertNoVPNRestart(t, "restart policy", s)

	if !haveBash() {
		t.Skip("no working bash")
	}
	// A fake systemctl: nginx has no policy, xray has one, ghost.service is not installed.
	fake := `systemctl() {
  case "$*" in
    "show -p LoadState --value nginx.service"|"show -p LoadState --value xray.service") echo loaded ;;
    "show -p LoadState --value ghost.service") echo not-found ;;
    "show -p Restart --value nginx.service") echo no ;;
    "show -p Restart --value xray.service") echo always ;;
    "show -p Restart --value ghost.service") echo no ;;
    *) echo "UNEXPECTED systemctl $*"; exit 99 ;;
  esac
}
`
	script := `set -eu
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
` + fake + opsPutFileFunc + `UNIT_ROOT="$T"
RP_CHANGED=0
` + restartPolicyFunc + `ensure_restart_policy nginx.service; echo "STATE nginx changed=$RP_CHANGED"
RP_CHANGED=0
ensure_restart_policy xray.service; echo "STATE xray changed=$RP_CHANGED"
ensure_restart_policy ghost.service; echo "STATE ghost changed=$RP_CHANGED"
RP_CHANGED=0
ensure_restart_policy nginx.service; echo "STATE nginx again changed=$RP_CHANGED"
echo "STATE files=$(cd "$T" && find . -type f | sort | tr '\n' ' ')"
echo "STATE body=$(grep -c '^Restart=on-failure$' "$T/nginx.service.d/10-heyearth-restart.conf")"
`
	out, exit := runBash(t, script)
	if exit != 0 || strings.Contains(out, "UNEXPECTED") {
		t.Fatalf("exit %d:\n%s", exit, out)
	}
	for _, l := range []string{
		"STATE nginx changed=1",       // no policy → drop-in written
		"STATE xray changed=0",        // has a policy → untouched
		"STATE ghost changed=0",       // not installed → skipped (systemd reports Restart=no for it)
		"STATE nginx again changed=0", // idempotent
		"STATE files=./nginx.service.d/10-heyearth-restart.conf ",
		"STATE body=1",
	} {
		if !hasLine(out, l) {
			t.Errorf("missing %q:\n%s", l, out)
		}
	}
}

// --setup-hygiene runs the four К0 steps and nothing else.
func TestHygieneSteps(t *testing.T) {
	var names []string
	for _, st := range hygieneSteps() {
		names = append(names, st.name)
		assertNoVPNRestart(t, st.name, st.script)
		if !haveBash() {
			continue
		}
		if out, exit := runBashStdin(t, st.script, "-n"); exit != 0 {
			t.Errorf("%s: bash -n failed (exit %d): %s", st.name, exit, out)
		}
	}
	want := "journald retention, restart on failure, backup restore drill timer, Bamboo log level"
	if got := strings.Join(names, ", "); got != want {
		t.Errorf("hygiene steps = %q, want %q", got, want)
	}
}
