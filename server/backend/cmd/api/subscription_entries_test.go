package main

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/pb/heyearth_v1"
)

// Reserve entries in the subscription (phase 3, Task 3.5): «· резерв» lines per healthy relay, the
// failover of the main lines to the target relay, Profile-Update-Interval. Through the real code
// down to SQL (scriptDB of subscription_golden_test.go).

var relayCols = []string{"id", "name", "ip_address", "country_code", "lifecycle_state", "address_mode", "domain",
	"ext_status", "ext_checked_at", "ext_error", "udp_status", "udp_checked_at", "udp_error", "ru_status", "ru_checked_at", "ru_link"}

func relayRow(id int64, ip, ext, udp string) []driver.Value {
	return []driver.Value{id, fmt.Sprintf("Резерв %d", id), ip, "RU", "online", "ip", "", ext, nil, "", udp, nil, "", "unknown", nil, ""}
}

const (
	eHub = "hub.example.test"
	eR5  = "203.0.113.50"
	eR6  = "203.0.113.60"
	eR7  = "203.0.113.70"
)

// entriesScript is goldenScript with reserve entries and the failover state of a scenario.
func entriesScript(t *testing.T, scenario string) *scriptDB {
	t.Helper()
	s := goldenScript()
	var rows [][]driver.Value
	switch scenario {
	case "relay": // 5 healthy (UDP too), 6 unhealthy, 7 healthy TCP but UDP unconfirmed
		rows = [][]driver.Value{relayRow(5, eR5, "healthy", "healthy"), relayRow(6, eR6, "unhealthy", "healthy"), relayRow(7, eR7, "healthy", "unknown")}
	case "failover", "relay_unhealthy":
		ext := map[string]string{"failover": "healthy", "relay_unhealthy": "unhealthy"}[scenario]
		rows = [][]driver.Value{relayRow(5, eR5, ext, "healthy")}
		s.settings["entry_failover_active"], s.settings["entry_failover_target"] = "true", "5"
		s.settings["entry_failover_since"] = "2026-10-12T10:00:00Z"
	default:
		t.Fatalf("scenario %s", scenario)
	}
	for i := range s.queries {
		if s.queries[i].name == "relays" {
			s.queries[i].rows = rows
			return s
		}
	}
	t.Fatal("goldenScript has no relays query")
	return nil
}

type subLine struct{ host, port, hostParam string }

func subLines(t *testing.T, uris []string) map[string]subLine {
	t.Helper()
	out := map[string]subLine{}
	for _, raw := range uris {
		if !strings.HasPrefix(raw, "vless://") && !strings.HasPrefix(raw, "hysteria2://") {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("bad URI %q: %v", raw, err)
		}
		out[u.Fragment] = subLine{u.Hostname(), u.Port(), u.Query().Get("host")}
	}
	return out
}

func entriesRequest(t *testing.T, s *scriptDB, ua, query string) (string, http.Header) {
	t.Helper()
	useScriptDB(t, s)
	rec := subscriptionRequest(t, ua, query)
	if rec.Code != http.StatusOK || len(s.unknown) > 0 {
		t.Fatalf("status %d, unscripted %v, body %.200q", rec.Code, s.unknown, rec.Body.String())
	}
	return rec.Body.String(), rec.Header()
}

func wantLines(t *testing.T, got map[string]subLine, want map[string]string) {
	t.Helper()
	for label, hostPort := range want {
		if l, ok := got[label]; !ok || l.host+":"+l.port != hostPort {
			t.Errorf("%q: got %+v (present %v), want %s", label, l, ok, hostPort)
		}
	}
}

func TestEntriesSubscriptionLines(t *testing.T) {
	happ := func(t *testing.T, s *scriptDB) (map[string]subLine, http.Header) {
		body, h := entriesRequest(t, s, "Happ/3.1 Android", "")
		return subLines(t, strings.Split(body, "\n")), h
	}
	t.Run("relay", func(t *testing.T) {
		lines, h := happ(t, entriesScript(t, "relay"))
		wantLines(t, lines, map[string]string{"🌳 Берёза 1": eHub + ":443", "🎋 Бамбук 1 only WiFi": eHub + ":50443",
			"🌳 Берёза · резерв": eR5 + ":443", "🌲 Дуб · резерв": eR5 + ":2443",
			"🎋 Бамбук 1 only WiFi · резерв": eR5 + ":50443", "🎋 Бамбук 2 only WiFi · резерв": eR5 + ":50443",
			"🌳 Берёза · резерв 2": eR7 + ":443", "🌲 Дуб · резерв 2": eR7 + ":2443"})
		if hp := lines["🌲 Дуб · резерв"].hostParam; hp != eHub {
			t.Errorf("XHTTP host= must stay %s (the Hub inbound checks it), got %q", eHub, hp)
		}
		if _, ok := lines["🎋 Бамбук 1 only WiFi · резерв 2"]; ok {
			t.Error("Bamboo through an entry whose UDP is not confirmed")
		}
		for label, l := range lines {
			if l.host == eR6 {
				t.Errorf("unhealthy relay in %q", label)
			}
		}
		if got := h.Get("Profile-Update-Interval"); got != "4" {
			t.Errorf("interval %q, want 4", got)
		}
	})
	t.Run("failover", func(t *testing.T) {
		lines, h := happ(t, entriesScript(t, "failover"))
		wantLines(t, lines, map[string]string{"🌳 Берёза 1": eR5 + ":443", "🌳 Берёза 2": eR5 + ":443", "🌲 Дуб 1": eR5 + ":2443",
			"🎋 Бамбук 2 only WiFi": eR5 + ":50443", "☁️ Облако (CDN)": "cdn.example.test:443", "🌳 Берёза · резерв": eR5 + ":443"})
		if hp := lines["🌲 Дуб 1"].hostParam; hp != eHub {
			t.Errorf("main XHTTP host= changed to %q during failover", hp)
		}
		if got := h.Get("Profile-Update-Interval"); got != "1" {
			t.Errorf("interval %q, want 1 during failover", got)
		}
	})
	t.Run("failover target unhealthy", func(t *testing.T) {
		lines, h := happ(t, entriesScript(t, "relay_unhealthy"))
		wantLines(t, lines, map[string]string{"🌳 Берёза 1": eHub + ":443"})
		for label := range lines {
			if strings.Contains(label, "резерв") {
				t.Errorf("reserve line %q of an unhealthy relay", label)
			}
		}
		if got := h.Get("Profile-Update-Interval"); got != "1" {
			t.Errorf("interval %q, want 1 while the failover flag is on", got)
		}
	})
	t.Run("test user sees the failover alone", func(t *testing.T) {
		s := entriesScript(t, "relay")
		s.settings["entry_failover_test_user"] = goldenUser
		lines, h := happ(t, s)
		wantLines(t, lines, map[string]string{"🌳 Берёза 1": eR5 + ":443"})
		if h.Get("Profile-Update-Interval") != "1" {
			t.Error("test user: interval must be 1")
		}
		s = entriesScript(t, "relay")
		s.settings["entry_failover_test_user"] = "someone-else"
		lines, _ = happ(t, s)
		wantLines(t, lines, map[string]string{"🌳 Берёза 1": eHub + ":443"})
	})
	t.Run("interval setting", func(t *testing.T) {
		for raw, want := range map[string]string{"6": "6", "24": "24", "0": "4", "25": "4", "abc": "4"} {
			s := entriesScript(t, "relay")
			s.settings["subscription_update_interval_hours"] = raw
			if _, h := happ(t, s); h.Get("Profile-Update-Interval") != want {
				t.Errorf("setting %q → %q, want %q", raw, h.Get("Profile-Update-Interval"), want)
			}
		}
	})
	t.Run("clash", func(t *testing.T) {
		body, _ := entriesRequest(t, entriesScript(t, "relay"), "clash-verge/2.0", "?format=clash")
		for _, want := range []string{"name: \"HeYearth-Home · резерв\"\n    type: vless\n    server: " + eR5,
			"name: \"HeYearth-XHTTP · резерв 2\"\n    type: vless\n    server: " + eR7,
			"name: \"HiEarth-Express · резерв\"\n    type: hysteria2\n    server: " + eR5, "      - \"HeYearth-Home · резерв\""} {
			if !strings.Contains(body, want) {
				t.Errorf("clash lacks %q", want)
			}
		}
		if strings.Contains(body, "HiEarth-Express · резерв 2") || strings.Contains(body, eR6) {
			t.Error("clash: Bamboo through an unconfirmed UDP entry, or an unhealthy relay")
		}
		body, _ = entriesRequest(t, entriesScript(t, "failover"), "clash-verge/2.0", "?format=clash")
		if !strings.Contains(body, "name: \"HeYearth-Home\"\n    type: vless\n    server: "+eR5) {
			t.Error("clash main proxy not moved to the reserve during failover")
		}
	})
}

// planEntries is the decision itself (pure): which relays are reserves and where the main lines
// point. Review Focus 4 on the subscription side: a stale or wrong failover flag never points
// users at a relay that is not a healthy reserve right now.
func TestPlanEntries(t *testing.T) {
	relay := func(id int, addr, lifecycle, ext, udp string) biz.Entry {
		return biz.Entry{ID: id, Kind: biz.EntryKindRelay, Address: addr, Lifecycle: lifecycle,
			Health: biz.EntryHealth{External: ext, UDP: udp}}
	}
	relays := []biz.Entry{
		relay(9, "r9.example.test", "online", biz.EntryHealthy, biz.EntryUnknown),
		relay(5, "r5.example.test", "online", biz.EntryHealthy, biz.EntryHealthy),
		relay(6, "r6.example.test", "online", biz.EntryUnhealthy, biz.EntryHealthy),
		relay(7, "r7.example.test", "provisioning", biz.EntryHealthy, biz.EntryHealthy),
		relay(8, "", "online", biz.EntryHealthy, biz.EntryHealthy),
		{ID: 0, Kind: biz.EntryKindHub, Address: "hub", Lifecycle: "online", Health: biz.EntryHealth{External: biz.EntryHealthy}},
	}
	active := func(target int) biz.EntryFailover { return biz.EntryFailover{Active: true, Target: target} }
	cases := []struct {
		name     string
		fo       biz.EntryFailover
		forced   bool
		interval string
		main     string
		failover bool
		hours    int
	}{
		{"no failover", biz.EntryFailover{}, false, "", "hub", false, 4},
		{"no failover, interval 12", biz.EntryFailover{}, false, " 12 ", "hub", false, 12},
		{"global failover to a healthy reserve", active(9), false, "12", "r9.example.test", true, 1},
		{"target unhealthy: lines stay on the Hub", active(6), false, "", "hub", false, 1},
		{"target not online", active(7), false, "", "hub", false, 1},
		{"target without an address", active(8), false, "", "hub", false, 1},
		{"target unknown", active(42), false, "", "hub", false, 1},
		{"test user: the first healthy reserve", biz.EntryFailover{}, true, "6", "r5.example.test", true, 1},
		{"test user during a global failover: its target", active(9), true, "", "r9.example.test", true, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := planEntries("hub", relays, c.fo, c.forced, c.interval)
			if p.main != c.main || p.failover != c.failover || p.interval != c.hours {
				t.Errorf("planEntries = main %q failover %v interval %d, want %q %v %d", p.main, p.failover, p.interval, c.main, c.failover, c.hours)
			}
			want := []reserveEntry{{5, "r5.example.test", true}, {9, "r9.example.test", false}}
			if fmt.Sprint(p.reserves) != fmt.Sprint(want) {
				t.Errorf("reserves %v, want %v (healthy online relays with an address, by id)", p.reserves, want)
			}
		})
	}
	if got := []string{reserveSuffix(0), reserveSuffix(1), reserveSuffix(2)}; fmt.Sprint(got) != fmt.Sprint([]string{" · резерв", " · резерв 2", " · резерв 3"}) {
		t.Errorf("reserveSuffix: %q", got)
	}
}

// V2Ray (base64) and the gRPC bundle carry exactly the Happ lines; then the byte-exact goldens.
func TestEntriesSubscriptionGolden(t *testing.T) {
	labels := func(m map[string]subLine) []string {
		var out []string
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	for _, sc := range []string{"relay", "failover", "relay_unhealthy"} {
		t.Run(sc, func(t *testing.T) {
			happBody, _ := entriesRequest(t, entriesScript(t, sc), "Happ/3.1 Android", "")
			want := fmt.Sprint(labels(subLines(t, strings.Split(happBody, "\n"))))
			v2, _ := entriesRequest(t, entriesScript(t, sc), "v2rayNG/1.9", "")
			dec, err := base64.StdEncoding.DecodeString(v2)
			if err != nil || fmt.Sprint(labels(subLines(t, strings.Split(string(dec), "\n")))) != want {
				t.Errorf("v2ray lines differ from happ (%v)", err)
			}
			s := entriesScript(t, sc)
			useScriptDB(t, s)
			resp, err := (&server{}).GetConnectionBundle(context.Background(), &heyearth_v1.GetConnectionBundleRequest{UserId: goldenUser})
			if err != nil {
				t.Fatal(err)
			}
			var uris []string
			for _, o := range resp.GetConnectionOptions() {
				uris = append(uris, o.GetUri())
			}
			if fmt.Sprint(labels(subLines(t, uris))) != want {
				t.Errorf("gRPC bundle lines differ from happ")
			}
			checkGolden(t, "entries_"+sc+"_bundle", bundleJSON(t, resp))
			for _, f := range []struct{ name, ua, query string }{{"happ", "Happ/3.1 Android", ""}, {"v2ray", "v2rayNG/1.9", ""}, {"clash", "clash-verge/2.0", "?format=clash"}} {
				s := entriesScript(t, sc)
				useScriptDB(t, s)
				rec := subscriptionRequest(t, f.ua, f.query)
				checkGolden(t, "entries_"+sc+"_"+f.name, rec.Body.Bytes())
				checkGolden(t, "entries_"+sc+"_"+f.name+"_headers", goldenHeaders(rec))
			}
		})
	}
}
