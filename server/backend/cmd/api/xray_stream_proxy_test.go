package main

import (
	"encoding/json"
	"strings"
	"testing"
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

// stream_proxy_protocol puts acceptProxyProtocol on the one inbound behind nginx's 443 stream
// (vless-reality on 127.0.0.1:10443) and nowhere else; off, the inbound has no sockopt at all.
func TestBuildXrayConfigStreamProxyProtocol(t *testing.T) {
	for _, on := range []bool{false, true} {
		in := baseInputs()
		in.settings.streamProxyProtocol = on
		in.settings.cdnEnabled, in.settings.cdnHost, in.settings.cdnXHPath = true, defaultCDNHost, defaultCDNXHPath
		cfg, _ := genConfig(t, in, quotaCutoff{})
		ib := inboundsByTag(t, cfg)
		r := ib["vless-reality"]
		if r == nil || r["listen"] != "127.0.0.1" || r["port"] != float64(10443) {
			t.Fatalf("on=%v: vless-reality inbound on 127.0.0.1:10443 missing: %v", on, r)
		}
		if got := acceptsProxyProtocol(r); got != on {
			t.Errorf("on=%v: vless-reality acceptProxyProtocol = %v", on, got)
		}
		if ss := r["streamSettings"].(map[string]any); !on && ss["sockopt"] != nil {
			t.Errorf("off: vless-reality must have no sockopt, got %v", ss["sockopt"])
		}
		// Reality itself is untouched by the flag.
		rs := r["streamSettings"].(map[string]any)["realitySettings"].(map[string]any)
		if rs["dest"] != "yandex.ru:443" || rs["privateKey"] != "priv" {
			t.Errorf("on=%v: realitySettings changed: %v", on, rs)
		}
		for tag, inb := range ib {
			if tag != "vless-reality" && acceptsProxyProtocol(inb) {
				t.Errorf("on=%v: inbound %s is not behind the 443 stream but accepts PROXY", on, tag)
			}
		}
		if on && strings.Count(string(cfg), `"acceptProxyProtocol": true`) != 1 {
			t.Errorf("on: acceptProxyProtocol must appear exactly once (deploy greps config.json for it)")
		}
		if !on && strings.Contains(string(cfg), "acceptProxyProtocol") {
			t.Errorf("off: acceptProxyProtocol must not appear")
		}
	}
}

// The setting is read strictly: absent = off, only "true"/"false" accepted, anything else
// aborts the sync (the running config is kept) instead of silently meaning off.
func TestXraySyncReadsStreamProxyProtocol(t *testing.T) {
	for _, c := range []struct {
		val     *string
		want    bool
		wantErr bool
	}{
		{nil, false, false},
		{streamPPValue("false"), false, false},
		{streamPPValue("true"), true, false},
		{streamPPValue(" true "), true, false}, // settingForSync trims
		{streamPPValue(""), false, false},      // blank = default
		{streamPPValue("yes"), false, true},
		{streamPPValue("TRUE"), false, true},
		{streamPPValue("1"), false, true},
	} {
		settings := map[string]string{}
		name := "absent"
		if c.val != nil {
			settings["stream_proxy_protocol"] = *c.val
			name = *c.val
		}
		r, _ := fakeReads(settings, "")
		in, err := r.load(xrayRealityKeys{})
		if c.wantErr {
			if err == nil || !strings.Contains(err.Error(), "stream_proxy_protocol") {
				t.Errorf("%q: want an error naming the setting, got %v", name, err)
			}
			continue
		}
		if err != nil || in.settings.streamProxyProtocol != c.want {
			t.Errorf("%q: got %v, %v; want %v", name, in.settings.streamProxyProtocol, err, c.want)
		}
	}
	// a DB error on this key aborts like every other read
	r, _ := fakeReads(map[string]string{}, "setting:stream_proxy_protocol")
	if _, err := r.load(xrayRealityKeys{}); err == nil || !strings.Contains(err.Error(), "db down") {
		t.Errorf("a failing read of stream_proxy_protocol must abort the load, got %v", err)
	}
}

func streamPPValue(s string) *string { return &s }
