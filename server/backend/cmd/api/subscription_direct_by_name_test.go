package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// directShape is what decides where a direct connection really dials, per config.
type directShape struct {
	RouteOnly       bool
	DomainsExcluded []string
	DomainStrategy  string // routing
	DirectDial      string // the direct outbound's sockopt.domainStrategy ("" = none)
}

func directShapes(t *testing.T, body []byte) []directShape {
	t.Helper()
	var cs []struct {
		Inbounds []struct {
			Sniffing struct {
				RouteOnly       bool     `json:"routeOnly"`
				DomainsExcluded []string `json:"domainsExcluded"`
			} `json:"sniffing"`
		} `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		Routing   struct {
			DomainStrategy string `json:"domainStrategy"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(body, &cs); err != nil {
		t.Fatalf("not a JSON array of configs: %v\n%s", err, body)
	}
	var out []directShape
	for _, c := range cs {
		if len(c.Inbounds) != 2 {
			t.Fatalf("inbounds %v", c.Inbounds)
		}
		a, b := c.Inbounds[0].Sniffing, c.Inbounds[1].Sniffing
		if a.RouteOnly != b.RouteOnly || !slices.Equal(a.DomainsExcluded, b.DomainsExcluded) {
			t.Fatalf("the SOCKS and HTTP inbounds sniff differently: %+v / %+v", a, b)
		}
		s := directShape{RouteOnly: a.RouteOnly, DomainsExcluded: a.DomainsExcluded, DomainStrategy: c.Routing.DomainStrategy}
		for _, o := range c.Outbounds {
			if o["tag"] != "direct" {
				continue
			}
			if ss, ok := o["streamSettings"].(map[string]any); ok {
				s.DirectDial, _ = ss["sockopt"].(map[string]any)["domainStrategy"].(string)
			}
		}
		out = append(out, s)
	}
	return out
}

// Happ desktop's TUN (sing-box) gives Windows an IPv6 address and route even on a
// network without IPv6, and accepts every TCP connection at once: a browser's
// happy eyeballs settles on the AAAA of a RU site, geoip:ru sends that IPv6 address
// direct, and the dial dies — ya.ru and every RU site with an AAAA record stop
// opening. By name (sub_direct_by_name), a sniffed connection carries its name,
// geoip:ru still decides by the name's addresses (IPOnDemand), and direct dials
// the name IPv4 first. The MTProto fronting domains keep their address: their
// fake-TLS SNI names someone else's site, not our exit.
func TestJSONSubscriptionDirectByName(t *testing.T) {
	d := directRouting{sites: []string{"domain:bank.example", "domain:front.example.test"}, fronting: []string{"domain:front.example.test"},
		ips: []string{"geoip:private", "geoip:ru"}}
	for _, s := range directShapes(t, jsonSubscription(autoInputs(), d)) {
		if !s.RouteOnly || s.DomainStrategy != "AsIs" || s.DirectDial != "" || s.DomainsExcluded != nil {
			t.Fatalf("off: %+v, want the address as the app chose it", s)
		}
	}
	d.byName = true
	shapes := directShapes(t, jsonSubscription(autoInputs(), d))
	if len(shapes) != 3 {
		t.Fatalf("%d configs, want «Авто» and both Бамбук lines", len(shapes))
	}
	for i, s := range shapes {
		if s.RouteOnly || s.DomainStrategy != "IPOnDemand" || s.DirectDial != "UseIPv4v6" ||
			!slices.Equal(s.DomainsExcluded, []string{"domain:front.example.test"}) {
			t.Fatalf("config %d by name: %+v", i, s)
		}
	}
	// No fronting domain: no exclusion list at all.
	d.fronting = nil
	for _, s := range directShapes(t, jsonSubscription(autoInputs(), d)) {
		if s.DomainsExcluded != nil {
			t.Fatalf("empty fronting: %+v", s)
		}
	}
}

// The handler: by name only while the owner serves it (all, or the «Авто» test user).
func TestSubscriptionDirectByNameGating(t *testing.T) {
	for _, c := range []struct {
		name, mode, test string
		byName           bool
	}{
		{"absent", "", "", false},
		{"off", "off", goldenUser, false},
		{"test, other user", "test", "someone-else", false},
		{"test, this user", "test", goldenUser, true},
		{"on", "on", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := goldenScript()
			s.settings["sub_auto_mode"] = "on"
			s.settings["sub_auto_test_user"] = c.test
			if c.mode != "" {
				s.settings[subDirectByNameKey] = c.mode
			}
			useScriptDB(t, s)
			rec := subscriptionRequest(t, "Happ/4.7 Windows", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d %q", rec.Code, rec.Body.String())
			}
			if len(s.unknown) > 0 {
				t.Fatalf("unscripted queries: %v", s.unknown)
			}
			for _, sh := range directShapes(t, rec.Body.Bytes()) {
				if got := sh.DomainStrategy == "IPOnDemand"; got != c.byName {
					t.Fatalf("by name %v, want %v: %+v", got, c.byName, sh)
				}
				if c.byName && !slices.Contains(sh.DomainsExcluded, "domain:front.example.test") {
					t.Fatalf("the fronting domain is re-addressed: %+v", sh)
				}
			}
			if !c.byName && strings.Contains(rec.Body.String(), "domainsExcluded") {
				t.Fatal("off must keep the JSON as it was")
			}
		})
	}
}

func TestEntrySettingsDirectByNameWrites(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, m := range []string{"off", "test", "on"} {
		w, msg := entrySettingsRequest{DirectByName: str(" " + m + " ")}.writes(time.Now())
		if msg != "" || w[subDirectByNameKey] != m {
			t.Fatalf("%s: %v %q", m, w, msg)
		}
	}
	if _, msg := (entrySettingsRequest{DirectByName: str("maybe")}).writes(time.Now()); msg == "" {
		t.Fatal("accepted a bogus mode")
	}
}
