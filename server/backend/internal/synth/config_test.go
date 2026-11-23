package synth

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// fixture is the JSON subscription of testdata/happ.json: «⚡ Авто» (ways p-*) and two Бамбук
// lines (ways b-*), by name with the dns section. cmd/api keeps the file equal to what the
// generator serves (TestSynthFixtureMatchesGenerator).
func fixture(t *testing.T) []map[string]any {
	t.Helper()
	body, err := os.ReadFile("testdata/happ.json")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := Lines(body)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func outboundByTag(cfg map[string]any, tag string) map[string]any {
	for _, o := range cfg["outbounds"].([]any) {
		if m := o.(map[string]any); m["tag"] == tag {
			return m
		}
	}
	return nil
}

func TestLines(t *testing.T) {
	lines := fixture(t)
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	if got := Label(lines[0]); got != "⚡ Авто" {
		t.Errorf("label = %q", got)
	}
	if !strings.HasPrefix(Label(lines[1]), "🎋 Бамбук 1") {
		t.Errorf("label = %q", Label(lines[1]))
	}
	for _, bad := range []string{"", "vless://not-json", "{}", "[]"} {
		if _, err := Lines([]byte(bad)); err == nil {
			t.Errorf("Lines(%q): want an error (a subscription without a single config is not a subscription)", bad)
		}
	}
}

func TestWays(t *testing.T) {
	lines := fixture(t)
	if got, want := Ways(lines[0]), []string{"p-hub-tcp", "p-hub-xhttp", "p-r5-tcp", "p-r5-xhttp", "p-r6-tcp", "p-r6-xhttp"}; !reflect.DeepEqual(got, want) {
		t.Errorf("«Авто» ways = %v, want %v", got, want)
	}
	if got, want := Ways(lines[1]), []string{"b-hub", "b-r5"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Бамбук ways = %v, want %v", got, want)
	}
	if got := Ways(map[string]any{"outbounds": []any{}}); len(got) != 0 {
		t.Errorf("no balancer: ways = %v", got)
	}
}

func TestForWay(t *testing.T) {
	lines := fixture(t)
	before := mustJSON(t, lines[0])
	cfg, err := ForWay(lines[0], "p-r5-xhttp", 21808, &Via{Host: "127.0.0.1", Port: 1090})
	if err != nil {
		t.Fatal(err)
	}
	if after := mustJSON(t, lines[0]); after != before {
		t.Fatal("ForWay changed the source config")
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Fatalf("inbounds = %d, want 1", len(inbounds))
	}
	in := inbounds[0].(map[string]any)
	if in["protocol"] != "socks" || in["listen"] != "127.0.0.1" || in["port"] != 21808 {
		t.Errorf("inbound = %v", in)
	}
	for _, gone := range []string{"burstObservatory", "dns", "remarks", "meta"} {
		if _, ok := cfg[gone]; ok {
			t.Errorf("a one-way config must not carry %q", gone)
		}
	}
	if dig(cfg, "routing", "balancers") != nil {
		t.Error("a one-way config must not carry balancers")
	}
	rules := dig(cfg, "routing", "rules").([]any)
	if len(rules) != 1 || rules[0].(map[string]any)["outboundTag"] != "p-r5-xhttp" {
		t.Errorf("rules = %v, want one rule to the way", rules)
	}
	outs := cfg["outbounds"].([]any)
	if len(outs) != 2 {
		t.Fatalf("outbounds = %d, want the way and synth-via", len(outs))
	}
	way := outboundByTag(cfg, "p-r5-xhttp")
	if dig(way, "streamSettings", "sockopt", "dialerProxy") != "synth-via" {
		t.Errorf("the way must dial through synth-via: %v", dig(way, "streamSettings", "sockopt"))
	}
	if dig(way, "streamSettings", "sockopt", "tcpKeepAliveIdle") == nil {
		t.Error("the way's own sockopt was lost")
	}
	via := outboundByTag(cfg, "synth-via")
	if via == nil || via["protocol"] != "socks" {
		t.Fatalf("synth-via = %v", via)
	}
	srv := dig(via, "settings", "servers").([]any)[0].(map[string]any)
	if srv["address"] != "127.0.0.1" || srv["port"] != 1090 {
		t.Errorf("synth-via server = %v", srv)
	}
	// the result is what xray reads: it must survive a JSON round trip
	var back map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, cfg)), &back); err != nil {
		t.Fatal(err)
	}
}

func TestForWayUnknown(t *testing.T) {
	lines := fixture(t)
	for _, tag := range []string{"p-r9-tcp", "direct", "block", ""} {
		if _, err := ForWay(lines[0], tag, 21808, nil); err == nil {
			t.Errorf("ForWay(%q): want an error", tag)
		}
	}
}

// Bamboo's port hopping cannot run through a proxy (xray: "udphop requires being at the
// outermost level"), so through Via the way dials ONE port picked from the hop range — which
// also checks the hop DNAT from outside. Without Via the outbound stays as served.
func TestForWayBambooHop(t *testing.T) {
	lines := fixture(t)
	old := pickPort
	defer func() { pickPort = old }()
	var lo, hi int
	pickPort = func(a, b int) int { lo, hi = a, b; return 41234 }

	cfg, err := ForWay(lines[1], "b-r5", 21808, &Via{Host: "127.0.0.1", Port: 1090})
	if err != nil {
		t.Fatal(err)
	}
	way := outboundByTag(cfg, "b-r5")
	if dig(way, "streamSettings", "finalmask", "quicParams", "udpHop") != nil {
		t.Error("udpHop must be removed when the way goes through Via")
	}
	if dig(way, "settings", "port") != 41234 || lo != 30000 || hi != 65000 {
		t.Errorf("port = %v picked from %d-%d, want 41234 from 30000-65000", dig(way, "settings", "port"), lo, hi)
	}
	if dig(way, "streamSettings", "finalmask", "quicParams", "congestion") != "bbr" {
		t.Error("the other quic parameters must stay")
	}
	if dig(way, "streamSettings", "finalmask", "udp") == nil {
		t.Error("salamander must stay")
	}
	if dig(way, "streamSettings", "sockopt", "dialerProxy") != "synth-via" {
		t.Error("the Bamboo way must dial through synth-via")
	}

	direct, err := ForWay(lines[1], "b-r5", 21808, nil)
	if err != nil {
		t.Fatal(err)
	}
	if dig(outboundByTag(direct, "b-r5"), "streamSettings", "finalmask", "quicParams", "udpHop") == nil {
		t.Error("without Via the hop stays as served")
	}
}

func TestAsServed(t *testing.T) {
	lines := fixture(t)
	before := mustJSON(t, lines[0])
	cfg, err := AsServed(lines[0], 21808, &Via{Host: "127.0.0.1", Port: 1090})
	if err != nil {
		t.Fatal(err)
	}
	if mustJSON(t, lines[0]) != before {
		t.Fatal("AsServed changed the source config")
	}
	for _, same := range [][]string{{"routing", "rules"}, {"routing", "balancers"}, {"routing", "domainStrategy"}, {"dns"}, {"burstObservatory"}} {
		if got, want := mustJSON(t, dig(cfg, same...)), mustJSON(t, dig(lines[0], same...)); got != want {
			t.Errorf("%v differs from what is served:\n got %s\nwant %s", same, got, want)
		}
	}
	inbounds := cfg["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Fatalf("inbounds = %d, want 1", len(inbounds))
	}
	in := inbounds[0].(map[string]any)
	if in["protocol"] != "socks" || in["port"] != 21808 || in["listen"] != "127.0.0.1" || in["tag"] != "socks" {
		t.Errorf("inbound = %v", in)
	}
	// by name: the sniffing of the served socks inbound decides where a name goes — keep it
	if got, want := mustJSON(t, in["sniffing"]), mustJSON(t, lines[0]["inbounds"].([]any)[0].(map[string]any)["sniffing"]); got != want {
		t.Errorf("sniffing = %s, want the served %s", got, want)
	}
	for _, way := range Ways(lines[0]) {
		if dig(outboundByTag(cfg, way), "streamSettings", "sockopt", "dialerProxy") != "synth-via" {
			t.Errorf("way %s does not dial through synth-via", way)
		}
	}
	for _, tag := range []string{"direct", "block"} {
		if dig(outboundByTag(cfg, tag), "streamSettings", "sockopt", "dialerProxy") != nil {
			t.Errorf("%s must not get a dialerProxy", tag)
		}
	}
	if outboundByTag(cfg, "synth-via") == nil {
		t.Error("synth-via is missing")
	}
	for _, gone := range []string{"remarks", "meta"} {
		if _, ok := cfg[gone]; ok {
			t.Errorf("%q is not an xray field", gone)
		}
	}
	// a Bamboo line as served, through Via: no hop on any way
	b, err := AsServed(lines[1], 21808, &Via{Host: "127.0.0.1", Port: 1090})
	if err != nil {
		t.Fatal(err)
	}
	for _, way := range Ways(lines[1]) {
		if dig(outboundByTag(b, way), "streamSettings", "finalmask", "quicParams", "udpHop") != nil {
			t.Errorf("way %s keeps udpHop behind a proxy", way)
		}
	}
}

func TestViaNil(t *testing.T) {
	lines := fixture(t)
	for name, build := range map[string]func() (map[string]any, error){
		"ForWay":   func() (map[string]any, error) { return ForWay(lines[0], "p-hub-tcp", 21808, nil) },
		"AsServed": func() (map[string]any, error) { return AsServed(lines[0], 21808, nil) },
	} {
		cfg, err := build()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if outboundByTag(cfg, "synth-via") != nil {
			t.Errorf("%s: synth-via without Via", name)
		}
		if strings.Contains(mustJSON(t, cfg), "dialerProxy") {
			t.Errorf("%s: dialerProxy without Via", name)
		}
	}
}

func TestLintByNameWithoutDNS(t *testing.T) {
	lines := fixture(t)
	if problems := Lint(lines[0]); len(problems) != 0 {
		t.Errorf("the served config must be clean: %v", problems)
	}
	var noDNS map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, lines[0])), &noDNS); err != nil {
		t.Fatal(err)
	}
	delete(noDNS, "dns")
	problems := Lint(noDNS)
	if len(problems) != 1 || !strings.Contains(problems[0], "by name without dns") {
		t.Errorf("by name and no dns section: problems = %v, want one «by name without dns»", problems)
	}
	// not by name (IPIfNonMatch): the device resolver is not asked for every name, no dns needed
	noDNS["routing"].(map[string]any)["domainStrategy"] = "IPIfNonMatch"
	if problems := Lint(noDNS); len(problems) != 0 {
		t.Errorf("not by name: %v", problems)
	}
}

func TestLintNoWays(t *testing.T) {
	lines := fixture(t)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(mustJSON(t, lines[0])), &cfg); err != nil {
		t.Fatal(err)
	}
	var kept []any
	for _, o := range cfg["outbounds"].([]any) {
		if tag, _ := o.(map[string]any)["tag"].(string); !strings.HasPrefix(tag, "p-") {
			kept = append(kept, o)
		}
	}
	cfg["outbounds"] = kept
	problems := Lint(cfg)
	if len(problems) != 1 || !strings.Contains(problems[0], "no ways") {
		t.Errorf("a balancer without a single way: problems = %v", problems)
	}
}
