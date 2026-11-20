package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// dnsShape is how a config resolves names for its routing and its direct dials.
type dnsShape struct {
	HasDNS    bool
	Tag       string
	Servers   []string
	FirstRule map[string]any
}

func dnsShapes(t *testing.T, body []byte) []dnsShape {
	t.Helper()
	var cs []struct {
		DNS *struct {
			Tag     string `json:"tag"`
			Servers []any  `json:"servers"`
		} `json:"dns"`
		Routing struct {
			Rules []map[string]any `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(body, &cs); err != nil {
		t.Fatalf("not a JSON array of configs: %v\n%s", err, body)
	}
	var out []dnsShape
	for _, c := range cs {
		s := dnsShape{HasDNS: c.DNS != nil}
		if c.DNS != nil {
			s.Tag = c.DNS.Tag
			for _, v := range c.DNS.Servers {
				switch x := v.(type) {
				case string:
					s.Servers = append(s.Servers, x)
				case map[string]any:
					a, _ := x["address"].(string)
					s.Servers = append(s.Servers, a)
				}
			}
		}
		if len(c.Routing.Rules) > 0 {
			s.FirstRule = c.Routing.Rules[0]
		}
		out = append(out, s)
	}
	return out
}

// By name, routing resolves every name on 80/443 (IPOnDemand: the first rule has an IP
// condition) to let geoip:ru decide. A config without "dns" resolves with the device's own
// resolver — on a RU network that is the operator's DNS, which the TSPU steers to the national
// DNS and which answers blocked names (instagram.com) with nothing or a stub: a stub in
// geoip:ru/geoip:private sends the site direct, where it is blocked. Through the tunnel
// (sub_json_dns) the names are resolved by public DoH behind the way in, never by the operator.
func TestJSONSubscriptionDNSThroughTunnel(t *testing.T) {
	d := directRouting{sites: []string{"domain:bank.example"}, ips: []string{"geoip:private", "geoip:ru"}}

	// Off by name: nothing resolves in routing (AsIs) — the JSON stays as it was.
	d.dnsTunnel = true
	for _, s := range dnsShapes(t, jsonSubscription(autoInputs(), d)) {
		if s.HasDNS {
			t.Fatalf("not by name: %+v, want no dns", s)
		}
	}

	// By name without the switch: as served since 16:52 on 2026-11-20 (no dns).
	d.byName, d.dnsTunnel = true, false
	for _, s := range dnsShapes(t, jsonSubscription(autoInputs(), d)) {
		if s.HasDNS {
			t.Fatalf("by name, switch off: %+v, want no dns", s)
		}
	}

	// By name through the tunnel: every config (Авто, Бамбук 1/2) resolves by DoH behind
	// its own balancer, and that rule comes before every rule that resolves.
	d.dnsTunnel = true
	shapes := dnsShapes(t, jsonSubscription(autoInputs(), d))
	if len(shapes) != 3 {
		t.Fatalf("%d configs, want «Авто» and both Бамбук lines", len(shapes))
	}
	for i, s := range shapes {
		if !s.HasDNS || s.Tag != jsonDNSTag || len(s.Servers) == 0 {
			t.Fatalf("config %d: %+v", i, s)
		}
		for _, a := range s.Servers {
			// https:// goes through routing; https+local://, plain UDP and "localhost" bypass it.
			if !strings.HasPrefix(a, "https://") {
				t.Fatalf("config %d: DNS server %q is not DoH through routing", i, a)
			}
		}
		r := s.FirstRule
		tags, _ := r["inboundTag"].([]any)
		if len(tags) != 1 || tags[0] != jsonDNSTag || r["balancerTag"] != "auto" || r["ip"] != nil || r["domain"] != nil {
			t.Fatalf("config %d: first rule %v, want the DNS queries to the balancer", i, r)
		}
	}
}

// The handler: through the tunnel only while the owner serves it, and only by name.
func TestSubscriptionJSONDNSGating(t *testing.T) {
	for _, c := range []struct {
		name, byName, mode, test string
		dns                      bool
	}{
		{"absent", "on", "", "", false},
		{"off", "on", "off", goldenUser, false},
		{"test, other user", "on", "test", "someone-else", false},
		{"test, this user", "on", "test", goldenUser, true},
		{"on", "on", "on", "", true},
		{"on, not by name", "off", "on", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := goldenScript()
			s.settings["sub_auto_mode"] = "on"
			s.settings["sub_auto_test_user"] = c.test
			s.settings[subDirectByNameKey] = c.byName
			if c.mode != "" {
				s.settings[subJSONDNSKey] = c.mode
			}
			useScriptDB(t, s)
			rec := subscriptionRequest(t, "Happ/4.7 Android", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d %q", rec.Code, rec.Body.String())
			}
			if len(s.unknown) > 0 {
				t.Fatalf("unscripted queries: %v", s.unknown)
			}
			for _, sh := range dnsShapes(t, rec.Body.Bytes()) {
				if sh.HasDNS != c.dns {
					t.Fatalf("dns %v, want %v: %+v", sh.HasDNS, c.dns, sh)
				}
			}
		})
	}
}

func TestEntrySettingsJSONDNSWrites(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, m := range []string{"off", "test", "on"} {
		w, msg := entrySettingsRequest{JSONDNS: str(" " + m + " ")}.writes(time.Now())
		if msg != "" || w[subJSONDNSKey] != m {
			t.Fatalf("%s: %v %q", m, w, msg)
		}
	}
	if _, msg := (entrySettingsRequest{JSONDNS: str("maybe")}).writes(time.Now()); msg == "" {
		t.Fatal("accepted a bogus mode")
	}
}
