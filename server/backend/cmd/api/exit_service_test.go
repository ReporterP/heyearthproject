package main

import (
	"bytes"
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/xraydiff"
)

// Spike C4: an exit out of service leaves every selector, keeps its outbounds (the tunnel
// probes use them), and the change is live for xraydiff — no restart.
func TestBuildXrayOutOfServiceExitLeavesSelectorsOnly(t *testing.T) {
	baseCfg, _ := genConfig(t, baseInputs(), quotaCutoff{})
	in := baseInputs()
	in.outOfService = map[int]bool{3: true}
	cfg, b := genConfig(t, in, quotaCutoff{})
	want := []string{
		"exit-balancer=exit-2.",
		"plan-3-balancer=exit-2.",
		"plan-6-balancer=exit-2.",
	}
	if got := balancerTags(t, cfg); !reflect.DeepEqual(got, want) {
		t.Errorf("balancers = %v, want %v", got, want)
	}
	if !strings.Contains(string(cfg), `"tag": "exit-3.vr"`) {
		t.Error("the exit's outbounds stay (the tunnel probe uses them)")
	}
	if b.exits != 2 {
		t.Errorf("the exit is still an exit of the config: %+v", b)
	}
	d, err := xraydiff.Classify(baseCfg, cfg)
	if err != nil || d.Kind != xraydiff.UserOnly || !d.RoutingChanged {
		t.Fatalf("taking an exit out is live: %v %v (%s)", d.Kind, err, d.Reason)
	}
}

func TestBuildXrayNoExitOutKeepsTheConfig(t *testing.T) {
	baseCfg, _ := genConfig(t, baseInputs(), quotaCutoff{})
	in := baseInputs()
	in.outOfService = map[int]bool{}
	cfg, _ := genConfig(t, in, quotaCutoff{})
	if !bytes.Equal(baseCfg, cfg) {
		t.Fatal("without an exit out of service the config is byte for byte the old one (no restart on deploy)")
	}
	in.outOfService = map[int]bool{7: true} // not an exit of this config
	if cfg, _ := genConfig(t, in, quotaCutoff{}); !bytes.Equal(baseCfg, cfg) {
		t.Fatal("an unknown id changes nothing")
	}
}

func TestBuildXrayAllExitsOutIsIgnored(t *testing.T) {
	baseCfg, _ := genConfig(t, baseInputs(), quotaCutoff{})
	in := baseInputs()
	in.outOfService = map[int]bool{2: true, 3: true}
	if cfg, _ := genConfig(t, in, quotaCutoff{}); !bytes.Equal(baseCfg, cfg) {
		t.Fatal("the generator never takes the last routed exit out")
	}
}

func TestExitOutNeverLast(t *testing.T) {
	cases := []struct {
		routed []int
		out    map[int]bool
		id     int
		ok     bool
	}{
		{[]int{2, 3}, nil, 3, true},
		{[]int{3}, nil, 3, false},                      // the only exit
		{[]int{2, 3}, map[int]bool{2: true}, 3, false}, // the other one is out already
		{[]int{2, 3}, map[int]bool{3: true}, 3, false}, // already out
		{[]int{2}, nil, 3, false},                      // not in the routing (down or offline)
	}
	for i, c := range cases {
		reason := exitOutBlocked(c.routed, c.out, c.id)
		if (reason == "") != c.ok {
			t.Errorf("#%d routed %v out %v id %d: %q", i, c.routed, c.out, c.id, reason)
		}
	}
}

type fakeExitSvc struct {
	routed   []int
	out      map[int]bool
	kind     xraydiff.Kind
	synced   int
	pid      string
	pidAfter string
}

func (f *fakeExitSvc) deps() exitDeps {
	return exitDeps{
		routed: func(context.Context) ([]int, error) { return f.routed, nil },
		outSet: func(context.Context) (map[int]bool, error) {
			m := map[int]bool{}
			for k, v := range f.out {
				m[k] = v
			}
			return m, nil
		},
		setOut: func(_ context.Context, id int, out bool, reason, by string) error {
			if f.out == nil {
				f.out = map[int]bool{}
			}
			f.out[id] = out
			return nil
		},
		classify: func(context.Context, map[int]bool) (xraydiff.Kind, string, error) {
			return f.kind, "changed outbounds", nil
		},
		sync: func() { f.synced++; f.pid = f.pidAfter },
		applied: func(_ context.Context, id int, out bool) (bool, error) {
			return f.out[id] == out, nil
		},
		xrayPID: func(context.Context) (string, error) { return f.pid, nil },
		synth:   func(context.Context) (string, string, error) { return "pass", "17 проверок", nil },
		online:  func(time.Time) onlineCounts { return onlineCounts{Xray: 40} },
		sleep:   func(time.Duration) {},
	}
}

func exitStand(t *testing.T, f *fakeExitSvc) *opsStand {
	t.Helper()
	s := newOpsStand(t, buildExitActions(f.deps())...)
	_ = s.store.setMode(context.Background(), "exit.out-of-service", opsModeOn, "owner")
	return s
}

func TestExitOutOfServiceIsLiveAndReturns(t *testing.T) {
	f := &fakeExitSvc{routed: []int{2, 3}, kind: xraydiff.UserOnly, pid: "4242", pidAfter: "4242"}
	s := exitStand(t, f)
	code, out := s.runAction(t, "exit.out-of-service", "3", "")
	if code != http.StatusAccepted || !f.out[3] || f.synced != 1 {
		t.Fatalf("out of service: %d %v, out %v, synced %d", code, out, f.out, f.synced)
	}
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsOK {
		t.Fatalf("live, xray not restarted, synthetic pass → ok: %+v", r)
	}
	if code, _ := s.runAction(t, "exit.out-of-service", "3", ""); code != http.StatusUnprocessableEntity && code != http.StatusTooManyRequests {
		t.Fatalf("already out → refused: %d", code)
	}
	code, out = s.runAction(t, "exit.return", "3", "")
	if code != http.StatusAccepted || f.out[3] {
		t.Fatalf("return: %d %v, out %v", code, out, f.out)
	}
}

func TestExitOutOfServiceRefusesAStructuralChange(t *testing.T) {
	f := &fakeExitSvc{routed: []int{2, 3}, kind: xraydiff.Structural}
	s := exitStand(t, f)
	code, out := s.runAction(t, "exit.out-of-service", "3", "")
	if code != http.StatusUnprocessableEntity || f.out[3] || f.synced != 0 {
		t.Fatalf("a change that would restart xray is refused before anything: %d %v", code, out)
	}
}

func TestExitOutOfServiceRefusesTheLast(t *testing.T) {
	f := &fakeExitSvc{routed: []int{3}, kind: xraydiff.UserOnly}
	s := exitStand(t, f)
	if code, _ := s.runAction(t, "exit.out-of-service", "3", ""); code != http.StatusUnprocessableEntity || f.out[3] {
		t.Fatalf("the last exit stays: %d", code)
	}
}

func TestExitOutOfServiceNoticesARestart(t *testing.T) {
	f := &fakeExitSvc{routed: []int{2, 3}, kind: xraydiff.UserOnly, pid: "4242", pidAfter: "5151"}
	s := exitStand(t, f)
	_, out := s.runAction(t, "exit.out-of-service", "3", "")
	r, _ := s.store.get(context.Background(), int64(out["run_id"].(float64)))
	if r.Status != opsPostFailed {
		t.Fatalf("xray restarted under the action → post_failed, the owner must know: %+v", r)
	}
}

func TestExitActionsClass(t *testing.T) {
	out, ret := opsActions["exit.out-of-service"], opsActions["exit.return"]
	if out.Impact != biz.ImpactK2 || requiredPerm(out) != "ops.disrupt" || out.Target != "exit" {
		t.Errorf("exit.out-of-service: %s %s %q", out.Impact, requiredPerm(out), out.Target)
	}
	if ret.Impact != biz.ImpactK0 || requiredPerm(ret) != "nodes.provision" || ret.Target != "exit" {
		t.Errorf("exit.return: %s %s %q", ret.Impact, requiredPerm(ret), ret.Target)
	}
}

// The check «балансировщики»: an exit taken out is in no selector; a returned exit is in them, or
// it is dead — then it stays out of every selector until its tunnel lives (xray_exits.go) and the
// return has nothing more to apply.
func TestExitApplied(t *testing.T) {
	for _, tc := range []struct {
		in, healthy, out, want bool
	}{
		{in: false, healthy: true, out: true, want: true},
		{in: true, healthy: true, out: true, want: false},
		{in: true, healthy: true, out: false, want: true},
		{in: false, healthy: true, out: false, want: false}, // the sync has not applied it yet
		{in: false, healthy: false, out: false, want: true}, // returned while dead
	} {
		if got := exitApplied(tc.in, tc.healthy, tc.out); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
	// A dead exit returned to service: no exit is out any more, yet the catch-all names the healthy
	// ones only — the returned exit is in no selector of the running config.
	in := baseInputs()
	in.exitNodes = append(in.exitNodes, exitFixture(5, 1, "unhealthy", true))
	cfg, _ := genConfig(t, in, quotaCutoff{})
	if inSel, err := exitInSelectors(cfg, 5); err != nil || inSel {
		t.Fatalf("a dead exit must be in no selector: %v %v", inSel, err)
	}
	if inSel, _ := exitInSelectors(cfg, 2); !inSel {
		t.Fatal("a healthy exit is in the selectors")
	}
}
