package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"strings"
)

// ── JSON subscription for Happ: «⚡ Авто», «🎋 Бамбук 1/2» ──
//
// A Happ subscription body may be a JSON array of full Xray configs: each one is
// a line in the app («remarks» is its name, meta.serverDescription the subtitle)
// and goes to the Xray core exactly as written — Happ's own routing profile is
// not applied to it. Three lines, each a balancer over every way in (the Hub and
// each healthy reserve entry):
//
//	⚡ Авто — Берёза (fp firefox) and Дуб (fp chrome) through each entry;
//	🎋 Бамбук 1 / 2 only WiFi — Hysteria2 with Salamander (Xray ≥ 26.3: finalmask)
//	  through each entry whose UDP answers; BBR / Brutal 20/80 like the URI lines.
//
// The choice is leastLoad over a burst observatory: every way is probed about
// once a minute, the last 5 probes count, and a way with 2 failed among them is
// left out (at the start, any failure). A mobile network that cuts the Hub lets a
// small probe through now and then (the TSPU drops connections only after the
// first packets or 5–16 KB): a lucky probe must not send traffic to the Hub.
// Among the ways left the order is fixed — the Hub's Берёза, the Hub's Дуб, then
// the entries — by costs far above the probes' jitter: leastLoad ranks by the RTT
// deviation, and ranking the Hub against an entry by 3–5 noisy samples flipped
// the choice every few minutes (Happ's ping jumping 0.15 ↔ 0.5 s).
// Until the first probes answer, traffic takes a fixed way (fallbackTag).

// Subscription formats of handleSubscriptionLink.
const (
	autoModeOff  = "off"  // plain URIs for everyone
	autoModeTest = "test" // JSON «Авто» for the test user (sub_auto_test_user) only
	autoModeOn   = "on"   // JSON «Авто» for every Happ client
)

// subDirectByNameKey switches how direct connections dial (off | test | on, the
// test user is sub_auto_test_user; absent = off).
//
//	off — the address the app chose. Happ desktop's TUN (sing-box) hands Windows an
//	  IPv6 address and route even on a network without IPv6 and accepts every TCP
//	  connection at once, so a browser settles on the AAAA of a RU site; geoip:ru
//	  sends that IPv6 address direct and the dial dies (ya.ru and every RU site with
//	  an AAAA record stop opening).
//	on — by name: a sniffed connection carries its name (routeOnly off), geoip:ru
//	  decides by the name's addresses (IPOnDemand), direct dials the name IPv4
//	  first. Proxied traffic now reaches the Hub by name: its VLESS inbounds
//	  (Берёза, Дуб) sniff and re-address by name themselves, so nothing changes
//	  there; Бамбук's hysteria2 does not sniff, it now resolves the name on the
//	  Hub instead of dialing the client's address.
const subDirectByNameKey = "sub_direct_by_name"

// subJSONDNSKey switches where a by-name config resolves names (off | test | on, the
// test user is sub_auto_test_user; absent = off; nothing changes while not by name).
//
//	off — no "dns" in the config: Xray resolves with the device's own resolver. By
//	  name every connection on 80/443 is resolved (the first rule has an IP
//	  condition), so on a RU network the operator's DNS decides geoip:ru: the TSPU
//	  steers plain DNS to the national DNS, which has no records for blocked names
//	  (instagram.com was removed from it) or answers with a stub — a stub in
//	  geoip:ru/private sends the site direct, where it is blocked; a resolver that
//	  does not answer holds every new connection for the DNS timeout.
//	on — the names are resolved by public DoH through the line's own balancer
//	  (jsonDNSServers behind the way in), never by the operator.
const subJSONDNSKey = "sub_json_dns"

// jsonDNSTag is the inbound tag of the config's own DNS queries (dns.tag): the first
// routing rule sends them to the balancer.
const jsonDNSTag = "dns-tunnel"

// jsonDNSServers: DoH by address (no name to resolve first, the certificates carry the
// addresses); https:// is dispatched through routing, unlike https+local://.
var jsonDNSServers = []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/dns-query"}

// directRouting is what goes straight out of the device, never through a way in.
type directRouting struct {
	sites     []string // Xray domain rules: "domain:sberbank.ru", …
	ips       []string // "geoip:private", "geoip:ru", "203.0.113.7/32", …
	fronting  []string // the MTProto fronting entries of sites: never re-addressed by name
	byName    bool     // direct by the sniffed name, IPv4 first (subDirectByNameKey)
	dnsTunnel bool     // by name, names are resolved through the tunnel (subJSONDNSKey)
}

// routingStrategy: by name, a domain is resolved when an IP rule is reached, so
// geoip:ru and geoip:private still see the addresses behind it.
func (d directRouting) routingStrategy() string {
	if d.byName {
		return "IPOnDemand"
	}
	return "AsIs"
}

// resolvesThroughTunnel: the config carries its own DNS (only by name: AsIs never
// resolves in routing, and direct dials the address it was given).
func (d directRouting) resolvesThroughTunnel() bool { return d.byName && d.dnsTunnel }

// jsonDNS is the config's "dns", nil when it resolves with the device's resolver.
func jsonDNS(d directRouting) xrayObj {
	if !d.resolvesThroughTunnel() {
		return nil
	}
	return xrayObj{"tag": jsonDNSTag, "servers": jsonDNSServers}
}

type xrayObj = map[string]any

// jsonWay is one way in: a proxy outbound of a balancer.
type jsonWay struct {
	tag, host string
	outbound  xrayObj
}

// wayEntry is where ways go: the Hub ("hub") or a healthy reserve entry ("r<id>").
type wayEntry struct {
	key, host string
	udp       bool // Бамбук answers through it
}

// wayEntries: the Hub, then every healthy reserve entry (by id). The Hub is
// always there: the balancer probes it itself, and a Hub cut on one network
// still answers on another; a server-side failover changes nothing here.
func wayEntries(in hubConnectionInputs) []wayEntry {
	es := []wayEntry{{"hub", in.mainServerHost, true}}
	for _, r := range in.entries.reserves {
		if r.address != "" && r.address != in.mainServerHost {
			es = append(es, wayEntry{fmt.Sprintf("r%d", r.id), r.address, r.udp})
		}
	}
	return es
}

// autoWays: Берёза and Дуб through every entry. Empty without VLESS (no keys,
// no UUID, plan without vless).
func autoWays(in hubConnectionInputs) []jsonWay {
	if in.mainServerHost == "" || in.vlessUUID == "" || in.realityPubKey == "" || !in.canVLESS {
		return nil
	}
	var ways []jsonWay
	for _, e := range wayEntries(in) {
		ways = append(ways,
			jsonWay{"p-" + e.key + "-tcp", e.host, realityTCPOutbound(in, e.host, "firefox")},
			jsonWay{"p-" + e.key + "-xhttp", e.host, realityXHTTPOutbound(in, e.host, "chrome")})
	}
	return ways
}

// bambooJSON are the Бамбук lines of the JSON subscription: the URI lines'
// bambooVariants as Xray congestion settings (no bandwidth → BBR; upmbps 20 /
// downmbps 80 → Brutal).
var bambooJSON = []struct {
	label string
	quic  xrayObj
}{
	{"🎋 Бамбук 1 only WiFi", xrayObj{"congestion": "bbr"}},
	{"🎋 Бамбук 2 only WiFi", xrayObj{"congestion": "brutal", "brutalUp": "20 mbps", "brutalDown": "80 mbps"}},
}

// bambooWays: Бамбук through every entry whose UDP answers, with quic's
// congestion. Empty without Бамбук (plan gate, passwords, the cert domain).
func bambooWays(in hubConnectionInputs, quic xrayObj) []jsonWay {
	if in.mainServerHost == "" || !in.canHysteria2 || in.hy2Password == "" || in.obfsPassword == "" || in.hysteria2SNI == "" {
		return nil
	}
	var ways []jsonWay
	for _, e := range wayEntries(in) {
		if e.udp {
			ways = append(ways, jsonWay{"b-" + e.key, e.host, hysteriaOutbound(in, e.host, quic)})
		}
	}
	return ways
}

// hysteriaOutbound is Бамбук (bambooURI) as an Xray outbound: the SNI is the
// cert domain, Salamander and the port hopping (30000-65000, every 15 s) live
// in finalmask (Xray 26.3).
func hysteriaOutbound(in hubConnectionInputs, host string, quic xrayObj) xrayObj {
	qp := xrayObj{"udpHop": xrayObj{"ports": "30000-65000", "interval": 15}}
	maps.Copy(qp, quic)
	return xrayObj{
		"protocol": "hysteria",
		"settings": xrayObj{"version": 2, "address": host, "port": 50443},
		"streamSettings": xrayObj{"network": "hysteria", "security": "tls",
			"tlsSettings":      xrayObj{"serverName": in.hysteria2SNI, "alpn": []string{"h3"}},
			"hysteriaSettings": xrayObj{"version": 2, "auth": in.hy2Password},
			"finalmask": xrayObj{
				"udp":        []any{xrayObj{"type": "salamander", "settings": xrayObj{"password": in.obfsPassword}}},
				"quicParams": qp,
			}},
	}
}

func realitySettings(in hubConnectionInputs, fp string) xrayObj {
	return xrayObj{"serverName": in.realitySNI, "fingerprint": fp, "publicKey": in.realityPubKey, "shortId": in.realityShortID, "spiderX": ""}
}

var jsonSockopt = xrayObj{"tcpKeepAliveInterval": 15, "tcpKeepAliveIdle": 100}

// realityTCPOutbound is Берёза (tcpURI) as an Xray outbound.
func realityTCPOutbound(in hubConnectionInputs, host, fp string) xrayObj {
	user := xrayObj{"id": in.vlessUUID, "encryption": "none"}
	if in.realityFlow != "" {
		user["flow"] = in.realityFlow
	}
	return xrayObj{
		"protocol": "vless",
		"settings": xrayObj{"vnext": []any{xrayObj{"address": host, "port": 443, "users": []any{user}}}},
		"streamSettings": xrayObj{"network": "tcp", "security": "reality",
			"realitySettings": realitySettings(in, fp), "sockopt": jsonSockopt},
	}
}

// realityXHTTPOutbound is Дуб (xhttpURI) as an Xray outbound: no flow, the Host
// header is always the Hub's Дуб Host (dubHost: the XHTTP inbound checks it).
func realityXHTTPOutbound(in hubConnectionInputs, host, fp string) xrayObj {
	port := 2443
	fmt.Sscanf(in.xhttpPort, "%d", &port)
	return xrayObj{
		"protocol": "vless",
		"settings": xrayObj{"vnext": []any{xrayObj{"address": host, "port": port,
			"users": []any{xrayObj{"id": in.vlessUUID, "encryption": "none"}}}}},
		"streamSettings": xrayObj{"network": "xhttp", "security": "reality",
			"realitySettings": realitySettings(in, fp),
			"xhttpSettings":   xrayObj{"path": "/", "host": in.dubHost(), "mode": "auto"},
			"sockopt":         jsonSockopt},
	}
}

// jsonInbounds: the local SOCKS/HTTP ports Happ's tunnel feeds (socks-auth-mode
// auto: Happ puts its own login on them). Sniffing is for routing only, or by
// name (d.byName) it re-addresses the connection to the sniffed name — except the
// MTProto fronting domains.
func jsonInbounds(d directRouting) []any {
	sniff := xrayObj{"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "routeOnly": !d.byName}
	if d.byName && len(d.fronting) > 0 {
		sniff["domainsExcluded"] = d.fronting
	}
	return []any{
		xrayObj{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
			"settings": xrayObj{"auth": "noauth", "udp": true}, "sniffing": sniff},
		xrayObj{"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http",
			"settings": xrayObj{}, "sniffing": sniff},
	}
}

// jsonRules: the site and the cabinet on the Hub's own address (ports 80/443)
// through the tunnel (it is a RU address, geoip:ru would send it direct — and a
// mobile network that cuts the Hub's subnet would then cut the site too), then
// the direct lists, then everything else to final (an outbound tag or the
// balancer). Other ports of the Hub (the owner's SSH) stay direct: through the
// tunnel they would end on the Hub itself, which lets its proxy reach its own
// address on 80/443 only.
func jsonRules(hub, siteHost string, d directRouting, final xrayObj) []any {
	var rules []any
	if d.resolvesThroughTunnel() {
		// The config's own DNS queries go through the tunnel, before any rule that resolves.
		r := xrayObj{"type": "field", "inboundTag": []string{jsonDNSTag}}
		maps.Copy(r, final)
		rules = append(rules, r)
	}
	if hub != "" {
		r := xrayObj{"type": "field", "port": "80,443"}
		if net.ParseIP(hub) != nil {
			r["ip"] = []string{hub}
		} else {
			r["domain"] = []string{"full:" + hub}
		}
		maps.Copy(r, final)
		rules = append(rules, r)
	}
	// The site, its www and the admin by name (sniffed SNI / Host): whatever address their A
	// records point at — a new Hub address is a RU address that geoip:ru would send direct.
	if siteHost != "" {
		r := xrayObj{"type": "field", "port": "80,443",
			"domain": []string{"full:" + siteHost, "full:www." + siteHost, "full:admin." + siteHost}}
		maps.Copy(r, final)
		rules = append(rules, r)
	}
	if len(d.sites) > 0 {
		rules = append(rules, xrayObj{"type": "field", "domain": d.sites, "outboundTag": "direct"})
	}
	if len(d.ips) > 0 {
		rules = append(rules, xrayObj{"type": "field", "ip": d.ips, "outboundTag": "direct"})
	}
	last := xrayObj{"type": "field", "network": "tcp,udp"}
	maps.Copy(last, final)
	return append(rules, last)
}

// jsonTailOutbounds: direct and block. By name, direct resolves the name and dials
// IPv4 first, IPv6 only for a name without IPv4 (Xray ≥ 26: sockopt.domainStrategy).
func jsonTailOutbounds(d directRouting) []any {
	direct := xrayObj{"tag": "direct", "protocol": "freedom"}
	if d.byName {
		direct["streamSettings"] = xrayObj{"sockopt": xrayObj{"domainStrategy": "UseIPv4v6"}}
	}
	return []any{direct, xrayObj{"tag": "block", "protocol": "blackhole"}}
}

// autoProbeURL is what the observatory fetches through every way in.
const autoProbeURL = "https://www.gstatic.com/generate_204"

// Balancer tuning (see the top of the file).
const (
	probeInterval  = "1m" // the average time between two probes of one way
	probeSampling  = 5    // the probes that count
	probeTolerance = 0.2  // a way with more failed probes than this share is left out: 2 of 5
	probeMaxRTT    = "3s"
	// Costs multiply a way's deviation by their square root: ×100 for an entry,
	// ×10 for a Дуб — far above the jitter, so they only order the ways left in.
	entryCost = 10000
	dubCost   = 100
)

// balancedConfig is one line: ways (tags starting with prefix) behind a leastLoad
// balancer over a burst observatory, fallback until the first probes answer. The
// first matching cost applies: the entries ("<prefix>r…") first.
func balancedConfig(label, description, prefix, fallback, hub, siteHost string, ways []jsonWay, d directRouting) xrayObj {
	costs := []any{xrayObj{"regexp": true, "match": "^" + prefix + "r", "value": entryCost}}
	if prefix == "p-" {
		costs = append(costs, xrayObj{"regexp": true, "match": "-xhttp$", "value": dubCost})
	}
	outbounds := make([]any, 0, len(ways)+2)
	for _, w := range ways {
		ob := xrayObj{"tag": w.tag}
		maps.Copy(ob, w.outbound)
		outbounds = append(outbounds, ob)
	}
	cfg := xrayObj{
		"remarks":   label,
		"meta":      xrayObj{"serverDescription": description},
		"log":       xrayObj{"loglevel": "warning"},
		"inbounds":  jsonInbounds(d),
		"outbounds": append(outbounds, jsonTailOutbounds(d)...),
		"burstObservatory": xrayObj{"subjectSelector": []string{prefix}, "pingConfig": xrayObj{
			"destination": autoProbeURL, "interval": probeInterval, "sampling": probeSampling, "timeout": "5s"}},
		"routing": xrayObj{
			"domainStrategy": d.routingStrategy(),
			"balancers": []any{xrayObj{"tag": "auto", "selector": []string{prefix}, "fallbackTag": fallback,
				"strategy": xrayObj{"type": "leastLoad", "settings": xrayObj{
					"expected": 1, "maxRTT": probeMaxRTT, "tolerance": probeTolerance,
					"costs": costs,
				}}}},
			"rules": jsonRules(hub, siteHost, d, xrayObj{"balancerTag": "auto"}),
		},
	}
	if dns := jsonDNS(d); dns != nil {
		cfg["dns"] = dns
	}
	return cfg
}

// jsonSubscription builds the JSON subscription body of in: «⚡ Авто», then
// «🎋 Бамбук 1/2» when the plan has Бамбук. nil without VLESS (the caller serves
// plain URIs). Pure.
func jsonSubscription(in hubConnectionInputs, d directRouting) []byte {
	ways := autoWays(in)
	if len(ways) == 0 {
		return nil
	}
	// Before the first probes: the first reserve entry's Берёза — an entry answers
	// on Wi-Fi and on a mobile network alike.
	fallback := ways[0].tag
	for _, w := range ways {
		if w.host != in.mainServerHost {
			fallback = w.tag
			break
		}
	}
	configs := []any{balancedConfig("⚡ Авто", "сам выбирает самый быстрый вход", "p-", fallback, in.mainServerHost, in.siteHost, ways, d)}
	for _, b := range bambooJSON {
		// Бамбук is for Wi-Fi, where the Hub answers: the Hub first.
		if bw := bambooWays(in, b.quic); len(bw) > 0 {
			configs = append(configs, balancedConfig(b.label, "UDP, только Wi-Fi · сам выбирает вход", "b-", bw[0].tag, in.mainServerHost, in.siteHost, bw, d))
		}
	}
	body, err := json.Marshal(configs)
	if err != nil {
		return nil
	}
	return body
}

// autoModeServes says whether a Happ client of userID gets the JSON subscription.
func autoModeServes(mode, testUser, userID string) bool {
	switch strings.TrimSpace(mode) {
	case autoModeOn:
		return true
	case autoModeTest:
		return userID != "" && strings.TrimSpace(testUser) == userID
	}
	return false
}
