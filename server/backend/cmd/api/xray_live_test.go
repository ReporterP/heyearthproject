package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/xraydiff"
)

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
	liveErr    error // applyLive result
	installErr error // rename result
	restartErr error // systemctl restart result
	calls      []string
}

func (f *fakeXray) applier() xrayApplier {
	return xrayApplier{
		readRunning: func() ([]byte, error) { f.calls = append(f.calls, "read"); return []byte(f.disk), nil },
		applyLive: func(d xraydiff.Result) error {
			f.calls = append(f.calls, "live")
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
		restart: func() error { f.calls = append(f.calls, "restart"); return f.restartErr },
	}
}

// sync mimics one SyncXrayConfig tail: tmp written + validated, then apply.
func (f *fakeXray) sync(newCfg string, stale *bool) (xrayApplyAction, error, error) {
	f.calls, f.tmp = nil, newCfg
	action, _, liveErr, err := f.applier().apply([]byte(newCfg), stale)
	return action, liveErr, err
}

func wantCalls(t *testing.T, f *fakeXray, want string) {
	t.Helper()
	if got := strings.Join(f.calls, ","); got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestXrayApplierUnchangedAndLive(t *testing.T) {
	f := &fakeXray{disk: liveCfgBase}
	stale := false

	if action, liveErr, err := f.sync(liveCfgBase, &stale); err != nil || liveErr != nil || action != xrayApplyUnchanged {
		t.Fatalf("unchanged: action=%v liveErr=%v err=%v", action, liveErr, err)
	}
	wantCalls(t, f, "read,discard")

	if action, liveErr, err := f.sync(liveCfgCarol, &stale); err != nil || liveErr != nil || action != xrayApplyLive {
		t.Fatalf("user-only: action=%v liveErr=%v err=%v", action, liveErr, err)
	}
	wantCalls(t, f, "read,live,install") // no restart
	if stale || f.disk != liveCfgCarol {
		t.Fatalf("after live apply: stale=%v, disk updated=%v", stale, f.disk == liveCfgCarol)
	}

	if action, _, err := f.sync(liveCfgPort, &stale); err != nil || action != xrayApplyRestart {
		t.Fatalf("structural: action=%v err=%v", action, err)
	}
	wantCalls(t, f, "read,install,restart")
}

func TestXrayApplierLiveFailureFallsBackToRestart(t *testing.T) {
	f := &fakeXray{disk: liveCfgBase, liveErr: errors.New("adu: added 0 of 1")}
	stale := false
	action, liveErr, err := f.sync(liveCfgCarol, &stale)
	if err != nil || liveErr == nil || action != xrayApplyRestart {
		t.Fatalf("action=%v liveErr=%v err=%v", action, liveErr, err)
	}
	wantCalls(t, f, "read,live,install,restart")
	if stale {
		t.Fatal("a successful restart must clear the stale flag")
	}
}

// The review finding of Task 4: a failed restart after the file was renamed must
// not turn into "unchanged" on the next sync.
func TestXrayApplierFailedRestartStaysStale(t *testing.T) {
	f := &fakeXray{disk: liveCfgBase, restartErr: errors.New("restart xray: exit status 1")}
	stale := false
	if _, _, err := f.sync(liveCfgPort, &stale); err == nil || !stale {
		t.Fatalf("failed restart: err=%v stale=%v", err, stale)
	}
	if f.disk != liveCfgPort {
		t.Fatal("the validated config must already be installed")
	}

	// Same config again: the file is unchanged, but the runtime is not → restart.
	if _, _, err := f.sync(liveCfgPort, &stale); err == nil || !stale {
		t.Fatalf("retry while restart still fails: err=%v stale=%v", err, stale)
	}
	wantCalls(t, f, "install,restart") // no classification while stale

	f.restartErr = nil
	if action, _, err := f.sync(liveCfgPort, &stale); err != nil || action != xrayApplyRestart || stale {
		t.Fatalf("recovery: action=%v err=%v stale=%v", action, err, stale)
	}
	wantCalls(t, f, "install,restart")

	if action, _, err := f.sync(liveCfgPort, &stale); err != nil || action != xrayApplyUnchanged {
		t.Fatalf("after recovery: action=%v err=%v", action, err)
	}
	wantCalls(t, f, "read,discard")
}

func TestXrayApplierLiveFailureAndRestartFailure(t *testing.T) {
	f := &fakeXray{disk: liveCfgBase, liveErr: errors.New("rmu failed"), restartErr: errors.New("restart failed")}
	stale := false
	if _, liveErr, err := f.sync(liveCfgCarol, &stale); err == nil || liveErr == nil || !stale {
		t.Fatalf("liveErr=%v err=%v stale=%v", liveErr, err, stale)
	}
	// Even an unchanged config is restarted while stale.
	f.liveErr, f.restartErr = nil, nil
	if action, _, err := f.sync(liveCfgCarol, &stale); err != nil || action != xrayApplyRestart || stale {
		t.Fatalf("action=%v err=%v stale=%v", action, err, stale)
	}
	wantCalls(t, f, "install,restart")
}

func TestXrayApplierRenameAfterLiveApplyFails(t *testing.T) {
	f := &fakeXray{disk: liveCfgBase, installErr: errors.New("EXDEV")}
	stale := false
	action, _, err := f.sync(liveCfgCarol, &stale)
	if err == nil || !stale || action != xrayApplyLive {
		t.Fatalf("action=%v err=%v stale=%v", action, err, stale)
	}
	wantCalls(t, f, "read,live,install,discard")
	if f.disk != liveCfgBase {
		t.Fatal("disk must keep the old config")
	}
	// The runtime already has carol but the file does not: next sync restarts.
	f.installErr = nil
	if action, _, err := f.sync(liveCfgCarol, &stale); err != nil || action != xrayApplyRestart || stale {
		t.Fatalf("next sync: action=%v err=%v stale=%v", action, err, stale)
	}
}

func TestXrayApplierUnreadableRunningConfigRestarts(t *testing.T) {
	f := &fakeXray{disk: liveCfgBase}
	a := f.applier()
	a.readRunning = func() ([]byte, error) { return nil, errors.New("permission denied") }
	f.tmp = liveCfgBase
	action, _, _, err := a.apply([]byte(liveCfgBase), new(bool))
	if err != nil || action != xrayApplyRestart {
		t.Fatalf("action=%v err=%v", action, err)
	}
	wantCalls(t, f, "install,restart")
}
