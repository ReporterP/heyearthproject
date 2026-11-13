package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"strings"
)

// ── JSON subscription for Happ: «⚡ Авто» ──
//
// A Happ subscription body may be a JSON array of full Xray configs: each one is
// a line in the app («remarks» is its name, meta.serverDescription the subtitle)
// and goes to the Xray core exactly as written — Happ's own routing profile is
// not applied to it. The first config, «⚡ Авто», holds a proxy outbound for every
// way in (the Hub and each healthy reserve entry; Берёза fp firefox and Дуб fp
// chrome on each) behind a leastPing balancer: the observatory probes them all
// once a minute and traffic takes the fastest one that answers — the Hub on
// Wi-Fi, a reserve entry on a mobile network that cuts the Hub's subnet. The
// other configs are the same ways one by one, for a manual choice.
//
// Бамбук (Hysteria2 with the Salamander obfuscation) has no JSON form here: the
// Xray core's hysteria outbound does not speak Salamander, so it stays in the
// plain-URI subscription only.

// Subscription formats of handleSubscriptionLink.
const (
	autoModeOff  = "off"  // plain URIs for everyone
	autoModeTest = "test" // JSON «Авто» for the test user (entry_failover_test_user) only
	autoModeOn   = "on"   // JSON «Авто» for every Happ client
)

// directRouting is what goes straight out of the device, never through a way in.
type directRouting struct {
	sites []string // Xray domain rules: "domain:sberbank.ru", …
	ips   []string // "geoip:private", "geoip:ru", "203.0.113.7/32", …
}

type xrayObj = map[string]any

// jsonWay is one way in: a proxy outbound of the «Авто» balancer and a line of its own.
type jsonWay struct {
	tag, label, description, host string
	outbound                      xrayObj
}

// jsonWays lists the ways in of in: per entry a Берёза and a Дуб — the main
// address first (it follows a failover target like the URI lines of
// hubConnectionOptions), the Hub with «· напрямую» when the main address is a
// relay, then every other healthy reserve entry. Unlike the URI lines the Hub is
// always there: «Авто» probes it itself, and a Hub cut on one network still
// answers on another. Empty without VLESS (no keys, no UUID, plan without vless).
func jsonWays(in hubConnectionInputs) []jsonWay {
	if in.mainServerHost == "" || in.vlessUUID == "" || in.realityPubKey == "" || !in.canVLESS {
		return nil
	}
	connect := in.entries.main
	if connect == "" {
		connect = in.mainServerHost
	}
	type entryWays struct{ key, host, suffix, desc string }
	entries := []entryWays{{"main", connect, "", "основной вход"}}
	if connect != in.mainServerHost {
		entries = append(entries, entryWays{"hub", in.mainServerHost, directLabel, "напрямую к основному серверу"})
	}
	k := 0
	for _, r := range in.entries.reserves {
		if r.address == connect {
			continue
		}
		entries = append(entries, entryWays{fmt.Sprintf("r%d", r.id), r.address, reserveSuffix(k), "резервный вход в РФ"})
		k++
	}
	var ways []jsonWay
	for _, e := range entries {
		ways = append(ways,
			jsonWay{"p-" + e.key + "-tcp", "🌳 Берёза" + e.suffix, e.desc, e.host, realityTCPOutbound(in, e.host, "firefox")},
			jsonWay{"p-" + e.key + "-xhttp", "🌲 Дуб" + e.suffix, e.desc, e.host, realityXHTTPOutbound(in, e.host, "chrome")})
	}
	return ways
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
// header is always the Hub's main_server_host (the XHTTP inbound checks it).
func realityXHTTPOutbound(in hubConnectionInputs, host, fp string) xrayObj {
	port := 2443
	fmt.Sscanf(in.xhttpPort, "%d", &port)
	return xrayObj{
		"protocol": "vless",
		"settings": xrayObj{"vnext": []any{xrayObj{"address": host, "port": port,
			"users": []any{xrayObj{"id": in.vlessUUID, "encryption": "none"}}}}},
		"streamSettings": xrayObj{"network": "xhttp", "security": "reality",
			"realitySettings": realitySettings(in, fp),
			"xhttpSettings":   xrayObj{"path": "/", "host": in.mainServerHost, "mode": "auto"},
			"sockopt":         jsonSockopt},
	}
}

// jsonInbounds: the local SOCKS/HTTP ports Happ's tunnel feeds (socks-auth-mode
// auto: Happ puts its own login on them). Sniffing is for routing only.
func jsonInbounds() []any {
	sniff := xrayObj{"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "routeOnly": true}
	return []any{
		xrayObj{"tag": "socks", "listen": "127.0.0.1", "port": 10808, "protocol": "socks",
			"settings": xrayObj{"auth": "noauth", "udp": true}, "sniffing": sniff},
		xrayObj{"tag": "http", "listen": "127.0.0.1", "port": 10809, "protocol": "http",
			"settings": xrayObj{}, "sniffing": sniff},
	}
}

// jsonRules: the Hub's own address always through the tunnel (it is a RU address,
// geoip:ru would send it direct — and a mobile network that cuts the Hub's
// subnet would then cut the site and the cabinet too), then the direct lists,
// then everything else to final (an outbound tag or the balancer).
func jsonRules(hub string, d directRouting, final xrayObj) []any {
	var rules []any
	if hub != "" {
		r := xrayObj{"type": "field"}
		if net.ParseIP(hub) != nil {
			r["ip"] = []string{hub}
		} else {
			r["domain"] = []string{"full:" + hub}
		}
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

func jsonTailOutbounds() []any {
	return []any{
		xrayObj{"tag": "direct", "protocol": "freedom"},
		xrayObj{"tag": "block", "protocol": "blackhole"},
	}
}

// autoProbeURL is what the observatory fetches through every way in.
const autoProbeURL = "https://www.gstatic.com/generate_204"

// jsonSubscription builds the JSON subscription body of in: «⚡ Авто» first, then
// every way in alone. nil when in has no way in (the caller serves plain URIs). Pure.
func jsonSubscription(in hubConnectionInputs, d directRouting) []byte {
	ways := jsonWays(in)
	if len(ways) == 0 {
		return nil
	}
	var configs []any

	outbounds := make([]any, 0, len(ways)+2)
	for _, w := range ways {
		ob := xrayObj{"tag": w.tag}
		maps.Copy(ob, w.outbound)
		outbounds = append(outbounds, ob)
	}
	// Until the first probe answers, traffic takes the first reserve entry's
	// Берёза: a reserve entry answers on Wi-Fi and on a mobile network alike.
	fallback := ways[0].tag
	for _, w := range ways {
		if w.host != in.mainServerHost {
			fallback = w.tag
			break
		}
	}
	configs = append(configs, xrayObj{
		"remarks":   "⚡ Авто",
		"meta":      xrayObj{"serverDescription": "сам выбирает самый быстрый вход"},
		"log":       xrayObj{"loglevel": "warning"},
		"inbounds":  jsonInbounds(),
		"outbounds": append(outbounds, jsonTailOutbounds()...),
		"observatory": xrayObj{"subjectSelector": []string{"p-"}, "probeUrl": autoProbeURL,
			"probeInterval": "1m", "enableConcurrency": true},
		"routing": xrayObj{
			"domainStrategy": "AsIs",
			"balancers": []any{xrayObj{"tag": "auto", "selector": []string{"p-"},
				"strategy": xrayObj{"type": "leastPing"}, "fallbackTag": fallback}},
			"rules": jsonRules(in.mainServerHost, d, xrayObj{"balancerTag": "auto"}),
		},
	})

	for _, w := range ways {
		ob := xrayObj{"tag": "proxy"}
		maps.Copy(ob, w.outbound)
		configs = append(configs, xrayObj{
			"remarks":   w.label,
			"meta":      xrayObj{"serverDescription": w.description},
			"log":       xrayObj{"loglevel": "warning"},
			"inbounds":  jsonInbounds(),
			"outbounds": append([]any{ob}, jsonTailOutbounds()...),
			"routing": xrayObj{"domainStrategy": "AsIs",
				"rules": jsonRules(in.mainServerHost, d, xrayObj{"outboundTag": "proxy"})},
		})
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
