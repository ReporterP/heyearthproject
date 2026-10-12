package main

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
)

func ipNet(t *testing.T, cidr string) net.Addr {
	t.Helper()
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	n.IP = ip
	return n
}

func TestCollectHubIPs(t *testing.T) {
	addrs := []net.Addr{
		ipNet(t, "127.0.0.1/8"), ipNet(t, "::1/128"),
		ipNet(t, "10.0.0.5/24"), ipNet(t, "192.168.1.9/24"), ipNet(t, "172.17.0.1/16"), // private
		ipNet(t, "fe80::1/64"), ipNet(t, "fd00::2/64"), // link-local, ULA
		ipNet(t, "138.16.181.225/24"), ipNet(t, "45.150.39.236/24"), ipNet(t, "109.172.86.40/24"),
		ipNet(t, "2001:db8::2/64"),
		&net.IPAddr{IP: net.ParseIP("45.150.39.236")}, // duplicate, other address type
	}
	lookups := map[string][]net.IP{"hiearth.ru": {net.ParseIP("138.16.181.225"), net.ParseIP("203.0.113.9")}}
	lookup := func(h string) ([]net.IP, error) {
		if ips, ok := lookups[h]; ok {
			return ips, nil
		}
		return nil, errors.New("no such host")
	}

	got := collectHubIPs(addrs, []string{"hiearth.ru", " 198.51.100.7 ", "", "gone.example", "10.9.9.9"}, lookup)
	want := []string{"109.172.86.40", "138.16.181.225", "198.51.100.7", "2001:db8::2", "203.0.113.9", "45.150.39.236"}
	// sorted as strings, de-duplicated; loopback, private, link-local and ULA never listed
	if !reflect.DeepEqual(got, sortedCopy(want)) {
		t.Fatalf("collectHubIPs = %v, want %v", got, sortedCopy(want))
	}
	if again := collectHubIPs(addrs, []string{"hiearth.ru", " 198.51.100.7 ", "", "gone.example", "10.9.9.9"}, lookup); !reflect.DeepEqual(again, got) {
		t.Fatalf("not deterministic: %v vs %v", again, got)
	}
	if none := collectHubIPs(nil, nil, lookup); len(none) != 0 {
		t.Fatalf("nothing in, nothing out: %v", none)
	}
}

func sortedCopy(s []string) []string {
	c := append([]string{}, s...)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	return c
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func TestHysteria2ACLRules(t *testing.T) {
	hub := []string{"138.16.181.225", "2001:db8::2"}
	rules := hysteria2ACLRules(hub)

	// the three unconditional rejects come first, in this order
	for i, want := range []string{"reject(geoip:private)", "reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)"} {
		if rules[i] != want {
			t.Fatalf("rule %d = %q, want %q (all: %v)", i, rules[i], want, rules)
		}
	}
	firstDirect := -1
	for i, r := range rules {
		if strings.HasPrefix(r, "direct(") {
			firstDirect = i
			break
		}
	}
	if firstDirect < 0 {
		t.Fatalf("no direct(...) rule at all: %v", rules)
	}
	for _, ip := range hub {
		rej := indexOf(rules, "reject("+ip+")")
		if rej < 0 || rej > firstDirect {
			t.Errorf("reject(%s) must be present and before the first direct(...) (at %d, direct at %d): %v", ip, rej, firstDirect, rules)
		}
		// the Hub's web ports stay reachable through the exit, and only before the reject
		for _, port := range []string{"tcp/80", "tcp/443"} {
			via := indexOf(rules, "via_exit("+ip+", "+port+")")
			if via < 0 || via > indexOf(rules, "reject("+ip+")") {
				t.Errorf("via_exit(%s, %s) must precede reject(%s): %v", ip, port, ip, rules)
			}
		}
	}
	if indexOf(rules, "direct(geoip:private)") >= 0 {
		t.Errorf("direct(geoip:private) must be gone: %v", rules)
	}
	if rules[len(rules)-2] != "direct(geoip:ru)" || rules[len(rules)-1] != "via_exit(all)" {
		t.Errorf("the tail must stay direct(geoip:ru), via_exit(all): %v", rules)
	}
	// no Hub address known: the unconditional rejects are still there and still first
	bare := hysteria2ACLRules(nil)
	if !reflect.DeepEqual(bare, []string{"reject(geoip:private)", "reject(127.0.0.0/8)", "reject(::1/128)", "reject(0.0.0.0/8)", "reject(::/128)", "direct(geoip:ru)", "via_exit(all)"}) {
		t.Errorf("rules without Hub addresses: %v", bare)
	}
}

func TestRenderHysteria2ConfigACL(t *testing.T) {
	hub := []string{"109.172.86.40", "138.16.181.225", "45.150.39.236"}
	yaml := renderHysteria2Config(50, 100, "obfs-pass", "stats-secret", 1090, hub)
	if again := renderHysteria2Config(50, 100, "obfs-pass", "stats-secret", 1090, hub); again != yaml {
		t.Fatal("the config must be deterministic (the sync's no-op short-circuit compares bytes)")
	}
	_, acl, ok := strings.Cut(yaml, "\nacl:\n")
	if !ok {
		t.Fatalf("no acl block:\n%s", yaml)
	}
	var lines []string
	for _, l := range strings.Split(acl, "\n") {
		if r, ok := strings.CutPrefix(l, "    - "); ok {
			lines = append(lines, r)
		}
	}
	if !reflect.DeepEqual(lines, hysteria2ACLRules(hub)) {
		t.Fatalf("rendered ACL = %v, want %v", lines, hysteria2ACLRules(hub))
	}
	if !strings.Contains(acl, "  geoip: /etc/hysteria/geoip.dat\n  inline:\n    - reject(geoip:private)\n") {
		t.Errorf("the geoip file and the first reject must head the ACL:\n%s", acl)
	}
	if strings.Contains(yaml, "- direct(geoip:private)") {
		t.Errorf("direct(geoip:private) is still in the generated config")
	}
	// the rest of the config is untouched
	for _, want := range []string{"listen: ", "obfs:\n  type: salamander", "password: obfs-pass", "addr: 127.0.0.1:1090", "secret: stats-secret", "disablePathMTUDiscovery: true"} {
		if !strings.Contains(yaml, want) {
			t.Errorf("config lacks %q", want)
		}
	}
}

func TestBaseSecurityRules(t *testing.T) {
	hub := []string{"138.16.181.225", "45.150.39.236"}
	rules := baseSecurityRules(hub)
	// 0 dns, 1 private block, 2 loopback + "this host", 3 Hub addresses block, 4 bittorrent, 5 xmpp
	if len(rules) != 6 {
		t.Fatalf("want 6 rules, got %d: %v", len(rules), rules)
	}
	if !reflect.DeepEqual(rules[0], dnsDirectRule()) {
		t.Errorf("rule 0 must be xray's own DNS: %v", rules[0])
	}
	if !isPrivateBlockRule(rules[1]) {
		t.Errorf("rule 1 must be the unconditional geoip:private block: %v", rules[1])
	}
	local := rules[2]
	if local["outboundTag"] != "block" || !reflect.DeepEqual(local["ip"], []string{"127.0.0.0/8", "::1/128", "0.0.0.0/8", "::/128"}) {
		t.Errorf("rule 2 must block loopback and \"this host\" explicitly: %v", local)
	}
	hubRule := rules[3]
	if hubRule["outboundTag"] != "block" || !reflect.DeepEqual(hubRule["ip"], hub) || hubRule["port"] != "1-79,81-442,444-65535" {
		t.Errorf("rule 2 must block the Hub addresses on every port but 80/443: %v", hubRule)
	}
	if _, restricted := hubRule["inboundTag"]; restricted {
		t.Errorf("the Hub-address block must apply to every inbound: %v", hubRule)
	}
	if !reflect.DeepEqual(rules[4]["protocol"], []string{"bittorrent"}) || rules[5]["port"] != "5222,5223" {
		t.Errorf("bittorrent / XMPP rules changed: %v %v", rules[4], rules[5])
	}
	// the caller's slice is not aliased into the rule
	hub[0] = "tampered"
	if ips := rules[3]["ip"].([]string); ips[0] != "138.16.181.225" {
		t.Errorf("rule keeps a reference to the caller's slice")
	}
	// no Hub address known: the rule is omitted, the private block stays
	bare := baseSecurityRules(nil)
	if len(bare) != 5 || !isPrivateBlockRule(bare[1]) {
		t.Errorf("without Hub addresses: %v", bare)
	}
	// the port exception really leaves out exactly 80 and 443
	for _, p := range []int{80, 443} {
		if portInRanges(p, xrayHubBlockedPorts) {
			t.Errorf("port %d must stay open", p)
		}
	}
	for _, p := range []int{1, 22, 79, 81, 442, 444, 5432, 8000, 10085, 65535} {
		if !portInRanges(p, xrayHubBlockedPorts) {
			t.Errorf("port %d must be blocked", p)
		}
	}
}

func portInRanges(p int, ranges string) bool {
	for _, r := range strings.Split(ranges, ",") {
		lo, hi := 0, 0
		if a, b, ok := strings.Cut(r, "-"); ok {
			lo, hi = atoiSafe(a), atoiSafe(b)
		} else {
			lo = atoiSafe(r)
			hi = lo
		}
		if p >= lo && p <= hi {
			return true
		}
	}
	return false
}

func TestCheckNoDirectLeakPrivateFirst(t *testing.T) {
	fx := newLeakFixture()
	bal := newBalancer("exit-balancer", []string{"exit-2"}, "random")
	catchAll := map[string]interface{}{"type": "field", "network": "tcp,udp", "balancerTag": "exit-balancer"}
	freedom := map[string]interface{}{"type": "field", "ruleTag": "plan-3-direct-ip", "user": []string{"u1"}, "ip": []string{"1.2.3.0/24"}, "outboundTag": "direct-freedom"}
	mask := map[string]interface{}{"type": "field", "domain": []string{"domain:example.com"}, "outboundTag": "mask-7"}

	// the real generator's baseline, then the freedom rules: accepted
	ok := append(append([]map[string]interface{}{}, baseSecurityRules([]string{"138.16.181.225"})...), freedom, mask, catchAll)
	if err := checkNoDirectLeak(fx.inbounds, fx.good, routingWith(ok, bal)); err != nil {
		t.Fatalf("baseline + freedom rules rejected: %v", err)
	}

	// a freedom rule BEFORE the private block: rejected, for both kinds of freedom outbound
	for name, early := range map[string]map[string]interface{}{"direct-freedom": freedom, "mask-*": mask} {
		bad := []map[string]interface{}{dnsDirectRule(), early}
		bad = append(bad, baseSecurityRules(nil)[1:]...)
		bad = append(bad, catchAll)
		if err := checkNoDirectLeak(fx.inbounds, fx.good, routingWith(bad, bal)); err == nil || !strings.Contains(err.Error(), "before the \"geoip:private\" block rule") {
			t.Errorf("%s before the private block must be rejected, got %v", name, err)
		}
	}
	// no private block at all while a freedom rule exists: rejected
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith([]map[string]interface{}{dnsDirectRule(), freedom, catchAll}, bal)) == nil {
		t.Error("a freedom rule without any private block accepted")
	}
	// a private block that is narrowed (by inbound or port) does not count
	narrow := map[string]interface{}{"type": "field", "ip": []string{"geoip:private"}, "inboundTag": []string{"vless-reality"}, "outboundTag": "block"}
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith([]map[string]interface{}{dnsDirectRule(), narrow, freedom, catchAll}, bal)) == nil {
		t.Error("an inbound-restricted private block accepted as the guard")
	}
	// a private rule that does not block does not count either
	toExit := map[string]interface{}{"type": "field", "ip": []string{"geoip:private"}, "outboundTag": "exit-2"}
	if checkNoDirectLeak(fx.inbounds, fx.good, routingWith([]map[string]interface{}{dnsDirectRule(), toExit, freedom, catchAll}, bal)) == nil {
		t.Error("a private rule that does not block accepted as the guard")
	}
}

func TestHubIPCache(t *testing.T) {
	var c hubIPCache
	var warns []string
	warn := func(m string) { warns = append(warns, m) }
	calls := 0
	seq := [][]string{nil, {}, {"138.16.181.225"}, {"203.0.113.9"}}
	compute := func() []string {
		r := seq[calls]
		calls++
		return r
	}

	// two empty answers in a row: nothing is cached, each one warns, the caller gets nil
	for i := 0; i < 2; i++ {
		if got := c.get(compute, warn); len(got) != 0 {
			t.Fatalf("empty answer %d returned %v", i, got)
		}
	}
	if calls != 2 || len(warns) != 2 || !strings.Contains(warns[0], "OMITTED") {
		t.Fatalf("empty results must not be cached and must warn: calls=%d warns=%v", calls, warns)
	}

	// the first non-empty answer is kept for good, and no longer asks
	if got := c.get(compute, warn); !reflect.DeepEqual(got, []string{"138.16.181.225"}) {
		t.Fatalf("got %v", got)
	}
	if got := c.get(compute, warn); !reflect.DeepEqual(got, []string{"138.16.181.225"}) || calls != 3 || len(warns) != 2 {
		t.Fatalf("a found list must stay (byte-stable configs): got %v, calls=%d, warns=%d", got, calls, len(warns))
	}
}
