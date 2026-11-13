package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

func autoInputs() hubConnectionInputs {
	return hubConnectionInputs{
		mainServerHost: "203.0.113.1", vlessUUID: goldenUUID, canVLESS: true,
		realityPubKey: "PBK-test", realityShortID: "ab12", realitySNI: "storage.yandexcloud.net",
		realityFlow: "xtls-rprx-vision", xhttpPort: "2443",
		canHysteria2: true, hy2Password: "hy2", obfsPassword: "obfs", hysteria2SNI: "tls.example.test",
		entries: entryPlan{interval: 4, reserves: []reserveEntry{{id: 5, address: "203.0.113.5", udp: true}, {id: 6, address: "203.0.113.6"}}},
	}
}

type autoConfig struct {
	Remarks   string           `json:"remarks"`
	Outbounds []map[string]any `json:"outbounds"`
	Routing   struct {
		Balancers []map[string]any `json:"balancers"`
		Rules     []map[string]any `json:"rules"`
	} `json:"routing"`
	Observatory map[string]any `json:"observatory"`
}

func parseAuto(t *testing.T, body []byte) []autoConfig {
	t.Helper()
	var cs []autoConfig
	if err := json.Unmarshal(body, &cs); err != nil {
		t.Fatalf("not a JSON array of configs: %v\n%s", err, body)
	}
	return cs
}

func outboundTags(c autoConfig) []string {
	var tags []string
	for _, o := range c.Outbounds {
		tags = append(tags, o["tag"].(string))
	}
	return tags
}

// «Авто» first, then every way in alone; the Hub and each reserve get a Берёза
// (fp firefox, flow) and a Дуб (fp chrome, the Hub's Host header); no Бамбук.
func TestJSONSubscriptionWays(t *testing.T) {
	d := directRouting{sites: []string{"domain:bank.example"}, ips: []string{"geoip:private", "geoip:ru", "198.51.100.7/32"}}
	cs := parseAuto(t, jsonSubscription(autoInputs(), d))
	var names []string
	for _, c := range cs {
		names = append(names, c.Remarks)
	}
	want := []string{"⚡ Авто", "🌳 Берёза", "🌲 Дуб", "🌳 Берёза · резерв", "🌲 Дуб · резерв", "🌳 Берёза · резерв 2", "🌲 Дуб · резерв 2"}
	if !slices.Equal(names, want) {
		t.Fatalf("configs %q, want %q", names, want)
	}
	auto := cs[0]
	if got := outboundTags(auto); !slices.Equal(got, []string{"p-main-tcp", "p-main-xhttp", "p-r5-tcp", "p-r5-xhttp", "p-r6-tcp", "p-r6-xhttp", "direct", "block"}) {
		t.Fatalf("auto outbounds %v", got)
	}
	b := auto.Routing.Balancers[0]
	if b["tag"] != "auto" || b["fallbackTag"] != "p-r5-tcp" || b["strategy"].(map[string]any)["type"] != "leastPing" {
		t.Fatalf("balancer %v", b)
	}
	if auto.Observatory["probeUrl"] != autoProbeURL || auto.Observatory["subjectSelector"].([]any)[0] != "p-" {
		t.Fatalf("observatory %v", auto.Observatory)
	}
	rules := auto.Routing.Rules
	if len(rules) != 4 || rules[0]["balancerTag"] != "auto" || rules[0]["ip"].([]any)[0] != "203.0.113.1" ||
		rules[1]["outboundTag"] != "direct" || rules[2]["outboundTag"] != "direct" || rules[3]["balancerTag"] != "auto" {
		t.Fatalf("auto rules %v", rules)
	}
	raw := string(jsonSubscription(autoInputs(), d))
	for _, bad := range []string{"hysteria", "salamander", "obfs"} {
		if strings.Contains(raw, bad) {
			t.Fatalf("JSON subscription mentions %q", bad)
		}
	}
	// A Берёза: TCP, flow, fp firefox, port 443; a Дуб: XHTTP, no flow, fp chrome, the Hub's Host.
	tcp, _ := json.Marshal(auto.Outbounds[2])
	xh, _ := json.Marshal(auto.Outbounds[3])
	for _, s := range []string{`"address":"203.0.113.5"`, `"port":443`, `"flow":"xtls-rprx-vision"`, `"fingerprint":"firefox"`, `"network":"tcp"`, `"publicKey":"PBK-test"`} {
		if !strings.Contains(string(tcp), s) {
			t.Errorf("Берёза · резерв lacks %s: %s", s, tcp)
		}
	}
	for _, s := range []string{`"address":"203.0.113.5"`, `"port":2443`, `"fingerprint":"chrome"`, `"network":"xhttp"`, `"host":"203.0.113.1"`} {
		if !strings.Contains(string(xh), s) {
			t.Errorf("Дуб · резерв lacks %s: %s", s, xh)
		}
	}
	if strings.Contains(string(xh), "flow") {
		t.Errorf("Дуб has a flow: %s", xh)
	}
	// A single way: its own outbound «proxy», the same rules to it.
	one := cs[1]
	if got := outboundTags(one); !slices.Equal(got, []string{"proxy", "direct", "block"}) || one.Routing.Rules[3]["outboundTag"] != "proxy" {
		t.Fatalf("single config %v %v", got, one.Routing.Rules)
	}
}

// During a failover the main lines follow the target; the Hub stays as «· напрямую».
func TestJSONSubscriptionFailover(t *testing.T) {
	in := autoInputs()
	in.entries.main, in.entries.failover = "203.0.113.5", true
	cs := parseAuto(t, jsonSubscription(in, directRouting{}))
	var names []string
	for _, c := range cs[1:] {
		names = append(names, c.Remarks)
	}
	if want := []string{"🌳 Берёза", "🌲 Дуб", "🌳 Берёза" + directLabel, "🌲 Дуб" + directLabel, "🌳 Берёза · резерв", "🌲 Дуб · резерв"}; !slices.Equal(names, want) {
		t.Fatalf("lines %q, want %q", names, want)
	}
	if fb := cs[0].Routing.Balancers[0]["fallbackTag"]; fb != "p-main-tcp" {
		t.Fatalf("fallback %v, want the target's Берёза", fb)
	}
	if !strings.Contains(string(jsonSubscription(in, directRouting{})), `"address":"203.0.113.6"`) {
		t.Fatal("the other reserve is missing")
	}
}

func TestJSONSubscriptionWithoutVLESS(t *testing.T) {
	for name, mut := range map[string]func(*hubConnectionInputs){
		"plan without vless": func(in *hubConnectionInputs) { in.canVLESS = false },
		"no reality key":     func(in *hubConnectionInputs) { in.realityPubKey = "" },
		"no hub":             func(in *hubConnectionInputs) { in.mainServerHost = "" },
	} {
		in := autoInputs()
		mut(&in)
		if b := jsonSubscription(in, directRouting{}); b != nil {
			t.Errorf("%s: %s", name, b)
		}
	}
}

func TestAutoModeServes(t *testing.T) {
	for _, c := range []struct {
		mode, test, user string
		want             bool
	}{
		{"", "", "u1", false}, {"off", "u1", "u1", false}, {"on", "", "u1", true},
		{"test", "u1", "u1", true}, {"test", "u2", "u1", false}, {"test", "", "", false}, {"bogus", "u1", "u1", false},
	} {
		if got := autoModeServes(c.mode, c.test, c.user); got != c.want {
			t.Errorf("autoModeServes(%q,%q,%q) = %v", c.mode, c.test, c.user, got)
		}
	}
}

// The handler: a Happ client gets «Авто» only while the owner serves it (all, or
// the test user); ?format=json asks for it; the plain URIs stay otherwise.
func TestSubscriptionAutoGating(t *testing.T) {
	plain := func(t *testing.T) []byte {
		s := goldenScript()
		useScriptDB(t, s)
		return subscriptionRequest(t, "Happ/3.1 Android", "").Body.Bytes()
	}(t)
	for _, c := range []struct {
		name, mode, test, ua, query string
		json                        bool
	}{
		{"off", "", "", "Happ/3.1 Android", "", false},
		{"test, other user", "test", "someone-else", "Happ/3.1 Android", "", false},
		{"test, this user", "test", goldenUser, "Happ/3.1 Android", "", true},
		{"on", "on", "", "Happ/3.1 Android", "", true},
		{"on, not Happ", "on", "", "v2rayNG/1.9", "", false},
		{"on, clash", "on", "", "Happ/3.1 Android", "?format=clash", false},
		{"asked", "", "", "v2rayNG/1.9", "?format=json", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := goldenScript()
			s.settings["sub_auto_mode"], s.settings["sub_auto_test_user"] = c.mode, c.test
			useScriptDB(t, s)
			rec := subscriptionRequest(t, c.ua, c.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d %q", rec.Code, rec.Body.String())
			}
			if len(s.unknown) > 0 {
				t.Fatalf("unscripted queries: %v", s.unknown)
			}
			isJSON := bytes.HasPrefix(rec.Body.Bytes(), []byte("["))
			if isJSON != c.json {
				t.Fatalf("json=%v, want %v: %.200s", isJSON, c.json, rec.Body.String())
			}
			if !c.json {
				if c.ua == "Happ/3.1 Android" && c.query == "" && !bytes.Equal(rec.Body.Bytes(), plain) {
					t.Fatal("the plain Happ subscription changed")
				}
				return
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") || rec.Header().Get("Socks-Auth-Mode") != "auto" {
				t.Fatalf("headers %v", rec.Header())
			}
			cs := parseAuto(t, rec.Body.Bytes())
			if cs[0].Remarks != "⚡ Авто" {
				t.Fatalf("first config %q", cs[0].Remarks)
			}
			// The plan's direct sites (eTLD+1), the fronting domain, geoip and the MTProto exit.
			body := rec.Body.String()
			for _, s := range []string{`"domain:bank.example"`, `"domain:front.example.test"`, `"geoip:ru"`, `"198.51.100.7/32"`} {
				if !strings.Contains(body, s) {
					t.Errorf("JSON lacks %s", s)
				}
			}
			if strings.Contains(body, "192.0.2.0/24") {
				t.Error("the services' addresses must stay out of the JSON (size)")
			}
		})
	}
}

// Links in the profile follow the host the client fetched it from.
func TestSubscriptionLinksOnSubDomain(t *testing.T) {
	for _, c := range []struct{ host, want string }{
		{"site.example.test", "https://hiearth.ru/dashboard"},
		{"go.example.test", "https://go.example.test/dashboard"},
		{"GO.example.test:443", "https://go.example.test/dashboard"},
	} {
		s := goldenScript()
		s.settings["sub_domain"] = "go.example.test"
		useScriptDB(t, s)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/user/subscribe/"+goldenToken, nil)
		req.Host = c.host
		req.Header.Set("User-Agent", "Happ/3.1 Android")
		rec := httptest.NewRecorder()
		handleSubscriptionLink(rec, req)
		if rec.Code != http.StatusOK || rec.Header().Get("Profile-Web-Page-Url") != c.want {
			t.Fatalf("%s: %d %q", c.host, rec.Code, rec.Header().Get("Profile-Web-Page-Url"))
		}
		if wantAnn := strings.TrimSuffix(c.want, "/dashboard"); !strings.HasPrefix(rec.Header().Get("Announce-Url"), strings.Replace(wantAnn, "https://hiearth.ru", "https://site.example.test", 1)) {
			t.Fatalf("%s: announce %q", c.host, rec.Header().Get("Announce-Url"))
		}
	}
}

func TestEntrySettingsSubscriptionWrites(t *testing.T) {
	str := func(s string) *string { return &s }
	w, msg := entrySettingsRequest{SubDomain: str(" GO.Example.Test. "), AutoMode: str("test"), AutoTestUser: str("user-1")}.writes(time.Now())
	if msg != "" || w["sub_domain"] != "go.example.test" || w["sub_auto_mode"] != "test" || w["sub_auto_test_user"] != "user-1" {
		t.Fatalf("%v %q", w, msg)
	}
	for _, bad := range []entrySettingsRequest{{SubDomain: str("https://go.example.test")}, {AutoMode: str("maybe")}, {AutoTestUser: str("a b")}} {
		if _, msg := bad.writes(time.Now()); msg == "" {
			t.Errorf("accepted %+v", bad)
		}
	}
	if w, msg := (entrySettingsRequest{SubDomain: str("")}).writes(time.Now()); msg != "" || w["sub_domain"] != "" {
		t.Fatalf("clearing: %v %q", w, msg)
	}
}

// A domain is saved only while the Hub answers for it with a valid certificate.
func TestEntrySettingsSubDomainNeedsCertificate(t *testing.T) {
	prevTLS, prevSet, prevAudit, prevBump := subDomainTLS, entriesSetSetting, entriesAudit, entriesBump
	t.Cleanup(func() {
		subDomainTLS, entriesSetSetting, entriesAudit, entriesBump = prevTLS, prevSet, prevAudit, prevBump
	})
	saved := map[string]string{}
	entriesSetSetting = func(_ context.Context, k, v string) error { saved[k] = v; return nil }
	entriesAudit = func(*http.Request, string, string, string, any) {}
	bumped := 0
	entriesBump = func(context.Context) error { bumped++; return nil }
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handleAdminEntrySettings(rec, httptest.NewRequest(http.MethodPut, "/api/admin/entries/settings", strings.NewReader(body)))
		return rec
	}
	subDomainTLS = func(context.Context, string) (time.Time, error) {
		return time.Time{}, errors.New("x509: certificate is valid for hiearth.ru")
	}
	if rec := put(`{"sub_domain":"go.example.test"}`); rec.Code != http.StatusConflict || saved["sub_domain"] != "" {
		t.Fatalf("no certificate: %d %v", rec.Code, saved)
	}
	subDomainTLS = func(context.Context, string) (time.Time, error) { return time.Now().Add(80 * 24 * time.Hour), nil }
	if rec := put(`{"sub_domain":"go.example.test"}`); rec.Code != http.StatusOK || saved["sub_domain"] != "go.example.test" || bumped != 0 {
		t.Fatalf("certificate: %d %v bumped %d", rec.Code, saved, bumped)
	}
	if rec := put(`{"auto_mode":"on"}`); rec.Code != http.StatusOK || saved["sub_auto_mode"] != "on" || bumped != 1 {
		t.Fatalf("auto mode: %d %v bumped %d", rec.Code, saved, bumped)
	}
}

func TestSubDomainStatus(t *testing.T) {
	prevLookup, prevTLS := subDomainLookup, subDomainTLS
	t.Cleanup(func() { subDomainLookup, subDomainTLS = prevLookup, prevTLS })
	subDomainLookup = func(context.Context, string) ([]string, error) { return []string{"203.0.113.9", "203.0.113.5"}, nil }
	until := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	subDomainTLS = func(context.Context, string) (time.Time, error) { return until, nil }
	entries := []biz.Entry{
		{ID: 0, Kind: biz.EntryKindHub, IP: "203.0.113.1", Lifecycle: "online"},
		{ID: 5, Kind: biz.EntryKindRelay, IP: "203.0.113.5", Lifecycle: "online"},
		{ID: 6, Kind: biz.EntryKindRelay, IP: "203.0.113.6", Lifecycle: "online"},
		{ID: 7, Kind: biz.EntryKindRelay, IP: "203.0.113.7", Lifecycle: "installing"},
	}
	v := subDomainStatus(context.Background(), "go.example.test", "test", "user-1", entries)
	if !slices.Equal(v.DNS, []string{"203.0.113.5", "203.0.113.9"}) || !slices.Equal(v.DNSForeign, []string{"203.0.113.9"}) ||
		!slices.Equal(v.DNSMissing, []string{"203.0.113.6"}) || !v.CertOK || !v.CertUntil.Equal(until) || v.AutoMode != "test" {
		t.Fatalf("%+v", v)
	}
	if v := subDomainStatus(context.Background(), "", "", "", entries); v.CertOK || len(v.DNS) != 0 || v.AutoMode != autoModeOff {
		t.Fatalf("no domain: %+v", v)
	}
}

// ?format=json without a VLESS way in (no Reality key here) answers the plain list, never an empty 200.
func TestSubscriptionJSONWithoutVLESSFallsBack(t *testing.T) {
	s := goldenScript()
	s.settings["reality_public_key"] = ""
	useScriptDB(t, s)
	rec := subscriptionRequest(t, "Happ/3.1 Android", "?format=json")
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 || bytes.HasPrefix(rec.Body.Bytes(), []byte("[")) ||
		strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "hysteria2://") {
		t.Fatalf("the plain list lacks Бамбук: %q", rec.Body.String())
	}
}
