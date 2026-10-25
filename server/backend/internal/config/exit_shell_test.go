package config

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// leftoverAbs finds an absolute /opt/heyearth or /etc path that survived shellRun's rewrite.
var leftoverAbs = regexp.MustCompile(`(^|[^.\w])/(opt/heyearth|etc)(/|\b)`)

// shellRun runs prelude+script in a real bash in dir, with /opt/heyearth/ and /etc/ rewritten to
// ./opt/heyearth/ and ./etc/ (relative to dir) and the restart check's sleeps zeroed; it refuses
// to run when an absolute path survived the rewrite. Returns stdout+stderr.
func shellRun(t *testing.T, dir, prelude, script string, env ...string) (string, error) {
	t.Helper()
	return shellRunArgv(t, dir, []string{"bash", "-s"}, prelude, script, env...)
}

// shellRunArgv is shellRun with the shell started as argv (e.g. inside `unshare -rn`).
func shellRunArgv(t *testing.T, dir string, argv []string, prelude, script string, env ...string) (string, error) {
	t.Helper()
	s := strings.NewReplacer("/opt/heyearth/", "./opt/heyearth/", "/etc/", "./etc/").Replace(script)
	if m := leftoverAbs.FindString(s); m != "" {
		t.Fatalf("path rewrite incomplete (%q); refusing to run against the real filesystem", m)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"RESTART_CHECK_SLEEP=0", "RESTART_CHECK_SLEEP2=0"}, env...)...)
	cmd.Stdin = strings.NewReader(prelude + s)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// systemctl stub: ./active.<unit> = running; restart appends the unit to ./restarts; FLAP=1 makes
// NRestarts grow between the two samples; DEAD=1 keeps units down after restart; RESTART_FAILS=1
// makes `systemctl restart` itself fail; disable records whether the unit file still existed at
// that moment. enable (logged) creates ./enabled.<unit>, which is-enabled reports; SubState is
// ./substate.<unit> ("running" without it). Everything else is logged to ./calls.
const systemctlStub = `systemctl() {
  case "$1" in
    restart) echo "$2" >> ./restarts; [ "${RESTART_FAILS:-0}" = 1 ] && return 1; [ "${DEAD:-0}" = 1 ] || touch "./active.$2" ;;
    is-active) [ -f "./active.${@: -1}" ] ;;
    is-enabled) if [ -f "./enabled.${@: -1}" ]; then echo enabled; else echo disabled; return 1; fi ;;
    enable) echo "$*" >> ./calls; touch "./enabled.${@: -1}" ;;
    show) if [ "$3" = SubState ]; then cat "./substate.${@: -1}" 2>/dev/null || echo running; return 0; fi
      n=$(cat "./nr.${@: -1}" 2>/dev/null || echo 0); echo "$n"; [ "${FLAP:-0}" = 1 ] && echo $((n+1)) > "./nr.${@: -1}"; true ;;
    disable) u=${@: -1}; if [ -f "./etc/systemd/system/${u%.service}.service" ]; then uf=present; else uf=absent; fi
      echo "disable $* unitfile=$uf" >> ./calls; rm -f "./active.${@: -1}" ;;
    *) echo "$*" >> ./calls ;;
  esac
}
journalctl() { echo "journalctl $*" >> ./calls; }
`

// count returns how many lines of dir/file equal line.
func count(dir, file, line string) int {
	b, _ := os.ReadFile(filepath.Join(dir, file))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if l == line {
			n++
		}
	}
	return n
}

func exists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	return err == nil
}

func mustWrite(t *testing.T, dir, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readRel(dir, rel string) string {
	b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	return string(b)
}

func rel(abs string) string { return strings.TrimPrefix(abs, "/") }

func TestRestartCheckedTwoSamples(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	for _, tc := range []struct {
		name, env string
		ok        bool
		out       string
	}{
		{"stable", "X=1", true, "работает стабильно"},
		{"not started", "DEAD=1", false, "не запустился"},
		{"restart command fails", "RESTART_FAILS=1", false, "не запустился"},
		{"crash loop", "FLAP=1", false, "перезапускается сам"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			out, err := shellRun(t, dir, systemctlStub, "set -e\n"+RestartCheckFunc+"restart_checked xray\necho AFTER\n", tc.env)
			if (err == nil) != tc.ok || !strings.Contains(out, tc.out) || strings.Contains(out, "AFTER") != tc.ok {
				t.Fatalf("err=%v out=%s", err, out)
			}
			if count(dir, "restarts", "xray") != 1 {
				t.Fatalf("want exactly one restart:\n%s", readRel(dir, "restarts"))
			}
			// A failed start shows the unit's journal to the owner.
			if journal := strings.Contains(readRel(dir, "calls"), "journalctl -u xray"); journal == tc.ok {
				t.Errorf("journal shown=%v for ok=%v", journal, tc.ok)
			}
		})
	}
}

func TestUnitConvergeRestartsOnlyOnMarkerOrDown(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	run := func(env ...string) (string, error) {
		return shellRun(t, dir, systemctlStub, UnitConvergeScript("xray", ""), env...)
	}
	mustRun := func() {
		t.Helper()
		if out, err := run(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	marker, binMarker := rel(RestartMarker("xray")), rel(BinaryPendingMarker("xray"))
	mustRun() // down → started
	if count(dir, "restarts", "xray") != 1 {
		t.Fatal("a stopped unit must be started")
	}
	if count(dir, "calls", "enable --quiet xray") != 1 || count(dir, "calls", "daemon-reload") != 1 {
		t.Fatalf("unit must be enabled after a daemon-reload:\n%s", readRel(dir, "calls"))
	}
	mustRun() // up, no marker → untouched
	if count(dir, "restarts", "xray") != 1 {
		t.Fatal("an unchanged running unit was restarted")
	}
	mustWrite(t, dir, marker, "", 0o644)
	mustWrite(t, dir, binMarker, ExitXrayBinary+"\n", 0o644)
	mustRun() // marker (written but not restarted before an SSH drop) → exactly one restart, markers gone
	if count(dir, "restarts", "xray") != 2 {
		t.Fatal("the pending marker must trigger a restart")
	}
	if exists(dir, marker) || exists(dir, binMarker) {
		t.Fatal("marker left after a successful restart")
	}
	// A restart that does not come up fails the step and keeps both markers: the files on disk are
	// still not confirmed running (the next converge restarts again, the rollback sees the binary).
	mustWrite(t, dir, marker, "", 0o644)
	mustWrite(t, dir, binMarker, ExitXrayBinary+"\n", 0o644)
	os.Remove(filepath.Join(dir, "active.xray")) // it crashed
	if out, err := run("DEAD=1"); err == nil || !strings.Contains(out, "не запустился") {
		t.Fatalf("a failed restart must fail the converge: %v\n%s", err, out)
	}
	if !exists(dir, marker) || !exists(dir, binMarker) {
		t.Fatal("markers removed although the restart failed")
	}
}

// A failing extra (installer, config test) stops the converge before any restart.
func TestUnitConvergeExtraFailureStopsBeforeRestart(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	mustWrite(t, dir, rel(RestartMarker("xray")), "", 0o644)
	out, err := shellRun(t, dir, systemctlStub, UnitConvergeScript("xray", "echo extra-ran\nfalse\n"))
	if err == nil || !strings.Contains(out, "extra-ran") || exists(dir, "restarts") {
		t.Fatalf("err=%v restarts=%q\n%s", err, readRel(dir, "restarts"), out)
	}
}

func TestUnitRollbackRestoresOnlyChangedFiles(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	cfg, dropIn := rel(ExitXrayConfig), "/etc/systemd/system/xray.service.d/override.conf"
	mustWrite(t, dir, cfg, "new", 0o600)
	mustWrite(t, dir, cfg+".bak", "old", 0o600)
	mustWrite(t, dir, "opt/heyearth/xray/other.json", "untouched-new", 0o600)
	mustWrite(t, dir, "opt/heyearth/xray/other.json.bak", "untouched-old", 0o600)
	mustWrite(t, dir, rel(ExitXrayUnitPath), "new-unit", 0o644)
	mustWrite(t, dir, rel(ExitXrayUnitPath)+".bak", "old-unit", 0o644)
	mustWrite(t, dir, rel(dropIn), "fresh", 0o644)
	mustWrite(t, dir, rel(RestartMarker("xray")), "", 0o644)
	script := UnitRollbackScript("xray", []string{ExitXrayConfig, ExitXrayUnitPath, dropIn})
	out, err := shellRun(t, dir, systemctlStub, script)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if readRel(dir, cfg) != "old" || readRel(dir, rel(ExitXrayUnitPath)) != "old-unit" {
		t.Fatalf("config.json = %q, unit = %q, want the .bak", readRel(dir, cfg), readRel(dir, rel(ExitXrayUnitPath)))
	}
	if readRel(dir, "opt/heyearth/xray/other.json") != "untouched-new" {
		t.Fatal("a file not changed in this run was rolled back")
	}
	if exists(dir, rel(dropIn)) {
		t.Fatal("a file that had no previous version must be removed")
	}
	if count(dir, "restarts", "xray") != 1 || !strings.Contains(out, "возвращена прежняя версия") || exists(dir, rel(RestartMarker("xray"))) ||
		strings.Contains(readRel(dir, "calls"), "disable") {
		t.Fatalf("restored unit must be restarted once (not disabled) and its marker cleared:\n%s", out)
	}
	// Re-running the same rollback is harmless (the .bak is kept, the result is the same).
	if out, err := shellRun(t, dir, systemctlStub, script); err != nil || readRel(dir, cfg) != "old" {
		t.Fatalf("second rollback: %v %q\n%s", err, readRel(dir, cfg), out)
	}
}

// The binary goes back only when an installer replaced it and that was never confirmed
// (BinaryPendingMarker). Exit 2 keeps hysteria 2.6.1 as hysteria.bak next to 2.13.0: a rollback
// of a config change there must never downgrade the binary.
func TestUnitRollbackBinaryOnlyWhenPending(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	newSHA := sha256Hex(fakeHysteria(HysteriaVersion))
	setup := func(pending bool) string {
		dir := t.TempDir()
		mustWrite(t, dir, "bin/hysteria", fakeHysteria(HysteriaVersion), 0o755)
		mustWrite(t, dir, "bin/hysteria.bak", fakeHysteria("v2.6.1"), 0o755)
		mustWrite(t, dir, rel(ExitHysteriaConfig), "new", 0o600)
		mustWrite(t, dir, rel(ExitHysteriaConfig)+".bak", "old", 0o600)
		mustWrite(t, dir, rel(ExitHysteriaUnitPath), ExitHysteriaUnit, 0o644)
		mustWrite(t, dir, rel(RestartMarker("hysteria2")), "", 0o644)
		if pending {
			mustWrite(t, dir, rel(BinaryPendingMarker("hysteria2")), newSHA+"  ./bin/hysteria\n", 0o644)
		}
		return dir
	}
	script := UnitRollbackScript("hysteria2", []string{ExitHysteriaConfig})

	dir := setup(false)
	if out, err := shellRun(t, dir, systemctlStub, script); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if v := binVersion(t, filepath.Join(dir, "bin/hysteria")); v != HysteriaVersion {
		t.Fatalf("binary %s after a config rollback without a pending binary: the old .bak was restored", v)
	}
	if readRel(dir, rel(ExitHysteriaConfig)) != "old" || count(dir, "restarts", "hysteria2") != 1 {
		t.Fatal("config not restored / not restarted")
	}

	dir = setup(true)
	out, err := shellRun(t, dir, systemctlStub, script)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if v := binVersion(t, filepath.Join(dir, "bin/hysteria")); v != "v2.6.1" {
		t.Fatalf("pending binary not rolled back: %s\n%s", v, out)
	}
	if binVersion(t, filepath.Join(dir, "bin/hysteria.bak")) != "v2.6.1" || exists(dir, rel(BinaryPendingMarker("hysteria2"))) {
		t.Fatal("the .bak must stay and the pending marker must be consumed")
	}
	if exists(dir, "bin/hysteria.rollback.tmp") || count(dir, "restarts", "hysteria2") != 1 {
		t.Fatal("temp left behind or not restarted exactly once")
	}

	// Pending binary only (an SSH drop lost the in-memory list of changed files): still restored.
	dir = setup(true)
	if out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("hysteria2", nil)); err != nil ||
		binVersion(t, filepath.Join(dir, "bin/hysteria")) != "v2.6.1" || count(dir, "restarts", "hysteria2") != 1 {
		t.Fatalf("pending binary with no changed files: %v\n%s", err, out)
	}

	// Marker written but the replacement never happened (download failed / interrupted before the
	// mv): the binary on disk is not the recorded one, so its .bak is not taken; marker consumed.
	dir = setup(false)
	mustWrite(t, dir, "bin/hysteria", fakeHysteria("v2.12.0"), 0o755)
	mustWrite(t, dir, rel(BinaryPendingMarker("hysteria2")), newSHA+"  ./bin/hysteria\n", 0o644)
	if out, err := shellRun(t, dir, systemctlStub, script); err != nil || binVersion(t, filepath.Join(dir, "bin/hysteria")) != "v2.12.0" ||
		exists(dir, rel(BinaryPendingMarker("hysteria2"))) {
		t.Fatalf("binary that was never replaced got rolled back to its .bak: %v\n%s", err, out)
	}
}

// Review: a live unit whose run only added files (a drop-in) is restarted without them, never
// disabled; a fresh install is disabled while its unit file still exists, then removed.
func TestUnitRollbackLiveUnitVsFreshInstall(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dropIn := "/etc/systemd/system/xray.service.d/override.conf"
	dir := t.TempDir()
	mustWrite(t, dir, rel(ExitXrayUnitPath), ExitXrayUnit, 0o644) // installed long ago, unchanged
	mustWrite(t, dir, rel(dropIn), "[Service]\nLimitNOFILE=1\n", 0o644)
	mustWrite(t, dir, "active.xray", "", 0o644)
	out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", []string{dropIn}))
	if err != nil || !strings.Contains(out, "возвращена прежняя версия") {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(dir, rel(dropIn)) || !exists(dir, rel(ExitXrayUnitPath)) || strings.Contains(readRel(dir, "calls"), "disable") ||
		count(dir, "restarts", "xray") != 1 || !exists(dir, "active.xray") {
		t.Fatalf("live unit disabled or not restarted without the drop-in:\n%s", readRel(dir, "calls"))
	}

	dir = t.TempDir()
	mustWrite(t, dir, rel(ExitXrayConfig), "new", 0o600)
	mustWrite(t, dir, rel(ExitXrayUnitPath), "new", 0o644)
	mustWrite(t, dir, rel(ExitXrayGuardUnitPath), ExitXrayGuardUnit, 0o644)
	mustWrite(t, dir, "active.xray", "", 0o644)
	if out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", []string{ExitXrayConfig, ExitXrayUnitPath, ExitXrayGuardUnitPath})); err != nil ||
		!strings.Contains(out, "новая установка снята") {
		t.Fatalf("%v\n%s", err, out)
	}
	// Review: every new unit of the run (the guard too) is disabled while its file still exists.
	for _, w := range []string{"disable --now xray unitfile=present", "disable --now heyearth-exit-guard.service unitfile=present"} {
		if !strings.Contains(readRel(dir, "calls"), w) {
			t.Fatalf("want %q before the files are removed:\n%s", w, readRel(dir, "calls"))
		}
	}
	if exists(dir, rel(ExitXrayGuardUnitPath)) || exists(dir, rel(ExitXrayUnitPath)) {
		t.Fatal("new unit files left behind")
	}

	// A live xray whose run added the guard unit: the guard is disabled and removed, xray restarted.
	dir = t.TempDir()
	mustWrite(t, dir, rel(ExitXrayUnitPath), ExitXrayUnit, 0o644)
	mustWrite(t, dir, rel(ExitXrayGuardUnitPath), ExitXrayGuardUnit, 0o644)
	mustWrite(t, dir, "active.xray", "", 0o644)
	if out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", []string{ExitXrayGuardUnitPath})); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := readRel(dir, "calls")
	if !strings.Contains(calls, "disable --now heyearth-exit-guard.service unitfile=present") || strings.Contains(calls, "disable --now xray") ||
		exists(dir, rel(ExitXrayGuardUnitPath)) || count(dir, "restarts", "xray") != 1 {
		t.Fatalf("new guard unit not disabled before removal, or xray disabled / not restarted:\n%s", calls)
	}
}

// Review: a rollback in a resumed job (empty in-memory list) still undoes config and unit from
// the writes recorded in the restart marker — but only those that really happened.
func TestUnitRollbackFromMarkerAfterResume(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	newCfg, newUnit := `{"new":1}`, "[Unit]\nnew\n"
	mustWrite(t, dir, rel(ExitXrayConfig), newCfg, 0o600)
	mustWrite(t, dir, rel(ExitXrayConfig)+".bak", "old-cfg", 0o600)
	mustWrite(t, dir, rel(ExitXrayUnitPath), newUnit, 0o644)
	mustWrite(t, dir, rel(ExitXrayUnitPath)+".bak", "old-unit", 0o644)
	mustWrite(t, dir, "opt/heyearth/xray/resolv.conf", "current", 0o644) // the write recorded for it never happened
	mustWrite(t, dir, "opt/heyearth/xray/resolv.conf.bak", "older", 0o644)
	mustWrite(t, dir, "active.xray", "", 0o644)
	mark := MarkRestartScript("xray", []PendingWrite{
		{ExitXrayConfig, sha256Hex(newCfg)}, {ExitXrayUnitPath, sha256Hex(newUnit)},
		{ExitXrayResolvConfPath, sha256Hex("never written")}})
	for i := 0; i < 2; i++ { // marked twice (a retried step): entries are not duplicated
		if out, err := shellRun(t, dir, systemctlStub, mark); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	if n := strings.Count(readRel(dir, rel(RestartMarker("xray"))), "\n"); n != 3 {
		t.Fatalf("marker has %d lines:\n%s", n, readRel(dir, rel(RestartMarker("xray"))))
	}
	out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", nil))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if readRel(dir, rel(ExitXrayConfig)) != "old-cfg" || readRel(dir, rel(ExitXrayUnitPath)) != "old-unit" {
		t.Fatalf("resumed rollback did not restore config/unit:\n%s", out)
	}
	if readRel(dir, "opt/heyearth/xray/resolv.conf") != "current" {
		t.Fatal("a recorded write that never happened was rolled back to an older .bak")
	}
	if count(dir, "restarts", "xray") != 1 || exists(dir, rel(RestartMarker("xray"))) {
		t.Fatalf("want one restart and the marker cleared:\n%s", out)
	}
}

// A restored nftables file of ours is loaded into the kernel again — in one transaction that
// drops the table first (a hand-made previous file has no `delete table`) — before the unit
// restarts; a rule file that was new in the run is removed and its table left loaded; a failing
// load fails the rollback.
func TestUnitRollbackReloadsRestoredNftRules(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const nftStub = `nft() { echo "nft $*" >> ./restarts; [ "$2" = - ] && cat >> ./nft.stdin; [ ! -f ./nft-fails ]; }
`
	const handMade = "table inet heyearth_agent_guard {\n  chain input {\n    tcp dport 50052 ip saddr != { 138.16.181.225 } drop\n  }\n}\n"
	setup := func() string {
		dir := t.TempDir()
		mustWrite(t, dir, rel(ExitAgentGuardConfPath), "new rules", 0o644)
		mustWrite(t, dir, rel(ExitAgentGuardConfPath)+".bak", handMade, 0o644)
		mustWrite(t, dir, rel(NodeAgentUnitPath), "new unit", 0o644)
		mustWrite(t, dir, rel(NodeAgentUnitPath)+".bak", "old unit", 0o644)
		mustWrite(t, dir, rel(NodeAgentCACert), "new ca", 0o644) // not a rule file: no load for it
		mustWrite(t, dir, rel(NodeAgentCACert)+".bak", "old ca", 0o644)
		mustWrite(t, dir, "active.node-agent", "", 0o644)
		return dir
	}
	dir := setup()
	out, err := shellRun(t, dir, systemctlStub+nftStub, UnitRollbackScript("node-agent", []string{ExitAgentGuardConfPath, NodeAgentUnitPath, NodeAgentCACert}))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := "table inet heyearth_agent_guard\ndelete table inet heyearth_agent_guard\n" + handMade
	if got := readRel(dir, "nft.stdin"); got != want || readRel(dir, rel(ExitAgentGuardConfPath)) != handMade {
		t.Fatalf("loaded:\n%q\nwant\n%q", got, want)
	}
	if got := readRel(dir, "restarts"); got != "nft -f -\nnode-agent\n" {
		t.Fatalf("the previous rules must be in force before node-agent restarts:\n%s", got)
	}

	// Resumed rollback (no in-memory list): the hop file restored from the marker is reloaded too.
	dir = t.TempDir()
	mustWrite(t, dir, rel(ExitPortHopConfPath), ExitPortHopConf, 0o644)
	mustWrite(t, dir, rel(ExitPortHopConfPath)+".bak", "old hop", 0o644)
	mustWrite(t, dir, rel(ExitHysteriaUnitPath), ExitHysteriaUnit, 0o644)
	mustWrite(t, dir, rel(RestartMarker("hysteria2")), sha256Hex(ExitPortHopConf)+"  ./etc/nftables-hysteria.conf\n", 0o644)
	if out, err := shellRun(t, dir, systemctlStub+nftStub, UnitRollbackScript("hysteria2", nil)); err != nil ||
		readRel(dir, "nft.stdin") != "table inet hysteria_porthopping\ndelete table inet hysteria_porthopping\nold hop" {
		t.Fatalf("resumed: %v %q\n%s", err, readRel(dir, "nft.stdin"), out)
	}

	// A guard new in this run: removed with its unit, its table stays (it only ever restricts).
	dir = t.TempDir()
	mustWrite(t, dir, rel(ExitXrayGuardConfPath), "new guard", 0o644)
	mustWrite(t, dir, rel(ExitXrayUnitPath), "new unit", 0o644)
	mustWrite(t, dir, rel(ExitXrayUnitPath)+".bak", "old unit", 0o644)
	if out, err := shellRun(t, dir, systemctlStub+nftStub, UnitRollbackScript("xray", []string{ExitXrayGuardConfPath, ExitXrayUnitPath})); err != nil ||
		exists(dir, rel(ExitXrayGuardConfPath)) || exists(dir, "nft.stdin") {
		t.Fatalf("new guard: %v\n%s", err, out)
	}

	// The previous rules cannot be loaded: the rollback fails (the step reports it), nothing restarted.
	dir = setup()
	mustWrite(t, dir, "nft-fails", "", 0o644)
	if out, err := shellRun(t, dir, systemctlStub+nftStub, UnitRollbackScript("node-agent", []string{ExitAgentGuardConfPath})); err == nil ||
		count(dir, "restarts", "node-agent") != 0 {
		t.Fatalf("a failed load must fail the rollback: %v\n%s", err, out)
	}
}

func TestUnitRollbackFreshInstallAndNothingChanged(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	// A fresh install (no file had a previous version): files removed, unit disabled, not restarted.
	dir := t.TempDir()
	mustWrite(t, dir, rel(ExitXrayConfig), "new", 0o600)
	mustWrite(t, dir, rel(ExitXrayUnitPath), "new", 0o644)
	mustWrite(t, dir, "active.xray", "", 0o644)
	mustWrite(t, dir, rel(RestartMarker("xray")), "", 0o644)
	out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", []string{ExitXrayConfig, ExitXrayUnitPath}))
	if err != nil || !strings.Contains(out, "новая установка снята") {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(dir, rel(ExitXrayConfig)) || exists(dir, rel(ExitXrayUnitPath)) || exists(dir, "active.xray") ||
		exists(dir, "restarts") || exists(dir, rel(RestartMarker("xray"))) {
		t.Fatalf("fresh install not removed cleanly:\n%s", readRel(dir, "calls"))
	}

	// Nothing changed in this run (e.g. resumed after an SSH drop): a running service is never
	// disabled or restarted, and the restart marker stays for the next converge.
	dir = t.TempDir()
	mustWrite(t, dir, "active.xray", "", 0o644)
	mustWrite(t, dir, rel(RestartMarker("xray")), "", 0o644)
	out, err = shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", nil))
	if err != nil || !strings.Contains(out, "возвращать нечего") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !exists(dir, "active.xray") || exists(dir, "restarts") || strings.Contains(readRel(dir, "calls"), "disable") ||
		!exists(dir, rel(RestartMarker("xray"))) {
		t.Fatalf("an untouched unit was changed by the rollback:\n%s", readRel(dir, "calls"))
	}
}

func TestUnitStateAndMarkScripts(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	state := func() string {
		out, err := shellRun(t, dir, systemctlStub, UnitStateScript("xray"))
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return out
	}
	if s := state(); s != "ACTIVE=0\nENABLED=0\nRETRY=0\nMARKER=0\n" {
		t.Fatalf("%q", s)
	}
	if out, err := shellRun(t, dir, systemctlStub, MarkRestartScript("xray", []PendingWrite{{"/opt/heyearth/a b", sha256Hex("x")},
		{"/opt/heyearth/c", "not-a-sha'; touch ./pwned"}})); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	mustWrite(t, dir, "active.xray", "", 0o644)
	mustWrite(t, dir, "enabled.xray", "", 0o644)
	if s := state(); s != "ACTIVE=1\nENABLED=1\nRETRY=0\nMARKER=1\n" {
		t.Fatalf("%q", s)
	}
	// The pause of a crash loop: not active, systemd will start it again.
	os.Remove(filepath.Join(dir, "active.xray"))
	mustWrite(t, dir, "substate.xray", "auto-restart\n", 0o644)
	if s := state(); s != "ACTIVE=0\nENABLED=1\nRETRY=1\nMARKER=1\n" {
		t.Fatalf("%q", s)
	}
	if got := readRel(dir, rel(RestartMarker("xray"))); got != sha256Hex("x")+"  ./opt/heyearth/a b\n" || exists(dir, "pwned") {
		t.Fatalf("marker content %q (an entry without a valid sha256 must be dropped)", got)
	}
}

func TestSumsAndCatScripts(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "opt/heyearth/a.conf", "alpha\n", 0o600)
	mustWrite(t, dir, "etc/b c.conf", "beta", 0o644)
	out, err := shellRun(t, dir, "", SumsScript("/opt/heyearth/a.conf", "/etc/b c.conf", "/opt/heyearth/missing"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// "<sha256> <mode>  <path as given>" per existing file (the path is the rewritten one here;
	// the mode as this filesystem keeps it — Git Bash on NTFS reports 644 for both).
	want := sha256Hex("alpha\n") + " " + statMode(t, dir, "opt/heyearth/a.conf") + "  ./opt/heyearth/a.conf\n" +
		sha256Hex("beta") + " " + statMode(t, dir, "etc/b c.conf") + "  ./etc/b c.conf\n"
	if out != want {
		t.Fatalf("sums:\n%q\nwant\n%q", out, want)
	}
	if runtime.GOOS != "windows" && !strings.Contains(out, sha256Hex("alpha\n")+" 600  ") {
		t.Fatalf("the mode must be reported: %q", out)
	}
	if out, err := shellRun(t, dir, "", SumsScript()); err != nil || out != "" {
		t.Fatalf("no paths: %v %q", err, out)
	}
	if out, err := shellRun(t, dir, "", CatScript("/opt/heyearth/a.conf")); err != nil || out != "alpha\n" {
		t.Fatalf("cat: %v %q", err, out)
	}
	if out, err := shellRun(t, dir, "", CatScript("/opt/heyearth/missing")); err != nil || out != "" {
		t.Fatalf("cat of a missing file: %v %q", err, out)
	}
}

// KeyPublicScript gives the key's public half (openssl on the node), nothing for a missing key,
// NO_OPENSSL without openssl — never the private key.
func TestKeyPublicScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("no openssl")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	mustWrite(t, dir, "opt/heyearth/certs/server-key.pem", string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), 0o600)
	out, err := shellRun(t, dir, "", KeyPublicScript("/opt/heyearth/certs/server-key.pem"))
	b, _ := pem.Decode([]byte(out))
	want, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil || b == nil || b.Type != "PUBLIC KEY" || !bytes.Equal(b.Bytes, want) || strings.Contains(out, "PRIVATE") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := shellRun(t, dir, "", KeyPublicScript("/opt/heyearth/certs/missing.pem")); err != nil || out != "" {
		t.Fatalf("missing key: %v %q", err, out)
	}
	if out, err := shellRun(t, dir, "", KeyPublicScript("/opt/heyearth/certs/server-key.pem"), "PATH=/nonexistent"); err != nil || out != "NO_OPENSSL\n" {
		t.Fatalf("no openssl: %v %q", err, out)
	}
}

// A restored rule file is loaded with the real nft, in a throwaway network namespace (unshare
// -rn: an unprivileged user + network namespace with its own empty ruleset; the host's rules are
// never touched — Linux only): the run's new rules are in the kernel, the rollback puts the
// previous file back (hand-made, without `delete table`) and the kernel then holds exactly the
// previous rules, before the unit restarts.
func TestUnitRollbackNftReloadInNetns(t *testing.T) {
	if runtime.GOOS != "linux" || !haveBash() {
		t.Skip("needs Linux and bash")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("no nft")
	}
	if out, err := exec.Command("unshare", "-rn", "nft", "list", "ruleset").CombinedOutput(); err != nil {
		t.Skipf("no unprivileged network namespace with nft: %v %s", err, out)
	}
	newGuard, err := ExitAgentGuardConf([]string{"203.0.113.9"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		unit, conf, table, newRules, previous, want, gone string
	}{
		"agent guard": {"node-agent", ExitAgentGuardConfPath, ExitAgentGuardTable, newGuard,
			"table inet heyearth_agent_guard {\n  chain input {\n    type filter hook input priority 0; policy accept;\n" +
				"    tcp dport 50052 ip saddr != { 138.16.181.225, 127.0.0.1 } drop\n  }\n}\n",
			"tcp dport 50052 ip saddr != { 127.0.0.1, 138.16.181.225 } drop", "203.0.113.9"},
		"port hop": {"hysteria2", ExitPortHopConfPath, ExitPortHopTable, ExitPortHopConf,
			"table inet hysteria_porthopping {\n  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n" +
				"    udp dport 20000-50000 redirect to :50443\n  }\n}\n",
			"udp dport 20000-50000 redirect to :50443", "fib daddr"},
	} {
		dir := t.TempDir()
		mustWrite(t, dir, rel(c.conf), c.newRules, 0o644)
		mustWrite(t, dir, rel(c.conf)+".bak", c.previous, 0o644)
		mustWrite(t, dir, rel(unitFilePath(c.unit)), "[Unit]\n", 0o644)
		mustWrite(t, dir, "active."+c.unit, "", 0o644)
		// In the namespace: the run's rules loaded, the ruleset recorded when the rollback calls
		// systemctl restart (the reload must be done by then) and at the end.
		prelude := systemctlStub + "nft -f ./" + rel(c.conf) + "\n" +
			`eval "$(declare -f systemctl | sed '1s/systemctl/rb_systemctl/')"` + "\n" +
			`systemctl() { [ "$1" = restart ] && nft list ruleset > ./ruleset.at-restart; rb_systemctl "$@"; }` + "\n" +
			"trap 'nft list ruleset > ./ruleset' EXIT\n"
		out, err := shellRunArgv(t, dir, []string{"unshare", "-rn", "bash", "-s"}, prelude, UnitRollbackScript(c.unit, []string{c.conf}))
		final, atRestart := readRel(dir, "ruleset"), readRel(dir, "ruleset.at-restart")
		if err != nil || final != atRestart || !strings.Contains(final, c.want) || strings.Contains(final, c.gone) ||
			strings.Count(final, "table inet "+c.table) != 1 || count(dir, "restarts", c.unit) != 1 {
			t.Fatalf("%s: %v\n%s\nruleset at restart:\n%s\nfinal:\n%s", name, err, out, atRestart, final)
		}
	}
}

// statMode is the permission bits of dir/rel as the test host's stat reports them.
func statMode(t *testing.T, dir, rel string) string {
	t.Helper()
	out, err := exec.Command("bash", "-c", `stat -L -c %a "$1"`, "_", filepath.Join(dir, filepath.FromSlash(rel))).Output()
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	return strings.TrimSpace(string(out))
}

// Before a private file is replaced, it and its .bak lose their group/other bits (an exit set up
// by hand kept keys world-readable; PutFile's .bak would keep them so); missing files are fine.
func TestPrivateModeScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	if runtime.GOOS == "windows" {
		t.Skip("NTFS under Git Bash keeps no group/other bits")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "opt/heyearth/state/k.json", "k", 0o644)
	mustWrite(t, dir, "opt/heyearth/state/k.json.bak", "old", 0o664)
	mustWrite(t, dir, "opt/heyearth/x.conf", "x", 0o640)
	out, err := shellRun(t, dir, "", PrivateModeScript("/opt/heyearth/state/k.json", "/opt/heyearth/x.conf", "/opt/heyearth/missing"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for rel, want := range map[string]string{"opt/heyearth/state/k.json": "600", "opt/heyearth/state/k.json.bak": "600", "opt/heyearth/x.conf": "600"} {
		if got := statMode(t, dir, rel); got != want {
			t.Errorf("%s: mode %s, want %s", rel, got, want)
		}
	}
	if readRel(dir, "opt/heyearth/state/k.json") != "k" || readRel(dir, "opt/heyearth/state/k.json.bak") != "old" {
		t.Error("the content must not change")
	}
}

// The Hub's hysteria client converge (Review Focus 3): restarted exactly when UnitConvergeScript
// would, the markers gone after the restart, but a client that does not hold (UDP to the exit
// choked: it crash-loops) only reports HEALTH=0 instead of failing; a failing extra still fails.
func TestUnitConvergeSoft(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const u = "hysteria2-client-7"
	marker, binMarker := rel(RestartMarker(u)), rel(BinaryPendingMarker(u))
	run := func(dir, extra string, env ...string) (string, error) {
		return shellRun(t, dir, systemctlStub, UnitConvergeSoftScript(u, extra), env...)
	}
	for _, tc := range []struct {
		name, env, health string
	}{{"stable", "X=1", "HEALTH=1\n"}, {"crash loop", "FLAP=1", "HEALTH=0\n"}, {"never up", "DEAD=1", "HEALTH=0\n"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWrite(t, dir, marker, "", 0o644)
			mustWrite(t, dir, binMarker, "", 0o644)
			if tc.env != "DEAD=1" {
				mustWrite(t, dir, "active."+u, "", 0o644)
			}
			out, err := run(dir, "", tc.env)
			if err != nil || !strings.Contains(out, tc.health) || count(dir, "restarts", u) != 1 {
				t.Fatalf("err=%v restarts=%q\n%s", err, readRel(dir, "restarts"), out)
			}
			if exists(dir, marker) || exists(dir, binMarker) {
				t.Fatal("the restart happened: the markers must go")
			}
			if count(dir, "calls", "enable --quiet "+u) != 1 || count(dir, "calls", "reset-failed "+u) != 1 {
				t.Fatalf("enable / reset-failed before the restart:\n%s", readRel(dir, "calls"))
			}
		})
	}
	// Running, no marker: untouched, no HEALTH line.
	dir := t.TempDir()
	mustWrite(t, dir, "active."+u, "", 0o644)
	if out, err := run(dir, ""); err != nil || strings.Contains(out, "HEALTH=") || exists(dir, "restarts") {
		t.Fatalf("%v\n%s", err, out)
	}
	// A failing extra (the hysteria install) fails it before any restart, the marker kept.
	mustWrite(t, dir, marker, "", 0o644)
	if out, err := run(dir, "echo extra-ran\nfalse\n"); err == nil || !strings.Contains(out, "extra-ran") || exists(dir, "restarts") || !exists(dir, marker) {
		t.Fatalf("err=%v\n%s", err, out)
	}
}

// ufw stub: status is localized unless LC_ALL=C (as on a Russian-locale VPS); an active ufw lists
// ./ufw.listed (pre-existing rules) and the rules added through the stub in `ufw status` format.
const fwStubs = `unset LC_ALL
ufw() { echo "ufw $*" >> ./fw.log
  case "$1" in
  status)
    if [ "${UFW_ACTIVE:-0}" != 1 ]; then if [ "$LC_ALL" = C ]; then echo "Status: inactive"; else echo "Состояние: неактивен"; fi; return 0; fi
    if [ "$LC_ALL" = C ]; then echo "Status: active"; else echo "Состояние: активен"; fi
    printf '\nTo                         Action      From\n--                         ------      ----\n'
    cat ./ufw.listed 2>/dev/null
    awk '$1=="allow" && $2 ~ /\// {printf "%-26s ALLOW IN    Anywhere\n", $2} $1=="allow" && $2=="from" {printf "%-26s ALLOW IN    %s\n", $7"/"$9, $3}' ./ufw.rules 2>/dev/null; true ;;
  allow) grep -qxF "$*" ./ufw.rules 2>/dev/null || echo "$*" >> ./ufw.rules ;; esac; }
iptables() { echo "iptables $*" >> ./fw.log
  case "$1" in -S) echo "-P INPUT ${IPT_POLICY:-ACCEPT}"; cat ./ipt.rules 2>/dev/null ;;
  -C) shift; grep -qxF -- "-A $*" ./ipt.rules 2>/dev/null ;; -I) shift; echo "-A $*" >> ./ipt.rules ;; esac; }
nft() { echo "nft $*" >> ./fw.log; cat ./nft.ruleset 2>/dev/null; }
netfilter-persistent() { echo "netfilter-persistent $*" >> ./fw.log; [ "${NFP_OK:-1}" = 1 ]; }
`

func TestExitFirewallScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	script := ExitFirewallScript(2222, []string{"138.16.181.225", "bad;id"}, 8443)
	t.Run("ufw active: rules added, never enabled, no duplicates", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < 2; i++ {
			if out, err := shellRun(t, dir, fwStubs, script, "UFW_ACTIVE=1"); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		}
		for _, r := range []string{"allow from 138.16.181.225 to any port 2222 proto tcp", "allow 10443/tcp", "allow 8443/tcp", "allow 50443/udp",
			"allow 20000:50000/udp", "allow from 138.16.181.225 to any port 50052 proto tcp"} {
			if count(dir, "ufw.rules", r) != 1 {
				t.Errorf("rule %q present %d times", r, count(dir, "ufw.rules", r))
			}
		}
		if count(dir, "ufw.rules", "allow 2222/tcp") != 0 {
			t.Error("SSH allowed from anywhere")
		}
		if b := readRel(dir, "fw.log"); strings.Contains(b, "enable") || strings.Contains(b, "bad;id") || strings.Contains(b, "iptables -I") {
			t.Errorf("ufw enabled, an invalid hub address passed through or iptables touched:\n%s", b)
		}
	})
	t.Run("firewall off: nothing touched", func(t *testing.T) {
		dir := t.TempDir()
		out, err := shellRun(t, dir, fwStubs, script)
		if err != nil || !strings.Contains(out, "фаервол выключен") || strings.Contains(out, "ВНИМАНИЕ") {
			t.Fatalf("%v\n%s", err, out)
		}
		if exists(dir, "ipt.rules") || exists(dir, "ufw.rules") {
			t.Error("rules added while the firewall is off")
		}
	})
	t.Run("iptables DROP policy: rules inserted once", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < 2; i++ {
			out, err := shellRun(t, dir, fwStubs, script, "IPT_POLICY=DROP")
			if err != nil || strings.Contains(out, "ВНИМАНИЕ") {
				t.Fatalf("%v\n%s", err, out)
			}
		}
		if count(dir, "fw.log", "netfilter-persistent save") != 2 {
			t.Errorf("rules not persisted:\n%s", readRel(dir, "fw.log"))
		}
		if out, err := shellRun(t, dir, fwStubs, script, "IPT_POLICY=DROP", "NFP_OK=0"); err != nil || !strings.Contains(out, "пропадут после перезагрузки") {
			t.Errorf("a failed save must be reported: %v\n%s", err, out)
		}
		if count(dir, "ipt.rules", "-A INPUT -p tcp --dport 2222 -j ACCEPT") != 0 {
			t.Error("SSH allowed from anywhere")
		}
		for _, r := range []string{"-A INPUT -p tcp -s 138.16.181.225 --dport 2222 -j ACCEPT", "-A INPUT -p tcp --dport 10443 -j ACCEPT",
			"-A INPUT -p tcp --dport 8443 -j ACCEPT", "-A INPUT -p udp --dport 50443 -j ACCEPT",
			"-A INPUT -p udp --dport 20000:50000 -j ACCEPT", "-A INPUT -p tcp -s 138.16.181.225 --dport 50052 -j ACCEPT"} {
			if n := count(dir, "ipt.rules", r); n != 1 {
				t.Errorf("%q %d times", r, n)
			}
		}
	})
	t.Run("ipv6 hub: ufw only, iptables skips it", func(t *testing.T) {
		s := ExitFirewallScript(22, []string{"2001:db8::1", "138.16.181.225", " 138.16.181.225 "}, 0)
		dir := t.TempDir()
		if out, err := shellRun(t, dir, fwStubs, s, "UFW_ACTIVE=1"); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if count(dir, "ufw.rules", "allow from 2001:db8::1 to any port 50052 proto tcp") != 1 ||
			count(dir, "ufw.rules", "allow from 138.16.181.225 to any port 50052 proto tcp") != 1 ||
			count(dir, "ufw.rules", "allow from 2001:db8::1 to any port 22 proto tcp") != 1 ||
			count(dir, "ufw.rules", "allow from 138.16.181.225 to any port 22 proto tcp") != 1 {
			t.Errorf("ufw rules:\n%s", readRel(dir, "ufw.rules"))
		}
		dir = t.TempDir()
		if out, err := shellRun(t, dir, fwStubs, s, "IPT_POLICY=DROP"); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if strings.Contains(readRel(dir, "ipt.rules"), "2001:db8::1") || count(dir, "ipt.rules", "-A INPUT -p tcp -s 138.16.181.225 --dport 50052 -j ACCEPT") != 1 ||
			count(dir, "ipt.rules", "-A INPUT -p tcp -s 138.16.181.225 --dport 22 -j ACCEPT") != 1 {
			t.Errorf("iptables rules:\n%s", readRel(dir, "ipt.rules"))
		}
	})
	t.Run("no Hub addresses: SSH not touched", func(t *testing.T) {
		s := ExitFirewallScript(2222, nil, 0)
		for _, env := range []string{"UFW_ACTIVE=1", "IPT_POLICY=DROP"} {
			dir := t.TempDir()
			out, err := shellRun(t, dir, fwStubs, s, env)
			if err != nil || !strings.Contains(out, "адреса Hub не заданы") || strings.Contains(readRel(dir, "ufw.rules")+readRel(dir, "ipt.rules"), "2222") {
				t.Errorf("%s: %v\n%s\n%s%s", env, err, out, readRel(dir, "ufw.rules"), readRel(dir, "ipt.rules"))
			}
		}
	})
	t.Run("invalid ssh port and no MTProxy are left out", func(t *testing.T) {
		dir := t.TempDir()
		if out, err := shellRun(t, dir, fwStubs, ExitFirewallScript(0, nil, 0), "UFW_ACTIVE=1"); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if got := readRel(dir, "ufw.rules"); got != "allow 10443/tcp\nallow 50443/udp\nallow 20000:50000/udp\n" {
			t.Errorf("ufw rules:\n%s", got)
		}
	})
	t.Run("native nftables input filter: warned, not touched", func(t *testing.T) {
		dir := t.TempDir()
		mustWrite(t, dir, "nft.ruleset", "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n}\n", 0o644)
		out, err := shellRun(t, dir, fwStubs, script)
		if err != nil || !strings.Contains(out, "ВНИМАНИЕ") || !strings.Contains(out, "50052") {
			t.Fatalf("%v\n%s", err, out)
		}
		if exists(dir, "ipt.rules") || exists(dir, "ufw.rules") {
			t.Error("rules added to a firewall we do not manage")
		}
	})
	// Review: `ufw allow 22/tcp` would turn an existing `limit` into `allow` ("Rule updated").
	t.Run("ufw: any existing rule on the SSH port is kept", func(t *testing.T) {
		for name, tc := range map[string]struct {
			port   int
			listed string
			keep   bool
		}{
			"limit":             {2222, "2222/tcp                   LIMIT IN    Anywhere\n2222/tcp (v6)              LIMIT IN    Anywhere (v6)\n", true},
			"app profile":       {22, "22/tcp (OpenSSH)           ALLOW IN    Anywhere\n", true},
			"address and port":  {2222, "10.0.0.5 2222/tcp          DENY IN     203.0.113.9\n", true},
			"port list":         {2222, "80,2222/tcp                ALLOW IN    Anywhere\n", true},
			"other port only":   {2222, "22222/tcp                  ALLOW IN    Anywhere\n", false},
			"no rules (fresh)":  {2222, "", false},
			"rule from address": {2222, "Anywhere                   ALLOW IN    2222\n", false},
			// Review: rules that do not name the port cannot be recognised — the allow that is then
			// added must come from the Hub only, never from anywhere.
			"app profile by name only": {22, "OpenSSH                    ALLOW IN    Anywhere\n", false},
			"network allow":            {2222, "Anywhere                   ALLOW IN    10.0.0.0/8\n", false},
		} {
			dir := t.TempDir()
			mustWrite(t, dir, "ufw.listed", tc.listed, 0o644)
			out, err := shellRun(t, dir, fwStubs, ExitFirewallScript(tc.port, []string{"138.16.181.225"}, 0), "UFW_ACTIVE=1")
			if err != nil {
				t.Fatalf("%s: %v\n%s", name, err, out)
			}
			added := count(dir, "ufw.rules", fmt.Sprintf("allow from 138.16.181.225 to any port %d proto tcp", tc.port)) == 1
			if added == tc.keep || strings.Contains(out, "уже есть правило") != tc.keep {
				t.Errorf("%s: ssh rule added=%v, want %v\n%s", name, added, !tc.keep, out)
			}
			if strings.Contains(readRel(dir, "fw.log"), fmt.Sprintf("ufw allow %d/tcp", tc.port)) || strings.Contains(readRel(dir, "fw.log"), "delete") {
				t.Errorf("%s: SSH allowed from anywhere or an existing rule changed:\n%s", name, readRel(dir, "fw.log"))
			}
			if count(dir, "ufw.rules", "allow 10443/tcp") != 1 || count(dir, "ufw.rules", "allow from 138.16.181.225 to any port 50052 proto tcp") != 1 {
				t.Errorf("%s: tunnel / node-agent rules missing:\n%s", name, readRel(dir, "ufw.rules"))
			}
		}
	})
	// Review: an iptables ACCEPT inserted on top would bypass fail2ban / -m recent drops.
	t.Run("iptables: existing rules on the SSH port are kept", func(t *testing.T) {
		for _, tc := range []struct {
			existing string
			named    bool // the rule names the port: SSH left alone; otherwise an allow from the Hub only
		}{
			{"-A INPUT -p tcp -m tcp --dport 2222 -m recent --name ssh --update --seconds 60 --hitcount 4 -j DROP", true},
			{"-A INPUT -p tcp -m multiport --dports 80,2222 -j f2b-sshd", true},
			{"-A INPUT -s 10.0.0.0/8 -j ACCEPT", false},
			{"-A INPUT -j sshguard", false},
		} {
			dir := t.TempDir()
			mustWrite(t, dir, "ipt.rules", tc.existing+"\n", 0o644)
			out, err := shellRun(t, dir, fwStubs, script, "IPT_POLICY=DROP")
			if err != nil || strings.Contains(out, "уже есть правила") != tc.named {
				t.Fatalf("%q: %v\n%s", tc.existing, err, out)
			}
			fromHub := count(dir, "ipt.rules", "-A INPUT -p tcp -s 138.16.181.225 --dport 2222 -j ACCEPT")
			if strings.Contains(readRel(dir, "ipt.rules"), "-A INPUT -p tcp --dport 2222 -j ACCEPT") || count(dir, "ipt.rules", tc.existing) != 1 ||
				(fromHub == 1) == tc.named || count(dir, "ipt.rules", "-A INPUT -p tcp --dport 10443 -j ACCEPT") != 1 {
				t.Errorf("%q: ssh protection bypassed, ssh allowed from anywhere or tunnel rule missing:\n%s", tc.existing, readRel(dir, "ipt.rules"))
			}
		}
	})
	// Review: on a Russian-locale VPS `ufw status` says «Состояние: активен».
	t.Run("ufw asked in the C locale", func(t *testing.T) {
		if !strings.Contains(script, "LC_ALL=C ufw status 2>/dev/null | grep -q '^Status: active'") {
			t.Fatal("ufw status must be read with LC_ALL=C")
		}
		dir := t.TempDir()
		if out, err := shellRun(t, dir, fwStubs, script, "UFW_ACTIVE=1"); err != nil || !strings.Contains(out, "ufw включён") {
			t.Fatalf("localized active ufw not detected: %v\n%s", err, out)
		}
	})
}

// fakeXray mimics `xray version` (first line "Xray <v> (...)") and `xray run -test`, which fails
// when ./xray-test-fails exists.
func fakeXray(v string) string {
	return "#!/bin/sh\ncase \"$1\" in\n  version) echo 'Xray " + v + " (Xray, Penetrates Everything.) 0123abc (go1.26.1 linux/amd64)'; echo 'A unified platform for anti-censorship.' ;;\n" +
		"  run) if [ -f ./xray-test-fails ]; then echo 'Failed to start: main: failed to load config files'; exit 23; fi; echo 'Configuration OK.' ;;\nesac\n"
}

// curl/unzip stubs for the xray installer: curl "downloads" ./download (fails like curl -f when
// absent) and records its args; unzip extracts ./payload as xray plus two release data files.
const xrayDownloadStubs = `curl() { printf '%s\n' "$*" >> ./curl.args; out=""; while [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift 2;; *) shift;; esac; done; [ -f ./download ] || return 22; cp ./download "$out"; }
unzip() { echo "unzip $*" >> ./unzip.args; d=""; while [ $# -gt 0 ]; do case "$1" in -d) d="$2"; shift 2;; *) shift;; esac; done; cp ./payload "$d/xray"; echo geoip-new > "$d/geoip.dat"; echo geosite-new > "$d/geosite.dat"; }
`

func xrayVersionOf(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	out, _ := exec.Command("bash", path, "version").Output()
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return "?"
	}
	return f[1]
}

func TestXrayExitPinned(t *testing.T) {
	if XrayExitVersion != "v26.3.27" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(XrayExitSHA256) {
		t.Fatalf("pin %s %s", XrayExitVersion, XrayExitSHA256)
	}
	// SHA2-256 of Xray-linux-64.zip, from the release's Xray-linux-64.zip.dgst; re-checked by
	// downloading the asset (2026-10-15).
	if XrayExitSHA256 != "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae" {
		t.Fatal("XrayExitSHA256 changed without updating this test")
	}
	if u := XrayExitDownloadURL(); u != "https://github.com/XTLS/Xray-core/releases/download/v26.3.27/Xray-linux-64.zip" {
		t.Fatal(u)
	}
	s := XrayExitInstallScript()
	if !strings.Contains(s, `XR_SHA256="`+XrayExitSHA256+`"`) || !strings.Contains(s, XrayExitDownloadURL()) {
		t.Fatal("production installer does not pin the release")
	}
	iSHA, iUnzip, iExec := strings.Index(s, "sha256sum -c"), strings.Index(s, "unzip "), strings.Index(s, `"$XR_TMP/xray" version`)
	if iSHA < 0 || iSHA > iUnzip || iUnzip > iExec {
		t.Errorf("order sha256sum(%d) < unzip(%d) < exec(%d) violated", iSHA, iUnzip, iExec)
	}
}

func TestXrayExitInstall(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const installedBefore = "# installed before\n"
	for _, tc := range []struct {
		name               string
		current, bak       string // bin content, "" = absent
		dats               bool   // geoip.dat/geosite.dat already there
		download, payload  string // "" download = curl fails
		wrongSHA, pipefail bool
		ok                 bool
		wantVer, wantBak   string
		wantCurl           bool
		want               []string
	}{
		{name: "fresh", download: "zip", payload: fakeXray("26.3.27"), ok: true, wantVer: "26.3.27", wantCurl: true,
			want: []string{"XR_REPLACED=1", "XR_DATA=1", "[xray] installed 26.3.27 (previous: none)"}},
		// Review: no binary but a leftover xray.bak — removed, a rollback cannot resurrect it.
		{name: "fresh with a leftover bak", bak: fakeXray("24.9.30"), download: "zip", payload: fakeXray("26.3.27"), ok: true, wantVer: "26.3.27",
			wantCurl: true, want: []string{"XR_REPLACED=1"}},
		{name: "fresh under pipefail", pipefail: true, download: "zip", payload: fakeXray("26.3.27"), ok: true, wantVer: "26.3.27", wantCurl: true,
			want: []string{"XR_REPLACED=1"}},
		{name: "current kept", current: fakeXray("26.3.27"), dats: true, download: "zip", payload: fakeXray("26.3.27"), ok: true,
			wantVer: "26.3.27", want: []string{"XR_REPLACED=0", "XR_DATA=0", "already installed"}},
		{name: "upgrade keeps bak", current: fakeXray("25.1.1"), bak: fakeXray("24.9.30"), dats: true, download: "zip", payload: fakeXray("26.3.27"),
			ok: true, wantVer: "26.3.27", wantBak: "25.1.1", wantCurl: true, want: []string{"XR_REPLACED=1", "XR_DATA=1", "(previous: 25.1.1)"}},
		{name: "data files restored, binary kept", current: fakeXray("26.3.27") + installedBefore, bak: fakeXray("25.1.1"), download: "zip",
			payload: fakeXray("26.3.27"), ok: true, wantVer: "26.3.27", wantBak: "25.1.1", wantCurl: true,
			want: []string{"XR_REPLACED=0", "XR_DATA=1", "geoip.dat"}},
		{name: "sha mismatch keeps old", current: fakeXray("25.1.1"), dats: true, download: "zip", payload: fakeXray("26.3.27"), wrongSHA: true,
			wantVer: "25.1.1", wantCurl: true, want: []string{"[xray] ERROR"}},
		{name: "download fails", current: fakeXray("25.1.1"), dats: true, payload: fakeXray("26.3.27"),
			wantVer: "25.1.1", wantCurl: true, want: []string{"[xray] ERROR"}},
		{name: "wrong version in the archive", current: fakeXray("25.1.1"), dats: true, download: "zip", payload: fakeXray("26.3.26"),
			wantVer: "25.1.1", wantCurl: true, want: []string{"[xray] ERROR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			xdir := "opt/heyearth/xray/"
			mustWrite(t, dir, xdir+".dl.stale1/xray.zip", "left by a dropped run", 0o644)
			if tc.current != "" {
				mustWrite(t, dir, xdir+"xray", tc.current, 0o755)
			}
			if tc.bak != "" {
				mustWrite(t, dir, xdir+"xray.bak", tc.bak, 0o755)
			}
			if tc.dats {
				mustWrite(t, dir, xdir+"geoip.dat", "geoip-old\n", 0o644)
				mustWrite(t, dir, xdir+"geosite.dat", "geosite-old\n", 0o644)
			}
			if tc.download != "" {
				mustWrite(t, dir, "download", tc.download, 0o644)
			}
			mustWrite(t, dir, "payload", tc.payload, 0o755)
			sha := sha256Hex("zip")
			if tc.wrongSHA {
				sha = sha256Hex("other")
			}
			prelude := "set -e\n"
			if tc.pipefail {
				prelude = "set -eo pipefail\n"
			}
			out, err := shellRun(t, dir, prelude+xrayDownloadStubs, xrayExitInstallScript(sha)+"echo XR_REPLACED=$XR_REPLACED XR_DATA=$XR_DATA\necho AFTER\n")
			if (err == nil) != tc.ok || strings.Contains(out, "AFTER") != tc.ok {
				t.Fatalf("err=%v\n%s", err, out)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if v := xrayVersionOf(t, filepath.Join(dir, xdir+"xray")); v != tc.wantVer {
				t.Errorf("binary now %q, want %q", v, tc.wantVer)
			}
			if v := xrayVersionOf(t, filepath.Join(dir, xdir+"xray.bak")); v != tc.wantBak {
				t.Errorf("xray.bak %q, want %q", v, tc.wantBak)
			}
			if tc.current != "" && strings.HasSuffix(tc.current, installedBefore) && !strings.HasSuffix(readRel(dir, xdir+"xray"), installedBefore) {
				t.Error("a current binary was replaced although only the data files were missing")
			}
			if curl := exists(dir, "curl.args"); curl != tc.wantCurl {
				t.Errorf("curl called=%v, want %v", curl, tc.wantCurl)
			}
			if tc.ok && tc.wantCurl {
				if readRel(dir, xdir+"geoip.dat") != "geoip-new\n" || readRel(dir, xdir+"geosite.dat") != "geosite-new\n" {
					t.Error("geoip.dat/geosite.dat not installed from the release")
				}
				if !strings.Contains(readRel(dir, "unzip.args"), " xray geoip.dat geosite.dat -d ") {
					t.Errorf("unzip args: %s", readRel(dir, "unzip.args"))
				}
			}
			if !tc.ok && tc.dats && readRel(dir, xdir+"geoip.dat") != "geoip-old\n" {
				t.Error("data files changed by a failed install")
			}
			// A download attempt clears stale temp dirs of a dropped run and leaves none of its own.
			if left, _ := filepath.Glob(filepath.Join(dir, xdir, ".dl.*")); len(left) != 0 && tc.wantCurl {
				t.Errorf("temp dirs left behind: %v", left)
			}
			if exists(dir, xdir+"xray.bak.tmp") {
				t.Error("xray.bak.tmp left behind")
			}
		})
	}
}

// guardStubs: the user/guard commands of ExitXrayUserScript / ExitXrayGuardApplyScript.
const guardStubs = `getent() { [ "$1" = group ] && [ "$2" = xray ] && [ -f ./group-xray ]; }
groupadd() { echo "groupadd $*" >> ./groupadd.log; touch ./group-xray; }
id() { [ "$1" = -u ] && [ "$2" = xray ] && [ -f ./user-xray ]; }
useradd() { echo "useradd $*" >> ./useradd.log; touch ./user-xray; }
nft() { [ -f ./user-xray ] || { echo "nft: user xray unknown" >&2; return 1; }; echo "nft $*" >> ./nft.log; }
`

// The whole xray step on the node: converge with ExitXrayPrepareScript — pinned binary, the user,
// the kernel guard, `xray run -test`, then a restart only when something changed.
func TestExitXrayConverge(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	mustWrite(t, dir, rel(ExitXrayConfig), "{}\n", 0o600)
	mustWrite(t, dir, rel(ExitXrayGuardConfPath), "guard\n", 0o644)
	mustWrite(t, dir, rel(ExitXrayGuardUnitPath), ExitXrayGuardUnit, 0o644)
	mustWrite(t, dir, rel(ExitXrayResolvConfPath), ExitXrayResolvConf, 0o644)
	mustWrite(t, dir, "download", "zip", 0o644)
	mustWrite(t, dir, "payload", fakeXray("26.3.27"), 0o755)
	script := UnitConvergeScript("xray", exitXrayPrepareScript(xrayExitInstallScript(sha256Hex("zip"))))
	stubs := systemctlStub + guardStubs + xrayDownloadStubs
	marker, binMarker := rel(RestartMarker("xray")), rel(BinaryPendingMarker("xray"))

	// Fresh exit: installed, user created, guard loaded, config tested, started once.
	out, err := shellRun(t, dir, stubs, script)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, w := range []string{"[xray] installed 26.3.27", "egress guard applied", "xray run -test", "работает стабильно"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if count(dir, "restarts", "xray") != 1 || readRel(dir, "useradd.log") == "" || exists(dir, marker) || exists(dir, binMarker) {
		t.Fatalf("restarts=%d useradd=%q markers=%v/%v\n%s", count(dir, "restarts", "xray"), readRel(dir, "useradd.log"),
			exists(dir, marker), exists(dir, binMarker), out)
	}
	if count(dir, "nft.log", "nft -f ./etc/nftables-heyearth-exit-guard.conf") != 1 {
		t.Fatalf("guard not loaded:\n%s", readRel(dir, "nft.log"))
	}

	// Re-run with nothing changed: no download, no restart, the guard is re-applied, user kept.
	os.Remove(filepath.Join(dir, "curl.args"))
	if out, err := shellRun(t, dir, stubs, script); err != nil || !strings.Contains(out, "без перезапуска") {
		t.Fatalf("%v\n%s", err, out)
	}
	if exists(dir, "curl.args") || count(dir, "restarts", "xray") != 1 || count(dir, "nft.log", "nft -f ./etc/nftables-heyearth-exit-guard.conf") != 2 ||
		strings.Count(readRel(dir, "useradd.log"), "useradd") != 1 {
		t.Fatalf("idempotent re-run changed things: restarts=%d\n%s", count(dir, "restarts", "xray"), readRel(dir, "nft.log"))
	}

	// A new config that xray refuses: the converge fails before any restart, the marker stays.
	mustWrite(t, dir, marker, "", 0o644)
	mustWrite(t, dir, "xray-test-fails", "", 0o644)
	out, err = shellRun(t, dir, stubs, script)
	if err == nil || !strings.Contains(out, "failed to load config files") || !strings.Contains(out, "[xray] ERROR") {
		t.Fatalf("a failing xray -test must fail the step: %v\n%s", err, out)
	}
	if count(dir, "restarts", "xray") != 1 || !exists(dir, marker) {
		t.Fatal("restarted with a config xray refused, or the marker was lost")
	}
	os.Remove(filepath.Join(dir, "xray-test-fails"))

	// The guard files are written by the recipe; without them nothing is started.
	os.Remove(filepath.Join(dir, rel(ExitXrayGuardConfPath)))
	out, err = shellRun(t, dir, stubs, script)
	if err == nil || !strings.Contains(out, ExitXrayGuardConfPath) || count(dir, "restarts", "xray") != 1 {
		t.Fatalf("missing guard file must stop the step: %v\n%s", err, out)
	}
}

// An upgrade marks both the restart and the pending binary; a failing restart then rolls the
// binary back with the config.
func TestExitXrayUpgradeRollback(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := xrayUpgradeFixture(t)
	// The new binary crash-loops (NRestarts moves between the samples).
	out, err := shellRun(t, dir, xrayConvergeStubs, xrayTestConverge(), "FLAP=1")
	if err == nil || !strings.Contains(out, "перезапускается сам") {
		t.Fatalf("a crash-looping upgrade must fail the converge: %v\n%s", err, out)
	}
	// On the node the marker names /opt/heyearth/xray/xray (rewritten to ./ here) and the new sha256.
	if readRel(dir, rel(BinaryPendingMarker("xray"))) != sha256Hex(fakeXray("26.3.27"))+"  ./"+rel(ExitXrayBinary)+"\n" || !exists(dir, rel(RestartMarker("xray"))) {
		t.Fatalf("upgrade must leave both markers until the restart is confirmed: %q\n%s", readRel(dir, rel(BinaryPendingMarker("xray"))), out)
	}
	out, err = shellRun(t, dir, systemctlStub, UnitRollbackScript("xray", []string{ExitXrayConfig}))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if v := xrayVersionOf(t, filepath.Join(dir, rel(ExitXrayBinary))); v != "25.1.1" || readRel(dir, rel(ExitXrayConfig)) != "old" {
		t.Fatalf("binary %s config %q after rollback\n%s", v, readRel(dir, rel(ExitXrayConfig)), out)
	}
}

// xrayUpgradeFixture: a live exit on xray 25.1.1 (unit installed, config just rewritten with a
// .bak) about to be upgraded from the stub download.
func xrayUpgradeFixture(t *testing.T) string {
	dir := t.TempDir()
	mustWrite(t, dir, rel(ExitXrayBinary), fakeXray("25.1.1"), 0o755)
	mustWrite(t, dir, "opt/heyearth/xray/geoip.dat", "geoip-old\n", 0o644)
	mustWrite(t, dir, "opt/heyearth/xray/geosite.dat", "geosite-old\n", 0o644)
	mustWrite(t, dir, rel(ExitXrayConfig), "new", 0o600)
	mustWrite(t, dir, rel(ExitXrayConfig)+".bak", "old", 0o600)
	mustWrite(t, dir, rel(ExitXrayUnitPath), ExitXrayUnit, 0o644)
	for _, p := range []string{ExitXrayGuardConfPath, ExitXrayGuardUnitPath, ExitXrayResolvConfPath} {
		mustWrite(t, dir, rel(p), "x\n", 0o644)
	}
	mustWrite(t, dir, "active.xray", "", 0o644)
	mustWrite(t, dir, "download", "zip", 0o644)
	mustWrite(t, dir, "payload", fakeXray("26.3.27"), 0o755)
	return dir
}

const xrayConvergeStubs = systemctlStub + guardStubs + xrayDownloadStubs + mvKillStub

func xrayTestConverge() string {
	return UnitConvergeScript("xray", exitXrayPrepareScript(xrayExitInstallScript(sha256Hex("zip"))))
}

// mvKillStub simulates an interrupted run (SSH drop, killed bash): the script dies right before
// (MV_KILL_BEFORE) or right after (MV_KILL_AFTER) an mv whose arguments match the glob.
const mvKillStub = `mv() { case "$*" in ${MV_KILL_BEFORE:-//none//}) exit 9 ;; esac; command mv "$@"; case "$*" in ${MV_KILL_AFTER:-//none//}) exit 9 ;; esac; }
`

// Review: the restart and pending-binary markers exist before the binary is moved into place, so
// an interruption right after the move is still restarted and rolled back; one right before it
// leaves a marker that the rollback ignores (the binary on disk is not the recorded one).
func TestBinaryMarkersBeforeReplace(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	rollback := func(dir, unit string) {
		t.Helper()
		if out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript(unit, nil)); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	// xray, killed right after the new binary landed.
	dir := xrayUpgradeFixture(t)
	if out, err := shellRun(t, dir, xrayConvergeStubs, xrayTestConverge(), "MV_KILL_AFTER=*.dl.*/xray *"); err == nil {
		t.Fatalf("stub did not interrupt:\n%s", out)
	}
	if xrayVersionOf(t, filepath.Join(dir, rel(ExitXrayBinary))) != "26.3.27" || !exists(dir, rel(RestartMarker("xray"))) || !exists(dir, rel(BinaryPendingMarker("xray"))) {
		t.Fatal("binary replaced without both markers")
	}
	rollback(dir, "xray")
	if v := xrayVersionOf(t, filepath.Join(dir, rel(ExitXrayBinary))); v != "25.1.1" {
		t.Fatalf("interrupted upgrade not rolled back: %s", v)
	}
	// hysteria: markers are set before the shared installer runs; when it then fails (download
	// error) the binary keeps its old sha256 and its stale .bak (2.6.1) is never taken.
	dir = t.TempDir()
	mustWrite(t, dir, "bin/hysteria", fakeHysteria("v2.12.0"), 0o755)
	mustWrite(t, dir, "bin/hysteria.bak", fakeHysteria("v2.6.1"), 0o755)
	mustWrite(t, dir, rel(ExitHysteriaUnitPath), ExitHysteriaUnit, 0o644)
	if out, err := shellRun(t, dir, "set -e\n"+mvKillStub+hysteriaPrepStubs, hysteriaTestPrepare(t)); err == nil || !exists(dir, rel(BinaryPendingMarker("hysteria2"))) {
		t.Fatalf("want a failed install with the markers already set: %v\n%s", err, out)
	}
	rollback(dir, "hysteria2")
	if v := binVersion(t, filepath.Join(dir, "bin/hysteria")); v != "v2.12.0" || exists(dir, rel(BinaryPendingMarker("hysteria2"))) {
		t.Fatalf("binary that was never replaced changed by the rollback: %s", v)
	}

	// hysteria, killed right after the installer's final mv.
	dir = t.TempDir()
	mustWrite(t, dir, "bin/hysteria", fakeHysteria("v2.6.1"), 0o755)
	mustWrite(t, dir, "download", fakeHysteria(HysteriaVersion), 0o644)
	mustWrite(t, dir, rel(ExitHysteriaUnitPath), ExitHysteriaUnit, 0o644)
	if out, err := shellRun(t, dir, "set -e\n"+mvKillStub+hysteriaPrepStubs, hysteriaTestPrepare(t), "MV_KILL_AFTER=*hysteria.new*"); err == nil {
		t.Fatalf("stub did not interrupt:\n%s", out)
	}
	if binVersion(t, filepath.Join(dir, "bin/hysteria")) != HysteriaVersion || !exists(dir, rel(RestartMarker("hysteria2"))) {
		t.Fatal("hysteria replaced without the restart marker")
	}
	rollback(dir, "hysteria2")
	if v := binVersion(t, filepath.Join(dir, "bin/hysteria")); v != "v2.6.1" {
		t.Fatalf("interrupted hysteria upgrade not rolled back: %s", v)
	}
}

// hysteriaPrepStubs: openssl writes a key/cert pair of one generation (./gen counts them), curl
// "downloads" ./download.
const hysteriaPrepStubs = `openssl() { echo "openssl $*" >> ./openssl.log; n=$(( $(cat ./gen 2>/dev/null || echo 0) + 1 )); echo $n > ./gen
  k=""; c=""; while [ $# -gt 0 ]; do case "$1" in -keyout) k="$2"; shift 2;; -out) c="$2"; shift 2;; *) shift;; esac; done; echo "key-$n" > "$k"; echo "crt-$n" > "$c"; }
curl() { out=""; while [ $# -gt 0 ]; do case "$1" in -o) out="$2"; shift 2;; *) shift;; esac; done; [ -f ./download ] || return 22; cp ./download "$out"; }
`

// hysteriaTestPrepare renders ExitHysteriaPrepareScript for the stub download, /usr/local/bin/
// rewritten to ./bin/.
func hysteriaTestPrepare(t *testing.T) string {
	t.Helper()
	sha := sha256Hex(fakeHysteria(HysteriaVersion))
	s := strings.ReplaceAll(exitHysteriaPrepareScript(hysteriaInstallScript(sha), sha), "/usr/local/bin/", "./bin/")
	if strings.Contains(s, "/usr/local/bin") {
		t.Fatal("path rewrite incomplete")
	}
	return s
}

func TestExitHysteriaPrepare(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	run := func(dir string, env ...string) (string, error) {
		t.Helper()
		return shellRun(t, dir, "set -e\n"+mvKillStub+hysteriaPrepStubs, hysteriaTestPrepare(t), env...)
	}
	mustRun := func(dir string) {
		t.Helper()
		if out, err := run(dir); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	marker, binMarker := rel(RestartMarker("hysteria2")), rel(BinaryPendingMarker("hysteria2"))
	if !strings.Contains(ExitHysteriaPrepareScript(), "printf '%s  %s\\n' "+HysteriaSHA256+" /usr/local/bin/hysteria > ") {
		t.Fatal("production prepare must record the pinned binary's sha256")
	}

	// Exit 2 today: hysteria current (.bak = 2.6.1), certificate present → nothing to restart.
	dir := t.TempDir()
	mustWrite(t, dir, "bin/hysteria", fakeHysteria(HysteriaVersion), 0o755)
	mustWrite(t, dir, "bin/hysteria.bak", fakeHysteria("v2.6.1"), 0o755)
	mustWrite(t, dir, rel(ExitHysteriaCert), "crt-old\n", 0o644)
	mustWrite(t, dir, rel(ExitHysteriaKey), "key-old\n", 0o600)
	mustRun(dir)
	if exists(dir, marker) || exists(dir, binMarker) || exists(dir, "openssl.log") || readRel(dir, rel(ExitHysteriaKey)) != "key-old\n" {
		t.Fatal("an unchanged exit got a restart marker or a new certificate")
	}
	// Review: the pinned binary is installed and hysteria.bak is the stale 2.6.1 — a rollback of a
	// failed config change on this exit never downgrades the binary.
	mustWrite(t, dir, rel(ExitHysteriaUnitPath), ExitHysteriaUnit, 0o644)
	mustWrite(t, dir, rel(ExitHysteriaConfig), "new", 0o600)
	mustWrite(t, dir, rel(ExitHysteriaConfig)+".bak", "old", 0o600)
	if out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("hysteria2", []string{ExitHysteriaConfig})); err != nil ||
		binVersion(t, filepath.Join(dir, "bin/hysteria")) != HysteriaVersion || readRel(dir, rel(ExitHysteriaConfig)) != "old" {
		t.Fatalf("pinned binary downgraded by a config rollback: %v %s\n%s", err, binVersion(t, filepath.Join(dir, "bin/hysteria")), out)
	}

	// Review: the binary on disk already IS the pinned one (sha256) even though its `version` cannot
	// be parsed — no rollback marker is written, so its .bak can never replace it.
	dir = t.TempDir()
	odd := "#!/bin/sh\necho 'hysteria (unparseable version output)'\n"
	mustWrite(t, dir, "bin/hysteria", odd, 0o755)
	mustWrite(t, dir, "bin/hysteria.bak", fakeHysteria("v2.6.1"), 0o755)
	oddPrep := strings.ReplaceAll(exitHysteriaPrepareScript(hysteriaInstallScript(sha256Hex(odd)), sha256Hex(odd)), "/usr/local/bin/", "./bin/")
	_, _ = shellRun(t, dir, "set -e\n"+hysteriaPrepStubs, oddPrep) // the installer then fails (no download): irrelevant here
	if exists(dir, binMarker) || exists(dir, marker) {
		t.Fatal("rollback markers written for a binary that already has the pinned sha256")
	}

	// Review: no binary yet but a leftover hysteria.bak (2.6.1): it is removed before the install,
	// so rolling the fresh install back cannot resurrect it.
	dir = t.TempDir()
	mustWrite(t, dir, "bin/hysteria.bak", fakeHysteria("v2.6.1"), 0o755)
	mustWrite(t, dir, "download", fakeHysteria(HysteriaVersion), 0o644)
	mustWrite(t, dir, rel(ExitHysteriaUnitPath), ExitHysteriaUnit, 0o644)
	mustRun(dir)
	if exists(dir, "bin/hysteria.bak") || !exists(dir, binMarker) || binVersion(t, filepath.Join(dir, "bin/hysteria")) != HysteriaVersion {
		t.Fatalf("fresh install: .bak kept=%v, binary marker=%v", exists(dir, "bin/hysteria.bak"), exists(dir, binMarker))
	}
	if out, err := shellRun(t, dir, systemctlStub, UnitRollbackScript("hysteria2", nil)); err != nil || binVersion(t, filepath.Join(dir, "bin/hysteria")) != HysteriaVersion {
		t.Fatalf("rollback resurrected an old binary: %v %s\n%s", err, binVersion(t, filepath.Join(dir, "bin/hysteria")), out)
	}

	// Key missing: a new pair, restart marker, no binary marker.
	dir = t.TempDir()
	mustWrite(t, dir, "bin/hysteria", fakeHysteria(HysteriaVersion), 0o755)
	mustWrite(t, dir, rel(ExitHysteriaCert), "crt-only\n", 0o644)
	mustRun(dir)
	if readRel(dir, rel(ExitHysteriaKey)) != "key-1\n" || readRel(dir, rel(ExitHysteriaCert)) != "crt-1\n" || !exists(dir, marker) || exists(dir, binMarker) {
		t.Fatal("missing key: pair not regenerated or markers wrong")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "opt/heyearth/certs/.hy2.*")); len(left) != 0 {
		t.Errorf("temp dir left: %v", left)
	}

	// Review: interrupted between the key and the certificate move — the old certificate is
	// already gone, so the next run sees a missing half and makes a fresh matching pair.
	dir = t.TempDir()
	mustWrite(t, dir, "bin/hysteria", fakeHysteria(HysteriaVersion), 0o755)
	mustWrite(t, dir, rel(ExitHysteriaCert), "crt-old\n", 0o644)
	if out, err := run(dir, "MV_KILL_BEFORE=*hy2.crt*"); err == nil {
		t.Fatalf("stub did not interrupt:\n%s", out)
	}
	if readRel(dir, rel(ExitHysteriaKey)) != "key-1\n" || exists(dir, rel(ExitHysteriaCert)) {
		t.Fatalf("after the interruption: key %q, old certificate kept=%v", readRel(dir, rel(ExitHysteriaKey)), exists(dir, rel(ExitHysteriaCert)))
	}
	mustRun(dir)
	if k, c := readRel(dir, rel(ExitHysteriaKey)), readRel(dir, rel(ExitHysteriaCert)); k != "key-2\n" || c != "crt-2\n" {
		t.Fatalf("re-run must regenerate the pair: key %q cert %q", k, c)
	}

	// Old binary: markers set before the install, the binary marker records the pinned sha256.
	dir = t.TempDir()
	mustWrite(t, dir, "bin/hysteria", fakeHysteria("v2.6.1"), 0o755)
	mustWrite(t, dir, "download", fakeHysteria(HysteriaVersion), 0o644)
	mustWrite(t, dir, rel(ExitHysteriaCert), "crt-old\n", 0o644)
	mustWrite(t, dir, rel(ExitHysteriaKey), "key-old\n", 0o600)
	mustRun(dir)
	if !exists(dir, marker) || readRel(dir, binMarker) != sha256Hex(fakeHysteria(HysteriaVersion))+"  ./bin/hysteria\n" {
		t.Fatalf("upgrade markers: restart=%v binary=%q", exists(dir, marker), readRel(dir, binMarker))
	}
}

func TestExitPreflightScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "etc/os-release", "ID=ubuntu\nPRETTY_NAME=\"Ubuntu 24.04\"\n", 0o644)
	stubs := `uname() { echo x86_64; }
id() { echo "${UID_STUB:-0}"; }
df() { printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/vda1 1 1 9000000 1%% /\n'; }
timeout() { [ "${NET:-1}" = 1 ] || { [ "${NET}" = gh ] && case "$*" in *gstatic*) true;; *) false;; esac; }; }
ss() { case "$*" in *10443*) echo 'LISTEN 0 4096 *:10443 *:* users:(("nginx",pid=1,fd=6))' ;; *50052*) echo 'LISTEN 0 4096 *:50052 *:* users:(("node-agent",pid=2,fd=3))' ;; esac; }
`
	out, err := shellRun(t, dir, stubs, ExitPreflightScript())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, w := range []string{"PF os ok Ubuntu 24.04", "PF arch ok", "PF root ok", "PF disk ok", "PF internet ok",
		"PF port_50443 ok", "PF port_50052 ok", "PF port_10443 fail порт 10443 занят: users:((\"nginx\"", "PF_TIME "} {
		if !strings.Contains(out, w) {
			t.Errorf("preflight lacks %q:\n%s", w, out)
		}
	}
	out, _ = shellRun(t, dir, stubs, ExitPreflightScript(), "NET=0", "UID_STUB=1000")
	if !strings.Contains(out, "PF internet fail") || !strings.Contains(out, "PF root fail") {
		t.Errorf("no internet / not root must fail:\n%s", out)
	}
	if out, _ = shellRun(t, dir, stubs, ExitPreflightScript(), "NET=gh"); !strings.Contains(out, "PF internet fail") || !strings.Contains(out, "github.com") {
		t.Errorf("github.com unreachable must fail (Xray and Hysteria are downloaded from there):\n%s", out)
	}
	mustWrite(t, dir, "etc/os-release", "ID=centos\nPRETTY_NAME=\"CentOS Stream 9\"\n", 0o644)
	if out, _ = shellRun(t, dir, stubs, ExitPreflightScript()); !strings.Contains(out, "PF os fail") {
		t.Errorf("CentOS accepted:\n%s", out)
	}
}

func TestExitBaseScripts(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const stubs = `modprobe() { :; }
sysctl() { case "$1" in -n) cat ./cc 2>/dev/null || echo cubic ;; *) echo "sysctl $*" >> ./sysctl.log; echo bbr > ./cc ;; esac; }
dpkg-query() { p=${@: -1}; if [ -f "./pkg.$p" ]; then cat "./pkg.$p"; else return 1; fi; }
apt-get() { echo "apt-get $*" >> ./apt.log
  case "$*" in *" update"*) [ ! -f ./apt-update-fails ] || return 100 ;;
  *" install "*) for p in "$@"; do case "$p" in -*|install|DPkg::*) ;; *) printf 'install ok installed' > "./pkg.$p" ;; esac; done ;; esac; }
`
	dir := t.TempDir()
	for _, p := range []string{"curl", "openssl", "ca-certificates"} {
		mustWrite(t, dir, "pkg."+p, "install ok installed", 0o644)
	}
	mustWrite(t, dir, "pkg.nftables", "deinstall ok config-files", 0o644) // removed, not purged: missing
	check := func() string {
		out, err := shellRun(t, dir, stubs, ExitBaseCheckScript)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return out
	}
	if c := check(); c != "BBR=0\nPKGS=0\n" {
		t.Fatalf("before: %q", c)
	}
	mustWrite(t, dir, "apt-update-fails", "", 0o644)
	if out, err := shellRun(t, dir, stubs, ExitBaseScript); err == nil || strings.Contains(out, "пакеты на месте") || strings.Contains(readRel(dir, "apt.log"), " install ") {
		t.Fatalf("a failed apt-get update must stop the step before installing: %v\n%s\n%s", err, out, readRel(dir, "apt.log"))
	}
	os.Remove(filepath.Join(dir, "apt-update-fails"))
	out, err := shellRun(t, dir, stubs, ExitBaseScript)
	if err != nil || !strings.Contains(out, "BBR: bbr") {
		t.Fatalf("%v\n%s", err, out)
	}
	if log := readRel(dir, "apt.log"); !strings.Contains(log, "DPkg::Lock::Timeout=") || !strings.Contains(log, " unzip nftables") {
		t.Errorf("apt-get must wait for the dpkg lock and install only the missing packages:\n%s", log)
	}
	if !strings.Contains(readRel(dir, "sysctl.log"), "-p ./etc/sysctl.d/99-bbr.conf") {
		t.Errorf("sysctl: %s", readRel(dir, "sysctl.log"))
	}
	if c := check(); c != "BBR=1\nPKGS=1\n" {
		t.Fatalf("after: %q", c)
	}
	os.Remove(filepath.Join(dir, "apt.log"))
	if _, err := shellRun(t, dir, stubs, ExitBaseScript); err != nil || exists(dir, "apt.log") {
		t.Fatal("apt-get ran although every package is installed")
	}
}

func TestPortHopApplyAndChecks(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	// What nft 1.0.9 prints for ExitPortHopConf (checked in a throwaway netns).
	const listed = "table inet hysteria_porthopping {\n\tchain prerouting {\n\t\ttype nat hook prerouting priority dstnat; policy accept;\n" +
		"\t\tudp dport 20000-50000 fib daddr type local counter packets 0 bytes 0 redirect to :50443\n\t}\n}\n"
	const oldUnguarded = "table inet hysteria_porthopping {\n\tchain prerouting {\n\t\tudp dport 20000-50000 counter packets 0 bytes 0 redirect to :50443\n\t}\n}\n"
	stubs := systemctlStub + `nft() { case "$1" in -f) echo "nft $*" >> ./nft.log ;; list) case "$*" in *hysteria_porthopping*) cat ./hop.listed 2>/dev/null ;; *heyearth_exit_guard*) cat ./guard.listed 2>/dev/null ;; esac ;; esac; }
ss() { case "$*" in *50443*) cat ./ss.udp 2>/dev/null ;; *10443*) cat ./ss.tcp 2>/dev/null ;; esac; }
`
	dir := t.TempDir()
	mustWrite(t, dir, "hop.listed", listed, 0o644)
	out, err := shellRun(t, dir, stubs, ExitPortHopApplyScript)
	if err != nil || count(dir, "nft.log", "nft -f ./etc/nftables-hysteria.conf") != 1 || count(dir, "calls", "enable --quiet nftables-hysteria") != 1 {
		t.Fatalf("%v\n%s\n%s", err, out, readRel(dir, "calls"))
	}
	// A table without the local-destination guard (the pre-tte5 rule) does not count as applied.
	mustWrite(t, dir, "hop.listed", oldUnguarded, 0o644)
	if out, err := shellRun(t, dir, stubs, ExitPortHopApplyScript); err == nil || !strings.Contains(out, "[hop] ERROR") {
		t.Fatalf("unguarded rule accepted: %v\n%s", err, out)
	}

	hy := strings.ReplaceAll(HysteriaCheckScript, "/usr/local/bin/", "./bin/")
	mustWrite(t, dir, "bin/hysteria", fakeHysteria(HysteriaVersion), 0o755)
	mustWrite(t, dir, "ss.udp", `UNCONN 0 0 *:50443 *:* users:(("hysteria",pid=7,fd=8))`+"\n", 0o644)
	if out, _ := shellRun(t, dir, stubs, hy); out != "VERSION="+HysteriaVersion+"\nLISTEN=1\nHOP=0\n" {
		t.Fatalf("hy-check: %q", out)
	}
	mustWrite(t, dir, "hop.listed", listed, 0o644)
	if out, _ := shellRun(t, dir, stubs, hy); out != "VERSION="+HysteriaVersion+"\nLISTEN=1\nHOP=1\n" {
		t.Fatalf("hy-check: %q", out)
	}

	mustWrite(t, dir, rel(ExitXrayBinary), fakeXray("26.3.27"), 0o755)
	mustWrite(t, dir, "ss.tcp", `LISTEN 0 4096 0.0.0.0:10443 0.0.0.0:* users:(("xray",pid=9,fd=3))`+"\n", 0o644)
	if out, _ := shellRun(t, dir, stubs, XrayCheckScript); out != "VERSION=26.3.27\nLISTEN=1\nGUARD=0\n" {
		t.Fatalf("xray-check: %q", out)
	}
	mustWrite(t, dir, "guard.listed", "table inet heyearth_exit_guard {\n\tchain output {\n\t\tmeta skuid \"xray\" jump xray_egress\n\t}\n}\n", 0o644)
	mustWrite(t, dir, "ss.tcp", `LISTEN 0 4096 0.0.0.0:10443 0.0.0.0:* users:(("nginx",pid=9,fd=3))`+"\n", 0o644)
	if out, _ := shellRun(t, dir, stubs, XrayCheckScript); out != "VERSION=26.3.27\nLISTEN=0\nGUARD=1\n" {
		t.Fatalf("xray-check: %q", out)
	}
}

func TestHubSSHKeyEnsureScript(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const stub = `ssh-keygen() { echo "ssh-keygen $*" >> ./keygen.log
  f=""; y=0; while [ $# -gt 0 ]; do case "$1" in -f) f="$2"; shift 2;; -y) y=1; shift;; *) shift;; esac; done
  if [ "$y" = 1 ]; then echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITEST"; return; fi
  echo PRIVATE > "$f"; echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITEST heyearth-hub" > "$f.pub"; }
`
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		out, err := shellRun(t, dir, stub, HubSSHKeyEnsureScript)
		if err != nil || out != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITEST heyearth-hub\n" {
			t.Fatalf("run %d: %v %q", i, err, out)
		}
	}
	if n := strings.Count(readRel(dir, "keygen.log"), "-t ed25519"); n != 1 {
		t.Fatalf("key generated %d times", n)
	}
	// A key whose .pub was lost (drop between the two writes) is not replaced: .pub is derived.
	os.Remove(filepath.Join(dir, rel(HubSSHKeyPath)+".pub"))
	out, err := shellRun(t, dir, stub, HubSSHKeyEnsureScript)
	if err != nil || out != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITEST heyearth-hub\n" || strings.Count(readRel(dir, "keygen.log"), "-t ed25519") != 1 {
		t.Fatalf("%v %q\n%s", err, out, readRel(dir, "keygen.log"))
	}
	// An unreadable key fails loudly instead of printing an empty public key.
	os.Remove(filepath.Join(dir, rel(HubSSHKeyPath)+".pub"))
	if out, err := shellRun(t, dir, "ssh-keygen() { return 1; }\n", HubSSHKeyEnsureScript); err == nil || strings.Contains(out, "ssh-ed25519") {
		t.Fatalf("broken key accepted: %v %q", err, out)
	}
}

func TestAuthorizedKeyScripts(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHub heyearth-hub"
	// The provisioning core's fake host reads the key between KEY=' and '.
	if !strings.Contains(AuthorizedKeyScript(pub), "\nKEY='"+pub+"'\n") || !strings.Contains(AuthorizedKeyCheckScript(pub), "\nKEY='"+pub+"'\n") {
		t.Fatal("KEY line format changed")
	}
	dir := t.TempDir()
	home := "HOME=" + dir
	mustWrite(t, dir, ".ssh/authorized_keys", "ssh-rsa AAAAold owner@laptop", 0o600) // no trailing newline
	for i := 0; i < 2; i++ {
		if out, err := shellRun(t, dir, "", AuthorizedKeyScript(pub), home); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	if got := readRel(dir, ".ssh/authorized_keys"); got != "ssh-rsa AAAAold owner@laptop\n"+pub+"\n" {
		t.Fatalf("authorized_keys:\n%q", got)
	}
	if out, _ := shellRun(t, dir, "", AuthorizedKeyCheckScript(pub), home); out != "PRESENT=1\n" {
		t.Fatalf("%q", out)
	}
	if out, _ := shellRun(t, dir, "", AuthorizedKeyCheckScript("ssh-ed25519 AAAAother"), home); out != "PRESENT=0\n" {
		t.Fatalf("%q", out)
	}
	for _, bad := range []string{"ssh-ed25519 AAAA x\nssh-rsa AAAAevil", "ssh-rsa AAAAnot-ed25519", "command=\"id\" ssh-ed25519 AAAA"} {
		before := readRel(dir, ".ssh/authorized_keys")
		if out, err := shellRun(t, dir, "", AuthorizedKeyScript(bad), home); err == nil {
			t.Errorf("key %q accepted:\n%s", bad, out)
		}
		if readRel(dir, ".ssh/authorized_keys") != before {
			t.Errorf("authorized_keys changed by %q", bad)
		}
	}
}

func TestProbeScripts(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	// curl answers 000 until ./ok-after-N tries, then 204; the requested SOCKS goes to ./curl.args.
	const curlStub = `curl() { echo "$*" >> ./curl.args; n=$(( $(cat ./tries 2>/dev/null || echo 0) + 1 )); echo $n > ./tries
  if [ "$n" -ge "$(cat ./ok-after 2>/dev/null || echo 99)" ]; then printf 204; else printf 000; return 7; fi; }
sleep() { :; }
`
	dir := t.TempDir()
	mustWrite(t, dir, "ok-after", "2", 0o644)
	out, err := shellRun(t, dir, curlStub, SocksProbeScript(1095))
	if err != nil || out != "HTTP=204\n" || !strings.Contains(readRel(dir, "curl.args"), "--socks5-hostname 127.0.0.1:1095 https://www.gstatic.com/generate_204") {
		t.Fatalf("%v %q\n%s", err, out, readRel(dir, "curl.args"))
	}
	dir = t.TempDir()
	if out, _ := shellRun(t, dir, curlStub, SocksProbeScript(1095)); out != "HTTP=000\n" || readRel(dir, "tries") != "3\n" {
		t.Fatalf("all tries failed: %q tries=%q", out, readRel(dir, "tries"))
	}

	// VR probe: a short-lived xray on the probe config, killed afterwards; the config (it carries
	// the tunnel UUID) and its .bak are removed.
	dir = t.TempDir()
	mustWrite(t, dir, rel(ExitXrayBinary), "#!/bin/bash\necho \"$*\" > ./probe-xray.args\ntouch ./probe-xray.up\nexec sleep 30\n", 0o755)
	mustWrite(t, dir, "opt/heyearth/state/vr-probe-7.json", "{}", 0o600)
	mustWrite(t, dir, "opt/heyearth/state/vr-probe-7.json.bak", "{\"uuid\":\"old\"}", 0o600)
	mustWrite(t, dir, "ok-after", "1", 0o644)
	stubs := curlStub + `ss() { [ -f ./probe-xray.up ] && echo 'LISTEN 0 4096 127.0.0.1:3095 0.0.0.0:*'; }
pkill() { echo "pkill $*" >> ./pkill.log; }
kill() { echo "kill $*" >> ./kill.log; builtin kill "$@"; }
`
	out, err = shellRun(t, dir, stubs, VRProbeScript(ExitXrayBinary, "/opt/heyearth/state/vr-probe-7.json", 3095))
	if err != nil || !strings.HasSuffix(out, "HTTP=204\n") {
		t.Fatalf("%v %q", err, out)
	}
	if got := readRel(dir, "probe-xray.args"); got != "run -config ./opt/heyearth/state/vr-probe-7.json\n" {
		t.Errorf("probe xray args %q", got)
	}
	if exists(dir, "opt/heyearth/state/vr-probe-7.json") || exists(dir, "opt/heyearth/state/vr-probe-7.json.bak") {
		t.Error("probe config with the tunnel UUID left on the Hub")
	}
	stopped := false
	for _, l := range strings.Split(readRel(dir, "kill.log"), "\n") {
		stopped = stopped || (strings.HasPrefix(l, "kill ") && !strings.HasPrefix(l, "kill -0 "))
	}
	if !stopped || !strings.Contains(readRel(dir, "pkill.log"), "vr-probe-7.json") {
		t.Errorf("probe xray not stopped / stale probe not cleared: kill=%q pkill=%q", readRel(dir, "kill.log"), readRel(dir, "pkill.log"))
	}
}

// Every op script carries its marker line, parses under bash -n, and neither a line break in an
// argument nor a hostile unit name escapes into a command.
func TestOpScriptsMarkedAndParse(t *testing.T) {
	if !haveBash() {
		t.Skip("no bash")
	}
	for name, s := range map[string]string{
		"converge": UnitConvergeScript("xray", ExitXrayPrepareScript()), "rollback": UnitRollbackScript("x", []string{"/a b"}),
		"state": UnitStateScript("xray"), "mark": MarkRestartScript("xray", nil), "sums": SumsScript("/a", "/b c"), "cat": CatScript("/a"),
		"firewall": ExitFirewallScript(22, nil, 0), "preflight": ExitPreflightScript(), "base": ExitBaseScript, "basecheck": ExitBaseCheckScript,
		"hop": ExitPortHopApplyScript, "hy": HysteriaCheckScript, "xr": XrayCheckScript, "hubkey": HubSSHKeyEnsureScript,
		"hyprep": ExitHysteriaPrepareScript(), "xrprep": ExitXrayPrepareScript(),
		"auth": AuthorizedKeyScript("ssh-ed25519 AAAA"), "authc": AuthorizedKeyCheckScript("ssh-ed25519 AAAA"),
		"socks": SocksProbeScript(1095), "vr": VRProbeScript("/opt/heyearth/xray/xray", "/opt/heyearth/state/vr-probe-7.json", 3095),
		"hyconv": UnitConvergeScript("hysteria2", ExitHysteriaPrepareScript()),
		"soft":   UnitConvergeSoftScript("hysteria2-client-7", HysteriaEnsureScript()), "private": PrivateModeScript("/a", "/b c"),
		"agentcheck": NodeAgentCheckScript, "agentconv": UnitConvergeScript("node-agent", ExitAgentGuardApplyScript),
		"keypub": KeyPublicScript("/a b"),
	} {
		if name != "hyprep" && name != "xrprep" && !strings.HasPrefix(s, "# hp:") {
			t.Errorf("%s: no marker line", name)
		}
		if out, err := exec.Command("bash", "-n", "-c", s).CombinedOutput(); err != nil {
			t.Errorf("%s: bash -n: %v %s", name, err, out)
		}
	}
	for op, s := range map[string]string{
		"converge xray": UnitConvergeScript("xray", ""), "rollback xray": UnitRollbackScript("xray", nil), "unit-state xray": UnitStateScript("xray"),
		"mark xray": MarkRestartScript("xray", nil), "sums /a /b": SumsScript("/a", "/b"), "cat /a": CatScript("/a"), "firewall": ExitFirewallScript(22, nil, 0),
		"preflight": ExitPreflightScript(), "base": ExitBaseScript, "base-check": ExitBaseCheckScript, "hop-apply": ExitPortHopApplyScript,
		"hy-check": HysteriaCheckScript, "xray-check": XrayCheckScript, "hub-key": HubSSHKeyEnsureScript, "authkey": AuthorizedKeyScript("k"),
		"authkey-check": AuthorizedKeyCheckScript("k"), "socks-probe 1095": SocksProbeScript(1095), "vr-probe 3095": VRProbeScript("/x", "/c", 3095),
		"converge hysteria2-client-7 soft": UnitConvergeSoftScript("hysteria2-client-7", ""), "private /a /b": PrivateModeScript("/a", "/b"),
		"agent-check": NodeAgentCheckScript, "key-pub /a": KeyPublicScript("/a"),
	} {
		if first, _, _ := strings.Cut(s, "\n"); first != "# hp:"+op {
			t.Errorf("marker %q, want %q", first, "# hp:"+op)
		}
	}
	dir := t.TempDir()
	for _, s := range []string{SumsScript("/a\ntouch ./pwned-sums"), CatScript("/a\rtouch ./pwned-cat\n"), UnitStateScript("x; touch ./pwned-unit"),
		MarkRestartScript("x$(touch ./pwned-mark)", nil), UnitRollbackScript("x`touch ./pwned-rb`", []string{"./a'; touch ./pwned-path; '"}),
		UnitConvergeSoftScript("x$(touch ./pwned-soft)", ""), PrivateModeScript("./a'; touch ./pwned-private; '"), KeyPublicScript("/a'; touch ./pwned-key; '")} {
		_, _ = shellRun(t, dir, systemctlStub, s)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "pwned-*")); len(left) != 0 {
		t.Fatalf("argument escaped into a command: %v", left)
	}
}
