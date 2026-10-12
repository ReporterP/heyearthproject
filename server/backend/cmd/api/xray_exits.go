package main

import (
	"fmt"
	"log"
	"regexp"
	"strconv"

	"github.com/heyearth/backend/internal/biz"
)

// ─────────────── Exit paths in the Hub's xray config (node pool, phase 1) ───────────────
//
// Every HEALTHY exit N (lifecycle online, tunnel_status healthy) gets up to two outbounds:
//
//	exit-N.hy  Hysteria: SOCKS 127.0.0.1:(1088+N), served by hysteria2-client-N (UDP to the exit)
//	exit-N.vr  TCP: VLESS Reality straight to the exit's xray on :10443 (flow xtls-rprx-vision)
//
// Balancers pick exits by the prefix "exit-N." (xray selectors are tag prefixes, so "exit-2."
// can never match exit-20.*; Beads 07f7) and the catch-all one by "exit-". The order inside an
// exit (Hysteria first, TCP while Hysteria is dead) and load_weight between exits come from the
// leastLoad strategy's costs (exitCosts); tags are never duplicated. An exit leaves the routing
// only when the tunnel monitor finds BOTH of its paths dead (tunnel_probe.go).
//
// Every ONLINE exit with a TCP path, healthy or not, also gets a probe pair outside every
// selector: the loopback SOCKS inbound probe-vr-N (127.0.0.1:12000+N) routed to the outbound
// probe-vr-N (the same VLESS Reality). The monitor fetches generate_204 through it to judge the
// TCP path, also while the exit is out of the routing, so its recovery is noticed. The pair
// depends only on the exit being online, never on its health: health flips restart xray exactly
// as before (an exit enters or leaves the balancers) and nothing more.
//
// Legacy rows: tunnel_protocol vless keeps its old single VLESS outbound (own port, mux, no flow)
// under exit-N.vr; shadowsocks/socks5/http are no longer generated (phase-1 pre-check: none online).

const (
	exitSocksPortBase  = 1088
	exitProbePortBase  = 12000
	exitMaxID          = 511 // the egress guard allows xray SOCKS 1088..1599 (cmd/deploy/ops_egress_guard.go)
	exitVRPort         = 10443
	exitVRFlow         = "xtls-rprx-vision"
	exitAllSelector    = "exit-"
	exitProbeTagPrefix = "probe-vr-"
	// leastLoad multiplies a node's observed RTT by sqrt(cost) and takes the cheapest live node:
	// hy cost = 11 - load_weight (weight 10 → 1 … weight 1 → 10); the TCP path of a Hysteria exit
	// costs ×100, i.e. it wins only while Hysteria is dead or ten times slower.
	exitHyCostBase   = 11
	exitVRCostFactor = 100
)

func exitOutboundTags(id int) (hy, vr string) {
	return fmt.Sprintf("exit-%d.hy", id), fmt.Sprintf("exit-%d.vr", id)
}

func exitSelector(id int) string { return fmt.Sprintf("exit-%d.", id) }
func exitSocksPort(id int) int   { return exitSocksPortBase + id }
func exitProbePort(id int) int   { return exitProbePortBase + id }
func exitProbeTag(id int) string { return exitProbeTagPrefix + strconv.Itoa(id) }
func exitIDValid(id int) bool    { return id >= 1 && id <= exitMaxID }

// exitHasHyPath: Hysteria exits are reached through their hysteria2-client SOCKS.
func exitHasHyPath(n biz.ExitNodeTunnel) bool {
	return exitIDValid(n.ID) && n.TunnelProtocol == "hysteria2"
}

// exitHasVRPath: a VLESS Reality path needs the Hub's UUID on the exit and, for a Hysteria exit
// (whose xray always runs Reality), the exit's public key.
func exitHasVRPath(n biz.ExitNodeTunnel) bool {
	if !exitIDValid(n.ID) || len(n.TunnelUUID) < 32 {
		return false
	}
	switch n.TunnelProtocol {
	case "hysteria2":
		return len(n.TunnelPubKey) >= 40
	case "vless":
		return n.TunnelPubKey == "" || len(n.TunnelPubKey) >= 40
	}
	return false
}

func exitHyOutbound(id int, tag string) map[string]interface{} {
	return map[string]interface{}{
		"tag": tag, "protocol": "socks",
		"settings": map[string]interface{}{
			"servers": []map[string]interface{}{{"address": "127.0.0.1", "port": exitSocksPort(id)}},
		},
	}
}

// exitVROutbound is VLESS Reality over TCP to the exit's xray. For a Hysteria exit it is the
// fallback path: the exit's xray serves the Hub's UUID with flow xtls-rprx-vision (cmd/deploy
// --setup-exit-node) and refuses requests without that flow and over mux.cool, so: Vision, no
// mux (UDP 443/QUIC is refused by Vision, clients fall back to TCP while on this path). A legacy
// primary VLESS exit keeps its old outbound: its own port, mux, no flow.
func exitVROutbound(n biz.ExitNodeTunnel, tag, fingerprint string, mux int) map[string]interface{} {
	sni := n.TunnelSNI
	if sni == "" {
		sni = n.IPAddress
	}
	reality := map[string]interface{}{"fingerprint": fingerprint, "serverName": sni}
	if n.TunnelPubKey != "" {
		reality["publicKey"] = n.TunnelPubKey
		if n.TunnelShortID != "" {
			reality["shortId"] = n.TunnelShortID // singular: the client side of Reality
		}
	}
	user := map[string]interface{}{"id": n.TunnelUUID, "encryption": "none"}
	out := map[string]interface{}{"tag": tag, "protocol": "vless"}
	port := exitVRPort
	if n.TunnelProtocol == "vless" {
		port = n.TunnelPort
		out["mux"] = map[string]interface{}{
			"enabled": true, "concurrency": mux, "xudpConcurrency": 16, "xudpProxyUDP443": "allow",
		}
	} else {
		user["flow"] = exitVRFlow
	}
	out["settings"] = map[string]interface{}{
		"vnext": []map[string]interface{}{{"address": n.IPAddress, "port": port, "users": []map[string]interface{}{user}}},
	}
	out["streamSettings"] = map[string]interface{}{
		"network": "tcp", "security": "reality", "realitySettings": reality,
		"sockopt": map[string]interface{}{"tcpFastOpen": true, "tcpNoDelay": true, "tcpKeepAliveInterval": 30},
	}
	return out
}

// exitProbeInbound: loopback SOCKS the tunnel monitor dials to test exit N's TCP path.
func exitProbeInbound(id int) map[string]interface{} {
	return map[string]interface{}{
		"tag": exitProbeTag(id), "listen": "127.0.0.1", "port": exitProbePort(id), "protocol": "socks",
		"settings": map[string]interface{}{"auth": "noauth", "udp": false},
	}
}

// exitRoute is one routed (healthy) exit as the balancers see it.
type exitRoute struct {
	id           int
	weight       int    // load_weight clamped to 1..10
	primary      string // the fallback tag: .hy when the exit has it, else .vr
	hasHy, hasVR bool
}

// exitConfig is the exit part of the xray config.
type exitConfig struct {
	routes         []exitRoute
	outbounds      []map[string]interface{} // exit-N.hy / exit-N.vr of the routed exits
	probeOutbounds []map[string]interface{}
	probeInbounds  []map[string]interface{}
	probeRules     []map[string]interface{}
}

// exitWeight clamps load_weight to 1..10 (migration 000022).
func exitWeight(w int) int {
	if w < 1 {
		return 1
	}
	if w > 10 {
		return 10
	}
	return w
}

// buildExitConfig builds the exit outbounds of the healthy exits and the probe pairs of every
// online exit with a TCP path, in node order (ORDER BY id).
func buildExitConfig(nodes []biz.ExitNodeTunnel, fingerprint string, mux int) exitConfig {
	var c exitConfig
	for _, n := range nodes {
		hy, vr := exitHasHyPath(n), exitHasVRPath(n)
		if !hy && !vr {
			log.Printf("[xray-sync] exit-нода %d (%s): пропущена — нет ни пути Hysteria, ни TCP-пути (протокол %q, id 1..%d)",
				n.ID, n.Name, n.TunnelProtocol, exitMaxID)
			continue
		}
		hyTag, vrTag := exitOutboundTags(n.ID)
		if n.TunnelStatus == "healthy" {
			r := exitRoute{id: n.ID, weight: exitWeight(n.LoadWeight), primary: vrTag, hasHy: hy, hasVR: vr}
			if hy {
				r.primary = hyTag
				c.outbounds = append(c.outbounds, exitHyOutbound(n.ID, hyTag))
			}
			if vr {
				c.outbounds = append(c.outbounds, exitVROutbound(n, vrTag, fingerprint, mux))
			}
			c.routes = append(c.routes, r)
		}
		if vr {
			tag := exitProbeTag(n.ID)
			c.probeOutbounds = append(c.probeOutbounds, exitVROutbound(n, tag, fingerprint, mux))
			c.probeInbounds = append(c.probeInbounds, exitProbeInbound(n.ID))
			c.probeRules = append(c.probeRules, map[string]interface{}{
				"type": xrayRuleTypeField, "inboundTag": []string{tag}, "outboundTag": tag,
			})
		}
	}
	return c
}

// exitCosts are the leastLoad costs of the given routed exits (see exitHyCostBase). Anchored
// regexps: xray's plain match is a SUBSTRING match.
func exitCosts(routes []exitRoute) []map[string]interface{} {
	costs := make([]map[string]interface{}, 0, 2*len(routes))
	for _, r := range routes {
		base := exitHyCostBase - r.weight
		hy, vr := exitOutboundTags(r.id)
		if r.hasHy {
			costs = append(costs, exitCost(hy, base))
		}
		if r.hasVR {
			v := base
			if r.hasHy {
				v = base * exitVRCostFactor
			}
			costs = append(costs, exitCost(vr, v))
		}
	}
	return costs
}

func exitCost(tag string, value int) map[string]interface{} {
	return map[string]interface{}{"regexp": true, "match": "^" + regexp.QuoteMeta(tag) + "$", "value": value}
}
