package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/xraydiff"
)

// exitFixture is an exit as an older cmd/deploy --setup-exit-node wrote it: Hysteria on 50443 plus
// the exit's VLESS Reality credentials (SNI microsoft.com; deploy now writes config.ExitVRSNI);
// withVR=false is an older row without them.
func exitFixture(id, weight int, status string, withVR bool) biz.ExitNodeTunnel {
	n := testExit(id, "hysteria2")
	n.TunnelPort, n.TunnelSNI, n.LoadWeight, n.TunnelStatus, n.LifecycleState = 50443, "microsoft.com", weight, status, "online"
	if !withVR {
		n.TunnelUUID, n.TunnelPubKey, n.TunnelShortID = "", "", ""
	}
	return n
}

// legacyVLESSExit is a primary VLESS exit from the old admin form: its own port, no Hysteria.
func legacyVLESSExit(id int) biz.ExitNodeTunnel {
	n := testExit(id, "vless")
	n.TunnelPort = 8443
	return n
}

// exitsInputs: plan 3 → exit 2, plan 4 → exit 20, plan 6 → exits 2, 3, 20, plan 7 → exit 7.
func exitsInputs(exits ...biz.ExitNodeTunnel) xrayInputs {
	in := baseInputs()
	in.exitNodes = exits
	in.planServers = map[int][]int{3: {2}, 4: {20}, 6: {2, 3, 20}, 7: {7}}
	return in
}

// multiExits: ids 2 and 20 (prefix trap), weights 1/5/10, exit 7 online but unhealthy.
func multiExits() []biz.ExitNodeTunnel {
	return []biz.ExitNodeTunnel{
		exitFixture(2, 1, "healthy", true),
		exitFixture(3, 5, "healthy", true),
		exitFixture(7, 1, "unhealthy", true),
		exitFixture(20, 10, "healthy", true),
	}
}

type xrayCost struct {
	Regexp bool    `json:"regexp"`
	Match  string  `json:"match"`
	Value  float64 `json:"value"`
}

type xrayBalancerView struct {
	Tag         string   `json:"tag"`
	Selector    []string `json:"selector"`
	FallbackTag string   `json:"fallbackTag"`
	Strategy    struct {
		Type     string `json:"type"`
		Settings struct {
			Costs     []xrayCost `json:"costs"`
			Baselines []string   `json:"baselines"`
			Expected  int32      `json:"expected"`
		} `json:"settings"`
	} `json:"strategy"`
}

type xrayView struct {
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Routing   struct {
		Rules     []map[string]any   `json:"rules"`
		Balancers []xrayBalancerView `json:"balancers"`
	} `json:"routing"`
	Observatory struct {
		SubjectSelector   []string `json:"subjectSelector"`
		EnableConcurrency bool     `json:"enableConcurrency"`
	} `json:"observatory"`
}

func viewOf(t *testing.T, cfg []byte) xrayView {
	t.Helper()
	var v xrayView
	if err := json.Unmarshal(cfg, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (v xrayView) outboundTags() []string {
	tags := make([]string, 0, len(v.Outbounds))
	for _, o := range v.Outbounds {
		tag, _ := o["tag"].(string)
		tags = append(tags, tag)
	}
	return tags
}

func (v xrayView) balancer(t *testing.T, tag string) xrayBalancerView {
	t.Helper()
	for _, b := range v.Routing.Balancers {
		if b.Tag == tag {
			return b
		}
	}
	t.Fatalf("balancer %s not generated", tag)
	return xrayBalancerView{}
}

// costsByTag maps every tag of tags that a cost entry of b matches to that cost.
func (b xrayBalancerView) costsByTag(t *testing.T, tags []string) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	for _, c := range b.Strategy.Settings.Costs {
		if !c.Regexp || !strings.HasPrefix(c.Match, "^") || !strings.HasSuffix(c.Match, "$") {
			t.Errorf("cost %q must be an anchored regexp (a plain match is a substring match)", c.Match)
		}
		re := regexp.MustCompile(c.Match)
		var hit []string
		for _, tag := range tags {
			if re.MatchString(tag) {
				hit = append(hit, tag)
			}
		}
		if len(hit) != 1 {
			t.Errorf("%s: cost %q matches %v, want exactly one tag", b.Tag, c.Match, hit)
			continue
		}
		got[hit[0]] = c.Value
	}
	return got
}

func byTag(items []map[string]any, tag string) map[string]any {
	for _, it := range items {
		if it["tag"] == tag {
			return it
		}
	}
	return nil
}

func asJSON(x any) string { b, _ := json.Marshal(x); return string(b) }

// xraySelect mirrors Xray-core 26.3.27 app/proxyman/outbound Manager.Select: an outbound is
// selected when one of the selectors is a PREFIX of its tag (there is no exact or regexp form).
func xraySelect(tags, selectors []string) []string {
	var out []string
	for _, tag := range tags {
		for _, s := range selectors {
			if strings.HasPrefix(tag, s) {
				out = append(out, tag)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// resetExitNotes gives the test a fresh "last logged" state of buildExitConfig.
func resetExitNotes(t *testing.T) {
	t.Helper()
	old := exitNotes
	exitNotes = &exitNoteLog{}
	t.Cleanup(func() { exitNotes = old })
}

// Review Focus 1 → with exits 2, 3 and 20 a plan bound to exit 2 never selects exit-20.*; the
// catch-all balancer and the observatory take every exit path and nothing else (no probe-vr-*).
func TestXrayExitSelectorsAreExact(t *testing.T) {
	cfg, b := genConfig(t, exitsInputs(multiExits()...), quotaCutoff{})
	v := viewOf(t, cfg)
	tags := v.outboundTags()
	all := []string{"exit-2.hy", "exit-2.vr", "exit-20.hy", "exit-20.vr", "exit-3.hy", "exit-3.vr"}
	want := map[string][]string{
		"exit-balancer":   all,
		"plan-3-balancer": {"exit-2.hy", "exit-2.vr"},
		"plan-4-balancer": {"exit-20.hy", "exit-20.vr"},
		"plan-6-balancer": all,
	}
	got := map[string][]string{}
	for _, bal := range v.Routing.Balancers {
		sel := xraySelect(tags, bal.Selector)
		got[bal.Tag] = sel
		if !slices.Contains(sel, bal.FallbackTag) {
			t.Errorf("%s: fallbackTag %q is not one of its own exit paths %v", bal.Tag, bal.FallbackTag, sel)
		}
		if bal.Strategy.Type != "leastLoad" {
			t.Errorf("%s: strategy %q, want leastLoad (the Hysteria-first order and weights live in its costs)", bal.Tag, bal.Strategy.Type)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("balancer selections:\n got %v\nwant %v", got, want)
	}
	if obs := xraySelect(tags, v.Observatory.SubjectSelector); !reflect.DeepEqual(obs, all) {
		t.Errorf("observatory probes %v, want exactly the exit paths %v", obs, all)
	}
	if b.exits != 3 {
		t.Errorf("routed exits = %d, want 3 (exit 7 is unhealthy)", b.exits)
	}
	// The pre-phase-1 form is exactly the bug: selector "exit-2" also takes exit-20.
	if old := xraySelect([]string{"exit-2", "exit-20"}, []string{"exit-2"}); len(old) != 2 {
		t.Fatal("xraySelect must model prefix selection")
	}
}

// Without enableConcurrency xray probes one path per probeInterval: with six paths each would be
// seen every three minutes, and leastLoad would route on stale verdicts.
func TestXrayExitObservatoryProbesConcurrently(t *testing.T) {
	cfg, _ := genConfig(t, exitsInputs(multiExits()...), quotaCutoff{})
	if v := viewOf(t, cfg); !v.Observatory.EnableConcurrency {
		t.Fatalf("observatory.enableConcurrency must be true: %+v", v.Observatory)
	}
}

// Weights and the Hysteria-first order come from leastLoad costs, never from duplicated tags.
func TestXrayExitCostsAndNoDuplicateTags(t *testing.T) {
	cfg, _ := genConfig(t, exitsInputs(multiExits()...), quotaCutoff{})
	v := viewOf(t, cfg)
	seen := map[string]bool{}
	for _, tag := range v.outboundTags() {
		if seen[tag] {
			t.Errorf("outbound tag %q is duplicated (load_weight must not duplicate tags)", tag)
		}
		seen[tag] = true
	}
	// Relative to the best priority of the balancer (weight 10): gap 0 → 1, gap 5 → (4·5)² = 400,
	// gap 9 → (4·9)² = 1296; the TCP path of a Hysteria exit costs ×400.
	want := map[string]float64{"exit-20.hy": 1, "exit-20.vr": 400, "exit-3.hy": 400, "exit-3.vr": 160000, "exit-2.hy": 1296, "exit-2.vr": 518400}
	tags := slices.Sorted(maps.Keys(want))
	for _, bal := range []string{"exit-balancer", "plan-6-balancer"} {
		if got := v.balancer(t, bal).costsByTag(t, tags); !reflect.DeepEqual(got, want) {
			t.Errorf("%s costs = %v, want %v", bal, got, want)
		}
	}
	// A balancer of one exit: that exit is its best priority, whatever its load_weight.
	if got := v.balancer(t, "plan-3-balancer").costsByTag(t, tags); !reflect.DeepEqual(got, map[string]float64{"exit-2.hy": 1, "exit-2.vr": 400}) {
		t.Errorf("plan-3 costs = %v", got)
	}
}

// The single path of a VLESS-only exit costs as its priority: no ×400, it is not a fallback.
func TestXrayExitVROnlyCost(t *testing.T) {
	legacy := legacyVLESSExit(3)
	legacy.LoadWeight = 4
	cfg, _ := genConfig(t, exitsInputs(exitFixture(2, 5, "healthy", true), legacy), quotaCutoff{})
	got := viewOf(t, cfg).balancer(t, "exit-balancer").costsByTag(t, []string{"exit-2.hy", "exit-2.vr", "exit-3.vr"})
	if want := map[string]float64{"exit-2.hy": 1, "exit-2.vr": 400, "exit-3.vr": 16}; !reflect.DeepEqual(got, want) {
		t.Fatalf("costs = %v, want %v (exit-3.vr: one priority step below, no TCP factor)", got, want)
	}
	if c := exitCosts([]exitRoute{{id: 5, weight: 1, hasVR: true, primary: "exit-5.vr"}}); asJSON(c) != `[{"match":"^exit-5\\.vr$","regexp":true,"value":1}]` {
		t.Fatalf("a lone VLESS-only exit: %s", asJSON(c))
	}
}

// The strategy settings every balancer carries (exitBaselines explains the values).
func TestXrayExitBalancerStrategyPinned(t *testing.T) {
	cfg, _ := genConfig(t, exitsInputs(multiExits()...), quotaCutoff{})
	v := viewOf(t, cfg)
	if len(v.Routing.Balancers) != 4 {
		t.Fatalf("balancers: %d", len(v.Routing.Balancers))
	}
	for _, b := range v.Routing.Balancers {
		s := b.Strategy.Settings
		if want := []string{"50ms", "100ms", "200ms", "400ms", "800ms", "1600ms"}; !reflect.DeepEqual(s.Baselines, want) {
			t.Errorf("%s: baselines %v, want %v", b.Tag, s.Baselines, want)
		}
		if s.Expected != 1 {
			t.Errorf("%s: expected %d, want 1 (else no path under the last baseline = fallbackTag)", b.Tag, s.Expected)
		}
	}
	for _, tc := range []struct {
		gap, cost int
	}{{0, 1}, {1, 16}, {2, 64}, {4, 256}, {9, 1296}} {
		if got := exitTierCost(tc.gap); got != tc.cost {
			t.Errorf("exitTierCost(%d) = %d, want %d", tc.gap, got, tc.cost)
		}
	}
}

// ─────────────── leastLoad as xray runs it ───────────────

// leastLoadSelect reproduces, for one balancer of a generated config, the selection of
// Xray-core v1.260327.0 (26.3.27) leastLoad with the plain observatory:
// app/router/strategy_leastload.go getNodes (alive candidates only; RTTDeviationCost =
// Duration(Delay × √cost)), leastloadSort and selectLeastLoad, with the cost lookup of
// app/router/weight.go (first matching entry; regexp → FindString, plain → substring; default 1).
// alive maps a path to its probe delay in ms, a path missing from it is dead. xray picks one of
// the returned paths at random per connection; none means every connection takes fallbackTag.
func leastLoadSelect(t *testing.T, v xrayView, balancer string, alive map[string]int64) []string {
	t.Helper()
	b := v.balancer(t, balancer)
	s := b.Strategy.Settings
	cost := func(tag string) float64 {
		for _, c := range s.Costs {
			matched := ""
			if c.Regexp {
				matched = regexp.MustCompile(c.Match).FindString(tag)
			} else if strings.Contains(tag, c.Match) {
				matched = c.Match
			}
			if matched == "" {
				continue
			}
			if c.Value <= 0 {
				t.Fatalf("cost %q has no positive value (xray would derive one from the tag)", c.Match)
			}
			return float64(float32(c.Value)) // StrategyWeight.value is a float32
		}
		return 1
	}
	type node struct {
		tag       string
		avg, cost time.Duration
	}
	var nodes []node
	for _, tag := range xraySelect(v.outboundTags(), b.Selector) {
		if ms, ok := alive[tag]; ok {
			d := time.Duration(ms) * time.Millisecond
			nodes = append(nodes, node{tag, d, time.Duration(float64(d) * math.Pow(cost(tag), 0.5))})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { // CountFail/CountAll are equal (1) without burst
		if nodes[i].cost != nodes[j].cost {
			return nodes[i].cost < nodes[j].cost
		}
		if nodes[i].avg != nodes[j].avg {
			return nodes[i].avg < nodes[j].avg
		}
		return nodes[i].tag < nodes[j].tag
	})
	tags := func(sel []node) []string {
		out := make([]string, 0, len(sel))
		for _, n := range sel {
			out = append(out, n.tag)
		}
		sort.Strings(out)
		return out
	}
	// selectLeastLoad
	if len(nodes) == 0 {
		return nil
	}
	expected := int(s.Expected)
	if expected > len(nodes) {
		return tags(nodes)
	}
	if expected <= 0 {
		expected = 1
	}
	if len(s.Baselines) == 0 {
		return tags(nodes[:expected])
	}
	count := 0
	for _, raw := range s.Baselines {
		baseline, err := time.ParseDuration(raw)
		if err != nil || baseline <= 0 {
			t.Fatalf("baseline %q", raw)
		}
		for i := count; i < len(nodes); i++ {
			if nodes[i].cost >= baseline {
				break
			}
			count = i + 1
		}
		if count >= expected {
			break
		}
	}
	if s.Expected > 0 && count < expected {
		count = expected
	}
	return tags(nodes[:count])
}

// Load sharing and priorities of the generated balancers, as xray 26.3.27 selects.
func TestXrayExitLeastLoadSelection(t *testing.T) {
	// pair: exit 2 with priority w2 and exit 3 with priority 1 (gap w2-1 below exit 2).
	pair := func(w2 int) xrayView {
		return viewOf(t, mustGen(t, exitsInputs(exitFixture(2, w2, "healthy", true), exitFixture(3, 1, "healthy", true))))
	}
	equal, prio := pair(1), pair(5)
	for _, tc := range []struct {
		name     string
		v        xrayView
		balancer string
		alive    map[string]int64
		want     []string
	}{
		{"two equal exits at 60/70 ms share the load; their TCP paths stay out", equal, "exit-balancer",
			map[string]int64{"exit-2.hy": 60, "exit-3.hy": 70, "exit-2.vr": 80, "exit-3.vr": 90},
			[]string{"exit-2.hy", "exit-3.hy"}},
		{"equal exits at realistic 150/190 ms share the load too", equal, "exit-balancer",
			map[string]int64{"exit-2.hy": 150, "exit-3.hy": 190, "exit-2.vr": 160, "exit-3.vr": 200},
			[]string{"exit-2.hy", "exit-3.hy"}},
		{"priority 5 over priority 1: only the preferred while it lives, even if the other is faster", prio, "exit-balancer",
			map[string]int64{"exit-2.hy": 150, "exit-3.hy": 100, "exit-2.vr": 160, "exit-3.vr": 110},
			[]string{"exit-2.hy"}},
		{"preferred exit dead: the reserve takes over", prio, "exit-balancer",
			map[string]int64{"exit-3.hy": 100, "exit-3.vr": 110},
			[]string{"exit-3.hy"}},
		{"preferred exit 16× slower (4g, g = 4): the reserve shares", prio, "exit-balancer",
			map[string]int64{"exit-2.hy": 1000, "exit-3.hy": 60},
			[]string{"exit-2.hy", "exit-3.hy"}},
		{"band edge: equal exits at 190/210 ms do not share at that moment", equal, "exit-balancer",
			map[string]int64{"exit-2.hy": 190, "exit-3.hy": 210},
			[]string{"exit-2.hy"}},
		{"priorities 2/1 at 101 vs 99 ms: the preferred alone", pair(2), "exit-balancer",
			map[string]int64{"exit-2.hy": 101, "exit-3.hy": 99, "exit-2.vr": 105, "exit-3.vr": 100},
			[]string{"exit-2.hy"}},
		{"priorities 2/1, preferred at 250 vs reserve at 90 ms (2.8×): the reserve joins", pair(2), "exit-balancer",
			map[string]int64{"exit-2.hy": 250, "exit-3.hy": 90},
			[]string{"exit-2.hy", "exit-3.hy"}},
		{"priorities 2/1, 250 vs 100 ms: 4×100 is not below the 400 ms edge, the preferred alone", pair(2), "exit-balancer",
			map[string]int64{"exit-2.hy": 250, "exit-3.hy": 100},
			[]string{"exit-2.hy"}},
		{"preferred Hysteria dead, reserve 2 steps lower, equal RTT: the reserve's Hysteria", pair(3), "exit-balancer",
			map[string]int64{"exit-2.vr": 100, "exit-3.hy": 100, "exit-3.vr": 100},
			[]string{"exit-3.hy"}},
		{"preferred Hysteria dead, reserve 6 steps lower: the preferred TCP", pair(7), "exit-balancer",
			map[string]int64{"exit-2.vr": 100, "exit-3.hy": 100},
			[]string{"exit-2.vr"}},
		{"preferred Hysteria dead, reserve 6 steps lower, 60 ms: the reserve joins in the band", pair(7), "exit-balancer",
			map[string]int64{"exit-2.vr": 60, "exit-3.hy": 60},
			[]string{"exit-2.vr", "exit-3.hy"}},
		{"Review Focus 3: Hysteria of the plan's exit dead → its TCP path", viewOf(t, mustGen(t, exitsInputs(multiExits()...))), "plan-3-balancer",
			map[string]int64{"exit-2.vr": 200, "exit-20.hy": 50, "exit-20.vr": 60},
			[]string{"exit-2.vr"}},
		{"one exit, Hysteria dead: TCP although 20× its RTT is past every baseline", equal, "plan-3-balancer",
			map[string]int64{"exit-2.vr": 300},
			[]string{"exit-2.vr"}},
		{"one exit, both paths alive: Hysteria only", equal, "plan-3-balancer",
			map[string]int64{"exit-2.hy": 300, "exit-2.vr": 150},
			[]string{"exit-2.hy"}},
		{"one exit, Hysteria 9× slower than TCP: still Hysteria alone (TCP joins only beyond 10×)", equal, "plan-3-balancer",
			map[string]int64{"exit-2.hy": 900, "exit-2.vr": 100},
			[]string{"exit-2.hy"}},
		{"one exit, Hysteria 25× slower than TCP: the TCP path", equal, "plan-3-balancer",
			map[string]int64{"exit-2.hy": 2500, "exit-2.vr": 100},
			[]string{"exit-2.vr"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := leastLoadSelect(t, tc.v, tc.balancer, tc.alive); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("selected %v, want %v", got, tc.want)
			}
		})
	}
}

func mustGen(t *testing.T, in xrayInputs) []byte {
	t.Helper()
	cfg, _ := genConfig(t, in, quotaCutoff{})
	return cfg
}

// The catch-all (and every plan balancer) falls back to the primary path of its highest-priority
// exit, the lowest id among equals; a VLESS-only exit's primary is its .vr.
func TestXrayExitFallback(t *testing.T) {
	legacy := legacyVLESSExit(5)
	legacy.LoadWeight = 9
	for _, tc := range []struct {
		name  string
		exits []biz.ExitNodeTunnel
		want  map[string]string
	}{
		{"highest weight wins", multiExits(), map[string]string{
			"exit-balancer": "exit-20.hy", "plan-3-balancer": "exit-2.hy", "plan-4-balancer": "exit-20.hy", "plan-6-balancer": "exit-20.hy"}},
		{"ties: lowest id", []biz.ExitNodeTunnel{exitFixture(20, 3, "healthy", true), exitFixture(3, 3, "healthy", true)}, map[string]string{
			"exit-balancer": "exit-3.hy", "plan-4-balancer": "exit-20.hy", "plan-6-balancer": "exit-3.hy"}},
		{"VLESS-only exit on top: its .vr", []biz.ExitNodeTunnel{exitFixture(2, 1, "healthy", true), legacy}, map[string]string{
			"exit-balancer": "exit-5.vr", "plan-3-balancer": "exit-2.hy", "plan-6-balancer": "exit-2.hy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]string{}
			for _, b := range viewOf(t, mustGen(t, exitsInputs(tc.exits...))).Routing.Balancers {
				got[b.Tag] = b.FallbackTag
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("fallbacks %v, want %v", got, tc.want)
			}
		})
	}
	if exitFallback(nil) != xrayBlockTag {
		t.Fatal("no exit: block")
	}
}

func TestXrayExitPathOutbounds(t *testing.T) {
	in := exitsInputs(exitFixture(2, 1, "healthy", true), exitFixture(20, 1, "healthy", true), legacyVLESSExit(5))
	cfg, _ := genConfig(t, in, quotaCutoff{})
	v := viewOf(t, cfg)

	hy := byTag(v.Outbounds, "exit-20.hy")
	if hy == nil || hy["protocol"] != "socks" || !strings.Contains(asJSON(hy["settings"]), `"address":"127.0.0.1","port":1108`) {
		t.Errorf("exit-20.hy must be the SOCKS of hysteria2-client-20 (127.0.0.1:1108): %s", asJSON(hy))
	}
	vr := asJSON(byTag(v.Outbounds, "exit-2.vr"))
	for _, want := range []string{`"address":"203.0.113.2"`, `"port":10443`, `"flow":"xtls-rprx-vision"`,
		`"serverName":"microsoft.com"`, `"publicKey":"` + testExitPBK + `"`, `"shortId":"abcd"`, `"fingerprint":"chrome"`} {
		if !strings.Contains(vr, want) {
			t.Errorf("exit-2.vr lacks %s: %s", want, vr)
		}
	}
	if strings.Contains(vr, `"mux"`) {
		t.Errorf("Vision cannot run over mux: %s", vr)
	}
	old := asJSON(byTag(v.Outbounds, "exit-5.vr"))
	if !strings.Contains(old, `"port":8443`) || !strings.Contains(old, `"mux"`) || strings.Contains(old, `"flow"`) {
		t.Errorf("a legacy VLESS exit keeps its own port, mux and no flow: %s", old)
	}
	if byTag(v.Outbounds, "exit-5.hy") != nil {
		t.Error("a VLESS exit has no Hysteria path")
	}
}

// realityOf returns the realitySettings of the outbound tag in v (fails the test when absent).
func realityOf(t *testing.T, v xrayView, tag string) map[string]any {
	t.Helper()
	o := byTag(v.Outbounds, tag)
	ss, _ := o["streamSettings"].(map[string]any)
	rs, _ := ss["realitySettings"].(map[string]any)
	if rs == nil {
		t.Fatalf("%s: no realitySettings in %s", tag, asJSON(o))
	}
	return rs
}

// The Hub→exit TCP path (exit-N.vr and probe-vr-N, routed or not, legacy VLESS included) always
// presents the fixed chrome fingerprint: a non-chrome reality_fingerprint (the auto-tuner rotates
// it; ios/edge/randomized fail the exit's Reality handshake on prod) changes neither those
// outbounds nor any other byte of the config, so a rotation restarts nothing. The user-facing
// inbounds stay unaffected as before: their fingerprints never came from the xray config (they
// are fixed in the URIs, client_api.go and subscription_handler.go).
func TestXrayExitVRFingerprintIgnoresSetting(t *testing.T) {
	gen := func(settings map[string]string) []byte {
		t.Helper()
		r, _ := fakeReads(settings, "")
		in, err := r.load(xrayRealityKeys{})
		if err != nil {
			t.Fatal(err)
		}
		in.exitNodes = []biz.ExitNodeTunnel{exitFixture(2, 1, "healthy", true), exitFixture(7, 1, "unhealthy", true), legacyVLESSExit(5)}
		cfg, _ := genConfig(t, in, quotaCutoff{})
		return cfg
	}
	base := gen(map[string]string{})
	for _, fp := range []string{"chrome", "firefox", "safari", "edge", "ios", "randomized"} {
		t.Run(fp, func(t *testing.T) {
			cfg := gen(map[string]string{"reality_fingerprint": fp})
			v := viewOf(t, cfg)
			for _, tag := range []string{"exit-2.vr", "probe-vr-2", "probe-vr-7", "exit-5.vr", "probe-vr-5"} {
				if got := realityOf(t, v, tag)["fingerprint"]; got != "chrome" {
					t.Errorf("%s: fingerprint %v with reality_fingerprint=%s, want chrome", tag, got, fp)
				}
			}
			if string(cfg) != string(base) {
				t.Errorf("reality_fingerprint=%s changed the xray config (an xray restart for every user)", fp)
			}
		})
	}
}

// An exit row without tunnel_sni: the TCP path of a Hysteria exit (exit-N.vr, probe-vr-N) presents
// www.microsoft.com, one of the serverNames --setup-exit-node gives the exit's Reality inbound
// (the apex microsoft.com fails the handshake on prod). An explicit tunnel_sni is used as is, and
// a legacy primary VLESS exit (not set up by deploy) keeps its old fallback, its own address.
func TestXrayExitVRDefaultSNI(t *testing.T) {
	if config.ExitVRSNI != "www.microsoft.com" {
		t.Fatalf("config.ExitVRSNI = %q, want www.microsoft.com", config.ExitVRSNI)
	}
	noSNI := exitFixture(2, 1, "healthy", true)
	noSNI.TunnelSNI = ""
	apex := exitFixture(3, 1, "healthy", true) // tunnel_sni microsoft.com, as older deploys wrote it
	other := exitFixture(7, 1, "unhealthy", true)
	other.TunnelSNI = "github.com"
	legacy := legacyVLESSExit(5)
	legacy.TunnelSNI = ""
	cfg, _ := genConfig(t, exitsInputs(noSNI, apex, other, legacy), quotaCutoff{})
	v := viewOf(t, cfg)
	for tag, want := range map[string]string{
		"exit-2.vr": "www.microsoft.com", "probe-vr-2": "www.microsoft.com",
		"exit-3.vr": "microsoft.com", "probe-vr-3": "microsoft.com",
		"probe-vr-7": "github.com",
		"exit-5.vr":  "203.0.113.5", "probe-vr-5": "203.0.113.5",
	} {
		if got := realityOf(t, v, tag)["serverName"]; got != want {
			t.Errorf("%s: serverName %v, want %s", tag, got, want)
		}
	}
}

// The VLESS Reality fields are checked as xray checks them: a row xray would reject loses only
// its TCP path (the Hysteria path stays), a legacy VLESS exit without a valid one is skipped.
func TestXrayExitVRValidation(t *testing.T) {
	resetExitNotes(t)
	captureLog(t)
	for _, tc := range []struct {
		name   string
		mod    func(*biz.ExitNodeTunnel)
		vless  bool // a legacy VLESS exit instead of a Hysteria one
		wantVR bool
	}{
		{"valid", func(*biz.ExitNodeTunnel) {}, false, true},
		{"valid: empty short id (xray allows it)", func(n *biz.ExitNodeTunnel) { n.TunnelShortID = "" }, false, true},
		{"valid: 16-char short id", func(n *biz.ExitNodeTunnel) { n.TunnelShortID = "0123456789abcdef" }, false, true},
		{"valid: UUID without dashes", func(n *biz.ExitNodeTunnel) { n.TunnelUUID = strings.ReplaceAll(testExitUUID, "-", "") }, false, true},
		{"valid: legacy VLESS", func(*biz.ExitNodeTunnel) {}, true, true},
		{"no Reality credentials at all", func(n *biz.ExitNodeTunnel) { n.TunnelUUID, n.TunnelPubKey = "", "" }, false, false},
		{"uuid: not hex", func(n *biz.ExitNodeTunnel) { n.TunnelUUID = "zzzzzzzz-bbbb-cccc-dddd-eeeeeeeeeeee" }, false, false},
		{"uuid: dashes misplaced", func(n *biz.ExitNodeTunnel) { n.TunnelUUID = "aaaaaaa-abbbb-cccc-dddd-eeeeeeeeeeee" }, false, false},
		{"uuid: 31 chars", func(n *biz.ExitNodeTunnel) { n.TunnelUUID = strings.Repeat("a", 31) }, false, false},
		{"uuid: a short name xray would hash", func(n *biz.ExitNodeTunnel) { n.TunnelUUID = "hub" }, false, false},
		{"public key: empty", func(n *biz.ExitNodeTunnel) { n.TunnelPubKey = "" }, false, false},
		{"public key: not base64url", func(n *biz.ExitNodeTunnel) { n.TunnelPubKey = strings.Repeat("+", 43) }, false, false},
		{"public key: padded std base64", func(n *biz.ExitNodeTunnel) { n.TunnelPubKey = testExitPBK + "=" }, false, false},
		{"public key: 31 bytes", func(n *biz.ExitNodeTunnel) { n.TunnelPubKey = testExitPBK[:42] }, false, false},
		{"public key: 33 bytes", func(n *biz.ExitNodeTunnel) { n.TunnelPubKey = testExitPBK + "A" }, false, false},
		{"short id: odd length", func(n *biz.ExitNodeTunnel) { n.TunnelShortID = "abc" }, false, false},
		{"short id: not hex", func(n *biz.ExitNodeTunnel) { n.TunnelShortID = "zz" }, false, false},
		{"short id: 18 chars", func(n *biz.ExitNodeTunnel) { n.TunnelShortID = "0123456789abcdef01" }, false, false},
		{"legacy VLESS: port 0 (NULL column)", func(n *biz.ExitNodeTunnel) { n.TunnelPort = 0 }, true, false},
		{"legacy VLESS: port 70000", func(n *biz.ExitNodeTunnel) { n.TunnelPort = 70000 }, true, false},
		{"legacy VLESS: no public key", func(n *biz.ExitNodeTunnel) { n.TunnelPubKey = "" }, true, false},
		{"legacy VLESS: bad uuid", func(n *biz.ExitNodeTunnel) { n.TunnelUUID = "x" }, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := exitFixture(2, 1, "healthy", true)
			if tc.vless {
				n = legacyVLESSExit(2)
			}
			tc.mod(&n)
			if got, problem := exitHasVRPath(n), exitVRProblem(n); got != tc.wantVR || (problem == "") != tc.wantVR {
				t.Fatalf("exitHasVRPath = %v (%q), want %v", got, problem, tc.wantVR)
			}
			cfg, b := genConfig(t, exitsInputs(n), quotaCutoff{}) // must still pass the leak guard
			v := viewOf(t, cfg)
			hasVR, hasProbe, hasHy := byTag(v.Outbounds, "exit-2.vr") != nil, byTag(v.Inbounds, "probe-vr-2") != nil, byTag(v.Outbounds, "exit-2.hy") != nil
			if hasVR != tc.wantVR || hasProbe != tc.wantVR {
				t.Errorf("exit-2.vr %v, probe-vr-2 %v, want %v", hasVR, hasProbe, tc.wantVR)
			}
			if hasHy != !tc.vless {
				t.Errorf("exit-2.hy %v: an invalid TCP path must not cost the Hysteria path", hasHy)
			}
			if wantRouted := !tc.vless || tc.wantVR; b.noExit == wantRouted {
				t.Errorf("noExit = %v, want %v", b.noExit, !wantRouted)
			}
		})
	}
}

// A bad row is logged once, again only when its problem changes, and once more when it is fixed.
func TestXrayExitNotesLoggedOnStateChange(t *testing.T) {
	resetExitNotes(t)
	logs := captureLog(t)
	badSID := exitFixture(2, 1, "healthy", true)
	badSID.TunnelShortID = "abc"
	badKey := exitFixture(2, 1, "healthy", true)
	badKey.TunnelPubKey = "short"
	skipped := legacyVLESSExit(3)
	skipped.TunnelPort = 0
	lines := func() int { return strings.Count(logs.String(), "\n") }

	for i := 0; i < 3; i++ {
		genConfig(t, exitsInputs(badSID, skipped), quotaCutoff{})
	}
	if lines() != 2 || !strings.Contains(logs.String(), "exit-нода 2 (exit-2): TCP-путь (exit-2.vr) не строится: tunnel_short_id") ||
		!strings.Contains(logs.String(), "exit-нода 3 (exit-3): пропущена") {
		t.Fatalf("three syncs, two bad rows: want one line each\n%s", logs)
	}
	genConfig(t, exitsInputs(badKey, skipped), quotaCutoff{})
	if lines() != 3 || !strings.Contains(logs.String(), "tunnel_public_key") {
		t.Fatalf("a changed problem is logged again\n%s", logs)
	}
	genConfig(t, exitsInputs(exitFixture(2, 1, "healthy", true), skipped), quotaCutoff{})
	genConfig(t, exitsInputs(exitFixture(2, 1, "healthy", true), skipped), quotaCutoff{})
	if lines() != 4 || !strings.Contains(logs.String(), "exit-нода 2 (exit-2): снова в порядке") {
		t.Fatalf("the fix is logged once\n%s", logs)
	}
}

// Second line of defence: an exit that is not online never enters the config, whatever the read
// returned.
func TestXrayExitNotOnlineSkipped(t *testing.T) {
	resetExitNotes(t)
	logs := captureLog(t)
	offline := exitFixture(3, 10, "healthy", true)
	offline.LifecycleState = "deprovisioning"
	cfg, b := genConfig(t, exitsInputs(exitFixture(2, 1, "healthy", true), offline), quotaCutoff{})
	if b.exits != 1 || strings.Contains(string(cfg), "exit-3.") || strings.Contains(string(cfg), "probe-vr-3") {
		t.Fatalf("a deprovisioning exit was generated: %d exits routed", b.exits)
	}
	if !strings.Contains(logs.String(), `exit-нода 3 (exit-3): пропущена — lifecycle_state "deprovisioning"`) {
		t.Errorf("skip not logged: %s", logs)
	}
}

// Every online exit with a TCP path gets a loopback probe pair, healthy or not, outside every
// selector; probe inbounds are not user inbounds (the no-exit block never covers them).
func TestXrayExitProbes(t *testing.T) {
	cfg, _ := genConfig(t, exitsInputs(multiExits()...), quotaCutoff{})
	v := viewOf(t, cfg)
	if byTag(v.Outbounds, "exit-7.hy") != nil || byTag(v.Outbounds, "exit-7.vr") != nil {
		t.Fatal("an unhealthy exit must stay out of the routing")
	}
	for _, id := range []int{2, 3, 7, 20} {
		tag := exitProbeTag(id)
		in := byTag(v.Inbounds, tag)
		if in == nil || in["listen"] != "127.0.0.1" || in["port"] != float64(12000+id) || in["protocol"] != "socks" {
			t.Errorf("%s: want a loopback SOCKS inbound on %d, got %v", tag, 12000+id, in)
		}
		if byTag(v.Outbounds, tag) == nil {
			t.Errorf("%s: no probe outbound", tag)
		}
		routed := false
		for _, r := range v.Routing.Rules {
			if r["outboundTag"] == tag {
				routed = reflect.DeepEqual(r["inboundTag"], []any{tag})
			}
		}
		if !routed {
			t.Errorf("%s: no rule routing exactly its own inbound to it", tag)
		}
	}

	down := multiExits()
	for i := range down {
		down[i].TunnelStatus = "unhealthy"
	}
	cfg, b := genConfig(t, exitsInputs(down...), quotaCutoff{}) // the leak guard runs inside
	if !b.noExit {
		t.Fatal("no healthy exit must be the no-exit mode")
	}
	v = viewOf(t, cfg)
	if byTag(v.Inbounds, "probe-vr-2") == nil {
		t.Fatal("probe pairs must survive the no-exit mode: a recovered TCP path must be noticed")
	}
	for _, r := range v.Routing.Rules {
		if r["outboundTag"] == "block" && strings.Contains(fmt.Sprint(r["inboundTag"]), "probe-vr") {
			t.Errorf("the no-exit block must not cover the probe inbounds: %v", r)
		}
	}
}

// Review Focus 1 → adding a second exit (id 20 next to 2) is one structural diff = one xray
// restart; the next sync with the same inputs restarts nothing.
func TestXrayExitAddedIsOneStructuralChange(t *testing.T) {
	one, _ := genConfig(t, exitsInputs(exitFixture(2, 1, "healthy", true)), quotaCutoff{})
	twoIn := exitsInputs(exitFixture(2, 1, "healthy", true), exitFixture(20, 1, "healthy", true))
	two, _ := genConfig(t, twoIn, quotaCutoff{})
	if d, err := xraydiff.Classify(one, two); err != nil || d.Kind != xraydiff.Structural {
		t.Fatalf("adding exit 20: kind %v (%v), want one structural restart", d.Kind, err)
	}
	again, _ := genConfig(t, twoIn, quotaCutoff{})
	if d, _ := xraydiff.Classify(two, again); d.Kind != xraydiff.None {
		t.Fatalf("the next sync must not restart again: %v (%s)", d.Kind, d.Reason)
	}
}

// The egress guard lets xray reach SOCKS 1088..1599 only: exits outside 1..511 are skipped.
func TestXrayExitIDRange(t *testing.T) {
	resetExitNotes(t)
	captureLog(t)
	cfg, b := genConfig(t, exitsInputs(exitFixture(0, 1, "healthy", true), exitFixture(2, 1, "healthy", true),
		exitFixture(511, 1, "healthy", true), exitFixture(512, 1, "healthy", true)), quotaCutoff{})
	s := string(cfg)
	for _, id := range []int{0, 512} {
		if strings.Contains(s, fmt.Sprintf(`"exit-%d.`, id)) || strings.Contains(s, fmt.Sprintf(`"probe-vr-%d"`, id)) {
			t.Errorf("exit %d is outside the allowed SOCKS range but was generated", id)
		}
	}
	v := viewOf(t, cfg)
	hy := byTag(v.Outbounds, "exit-511.hy")
	if b.exits != 2 || hy == nil || !strings.Contains(asJSON(hy), `"port":1599`) || byTag(v.Inbounds, "probe-vr-511")["port"] != float64(12511) {
		t.Fatalf("exit 511 (SOCKS 1599, probe 12511) must be generated: %d exits routed, %s", b.exits, asJSON(hy))
	}
}

// Invariant: inbound tags, inbound ports and outbound tags are unique (xray refuses duplicate
// tags, a duplicate listen port fails at start).
func TestXrayExitConfigTagsAndPortsUnique(t *testing.T) {
	in := exitsInputs(append(multiExits(), legacyVLESSExit(5), exitFixture(511, 2, "healthy", true))...)
	in.settings.cdnEnabled, in.settings.cdnHost, in.settings.cdnXHPath = true, "cdn.hiearth.ru", "/cdn-xh"
	v := viewOf(t, mustGen(t, in))
	unique := func(what string, vals []string) {
		seen := map[string]bool{}
		for _, x := range vals {
			if seen[x] {
				t.Errorf("duplicate %s %s", what, x)
			}
			seen[x] = true
		}
	}
	var inTags, inPorts []string
	for _, i := range v.Inbounds {
		inTags = append(inTags, fmt.Sprint(i["tag"]))
		inPorts = append(inPorts, fmt.Sprint(i["listen"], ":", i["port"]))
	}
	unique("inbound tag", inTags)
	unique("inbound port", inPorts)
	unique("outbound tag", v.outboundTags())
	if len(v.Inbounds) < 10 || len(v.Outbounds) < 15 {
		t.Fatalf("fixture too small: %d inbounds, %d outbounds", len(v.Inbounds), len(v.Outbounds))
	}
}

func TestXrayExitGoldens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		exits []biz.ExitNodeTunnel
	}{
		// A Hysteria exit whose row has no Reality credentials (older --setup-exit-node): .hy only.
		{"xray_exits_hy_only", []biz.ExitNodeTunnel{exitFixture(2, 1, "healthy", false)}},
		// A primary VLESS exit from the old admin form: one .vr (own port, mux, no flow).
		{"xray_exits_legacy_vless", []biz.ExitNodeTunnel{legacyVLESSExit(2)}},
		{"xray_exits_dual", []biz.ExitNodeTunnel{exitFixture(2, 1, "healthy", true)}},
		{"xray_exits_multi", multiExits()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := genConfig(t, exitsInputs(tc.exits...), quotaCutoff{})
			checkGolden(t, tc.name, exitProjection(t, cfg))
		})
	}
}

// exitProjection keeps what this task changes: exit and probe outbounds, probe inbounds, the rules
// that use a balancer or a probe outbound, the balancers and the observatory.
func exitProjection(t *testing.T, cfg []byte) []byte {
	t.Helper()
	var c struct {
		Inbounds    []map[string]any `json:"inbounds"`
		Outbounds   []map[string]any `json:"outbounds"`
		Routing     map[string]any   `json:"routing"`
		Observatory map[string]any   `json:"observatory"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	tagged := func(items []map[string]any, prefixes ...string) []map[string]any {
		out := []map[string]any{}
		for _, it := range items {
			tag, _ := it["tag"].(string)
			for _, p := range prefixes {
				if strings.HasPrefix(tag, p) {
					out = append(out, it)
					break
				}
			}
		}
		return out
	}
	rules := []any{}
	raw, _ := c.Routing["rules"].([]any)
	for _, r := range raw {
		m, _ := r.(map[string]any)
		out, _ := m["outboundTag"].(string)
		if m["balancerTag"] != nil || strings.HasPrefix(out, exitProbeTagPrefix) {
			rules = append(rules, m)
		}
	}
	b, err := json.MarshalIndent(map[string]any{
		"inbounds":    tagged(c.Inbounds, exitProbeTagPrefix),
		"outbounds":   tagged(c.Outbounds, exitAllSelector, exitProbeTagPrefix),
		"rules":       rules,
		"balancers":   c.Routing["balancers"],
		"observatory": c.Observatory,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}
