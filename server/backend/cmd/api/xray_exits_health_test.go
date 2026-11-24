package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/xraydiff"
)

// A dead exit (both tunnels down, tunnel_status unhealthy) keeps its outbounds and leaves only
// the balancer selectors: the health flip is live for xraydiff (adrules), not an xray restart.

// flipExits: Hysteria exits with a TCP backup (2, 3, 20), a Hysteria-only one (4) and a legacy
// VLESS one (5), all healthy. With exitsInputs: plan 3 → exit 2 (alice, bob), plan 4 → 20,
// plan 6 → 2, 3, 20, plan 7 → 7 (absent).
func flipExits() []biz.ExitNodeTunnel {
	return []biz.ExitNodeTunnel{
		exitFixture(2, 1, "healthy", true), exitFixture(3, 5, "healthy", true), exitFixture(4, 5, "healthy", false),
		legacyVLESSExit(5), exitFixture(20, 10, "healthy", true),
	}
}

func withTunnelStatus(nodes []biz.ExitNodeTunnel, status string, ids ...int) []biz.ExitNodeTunnel {
	out := slices.Clone(nodes)
	for i := range out {
		if slices.Contains(ids, out[i].ID) {
			out[i].TunnelStatus = status
		}
	}
	return out
}

func renderFull(t *testing.T, in xrayInputs) ([]byte, xrayBuild) {
	t.Helper()
	b, cfg, err := renderXrayConfig(in, quotaCutoff{})
	if err != nil {
		t.Fatalf("renderXrayConfig: %v", err)
	}
	return cfg, b
}

// The whole config as the code before this change wrote it (goldens taken from it): with every
// exit healthy — also with one out of service — and with none healthy (the no-exit block) the
// file is byte for byte the same, so the deploy changes nothing in xray.
func TestXrayExitReferenceConfigs(t *testing.T) {
	out := exitsInputs(flipExits()...)
	out.outOfService = map[int]bool{3: true}
	for name, in := range map[string]xrayInputs{
		"xray_full_exits_healthy":         exitsInputs(flipExits()...),
		"xray_full_exits_healthy_one_out": out,
		"xray_full_exits_none_healthy":    exitsInputs(withTunnelStatus(flipExits(), "unhealthy", 2, 3, 4, 5, 20)...),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, _ := renderFull(t, in)
			checkGolden(t, name, cfg)
		})
	}
}

// Every exit dying alone, two at once, and each coming back: live (balancers and plan-* rules
// only), the outbounds and the observatory as they were.
func TestXrayExitHealthFlipIsLive(t *testing.T) {
	healthy := mustGen(t, exitsInputs(flipExits()...))
	for _, dead := range [][]int{{2}, {3}, {4}, {5}, {20}, {2, 20}, {3, 4, 5}} {
		t.Run(fmt.Sprint(dead), func(t *testing.T) {
			down := mustGen(t, exitsInputs(withTunnelStatus(flipExits(), "unhealthy", dead...)...))
			for _, step := range []struct {
				name     string
				from, to []byte
			}{{"dies", healthy, down}, {"returns", down, healthy}} {
				d, err := xraydiff.Classify(step.from, step.to)
				if err != nil || d.Kind != xraydiff.UserOnly || !d.RoutingChanged || len(d.Inbounds) != 0 {
					t.Fatalf("%s: kind %v (%v, %s), routing changed %v, users %d — want a live routing change only",
						step.name, d.Kind, err, d.Reason, d.RoutingChanged, len(d.Inbounds))
				}
			}
			hv, dv := viewOf(t, healthy), viewOf(t, down)
			if !slices.Equal(hv.outboundTags(), dv.outboundTags()) {
				t.Errorf("outbounds changed: %v → %v", hv.outboundTags(), dv.outboundTags())
			}
			if !slices.Equal(dv.Observatory.SubjectSelector, []string{exitAllSelector}) {
				t.Errorf("observatory %v", dv.Observatory.SubjectSelector)
			}
		})
	}
	// With an exit out of service as well (exit_service.go): still live.
	out := exitsInputs(flipExits()...)
	out.outOfService = map[int]bool{2: true}
	outDown := out
	outDown.exitNodes = withTunnelStatus(flipExits(), "unhealthy", 3)
	if d, err := xraydiff.Classify(mustGen(t, out), mustGen(t, outDown)); err != nil || d.Kind != xraydiff.UserOnly {
		t.Fatalf("a flip next to an exit out of service: %v (%v, %s)", d.Kind, err, d.Reason)
	}
}

// A dead exit's outbounds stay, but no balancer selects them, none falls back to them, no cost
// names them; the catch-all lists the healthy exits by name.
func TestXrayExitDeadOutsideEverySelector(t *testing.T) {
	nodes := append(withTunnelStatus(flipExits(), "unhealthy", 3), exitFixture(7, 1, "unhealthy", true))
	cfg, b := genConfig(t, exitsInputs(nodes...), quotaCutoff{}) // the leak guard runs inside
	if b.noExit || b.exits != 4 {
		t.Fatalf("routed exits %d, no-exit %v; want the 4 healthy ones", b.exits, b.noExit)
	}
	v := viewOf(t, cfg)
	for _, tag := range []string{"exit-3.hy", "exit-3.vr", "exit-7.hy", "exit-7.vr", "probe-vr-3", "probe-vr-7"} {
		if byTag(v.Outbounds, tag) == nil {
			t.Errorf("%s: a dead exit keeps its outbounds", tag)
		}
	}
	var exitTags []string
	for _, tag := range v.outboundTags() {
		if strings.HasPrefix(tag, exitAllSelector) {
			exitTags = append(exitTags, tag)
		}
	}
	dead := func(tag string) bool { return strings.HasPrefix(tag, "exit-3.") || strings.HasPrefix(tag, "exit-7.") }
	for _, bal := range v.Routing.Balancers {
		for _, tag := range xraySelect(exitTags, bal.Selector) {
			if dead(tag) {
				t.Errorf("%s selects the dead path %s (selector %v)", bal.Tag, tag, bal.Selector)
			}
		}
		if dead(bal.FallbackTag) {
			t.Errorf("%s falls back to the dead path %s", bal.Tag, bal.FallbackTag)
		}
		for tag := range bal.costsByTag(t, exitTags) {
			if dead(tag) {
				t.Errorf("%s has a cost for the dead path %s", bal.Tag, tag)
			}
		}
	}
	if got := v.balancer(t, "exit-balancer").Selector; !slices.Equal(got, []string{"exit-2.", "exit-4.", "exit-5.", "exit-20."}) {
		t.Errorf("catch-all selector %v, want the healthy exits by name", got)
	}
	if got := v.balancer(t, "plan-6-balancer").Selector; !slices.Equal(got, []string{"exit-2.", "exit-20."}) {
		t.Errorf("plan 6 selector %v", got)
	}
	if !slices.Equal(v.Observatory.SubjectSelector, []string{exitAllSelector}) {
		t.Errorf("observatory %v", v.Observatory.SubjectSelector)
	}
}

// The last healthy exit dying is still the no-exit block (a structural change: the block rules and
// dns-hijack, no balancers, no observatory) and the block config carries no exit path at all, as
// before; coming back from it is structural too.
func TestXrayExitLastHealthyDiesIsTheNoExitBlock(t *testing.T) {
	oneLeft := mustGen(t, exitsInputs(withTunnelStatus(flipExits(), "unhealthy", 2, 3, 4, 5)...))
	cfg, b := genConfig(t, exitsInputs(withTunnelStatus(flipExits(), "unhealthy", 2, 3, 4, 5, 20)...), quotaCutoff{})
	if !b.noExit || b.exits != 0 {
		t.Fatalf("no healthy exit must be the no-exit mode: %+v", b)
	}
	for _, step := range [][2][]byte{{oneLeft, cfg}, {cfg, oneLeft}} {
		if d, err := xraydiff.Classify(step[0], step[1]); err != nil || d.Kind != xraydiff.Structural {
			t.Fatalf("into / out of the no-exit block: %v (%v)", d.Kind, err)
		}
	}
	v := viewOf(t, cfg)
	for _, tag := range v.outboundTags() {
		if strings.HasPrefix(tag, exitAllSelector) {
			t.Errorf("the no-exit block carries the exit path %s", tag)
		}
	}
	if len(v.Routing.Balancers) != 0 || v.Observatory.SubjectSelector != nil {
		t.Errorf("no-exit: balancers %v, observatory %v", v.Routing.Balancers, v.Observatory.SubjectSelector)
	}
	if byTag(v.Outbounds, "probe-vr-20") == nil {
		t.Error("probe pairs survive the no-exit mode")
	}
}

// The leak guard: a balancer may fall back only to block or to an exit path its own selector
// takes — never to a path outside it (a dead exit's outbound is in the config now).
func TestCheckNoDirectLeakFallbackInsideSelector(t *testing.T) {
	fx := newLeakFixture()
	outbounds := append(append([]map[string]interface{}{}, fx.good...), map[string]interface{}{"protocol": "socks", "tag": "exit-3.hy"})
	rules := []map[string]interface{}{dnsDirectRule(), {"type": "field", "network": "tcp,udp", "balancerTag": "exit-balancer"}}
	ok := newBalancer("exit-balancer", []string{"exit-2."}, "exit-2.hy", nil)
	if err := checkNoDirectLeak(fx.inbounds, outbounds, routingWith(rules, ok)); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	outside := newBalancer("exit-balancer", []string{"exit-2."}, "exit-3.hy", nil)
	if err := checkNoDirectLeak(fx.inbounds, outbounds, routingWith(rules, outside)); err == nil || !strings.Contains(err.Error(), "selector") {
		t.Fatalf("a fallback outside the balancer's selector accepted: %v", err)
	}
}

// The deploy tool's prediction: a config that differs only by an exit's health is «xray не
// изменится» (К1).
func TestPredictExitHealthFlipIsK1(t *testing.T) {
	healthy := mustGen(t, exitsInputs(flipExits()...))
	down := mustGen(t, exitsInputs(withTunnelStatus(flipExits(), "unhealthy", 3)...))
	p := predictWith(predictSources{
		xrayLive:   func() ([]byte, error) { return healthy, nil },
		xrayNew:    func() ([]byte, error) { return down, nil },
		bambooLive: func() ([]byte, error) { return []byte("yaml\n"), nil },
		bambooNew:  func([]byte) (string, bool, error) { return "yaml\n", true, nil },
	})
	class, lines := predictionClass(p)
	if p.Xray != "user-only" || class != "K1" || !slices.Contains(lines, "xray не изменится") {
		t.Fatalf("prediction %+v: class %s, lines %q", p, class, lines)
	}
}
