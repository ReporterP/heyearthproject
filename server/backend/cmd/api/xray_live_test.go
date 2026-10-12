package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/xraydiff"
)

// ───── applyUserOnlyDiff: CLI calls, order and verification ─────

// liveTestConfig builds a config shaped like SyncXrayConfig output: vless-reality
// (Vision flow) and vless-xhttp (no flow) with the same users, plan-3-users rule
// before the catch-all. levels: email -> level (default 1).
func liveTestConfig(t *testing.T, users []string, levels map[string]int) []byte {
	t.Helper()
	clients := func(flow bool) []any {
		cs := []any{}
		for _, u := range users {
			lvl, ok := levels[u]
			if !ok {
				lvl = 1
			}
			c := map[string]any{"id": "uuid-" + u, "email": u, "level": lvl}
			if flow {
				c["flow"] = "xtls-rprx-vision"
			}
			cs = append(cs, c)
		}
		return cs
	}
	rules := []any{map[string]any{"type": "field", "inboundTag": []any{"api-in"}, "outboundTag": "api"}}
	if len(users) > 0 {
		rules = append(rules, map[string]any{"type": "field", "ruleTag": "plan-3-users", "user": users, "balancerTag": "plan-3-balancer"})
	}
	rules = append(rules, map[string]any{"type": "field", "network": "tcp,udp", "balancerTag": "exit-balancer"})
	cfg := map[string]any{
		"api": map[string]any{"tag": "api", "services": []any{"HandlerService", "RoutingService", "StatsService"}},
		"inbounds": []any{
			map[string]any{"tag": "vless-reality", "port": 10443, "protocol": "vless",
				"settings": map[string]any{"decryption": "none", "clients": clients(true)}},
			map[string]any{"tag": "vless-xhttp", "port": 2443, "protocol": "vless",
				"settings": map[string]any{"decryption": "none", "clients": clients(false)}},
		},
		"routing": map[string]any{"domainStrategy": "IPIfNonMatch", "rules": rules,
			"balancers": []any{map[string]any{"tag": "plan-3-balancer", "selector": []any{"exit-2"}}}},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func liveDiff(t *testing.T, oldCfg, newCfg []byte) xraydiff.Result {
	t.Helper()
	d, err := xraydiff.Classify(oldCfg, newCfg)
	if err != nil || d.Kind != xraydiff.UserOnly {
		t.Fatalf("Classify = %v (%s), %v; want user-only", d.Kind, d.Reason, err)
	}
	return d
}

// fakeCLI answers like a running xray that applies every call correctly, unless
// an override (keyed by the rendered call) says otherwise.
type fakeCLI struct {
	d        xraydiff.Result
	calls    []string
	payloads map[string][]byte
	override map[string]string // rendered call -> stdout
	fail     map[string]error  // rendered call -> error
}

func newFakeCLI(d xraydiff.Result) *fakeCLI {
	return &fakeCLI{d: d, payloads: map[string][]byte{}, override: map[string]string{}, fail: map[string]error{}}
}

func (f *fakeCLI) run(sub string, args []string, payload []byte) (string, error) {
	call := strings.TrimSpace(sub + " " + strings.Join(args, " "))
	if payload != nil {
		call += " <file>"
		f.payloads[sub] = payload
	}
	f.calls = append(f.calls, call)
	if err := f.fail[call]; err != nil {
		return "", err
	}
	if out, ok := f.override[call]; ok {
		return out, nil
	}
	switch sub {
	case "rmu":
		n := 0
		for i, a := range args {
			if a == "--" {
				n = len(args) - i - 1
			}
		}
		return fmt.Sprintf("remove user: x\nRemoved %d user(s) in total.\n", n), nil
	case "adu":
		var v struct {
			Inbounds []struct {
				Settings struct{ Clients []map[string]any }
			}
		}
		if err := json.Unmarshal(payload, &v); err != nil {
			return "", err
		}
		n := 0
		for _, in := range v.Inbounds {
			n += len(in.Settings.Clients)
		}
		return fmt.Sprintf("processing inbound: x\nadd user: x\nresult: ok\nAdded %d user(s) in total.\n", n), nil
	case "adrules":
		return "{}\n", nil
	case "lsrules":
		var rs []string
		for _, tag := range f.d.RuleTags {
			if tag == "" {
				rs = append(rs, `{"tag": "x"}`)
			} else {
				rs = append(rs, fmt.Sprintf(`{"tag": "x", "ruleTag": %q}`, tag))
			}
		}
		return "{\n  \"rules\": [" + strings.Join(rs, ",") + "]\n}\n", nil
	case "inboundusercount":
		tag := strings.TrimPrefix(args[0], "-tag=")
		if n := f.d.UserCounts[tag]; n > 0 {
			return fmt.Sprintf("{\n  \"count\": \"%d\"\n}\n", n), nil
		}
		return "{}\n", nil
	}
	return "", fmt.Errorf("unexpected call %q", call)
}

func TestApplyUserOnlyDiff(t *testing.T) {
	base := liveTestConfig(t, []string{"alice", "bob"}, nil)
	revokeBobAddCarol := liveTestConfig(t, []string{"alice", "carol"}, nil)
	aliceLevel0 := liveTestConfig(t, []string{"alice", "bob"}, map[string]int{"alice": 0})

	fullOrder := []string{
		"rmu -tag=vless-reality -- bob",
		"rmu -tag=vless-xhttp -- bob",
		"adrules <file>",
		"lsrules",
		"adu <file>",
		"inboundusercount -tag=vless-reality",
		"inboundusercount -tag=vless-xhttp",
	}
	tests := []struct {
		name     string
		newCfg   []byte
		override map[string]string
		fail     map[string]error
		calls    []string
		errHas   string
	}{
		{name: "revoke + add + routing: rmu, adrules, lsrules, adu, counts", newCfg: revokeBobAddCarol, calls: fullOrder},
		{name: "clients only: no adrules/lsrules", newCfg: aliceLevel0, calls: []string{
			"rmu -tag=vless-reality -- alice",
			"rmu -tag=vless-xhttp -- alice",
			"adu <file>",
			"inboundusercount -tag=vless-reality",
			"inboundusercount -tag=vless-xhttp",
		}},
		{name: "rmu removed fewer", newCfg: revokeBobAddCarol,
			override: map[string]string{"rmu -tag=vless-reality -- bob": "remove user: bob\nUser bob not found.\nRemoved 0 user(s) in total.\n"},
			calls:    fullOrder[:1], errHas: "removed 0 of 1"},
		{name: "adu added fewer", newCfg: revokeBobAddCarol,
			override: map[string]string{"adu <file>": "add user: carol\nUser carol already exists.\nAdded 1 user(s) in total.\n"},
			calls:    fullOrder[:5], errHas: "added 1 of 2"},
		{name: "adu without a total line (build error)", newCfg: revokeBobAddCarol,
			override: map[string]string{"adu <file>": "failed to build config: port\n"},
			calls:    fullOrder[:5], errHas: "added -1 of 2"},
		{name: "lsrules order mismatch", newCfg: revokeBobAddCarol,
			override: map[string]string{"lsrules": `{"rules":[{"ruleTag":"plan-3-users"},{},{}]}`},
			calls:    fullOrder[:4], errHas: "lsrules order mismatch"},
		{name: "inboundusercount mismatch", newCfg: revokeBobAddCarol,
			override: map[string]string{"inboundusercount -tag=vless-xhttp": `{"count": 3}`},
			calls:    fullOrder, errHas: "inboundusercount vless-xhttp = 3, want 2"},
		{name: "cli error stops the apply", newCfg: revokeBobAddCarol,
			fail:  map[string]error{"adrules <file>": errors.New("xray api adrules: exit status 1 (duplicate ruleTag)")},
			calls: fullOrder[:3], errHas: "duplicate ruleTag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := liveDiff(t, base, tt.newCfg)
			f := newFakeCLI(d)
			for k, v := range tt.override {
				f.override[k] = v
			}
			for k, v := range tt.fail {
				f.fail[k] = v
			}
			err := applyUserOnlyDiff(f.run, d)
			if tt.errHas == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.errHas != "" && (err == nil || !strings.Contains(err.Error(), tt.errHas)) {
				t.Fatalf("err = %v, want substring %q", err, tt.errHas)
			}
			if got := strings.Join(f.calls, "\n"); got != strings.Join(tt.calls, "\n") {
				t.Fatalf("calls:\n%s\nwant:\n%s", got, strings.Join(tt.calls, "\n"))
			}
		})
	}
}

func TestApplyUserOnlyDiffPayloads(t *testing.T) {
	base := liveTestConfig(t, []string{"alice", "bob"}, nil)
	d := liveDiff(t, base, liveTestConfig(t, []string{"alice", "carol"}, nil))
	f := newFakeCLI(d)
	if err := applyUserOnlyDiff(f.run, d); err != nil {
		t.Fatal(err)
	}
	var adu struct {
		Inbounds []struct {
			Tag      string
			Port     int
			Settings struct{ Clients []map[string]any }
		}
	}
	if err := json.Unmarshal(f.payloads["adu"], &adu); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, in := range adu.Inbounds {
		for _, c := range in.Settings.Clients {
			got = append(got, fmt.Sprintf("%s:%d:%v:%v", in.Tag, in.Port, c["email"], c["flow"]))
		}
	}
	sort.Strings(got)
	want := "vless-reality:10443:carol:xtls-rprx-vision,vless-xhttp:2443:carol:<nil>"
	if strings.Join(got, ",") != want {
		t.Fatalf("adu clients = %v, want %s", got, want)
	}
	if rp := string(f.payloads["adrules"]); !strings.HasPrefix(rp, `{"routing":`) || !strings.Contains(rp, `"carol"`) || strings.Contains(rp, `"bob"`) {
		t.Fatalf("adrules payload = %s", rp)
	}
}

// ───── xrayRuntimeState: in-memory flag + persisted marker ─────

func newTestState(t *testing.T) *xrayRuntimeState {
	t.Helper()
	return &xrayRuntimeState{marker: filepath.Join(t.TempDir(), "xray-runtime-stale")}
}

func markerExists(st *xrayRuntimeState) bool {
	_, err := os.Stat(st.marker)
	return err == nil
}

func TestXrayRuntimeStateMarker(t *testing.T) {
	st := newTestState(t)
	if st.load() || st.isStale() {
		t.Fatal("no marker: not stale")
	}
	st.begin("live apply")
	if !markerExists(st) || st.isStale() {
		t.Fatal("begin: marker written before touching the runtime, flag untouched")
	}
	// The process dies here: the next process finds the marker.
	next := &xrayRuntimeState{marker: st.marker}
	if !next.load() || !next.isStale() {
		t.Fatal("marker left by a crashed apply must make the next process stale")
	}
	next.restarted()
	if markerExists(next) || next.isStale() {
		t.Fatal("successful restart clears flag and marker")
	}
	next.markStale("revoked user still online")
	if !markerExists(next) || !next.isStale() {
		t.Fatal("markStale persists")
	}
	next.liveApplied()
	if !markerExists(next) || !next.isStale() {
		t.Fatal("a live apply must not clear a stale runtime")
	}
}

// ───── xrayApplier: unchanged / live / restart and the stale state ─────

// Minimal configs: one VLESS inbound + one plan-* rule, like SyncXrayConfig output.
const (
	liveCfgBase = `{"api":{"services":["HandlerService","RoutingService","StatsService"]},
 "inbounds":[{"tag":"vless-reality","port":10443,"protocol":"vless","settings":{"decryption":"none",
  "clients":[{"id":"u-alice","email":"alice","level":1}]}}],
 "routing":{"rules":[{"type":"field","ruleTag":"plan-3-users","user":["alice"],"balancerTag":"plan-3-balancer"},
  {"type":"field","network":"tcp,udp","balancerTag":"exit-balancer"}]}}`
	liveCfgCarol = `{"api":{"services":["HandlerService","RoutingService","StatsService"]},
 "inbounds":[{"tag":"vless-reality","port":10443,"protocol":"vless","settings":{"decryption":"none",
  "clients":[{"id":"u-alice","email":"alice","level":1},{"id":"u-carol","email":"carol","level":1}]}}],
 "routing":{"rules":[{"type":"field","ruleTag":"plan-3-users","user":["alice","carol"],"balancerTag":"plan-3-balancer"},
  {"type":"field","network":"tcp,udp","balancerTag":"exit-balancer"}]}}`
	liveCfgPort = `{"api":{"services":["HandlerService","RoutingService","StatsService"]},
 "inbounds":[{"tag":"vless-reality","port":10444,"protocol":"vless","settings":{"decryption":"none",
  "clients":[{"id":"u-alice","email":"alice","level":1}]}}],
 "routing":{"rules":[{"type":"field","ruleTag":"plan-3-users","user":["alice"],"balancerTag":"plan-3-balancer"},
  {"type":"field","network":"tcp,udp","balancerTag":"exit-balancer"}]}}`
)

// fakeXray models the config file on disk, the tmp file and the running xray.
type fakeXray struct {
	disk, tmp  string
	st         *xrayRuntimeState
	liveErr    error // applyLive result
	installErr error // rename result
	restartErr error // systemctl restart result
	calls      []string
}

func newFakeXray(t *testing.T, disk string) *fakeXray {
	return &fakeXray{disk: disk, st: newTestState(t)}
}

func (f *fakeXray) applier() xrayApplier {
	return xrayApplier{
		readRunning: func() ([]byte, error) { f.calls = append(f.calls, "read"); return []byte(f.disk), nil },
		applyLive: func(d xraydiff.Result) error {
			f.calls = append(f.calls, "live")
			if !markerExists(f.st) {
				return errors.New("marker must exist while the runtime is being changed")
			}
			return f.liveErr
		},
		install: func() error {
			f.calls = append(f.calls, "install")
			if f.installErr != nil {
				return f.installErr
			}
			f.disk, f.tmp = f.tmp, ""
			return nil
		},
		discard: func() { f.calls = append(f.calls, "discard"); f.tmp = "" },
		restart: func() error {
			f.calls = append(f.calls, "restart")
			if !markerExists(f.st) {
				return errors.New("marker must exist while xray restarts")
			}
			return f.restartErr
		},
	}
}

// sync mimics one SyncXrayConfig tail: tmp written + validated, then apply.
func (f *fakeXray) sync(newCfg string) (xrayApplyAction, error, error) {
	f.calls, f.tmp = nil, newCfg
	action, _, liveErr, err := f.applier().apply([]byte(newCfg), f.st)
	return action, liveErr, err
}

func wantCalls(t *testing.T, f *fakeXray, want string) {
	t.Helper()
	if got := strings.Join(f.calls, ","); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestXrayApplierUnchangedAndLive(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)

	if action, liveErr, err := f.sync(liveCfgBase); err != nil || liveErr != nil || action != xrayApplyUnchanged {
		t.Fatalf("unchanged: action=%v liveErr=%v err=%v", action, liveErr, err)
	}
	wantCalls(t, f, "read,discard")
	if markerExists(f.st) {
		t.Fatal("unchanged: no marker")
	}

	if action, liveErr, err := f.sync(liveCfgCarol); err != nil || liveErr != nil || action != xrayApplyLive {
		t.Fatalf("user-only: action=%v liveErr=%v err=%v", action, liveErr, err)
	}
	wantCalls(t, f, "read,live,install") // no restart
	if f.st.isStale() || markerExists(f.st) || f.disk != liveCfgCarol {
		t.Fatalf("after live apply: stale=%v marker=%v disk updated=%v", f.st.isStale(), markerExists(f.st), f.disk == liveCfgCarol)
	}

	if action, _, err := f.sync(liveCfgPort); err != nil || action != xrayApplyRestart {
		t.Fatalf("structural: action=%v err=%v", action, err)
	}
	wantCalls(t, f, "read,install,restart")
	if markerExists(f.st) {
		t.Fatal("successful restart removes the marker")
	}
}

func TestXrayApplierLiveFailureFallsBackToRestart(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	f.liveErr = errors.New("adu: added 0 of 1")
	action, liveErr, err := f.sync(liveCfgCarol)
	if err != nil || liveErr == nil || action != xrayApplyRestart {
		t.Fatalf("action=%v liveErr=%v err=%v", action, liveErr, err)
	}
	wantCalls(t, f, "read,live,install,restart")
	if f.st.isStale() || markerExists(f.st) {
		t.Fatal("a successful restart must clear the stale flag and the marker")
	}
}

// The review finding of Task 4: a failed restart after the file was renamed must
// not turn into "unchanged" on the next sync.
func TestXrayApplierFailedRestartStaysStale(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	f.restartErr = errors.New("restart xray: exit status 1")
	if _, _, err := f.sync(liveCfgPort); err == nil || !f.st.isStale() || !markerExists(f.st) {
		t.Fatalf("failed restart: err=%v stale=%v marker=%v", err, f.st.isStale(), markerExists(f.st))
	}
	if f.disk != liveCfgPort {
		t.Fatal("the validated config must already be installed")
	}

	// Same config again: the file is unchanged, but the runtime is not → restart.
	if _, _, err := f.sync(liveCfgPort); err == nil || !f.st.isStale() {
		t.Fatalf("retry while restart still fails: err=%v stale=%v", err, f.st.isStale())
	}
	wantCalls(t, f, "install,restart") // no classification while stale

	// An API restart in between: the marker carries the state over.
	f.st = &xrayRuntimeState{marker: f.st.marker}
	if !f.st.load() {
		t.Fatal("marker must survive the process")
	}
	f.restartErr = nil
	if action, _, err := f.sync(liveCfgPort); err != nil || action != xrayApplyRestart || f.st.isStale() || markerExists(f.st) {
		t.Fatalf("recovery: action=%v err=%v stale=%v marker=%v", action, err, f.st.isStale(), markerExists(f.st))
	}
	wantCalls(t, f, "install,restart")

	if action, _, err := f.sync(liveCfgPort); err != nil || action != xrayApplyUnchanged {
		t.Fatalf("after recovery: action=%v err=%v", action, err)
	}
	wantCalls(t, f, "read,discard")
}

func TestXrayApplierLiveFailureAndRestartFailure(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	f.liveErr, f.restartErr = errors.New("rmu failed"), errors.New("restart failed")
	if _, liveErr, err := f.sync(liveCfgCarol); err == nil || liveErr == nil || !f.st.isStale() {
		t.Fatalf("liveErr=%v err=%v stale=%v", liveErr, err, f.st.isStale())
	}
	// Even an unchanged config is restarted while stale.
	f.liveErr, f.restartErr = nil, nil
	if action, _, err := f.sync(liveCfgCarol); err != nil || action != xrayApplyRestart || f.st.isStale() {
		t.Fatalf("action=%v err=%v stale=%v", action, err, f.st.isStale())
	}
	wantCalls(t, f, "install,restart")
}

func TestXrayApplierRenameAfterLiveApplyFails(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	f.installErr = errors.New("EXDEV")
	action, _, err := f.sync(liveCfgCarol)
	if err == nil || !f.st.isStale() || !markerExists(f.st) || action != xrayApplyLive {
		t.Fatalf("action=%v err=%v stale=%v", action, err, f.st.isStale())
	}
	wantCalls(t, f, "read,live,install,discard")
	if f.disk != liveCfgBase {
		t.Fatal("disk must keep the old config")
	}
	// The runtime already has carol but the file does not: next sync restarts.
	f.installErr = nil
	if action, _, err := f.sync(liveCfgCarol); err != nil || action != xrayApplyRestart || f.st.isStale() {
		t.Fatalf("next sync: action=%v err=%v stale=%v", action, err, f.st.isStale())
	}
}

// A revoked user still online (soft cutoff) marks the runtime stale from outside
// the sync; a live apply finishing meanwhile must not clear it.
func TestXrayApplierStaleSetDuringLiveApplyIsKept(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	a := f.applier()
	a.applyLive = func(xraydiff.Result) error { f.st.markStale("revoked user still online"); return nil }
	f.tmp = liveCfgCarol
	if action, _, _, err := a.apply([]byte(liveCfgCarol), f.st); err != nil || action != xrayApplyLive {
		t.Fatalf("action=%v err=%v", action, err)
	}
	if !f.st.isStale() || !markerExists(f.st) {
		t.Fatal("stale set during the live apply must survive it")
	}
	if action, _, err := f.sync(liveCfgCarol); err != nil || action != xrayApplyRestart {
		t.Fatalf("next sync must restart: action=%v err=%v", action, err)
	}
}

func TestXrayApplierUnreadableRunningConfigRestarts(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	a := f.applier()
	a.readRunning = func() ([]byte, error) { return nil, errors.New("permission denied") }
	f.tmp = liveCfgBase
	action, _, _, err := a.apply([]byte(liveCfgBase), f.st)
	if err != nil || action != xrayApplyRestart {
		t.Fatalf("action=%v err=%v", action, err)
	}
	wantCalls(t, f, "install,restart")
}

func TestCleanupXrayLiveTempFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"live-api-123.json", "live-api-abc.json", "config.json", "config.tmp.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if n := cleanupXrayLiveTempFiles(dir); n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	for i := range left {
		left[i] = filepath.Base(left[i])
	}
	sort.Strings(left)
	if strings.Join(left, ",") != "config.json,config.tmp.json" {
		t.Fatalf("left %v", left)
	}
}

// A failed rename on the restart path leaves file and runtime untouched: with a
// clean in-memory state the marker written by begin("restart") is removed (no
// spurious restart after an API restart); a stale state keeps it.
func TestXrayApplierInstallFailureOnRestartPath(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	f.installErr = errors.New("EXDEV")
	if _, _, err := f.sync(liveCfgPort); err == nil {
		t.Fatal("rename failure must be returned")
	}
	wantCalls(t, f, "read,install,discard")
	if f.st.isStale() || markerExists(f.st) {
		t.Fatalf("structural + rename failure: stale=%v marker=%v, want clean", f.st.isStale(), markerExists(f.st))
	}
	if next := (&xrayRuntimeState{marker: f.st.marker}); next.load() {
		t.Fatal("an API restart now must not force a restart")
	}

	// Already stale (here: failed live apply) → the marker and the flag stay.
	f.liveErr = errors.New("adu failed")
	if _, liveErr, err := f.sync(liveCfgCarol); err == nil || liveErr == nil {
		t.Fatalf("liveErr=%v err=%v", liveErr, err)
	}
	if !f.st.isStale() || !markerExists(f.st) {
		t.Fatalf("stale + rename failure: stale=%v marker=%v, want both kept", f.st.isStale(), markerExists(f.st))
	}
}

// onRestart (revokedUsers.reset in production) runs after a successful restart
// and BEFORE the stale state is cleared, so a collector verdict based on the old
// watch list cannot re-mark the fresh runtime stale.
func TestXrayApplierOnRestartBeforeStaleCleared(t *testing.T) {
	f := newFakeXray(t, liveCfgBase)
	f.st.markStale("revoked user still online")
	var staleDuringHook []bool
	var causes []string
	a := f.applier()
	a.onRestart = func(cause string) {
		staleDuringHook = append(staleDuringHook, f.st.isStale())
		causes = append(causes, cause)
	}

	f.restartErr = errors.New("restart failed")
	f.tmp = liveCfgBase
	if _, _, _, err := a.apply([]byte(liveCfgBase), f.st); err == nil || len(staleDuringHook) != 0 {
		t.Fatalf("failed restart: err=%v hook calls=%v (want none)", err, staleDuringHook)
	}
	f.restartErr = nil
	f.tmp = liveCfgBase
	if _, _, _, err := a.apply([]byte(liveCfgBase), f.st); err != nil {
		t.Fatal(err)
	}
	if len(staleDuringHook) != 1 || !staleDuringHook[0] || f.st.isStale() {
		t.Fatalf("hook saw stale=%v, after apply stale=%v; want hook once while still stale", staleDuringHook, f.st.isStale())
	}
	// The failed restart relabelled the stale runtime: the retry says why.
	if len(causes) != 1 || causes[0] != xrayCauseRestartFailed {
		t.Fatalf("restart cause = %v, want [%s]", causes, xrayCauseRestartFailed)
	}
}

// Every restart path hands its cause to onRestart (recordXrayRestart labels the
// restart-storm alert with it); a successful restart clears the stale cause.
func TestXrayApplierRestartCauses(t *testing.T) {
	cause := func(f *fakeXray, newCfg string, setup func(a *xrayApplier)) string {
		t.Helper()
		var got []string
		a := f.applier()
		a.onRestart = func(c string) { got = append(got, c) }
		if setup != nil {
			setup(&a)
		}
		f.tmp = newCfg
		if _, _, _, err := a.apply([]byte(newCfg), f.st); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("onRestart calls = %v, want one", got)
		}
		return got[0]
	}

	f := newFakeXray(t, liveCfgBase)
	if got := cause(f, liveCfgPort, nil); got != "structural (changed inbounds)" {
		t.Errorf("structural: cause %q", got)
	}

	f = newFakeXray(t, liveCfgBase)
	f.liveErr = errors.New("adu failed")
	if got := cause(f, liveCfgCarol, nil); got != xrayCauseLiveFailed {
		t.Errorf("live failure: cause %q", got)
	}
	if f.st.isStale() || f.st.staleCause() != xrayCauseStale {
		t.Errorf("after the restart: stale=%v cause=%q, want clean", f.st.isStale(), f.st.staleCause())
	}

	f = newFakeXray(t, liveCfgBase)
	f.st.markStaleCause("revoked(blocked,quota)", "revoked user still online: a (blocked), b (quota)")
	if got := cause(f, liveCfgBase, nil); got != "revoked(blocked,quota)" {
		t.Errorf("revocation: cause %q", got)
	}

	f = newFakeXray(t, liveCfgBase)
	if got := cause(f, liveCfgBase, func(a *xrayApplier) {
		a.readRunning = func() ([]byte, error) { return nil, errors.New("EACCES") }
	}); got != "unreadable-config" {
		t.Errorf("unreadable running config: cause %q", got)
	}

	// A marker left by a previous process.
	f = newFakeXray(t, liveCfgBase)
	f.st.begin("restart")
	f.st = &xrayRuntimeState{marker: f.st.marker}
	if !f.st.load() {
		t.Fatal("marker not loaded")
	}
	if got := cause(f, liveCfgBase, nil); got != xrayCausePrevProcess {
		t.Errorf("previous process: cause %q", got)
	}
}
