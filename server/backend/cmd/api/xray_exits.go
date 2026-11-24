package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"sync"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
)

// ─────────────── Exit paths in the Hub's xray config (node pool, phase 1) ───────────────
//
// Every ONLINE exit N with a path gets up to two outbounds, healthy or not:
//
//	exit-N.hy  Hysteria: SOCKS 127.0.0.1:(1088+N), served by hysteria2-client-N (UDP to the exit)
//	exit-N.vr  TCP backup: SOCKS 127.0.0.1:(2088+N), served by heyearth-stls-client-N — ShadowTLS v3
//	           to the exit's :443 (internal/config exit_stls.go; "vr" is the path's historical
//	           name: until 2026-11-16 it was VLESS Reality to the exit's xray on :10443)
//
// Balancers pick exits by the prefix "exit-N." (xray selectors are tag prefixes, so "exit-2."
// can never match exit-20.*; Beads 07f7) and the catch-all one by "exit-". The order inside an
// exit (Hysteria first, TCP while Hysteria is dead) and load_weight between exits come from the
// leastLoad strategy's costs and baselines (exitCosts, exitBaselines); tags are never duplicated.
// Only the HEALTHY exits (tunnel_status healthy) are in the balancers: an exit whose BOTH paths the
// tunnel monitor finds dead (tunnel_probe.go) keeps its outbounds and leaves every selector, the
// costs and the fallbacks. Its health flip is then a balancers-only change (plus plan-* rules when
// a plan loses its last exit), which xraydiff applies live through adrules — open connections stay,
// xray is not restarted (spike C4, docs/plan/stability-operations-2.md). Once some exit with
// outbounds is out of the balancers (dead or out of service, exit_service.go), the catch-all
// balancer lists the others by name instead of "exit-"; with every exit healthy and in service the
// config is the one it always was. With no healthy exit at all the config is the no-exit block
// (xray_sync.go) without any exit path, as before: entering and leaving it restarts xray.
//
// Every ONLINE exit with a TCP path, healthy or not, also gets a probe pair outside every
// selector: the loopback SOCKS inbound probe-vr-N (127.0.0.1:12000+N) routed to the outbound
// probe-vr-N (the same hop to the ShadowTLS client). The monitor fetches generate_204 through it to judge the
// TCP path, also while the exit is out of the routing, so its recovery is noticed. The pair
// depends only on the exit being online, never on its health.
//
// Legacy rows: tunnel_protocol vless keeps its old single VLESS outbound (own port, mux, no flow)
// under exit-N.vr; shadowsocks/socks5/http are no longer generated (phase-1 pre-check: none online).

const (
	exitSocksPortBase  = 1088
	exitProbePortBase  = 12000
	exitMaxID          = 511 // the egress guard allows xray SOCKS 1088..1599 and 2088..2599 (cmd/deploy/ops_egress_guard.go)
	exitAllSelector    = "exit-"
	exitProbeTagPrefix = "probe-vr-"
	exitVRCostFactor   = 400 // √400 = ×20 on RTT, see exitBaselines

	// exitVRFingerprint is the uTLS fingerprint of a legacy VLESS exit's single path (fixed:
	// chrome, firefox, safari complete the Reality handshake, measured on prod 2026-10-15).
	exitVRFingerprint = config.ExitVRFingerprint
)

// ─────────────── Balancer strategy: leastLoad with costs and baselines ───────────────
//
// leastLoad as Xray-core 26.3.27 (v1.260327.0) runs it with the plain observatory
// (app/router/strategy_leastload.go getNodes/selectLeastLoad, app/router/weight.go):
//   - every probe round each exit path gets Delay = the time of one HTTPS GET through it; a path
//     the probe could not use is dead and not a candidate;
//   - a live path's RTTDeviationCost = Delay × √cost, cost = the value of the first entry of
//     costs that matches its tag (default 1); the paths are sorted by it;
//   - with baselines and expected = 1 the balancer takes the smallest baseline with at least one
//     path below it and picks, per connection, uniformly among ALL paths below that baseline; if
//     even the cheapest path is not below the last baseline, it takes that path alone. Without
//     expected that last case selects nothing and every connection goes to fallbackTag, a fixed
//     path that may itself be dead (e.g. only exit-N.vr alive at 200 ms = 2 s weighted), hence
//     expected = 1.
//
// The baselines double. With the cheapest live path at x weighted (50 ms ≤ x < 1.6 s) the window
// ends at the next baseline b, x < b ≤ 2x: another path with weighted cost c joins it never when
// c ≥ 2x, always when c ≤ x, and in between depending on where the band edge falls. Hence:
//   - the exits of the balancer's highest priority cost 1: their Hysteria paths share the load
//     while their RTTs are in the same band — 60 and 70 ms (both under 100 ms) share, 190 and
//     210 ms do not at that moment (200 ms lies between them), 210 and 230 ms do;
//   - an exit g priority steps lower costs (4g)², i.e. ×4g on its RTT (exitTierCost): it gets
//     traffic never while the preferred exits live and are at most 2g times slower than it,
//     always once they are 4g times slower (2/1 at 101 vs 99 ms: the preferred alone; 250 vs
//     90 ms: both; 250 vs 100 ms: the preferred alone — 4×100 is not below the 400 ms edge);
//   - the TCP path of a Hysteria exit costs ×400 more (×20 RTT): it joins never while its
//     Hysteria path lives and is at most 10 times slower, always once it is 20 times slower or
//     dead. The single path of a VLESS-only exit costs as its priority, no ×400;
//   - the preferred exit's Hysteria dead, its TCP alive, a reserve g steps lower with a live
//     Hysteria, equal RTTs: 20 against 4g — g ≤ 2: the reserve's Hysteria alone; g = 3, 4: the
//     reserve's Hysteria, the preferred TCP joining depending on the band; g = 5: equal cost,
//     both; g = 6..9: the preferred TCP, the reserve joining depending on the band.
//
// RTTs under 50 ms (not reachable through an exit abroad) all share the first band; from 1.6 s
// weighted on, only the cheapest path is taken.
var exitBaselines = []string{"50ms", "100ms", "200ms", "400ms", "800ms", "1600ms"}

const exitLeastLoadExpected = 1

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

// exitHasVRPath is THE predicate of an exit's TCP path, shared by the generator (buildExitConfig),
// the tunnel monitor and «Проверить туннель»: a path xray accepts (exitVRProblem) that the owner
// did not switch off.
func exitHasVRPath(n biz.ExitNodeTunnel) bool {
	// The owner may switch this exit's TCP fallback off (servers.tunnel_fallback_enabled): then the
	// generator builds neither exit-<id>.vr nor the probe pair, and the monitor / «Проверить туннель»
	// report the path as absent instead of probing an inbound that does not exist. A legacy vless
	// exit's TCP path is its only one, not a fallback: the switch does not apply to it.
	if n.TunnelProtocol == "hysteria2" && !n.FallbackEnabled {
		return false
	}
	return exitVRProblem(n) == ""
}

// exitVRSwitchedOffWhy is the reason given for a TCP path that is valid but switched off.
const exitVRSwitchedOffWhy = "запасной путь выключен в карточке ноды («Запасной TCP-путь»)"

// exitVRAbsentWhy says why exitHasVRPath says no: the field problem (exitVRProblem) when there is
// one — the path would be missing even with the switch on — else the owner's switch.
func exitVRAbsentWhy(n biz.ExitNodeTunnel) string {
	if why := exitVRProblem(n); why != "" {
		return why
	}
	return exitVRSwitchedOffWhy
}

// exitVRProblem says why exit n has no usable TCP path, "" when it has one. A Hysteria exit's
// backup is ShadowTLS through the Hub's client of the exit, whose secrets derive from the tunnel
// UUID (config.STLSSecrets): the recipe sets that client up wherever the UUID is. A legacy VLESS
// exit's path is checked as Xray-core 26.3.27 does (infra/conf/vless.go: the user id;
// transport_internet.go REALITYConfig.Build, client side: publicKey = base64url of 32 bytes,
// shortId = hex, at most 16 characters, empty allowed): a single row xray rejects would fail
// `xray run -test` and block every sync, so such a path is dropped instead.
func exitVRProblem(n biz.ExitNodeTunnel) string {
	if !exitIDValid(n.ID) {
		return fmt.Sprintf("id вне 1..%d", exitMaxID)
	}
	switch n.TunnelProtocol {
	case "hysteria2":
		if !exitUUIDValid(n.TunnelUUID) {
			return "нет tunnel_uuid (из него выводятся ключи запасного туннеля ShadowTLS)"
		}
		return ""
	case "vless":
		if n.TunnelPort < 1 || n.TunnelPort > 65535 {
			return fmt.Sprintf("tunnel_port %d вне 1..65535", n.TunnelPort)
		}
	default:
		return fmt.Sprintf("протокол %q без TCP-пути", n.TunnelProtocol)
	}
	if !exitUUIDValid(n.TunnelUUID) {
		return "tunnel_uuid — не UUID"
	}
	if k, err := base64.RawURLEncoding.DecodeString(n.TunnelPubKey); err != nil || len(k) != 32 {
		return "tunnel_public_key — не ключ x25519 (base64url, 32 байта)"
	}
	if len(n.TunnelShortID) > 16 {
		return "tunnel_short_id длиннее 16 символов"
	}
	if _, err := hex.DecodeString(n.TunnelShortID); err != nil {
		return "tunnel_short_id — не hex чётной длины"
	}
	return ""
}

// exitUUIDValid accepts the canonical 8-4-4-4-12 form or 32 bare hex digits: both parse in
// xray's uuid.ParseString the same way (it would also hash a 1..30 character string into an id,
// which no exit row is meant to hold).
func exitUUIDValid(s string) bool {
	switch len(s) {
	case 32:
		_, err := hex.DecodeString(s)
		return err == nil
	case 36:
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return false
		}
		_, err := hex.DecodeString(s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36])
		return err == nil
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

// exitVRServerName is the SNI of a legacy VLESS exit's path: its tunnel_sni when set, else its
// own address.
func exitVRServerName(n biz.ExitNodeTunnel) string {
	if n.TunnelSNI != "" {
		return n.TunnelSNI
	}
	return n.IPAddress
}

// exitVROutbound is exit n's TCP path. For a Hysteria exit — the backup — SOCKS to the Hub's
// ShadowTLS client of the exit (config.HubSTLSSocksPort; TCP and UDP, the client carries UDP over
// TCP). A legacy primary VLESS exit keeps its old outbound: VLESS Reality to its own port, mux,
// no flow, fingerprint exitVRFingerprint, SNI exitVRServerName.
func exitVROutbound(n biz.ExitNodeTunnel, tag string, mux int) map[string]interface{} {
	if n.TunnelProtocol == "hysteria2" {
		return map[string]interface{}{
			"tag": tag, "protocol": "socks",
			"settings": map[string]interface{}{
				"servers": []map[string]interface{}{{"address": "127.0.0.1", "port": config.HubSTLSSocksPort(n.ID)}},
			},
		}
	}
	// publicKey is always there (exitHasVRPath); shortId is singular on the client side of Reality.
	reality := map[string]interface{}{"fingerprint": exitVRFingerprint, "serverName": exitVRServerName(n), "publicKey": n.TunnelPubKey}
	if n.TunnelShortID != "" {
		reality["shortId"] = n.TunnelShortID
	}
	user := map[string]interface{}{"id": n.TunnelUUID, "encryption": "none"}
	out := map[string]interface{}{"tag": tag, "protocol": "vless"}
	out["mux"] = map[string]interface{}{
		"enabled": true, "concurrency": mux, "xudpConcurrency": 16, "xudpProxyUDP443": "allow",
	}
	out["settings"] = map[string]interface{}{
		"vnext": []map[string]interface{}{{"address": n.IPAddress, "port": n.TunnelPort, "users": []map[string]interface{}{user}}},
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

// exitRoute is one exit with outbounds as the balancers see it.
type exitRoute struct {
	id           int
	weight       int    // load_weight clamped to 1..10
	primary      string // .hy when the exit has it, else .vr
	hasHy, hasVR bool
}

// exitConfig is the exit part of the xray config.
type exitConfig struct {
	routes         []exitRoute              // the healthy exits: the balancers' candidates
	dead           []exitRoute              // online exits with a path, not healthy: outbounds only, outside every selector
	outbounds      []map[string]interface{} // exit-N.hy / exit-N.vr of every exit of routes and dead, in node order
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

// exitNoteLog remembers, per exit id, the problem buildExitConfig last logged (a skipped exit, a
// dropped TCP path), so a bad row is logged once and again only when its state changes, not on
// every sync.
type exitNoteLog struct {
	mu   sync.Mutex
	last map[int]string
}

var exitNotes = &exitNoteLog{}

// update logs the notes that differ from the previous call (a problem that appeared or changed,
// or one that is gone for an exit still listed) and keeps notes as the new state; exits no longer
// listed are forgotten.
func (l *exitNoteLog) update(nodes []biz.ExitNodeTunnel, notes map[int]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, n := range nodes {
		note, prev := notes[n.ID], l.last[n.ID]
		switch {
		case note == prev:
		case note != "":
			log.Printf("[xray-sync] exit-нода %d (%s): %s", n.ID, n.Name, note)
		default:
			log.Printf("[xray-sync] exit-нода %d (%s): снова в порядке (было: %s)", n.ID, n.Name, prev)
		}
	}
	l.last = notes
}

// buildExitConfig builds the exit outbounds of every online exit with a path (healthy or not:
// only the healthy ones are routes) and the probe pairs of every online exit with a TCP path, in
// node order (ORDER BY id).
func buildExitConfig(nodes []biz.ExitNodeTunnel, mux int) exitConfig {
	var c exitConfig
	notes := map[int]string{}
	defer func() { exitNotes.update(nodes, notes) }()
	for _, n := range nodes {
		// Second line of defence: the reads return online exits only.
		if n.LifecycleState != "online" {
			notes[n.ID] = fmt.Sprintf("пропущена — lifecycle_state %q, не online", n.LifecycleState)
			continue
		}
		if !exitIDValid(n.ID) {
			notes[n.ID] = fmt.Sprintf("пропущена — id вне 1..%d (SOCKS 1088+id вне диапазона охраны исходящих)", exitMaxID)
			continue
		}
		hy, vr := exitHasHyPath(n), exitHasVRPath(n)
		if !vr {
			why := exitVRAbsentWhy(n)
			if !hy {
				notes[n.ID] = fmt.Sprintf("пропущена — нет пути Hysteria (протокол %q) и нет TCP-пути: %s", n.TunnelProtocol, why)
				continue
			}
			notes[n.ID] = "TCP-путь (exit-" + strconv.Itoa(n.ID) + ".vr) не строится: " + why
		}
		hyTag, vrTag := exitOutboundTags(n.ID)
		r := exitRoute{id: n.ID, weight: exitWeight(n.LoadWeight), primary: vrTag, hasHy: hy, hasVR: vr}
		if hy {
			r.primary = hyTag
			c.outbounds = append(c.outbounds, exitHyOutbound(n.ID, hyTag))
		}
		if vr {
			c.outbounds = append(c.outbounds, exitVROutbound(n, vrTag, mux))
		}
		// The outbounds never depend on the health, the balancers do (see the head of this file).
		if n.TunnelStatus == "healthy" {
			c.routes = append(c.routes, r)
		} else {
			c.dead = append(c.dead, r)
		}
		if vr {
			tag := exitProbeTag(n.ID)
			c.probeOutbounds = append(c.probeOutbounds, exitVROutbound(n, tag, mux))
			c.probeInbounds = append(c.probeInbounds, exitProbeInbound(n.ID))
			c.probeRules = append(c.probeRules, map[string]interface{}{
				"type": xrayRuleTypeField, "inboundTag": []string{tag}, "outboundTag": tag,
			})
		}
	}
	return c
}

// exitFallback is a balancer's fallbackTag: the primary path (.hy, else the only one) of its exit
// with the highest load_weight, the lowest id among equals; block for none.
func exitFallback(routes []exitRoute) string {
	best := -1
	for i, r := range routes {
		if best < 0 || r.weight > routes[best].weight || (r.weight == routes[best].weight && r.id < routes[best].id) {
			best = i
		}
	}
	if best < 0 {
		return xrayBlockTag
	}
	return routes[best].primary
}

// exitTierCost is the leastLoad cost of a path gap priority steps below the balancer's best
// exit: 1, then (4·gap)², i.e. ×4·gap on its RTT. With the doubling baselines such a path joins
// the window never while the preferred exit is at most 2·gap times slower, always once it is
// 4·gap times slower (see exitBaselines).
func exitTierCost(gap int) int {
	if gap <= 0 {
		return 1
	}
	return 16 * gap * gap
}

// exitCosts are the leastLoad costs of the given routed exits, relative to the highest
// load_weight among them (see exitBaselines). Anchored regexps: xray's plain match is a SUBSTRING
// match.
func exitCosts(routes []exitRoute) []map[string]interface{} {
	top := 0
	for _, r := range routes {
		top = max(top, r.weight)
	}
	costs := make([]map[string]interface{}, 0, 2*len(routes))
	for _, r := range routes {
		base := exitTierCost(top - r.weight)
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
