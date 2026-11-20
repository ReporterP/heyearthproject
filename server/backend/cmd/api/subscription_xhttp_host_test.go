package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Дуб's Host header is hub_xhttp_host, frozen at the value main_server_host had when the setting
// appeared: changing the Hub's address (main_server_host) must not change it, or every client
// keeps sending the old Host to an inbound that checks the new one.

func movedHubInputs() hubConnectionInputs {
	in := autoInputs()
	in.mainServerHost = "198.51.100.20"
	in.xhttpHost = "192.0.2.10"
	in.entries.pinned, in.entries.main = true, "203.0.113.5" // «· напрямую» lines exist too
	return in
}

func TestXHTTPHostSurvivesMainHostChange(t *testing.T) {
	in := movedHubInputs()

	var xhttpLines int
	for _, o := range hubConnectionOptions(in) {
		uri := o.GetUri()
		if !strings.Contains(uri, "type=xhttp") {
			continue
		}
		xhttpLines++
		if !strings.Contains(uri, "&host=192.0.2.10&") {
			t.Errorf("Дуб line keeps the frozen Host: %s", uri)
		}
	}
	if xhttpLines == 0 {
		t.Fatal("no Дуб lines built")
	}

	var cs []map[string]any
	if err := json.Unmarshal(jsonSubscription(in, directRouting{}), &cs); err != nil {
		t.Fatal(err)
	}
	var outbounds int
	for _, c := range cs {
		obs, _ := c["outbounds"].([]any)
		for _, o := range obs {
			ss, _ := o.(map[string]any)["streamSettings"].(map[string]any)
			xs, ok := ss["xhttpSettings"].(map[string]any)
			if !ok {
				continue
			}
			outbounds++
			if xs["host"] != "192.0.2.10" {
				t.Errorf("JSON XHTTP outbound host = %v, want 192.0.2.10", xs["host"])
			}
		}
	}
	if outbounds == 0 {
		t.Fatal("no XHTTP outbounds in the JSON subscription")
	}

	clash := clashProfile(in)
	if strings.Count(clash, "network: xhttp") == 0 {
		t.Fatal("no XHTTP proxies in the clash profile")
	}
	if strings.Count(clash, "network: xhttp") != strings.Count(clash, "host: 192.0.2.10") {
		t.Errorf("every clash XHTTP proxy carries host 192.0.2.10:\n%s", clash)
	}
}

func TestXHTTPHostDefaultsToMainHost(t *testing.T) {
	for _, tc := range []struct{ frozen, main, want string }{
		{"", "192.0.2.10", "192.0.2.10"},
		{"  ", "192.0.2.10", "192.0.2.10"},
		{"192.0.2.10", "198.51.100.20", "192.0.2.10"},
	} {
		if got := xhttpHostOr(tc.frozen, tc.main); got != tc.want {
			t.Errorf("xhttpHostOr(%q, %q) = %q, want %q", tc.frozen, tc.main, got, tc.want)
		}
	}
}

func TestXrayXHTTPInboundUsesFrozenHost(t *testing.T) {
	in := baseInputs()
	in.settings.mainServerHost = "198.51.100.20"
	in.settings.xhttpHost = "192.0.2.10"
	_, b := genConfig(t, in, quotaCutoff{})
	j, _ := json.Marshal(b.config)
	var cfg struct {
		Inbounds []struct {
			Tag            string `json:"tag"`
			StreamSettings struct {
				XHTTPSettings map[string]any `json:"xhttpSettings"`
			} `json:"streamSettings"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(j, &cfg); err != nil {
		t.Fatal(err)
	}
	for _, ib := range cfg.Inbounds {
		if ib.Tag == "vless-xhttp" {
			if got := ib.StreamSettings.XHTTPSettings["host"]; got != "192.0.2.10" {
				t.Fatalf("vless-xhttp host = %v, want 192.0.2.10", got)
			}
			return
		}
	}
	t.Fatal("no vless-xhttp inbound")
}
