package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/xraydiff"
)

// inboundsByTag decodes the generated config's inbounds.
func inboundsByTag(t *testing.T, cfg []byte) map[string]map[string]any {
	t.Helper()
	var c struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	m := map[string]map[string]any{}
	for _, in := range c.Inbounds {
		tag, _ := in["tag"].(string)
		m[tag] = in
	}
	return m
}

func acceptsProxyProtocol(in map[string]any) bool {
	ss, _ := in["streamSettings"].(map[string]any)
	so, _ := ss["sockopt"].(map[string]any)
	v, _ := so["acceptProxyProtocol"].(bool)
	return v
}

func emailsOf(t *testing.T, in map[string]any) []string {
	t.Helper()
	var out []string
	for _, c := range in["settings"].(map[string]any)["clients"].([]any) {
		out = append(out, c.(map[string]any)["email"].(string))
	}
	return out
}

// The Reality inbound behind the 443 stream exists twice: vless-reality (127.0.0.1:10443, no
// PROXY) and its mirror vless-reality-pp (127.0.0.1:10444, acceptProxyProtocol), identical
// otherwise — the same clients (quota excludes included), Reality settings and sniffing. No
// other inbound accepts PROXY, and no setting decides any of it.
func TestBuildXrayConfigRealityPPMirror(t *testing.T) {
	in := baseInputs()
	in.settings.cdnEnabled, in.settings.cdnHost, in.settings.cdnXHPath = true, defaultCDNHost, defaultCDNXHPath
	cfg, _ := genConfig(t, in, quotaCutoff{true, userSet("bob")})
	ib := inboundsByTag(t, cfg)
	r, pp := ib[xrayRealityTag], ib[xrayRealityPPTag]
	if r == nil || pp == nil {
		t.Fatalf("both Reality inbounds must exist: %v / %v", r != nil, pp != nil)
	}
	if r["listen"] != "127.0.0.1" || r["port"] != float64(10443) || pp["listen"] != "127.0.0.1" || pp["port"] != float64(10444) {
		t.Errorf("ports: %v:%v and %v:%v", r["listen"], r["port"], pp["listen"], pp["port"])
	}
	if acceptsProxyProtocol(r) || !acceptsProxyProtocol(pp) {
		t.Errorf("only the mirror accepts PROXY: reality=%v pp=%v", acceptsProxyProtocol(r), acceptsProxyProtocol(pp))
	}
	if ss := r["streamSettings"].(map[string]any); ss["sockopt"] != nil {
		t.Errorf("vless-reality must have no sockopt, got %v", ss["sockopt"])
	}
	// identical except tag, port and the PROXY sockopt
	strip := func(in map[string]any) map[string]any {
		c := map[string]any{}
		for k, v := range in {
			c[k] = v
		}
		delete(c, "tag")
		delete(c, "port")
		ss := map[string]any{}
		for k, v := range in["streamSettings"].(map[string]any) {
			ss[k] = v
		}
		delete(ss, "sockopt")
		c["streamSettings"] = ss
		return c
	}
	if !reflect.DeepEqual(strip(r), strip(pp)) {
		t.Errorf("the mirror differs from vless-reality:\n%v\n%v", strip(r), strip(pp))
	}
	if e := emailsOf(t, pp); strings.Contains(strings.Join(e, ","), "bob") || !reflect.DeepEqual(e, emailsOf(t, r)) {
		t.Errorf("quota-excluded bob must be absent from both, lists equal: %v vs %v", e, emailsOf(t, r))
	}
	for tag, inb := range ib {
		if tag != xrayRealityPPTag && acceptsProxyProtocol(inb) {
			t.Errorf("inbound %s accepts PROXY", tag)
		}
	}
	if strings.Count(string(cfg), `"acceptProxyProtocol": true`) != 1 {
		t.Error("acceptProxyProtocol must appear exactly once (the mirror)")
	}
}

// Clients never see the mirror: the subscription digest is the same with or without it, so
// introducing it does not bump subscription_version.
func TestClientFacingDigestSkipsRealityPP(t *testing.T) {
	cfg, _ := genConfig(t, baseInputs(), quotaCutoff{})
	var c map[string]any
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	var kept []any
	for _, raw := range c["inbounds"].([]any) {
		if raw.(map[string]any)["tag"] != xrayRealityPPTag {
			kept = append(kept, raw)
		}
	}
	c["inbounds"] = kept
	without, _ := json.Marshal(c)
	a, err := xrayClientFacing(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := xrayClientFacing(without)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Error("the PROXY mirror changes the client-facing digest")
	}
}

// User changes reach both Reality inbounds through the live path (xraydiff, per tag), and the
// no-exit block covers the mirror too.
func TestRealityPPMirrorLiveApplyAndNoExit(t *testing.T) {
	old, _ := genConfig(t, baseInputs(), quotaCutoff{})
	cur, _ := genConfig(t, withClients(baseInputs(), biz.ActiveVlessClient{UUID: "uuid-erin", UserID: "erin", IsPremium: true, PlanID: 3}), quotaCutoff{})
	d, err := xraydiff.Classify(old, cur)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != xraydiff.UserOnly {
		t.Fatalf("adding a user must stay user-only, got %v (%s)", d.Kind, d.Reason)
	}
	got := map[string]int{}
	for _, ic := range d.Inbounds {
		got[ic.Tag] = len(ic.Added)
	}
	if got[xrayRealityTag] != 1 || got[xrayRealityPPTag] != 1 {
		t.Errorf("the new user must be added to both Reality inbounds: %v", got)
	}
	in := baseInputs()
	in.exitNodes = nil
	cfg, b := genConfig(t, in, quotaCutoff{})
	if !b.noExit {
		t.Fatal("noExit not reported")
	}
	var c struct {
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	blocked := false
	for _, r := range c.Routing.Rules {
		if r["outboundTag"] == xrayBlockTag {
			tags, _ := r["inboundTag"].([]any) // decoded JSON
			for _, tag := range tags {
				if tag == xrayRealityPPTag {
					blocked = true
				}
			}
		}
	}
	if !blocked {
		t.Error("without exits the mirror must be blocked like every user inbound")
	}
}

// No setting couples xray to nginx's mode any more.
func TestXraySyncReadsNoStreamProxySetting(t *testing.T) {
	r, calls := fakeReads(map[string]string{"stream_proxy_protocol": "true"}, "")
	if _, err := r.load(xrayRealityKeys{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range *calls {
		if c == "setting:stream_proxy_protocol" {
			t.Error("stream_proxy_protocol is read; the mirror inbound always exists")
		}
	}
}
