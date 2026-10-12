package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/xraydiff"
)

// exitFixture is an exit as cmd/deploy --setup-exit-node writes it: Hysteria on 50443 plus the
// exit's VLESS Reality credentials (SNI microsoft.com); withVR=false is an older row without them.
func exitFixture(id, weight int, status string, withVR bool) biz.ExitNodeTunnel {
	n := testExit(id, "hysteria2")
	n.TunnelPort, n.TunnelSNI, n.LoadWeight, n.TunnelStatus, n.LifecycleState = 50443, "microsoft.com", weight, status, "online"
	if !withVR {
		n.TunnelUUID, n.TunnelPubKey, n.TunnelShortID = "", "", ""
	}
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

type xrayView struct {
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Routing   struct {
		Rules     []map[string]any `json:"rules"`
		Balancers []struct {
			Tag         string   `json:"tag"`
			Selector    []string `json:"selector"`
			FallbackTag string   `json:"fallbackTag"`
			Strategy    struct {
				Type     string `json:"type"`
				Settings struct {
					Costs []xrayCost `json:"costs"`
				} `json:"settings"`
			} `json:"strategy"`
		} `json:"balancers"`
	} `json:"routing"`
	Observatory struct {
		SubjectSelector []string `json:"subjectSelector"`
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
	// weight 1 → 10, weight 5 → 6, weight 10 → 1; the TCP path of a Hysteria exit costs ×100.
	want := map[string]float64{"exit-2.hy": 10, "exit-2.vr": 1000, "exit-3.hy": 6, "exit-3.vr": 600, "exit-20.hy": 1, "exit-20.vr": 100}
	for _, bal := range v.Routing.Balancers {
		if bal.Tag != "plan-6-balancer" {
			continue
		}
		got := map[string]float64{}
		for _, c := range bal.Strategy.Settings.Costs {
			if !c.Regexp || !strings.HasPrefix(c.Match, "^") || !strings.HasSuffix(c.Match, "$") {
				t.Errorf("cost %q must be an anchored regexp (a plain match is a substring match)", c.Match)
			}
			re := regexp.MustCompile(c.Match)
			var hit []string
			for tag := range want {
				if re.MatchString(tag) {
					hit = append(hit, tag)
				}
			}
			if len(hit) != 1 {
				t.Errorf("cost %q matches %v, want exactly one tag", c.Match, hit)
				continue
			}
			got[hit[0]] = c.Value
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("plan-6 costs = %v, want %v", got, want)
		}
		return
	}
	t.Fatal("plan-6-balancer not generated")
}

func TestXrayExitPathOutbounds(t *testing.T) {
	legacy := testExit(5, "vless") // a primary VLESS exit from the old admin form
	legacy.TunnelPort = 8443
	in := exitsInputs(exitFixture(2, 1, "healthy", true), exitFixture(20, 1, "healthy", true), legacy)
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
	captureLog(t)
	cfg, b := genConfig(t, exitsInputs(exitFixture(2, 1, "healthy", true), exitFixture(512, 1, "healthy", true)), quotaCutoff{})
	if b.exits != 1 || strings.Contains(string(cfg), "exit-512.") || strings.Contains(string(cfg), "probe-vr-512") {
		t.Fatalf("exit 512 is outside the allowed SOCKS range: %d exits routed", b.exits)
	}
}

func TestXrayExitGoldens(t *testing.T) {
	for _, tc := range []struct {
		name  string
		exits []biz.ExitNodeTunnel
	}{
		{"xray_exits_legacy_single", []biz.ExitNodeTunnel{exitFixture(2, 1, "healthy", false)}},
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
