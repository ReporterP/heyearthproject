package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRoutingBuilders(t *testing.T) {
	b := newBalancer("plan-3-balancer", []string{"exit-2"}, "random")
	if b["fallbackTag"] != "exit-2" || b["tag"] != "plan-3-balancer" || !reflect.DeepEqual(b["selector"], []string{"exit-2"}) {
		t.Fatalf("unexpected balancer: %v", b)
	}
	if e := newBalancer("plan-9-balancer", nil, "random"); e["fallbackTag"] != "block" {
		t.Fatalf("empty balancer must fall back to block: %v", e)
	}
	r := noExitBlockRule([]string{"vless-reality", "socks-awg"})
	if r["outboundTag"] != "block" || !reflect.DeepEqual(r["inboundTag"], []string{"vless-reality", "socks-awg"}) {
		t.Fatalf("unexpected no-exit rule: %v", r)
	}
	d := dnsDirectRule()
	if d["outboundTag"] != "direct" || !reflect.DeepEqual(d["inboundTag"], []string{"dns-out"}) {
		t.Fatalf("unexpected dns rule: %v", d)
	}
	h := dnsHijackOutbound()
	if h["protocol"] != "dns" || h["tag"] != "dns-hijack" {
		t.Fatalf("unexpected dns hijack outbound: %v", h)
	}
	nd := noExitDNSRule([]string{"vless-reality", "socks-awg"})
	if nd["outboundTag"] != "dns-hijack" || nd["port"] != "53" || nd["type"] != "field" ||
		!reflect.DeepEqual(nd["inboundTag"], []string{"vless-reality", "socks-awg"}) {
		t.Fatalf("unexpected no-exit dns rule: %v", nd)
	}
	inbounds := []map[string]interface{}{
		{"tag": "vless-reality"}, {"tag": "vless-xhttp"},
		{"tag": "vless-xhttp-cdn"}, {"tag": "api-in"}, {"tag": "socks-awg"},
	}
	want := []string{"vless-reality", "vless-xhttp", "vless-xhttp-cdn", "socks-awg"}
	if got := userInboundTags(inbounds); !reflect.DeepEqual(got, want) {
		t.Fatalf("userInboundTags = %v, want %v", got, want)
	}
}

// leakFixture holds a config shaped like what SyncXrayConfig builds, in both modes.
type leakFixture struct {
	inbounds []map[string]interface{}
	good     []map[string]interface{} // outbounds, healthy exits
	noExit   []map[string]interface{} // outbounds, no healthy exits (+ dns-hijack)
}

func newLeakFixture() leakFixture {
	direct := map[string]interface{}{"protocol": "freedom", "tag": "direct"}
	return leakFixture{
		inbounds: []map[string]interface{}{
			{"tag": "vless-reality"}, {"tag": "vless-xhttp"},
			{"tag": "api-in"}, {"tag": "socks-awg"},
		},
		good:   []map[string]interface{}{newBlockOutbound(), direct},
		noExit: []map[string]interface{}{newBlockOutbound(), direct, dnsHijackOutbound()},
	}
}

func routingWith(rules []map[string]interface{}, balancers ...map[string]interface{}) map[string]interface{} {
	r := map[string]interface{}{"rules": rules}
	if len(balancers) > 0 {
		r["balancers"] = balancers
	}
	return r
}

func TestCheckNoDirectLeak(t *testing.T) {
	fx := newLeakFixture()
	user := userInboundTags(fx.inbounds)
	bal := newBalancer("exit-balancer", []string{"exit-2"}, "random")
	// Shape of the Task 7 generator: one plan-* tagged rule per plan.
	exitRules := []map[string]interface{}{
		{"type": "field", "inboundTag": []string{"api-in"}, "outboundTag": "api"},
		dnsDirectRule(),
		{"type": "field", "ruleTag": "plan-3-direct-domains", "user": []string{"u1", "u2"}, "domain": []string{"domain:yandex.ru"}, "outboundTag": "direct-freedom"},
		{"type": "field", "ruleTag": "plan-3-users", "user": []string{"u1", "u2"}, "balancerTag": "plan-3-balancer"},
		{"type": "field", "network": "tcp,udp", "balancerTag": "exit-balancer"},
	}
	noExitRules := []map[string]interface{}{
		dnsDirectRule(), noExitDNSRule(user), noExitBlockRule(user),
	}

	// accepted
	planBal := newBalancer("plan-3-balancer", []string{"exit-2"}, "random")
	if err := checkNoDirectLeak(fx.inbounds, fx.good, routingWith(exitRules, bal, planBal)); err != nil {
		t.Fatalf("valid exit config rejected: %v", err)
	}
	if err := checkNoDirectLeak(fx.inbounds, fx.noExit, routingWith(noExitRules)); err != nil {
		t.Fatalf("valid no-exit config rejected: %v", err)
	}
	// an empty balancer list is the no-exit mode too
	if err := checkNoDirectLeak(fx.inbounds, fx.noExit, map[string]interface{}{
		"rules": noExitRules, "balancers": []map[string]interface{}{},
	}); err != nil {
		t.Fatalf("no-exit config with empty balancers rejected: %v", err)
	}

	// default outbound
	directFirst := []map[string]interface{}{fx.good[1], fx.good[0]}
	if checkNoDirectLeak(fx.inbounds, directFirst, routingWith(exitRules, bal)) == nil {
		t.Fatal("direct-first config accepted")
	}

	// balancer fallbacks
	legacyBal := map[string]interface{}{"tag": "exit-balancer", "selector": []string{"exit-2"}}
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith(exitRules, legacyBal)) == nil {
		t.Fatal("balancer without fallbackTag accepted")
	}
	directFallback := map[string]interface{}{"tag": "exit-balancer", "selector": []string{"exit-2"}, "fallbackTag": "direct"}
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith(exitRules, directFallback)) == nil {
		t.Fatal("balancer falling back to direct accepted")
	}

	// (a) the only rule that may use the Hub-local "direct" outbound is dns-out
	userToDirect := append(append([]map[string]interface{}{}, exitRules...),
		map[string]interface{}{"type": "field", "user": []string{"u1"}, "outboundTag": "direct"})
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith(userToDirect, bal)) == nil {
		t.Fatal("user rule to direct accepted")
	}
	inboundToDirect := append(append([]map[string]interface{}{}, exitRules...),
		map[string]interface{}{"type": "field", "inboundTag": []string{"socks-awg"}, "outboundTag": "direct"})
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith(inboundToDirect, bal)) == nil {
		t.Fatal("socks-awg inbound rule to direct accepted")
	}
	dnsPlusMore := []map[string]interface{}{{"type": "field", "inboundTag": []string{"dns-out", "socks-awg"}, "outboundTag": "direct"}}
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith(dnsPlusMore, bal)) == nil {
		t.Fatal("dns-out rule widened to another inbound accepted")
	}

	// (b) no balancers: one unconditional block rule must cover every user inbound
	noSocksAWG := []string{"vless-reality", "vless-xhttp"}
	if checkNoDirectLeak(fx.inbounds, fx.noExit,
		routingWith([]map[string]interface{}{dnsDirectRule(), noExitBlockRule(noSocksAWG)})) == nil {
		t.Fatal("no-exit config with socks-awg missing from the block rule accepted")
	}
	if checkNoDirectLeak(fx.inbounds, fx.noExit,
		routingWith([]map[string]interface{}{dnsDirectRule()})) == nil {
		t.Fatal("no-exit config without any block rule accepted")
	}
	narrowed := noExitBlockRule(user)
	narrowed["port"] = "443"
	if checkNoDirectLeak(fx.inbounds, fx.noExit, routingWith([]map[string]interface{}{dnsDirectRule(), narrowed})) == nil {
		t.Fatal("no-exit config whose block rule is restricted by port accepted")
	}
	toExit := noExitBlockRule(user)
	toExit["outboundTag"] = "exit-2"
	if checkNoDirectLeak(fx.inbounds, fx.noExit, routingWith([]map[string]interface{}{dnsDirectRule(), toExit})) == nil {
		t.Fatal("no-exit config whose catch rule does not block accepted")
	}
	// the block rule may be split over several rules, as long as together they cover everything
	split := []map[string]interface{}{
		dnsDirectRule(), noExitBlockRule(noSocksAWG), noExitBlockRule([]string{"socks-awg"}),
	}
	if err := checkNoDirectLeak(fx.inbounds, fx.noExit, routingWith(split)); err != nil {
		t.Fatalf("split block rules rejected: %v", err)
	}
}

func TestReportXraySync(t *testing.T) {
	h := newAlertHarness()
	boom := errors.New("xray config leak guard: default outbound is \"direct\", want \"block\"")

	reportXraySync(h.m, nil) // success while nothing is raised: silent
	if len(h.sent) != 0 {
		t.Fatalf("success without a prior failure must stay silent: %q", h.sent)
	}

	reportXraySync(h.m, boom)
	reportXraySync(h.m, boom) // dedup: a failing 5-minute loop must not spam
	if len(h.sent) != 1 || !strings.Contains(h.sent[0], "Конфиг xray не применяется") || !strings.Contains(h.sent[0], "leak guard") {
		t.Fatalf("failure must raise exactly one alert with the error text: %q", h.sent)
	}

	reportXraySync(h.m, nil) // "config unchanged" and "applied" both return nil
	if len(h.sent) != 2 || !strings.Contains(h.sent[1], "Конфиг xray снова применяется") {
		t.Fatalf("next successful sync must resolve the alert: %q", h.sent)
	}
	reportXraySync(h.m, nil)
	if len(h.sent) != 2 {
		t.Fatalf("a second success must not resend the recovery: %q", h.sent)
	}

	long := errors.New(strings.Repeat("я", 5000))
	h2 := newAlertHarness()
	reportXraySync(h2.m, long)
	if len(h2.sent) != 1 || len([]rune(h2.sent[0])) > 1500 {
		t.Fatalf("details must be truncated, got %d runes", len([]rune(h2.sent[0])))
	}
}
