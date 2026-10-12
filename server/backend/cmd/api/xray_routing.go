package main

import (
	"fmt"
	"reflect"
	"strings"
)

// Routing invariants of the generated xray config (C2, research 2026-10-11):
//   - outbounds[0], xray's default handler, is the blackhole "block";
//   - every balancer falls back to its first member — an exit our own health
//     monitor (checkTunnelHealth, 3-fail/2-ok hysteresis) considers healthy —
//     or to "block" when it has none; never to a direct/freedom outbound.
//     Falling back to the exit (not block) matters: xray's observatory marks
//     the only exit "dead" ~37x/day on short probe failures, and block would
//     turn each of those into a user-visible outage;
//   - with no healthy exit, every user inbound is blocked (DNS excepted, see
//     noExitDNSRule);
//   - the only rule that may use the Hub-local "direct" outbound is the one for
//     xray's own DNS client (dnsDirectRule).
//
// Otherwise unmatched traffic and balancer failures egress via the first
// outbound, which used to be "direct", i.e. the Hub's RU IP.
const (
	xrayBlockTag      = "block"
	xrayDirectTag     = "direct"     // Hub-local egress, used only by xray's own DNS
	xrayDNSTag        = "dns-out"    // dns.tag: inboundTag of xray's built-in DNS queries
	xrayDNSHijackTag  = "dns-hijack" // no-exit mode: answers users' port-53 queries from the built-in DNS
	xrayAPIInTag      = "api-in"
	xrayPrivateIPTag  = "geoip:private"
	xrayRulePortDNS   = "53"
	xrayRuleTypeField = "field"
)

func newBlockOutbound() map[string]interface{} {
	return map[string]interface{}{
		"protocol": "blackhole",
		"tag":      xrayBlockTag,
		"settings": map[string]interface{}{"response": map[string]string{"type": "none"}},
	}
}

// dnsHijackOutbound is xray's "dns" outbound: it terminates a DNS query itself
// and answers it from the built-in DNS client, so nothing is forwarded to the
// address the client asked. Used only in the no-exit mode (noExitDNSRule).
func dnsHijackOutbound() map[string]interface{} {
	return map[string]interface{}{"protocol": "dns", "tag": xrayDNSHijackTag}
}

// newBalancer builds a leastLoad balancer whose fallback is never the default outbound: the
// primary path of its preferred exit (exitFallback; an exit our own monitor reports healthy) or
// block when it has none. leastLoad needs the observatory and drops paths it sees dead; costs and
// baselines decide which live paths share the traffic (exitBaselines). The old balancer_strategy
// setting (random) is no longer used: random would split traffic between an exit's Hysteria and
// TCP paths instead of preferring Hysteria.
func newBalancer(tag string, selector []string, fallback string, costs []map[string]interface{}) map[string]interface{} {
	if fallback == "" {
		fallback = xrayBlockTag
	}
	if costs == nil {
		costs = []map[string]interface{}{}
	}
	return map[string]interface{}{
		"tag":      tag,
		"selector": selector,
		"strategy": map[string]interface{}{
			"type": "leastLoad",
			"settings": map[string]interface{}{
				"costs":     costs,
				"baselines": append([]string(nil), exitBaselines...),
				"expected":  exitLeastLoadExpected,
			},
		},
		"fallbackTag": fallback,
	}
}

// userInboundTags returns, in config order, the tags of all inbounds that carry
// user traffic: everything except the stats API inbound.
func userInboundTags(inbounds []map[string]interface{}) []string {
	tags := make([]string, 0, len(inbounds))
	for _, in := range inbounds {
		// probe-vr-* are the tunnel monitor's loopback inbounds, not user traffic: they must keep
		// working in the no-exit mode (a recovered TCP path has to be seen).
		if tag, _ := in["tag"].(string); tag != "" && tag != xrayAPIInTag && !strings.HasPrefix(tag, exitProbeTagPrefix) {
			tags = append(tags, tag)
		}
	}
	return tags
}

// noExitBlockRule blocks all user inbounds. It is appended after the explicit
// RU-direct rules, so RU-direct keeps working while every exit is down.
func noExitBlockRule(inboundTags []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        xrayRuleTypeField,
		"inboundTag":  inboundTags,
		"outboundTag": xrayBlockTag,
	}
}

// noExitDNSRule lets users' port-53 traffic reach the dns-hijack outbound
// instead of the no-exit block rule. Without it the AWG clients (DNS = 1.1.1.1,
// 8.8.8.8, arriving via tun2socks in socks-awg as UDP :53) could not resolve
// anything, not even the RU-direct sites that are still served. The queries are
// answered by xray's built-in DNS, which goes out through dnsDirectRule; no user
// data leaves the Hub. Must come BEFORE noExitBlockRule.
func noExitDNSRule(inboundTags []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        xrayRuleTypeField,
		"inboundTag":  inboundTags,
		"port":        xrayRulePortDNS,
		"outboundTag": xrayDNSHijackTag,
	}
}

// dnsDirectRule sends xray's own DNS queries out of the Hub. Their consumers
// are the freedom outbounds with UseIP (direct-freedom, mask-*), which connect
// from the Hub anyway, and, in the no-exit mode, the dns-hijack outbound.
// Without it the queries would hit the default outbound (block) whenever no exit
// is up.
//
// Behaviour change, intended: xray's built-in DNS now always resolves from the
// Hub, also while the exits are healthy. Previously its queries were routed like
// user traffic, i.e. through the catch-all balancer to the exit abroad. Resolving
// from the Hub gives RU-correct answers (RU CDN nodes) for direct-freedom, which
// is what the Hub connects to anyway; nothing new is exposed, as those domains
// are contacted from the Hub's IP regardless.
func dnsDirectRule() map[string]interface{} {
	return map[string]interface{}{
		"type":        xrayRuleTypeField,
		"inboundTag":  []string{xrayDNSTag},
		"outboundTag": xrayDirectTag,
	}
}

// baseSecurityRules are the first routing rules, in order:
//
//  0. xray's own DNS client goes out of the Hub (dnsDirectRule);
//  1. private ranges are blocked (geoip:private) — spokes and users cannot reach the Hub's
//     internal network or each other;
//  2. loopback and "this host" are blocked explicitly (127.0.0.0/8, ::1/128, 0.0.0.0/8, ::/128),
//     so the block does not depend on what the geoip file happens to contain;
//  3. the Hub's own public addresses are blocked on every port except 80/443 (hubIPs, see
//     hub_addrs.go): a proxy user must not be able to make the Hub connect to itself — sshd,
//     PostgreSQL, the API on :8000, the xray API — through a freedom outbound. Omitted when
//     the list is empty (the caller logs that);
//  4. BitTorrent; 5. XMPP ports 5222/5223 (Chinese stock ROMs retry the dead Google Talk
//     endpoint endlessly, which backs up SOCKS5 i/o timeouts in the Hysteria2 client).
//
// These rules see only IP targets: with domainStrategy IPIfNonMatch a domain that matches an
// earlier domain rule (a RU-direct service) is never re-matched against the IP rules, and a
// freedom outbound with UseIP then resolves it wherever the DNS says — a hostile name may point
// at 127.0.0.1 or at the Hub itself. So this is defence in depth; the authoritative protection
// is the kernel egress guard (cmd/deploy/ops_egress_guard.go: iptables rules on the xray cgroup).
//
// DNS-over-HTTPS is deliberately not blocked: browsers resolve through it and it goes via the
// exits. checkNoDirectLeak requires the private block to precede every freedom rule.
func baseSecurityRules(hubIPs []string) []map[string]interface{} {
	rules := []map[string]interface{}{
		dnsDirectRule(),
		{"type": xrayRuleTypeField, "ip": []string{xrayPrivateIPTag}, "outboundTag": xrayBlockTag},
		{"type": xrayRuleTypeField, "ip": append([]string{}, xrayLocalBlockIPs...), "outboundTag": xrayBlockTag},
	}
	if len(hubIPs) > 0 {
		rules = append(rules, map[string]interface{}{
			"type": xrayRuleTypeField, "ip": append([]string{}, hubIPs...), "port": xrayHubBlockedPorts, "outboundTag": xrayBlockTag,
		})
	}
	return append(rules,
		map[string]interface{}{"type": xrayRuleTypeField, "protocol": []string{"bittorrent"}, "outboundTag": xrayBlockTag},
		map[string]interface{}{"type": xrayRuleTypeField, "port": "5222,5223", "outboundTag": xrayBlockTag},
	)
}

// xrayLocalBlockIPs: loopback and "this host", IPv4 and IPv6.
var xrayLocalBlockIPs = []string{"127.0.0.0/8", "::1/128", "0.0.0.0/8", "::/128"}

// isFreedomRuleTarget reports whether outbound tag is a Hub-local freedom outbound a rule
// could send user traffic out of the Hub through: direct-freedom (RU-direct services) and the
// per-masking-rule mask-* outbounds.
func isFreedomRuleTarget(tag interface{}) bool {
	s, _ := tag.(string)
	return s == "direct-freedom" || strings.HasPrefix(s, "mask-")
}

// isPrivateBlockRule: an unconditional (no inbound / user / port restriction) rule that sends
// geoip:private to block.
func isPrivateBlockRule(r map[string]interface{}) bool {
	if r["outboundTag"] != xrayBlockTag {
		return false
	}
	ips, _ := r["ip"].([]string)
	if len(ips) != 1 || ips[0] != xrayPrivateIPTag {
		return false
	}
	for k := range r {
		switch k {
		case "type", "ip", "outboundTag":
		default:
			return false
		}
	}
	return true
}

// ruleTagList reads a rule's list-of-strings field (inboundTag), nil if absent.
func ruleTagList(rule map[string]interface{}, key string) []string {
	v, _ := rule[key].([]string)
	return v
}

// isInboundOnlyRule reports whether rule matches on inboundTag alone: any other
// matcher (port, domain, ip, user, network, protocol, ...) narrows it down.
func isInboundOnlyRule(rule map[string]interface{}) bool {
	for k := range rule {
		switch k {
		case "type", "inboundTag", "outboundTag":
		default:
			return false
		}
	}
	return true
}

// checkNoDirectLeak is the last guard before the config is written: a config
// violating the invariants is rejected and the running one stays in place.
func checkNoDirectLeak(inbounds, outbounds []map[string]interface{}, routing map[string]interface{}) error {
	if len(outbounds) == 0 {
		return fmt.Errorf("no outbounds")
	}
	if tag, _ := outbounds[0]["tag"].(string); tag != xrayBlockTag {
		return fmt.Errorf("default outbound is %q, want %q", tag, xrayBlockTag)
	}
	if proto, _ := outbounds[0]["protocol"].(string); proto != "blackhole" {
		return fmt.Errorf("default outbound protocol is %q, want blackhole", proto)
	}
	outboundTags := make(map[string]bool, len(outbounds))
	for _, o := range outbounds {
		if tag, _ := o["tag"].(string); tag != "" {
			outboundTags[tag] = true
		}
	}
	// Probe inbounds are loopback-only.
	for _, in := range inbounds {
		if tag, _ := in["tag"].(string); strings.HasPrefix(tag, exitProbeTagPrefix) && in["listen"] != "127.0.0.1" {
			return fmt.Errorf("probe inbound %q listens on %v, want 127.0.0.1", tag, in["listen"])
		}
	}

	var rules []map[string]interface{}
	if raw, ok := routing["rules"]; ok {
		if rules, ok = raw.([]map[string]interface{}); !ok {
			return fmt.Errorf("routing.rules has type %T", raw)
		}
	}
	// The Hub-local "direct" egress is reserved for xray's own DNS client.
	for _, r := range rules {
		if r["outboundTag"] == xrayDirectTag && !reflect.DeepEqual(ruleTagList(r, "inboundTag"), []string{xrayDNSTag}) {
			return fmt.Errorf("rule %v sends traffic to %q; only the %q inbound may", r, xrayDirectTag, xrayDNSTag)
		}
	}
	// A probe outbound is reachable only from its own loopback inbound.
	for _, r := range rules {
		out, _ := r["outboundTag"].(string)
		if strings.HasPrefix(out, exitProbeTagPrefix) && (!isInboundOnlyRule(r) || !reflect.DeepEqual(ruleTagList(r, "inboundTag"), []string{out})) {
			return fmt.Errorf("rule %v reaches the probe outbound %q; only its own inbound may", r, out)
		}
	}

	// A Hub-local freedom outbound (direct-freedom, mask-*) may only be reached after the
	// private-range block: otherwise a user could ask the Hub to connect to 127.0.0.1 / its own
	// LAN through it.
	privateBlocked := false
	for _, r := range rules {
		if isPrivateBlockRule(r) {
			privateBlocked = true
			continue
		}
		if isFreedomRuleTarget(r["outboundTag"]) && !privateBlocked {
			return fmt.Errorf("rule %v sends traffic through a Hub-local freedom outbound before the %q block rule", r, xrayPrivateIPTag)
		}
	}

	var balancers []map[string]interface{}
	if raw, ok := routing["balancers"]; ok {
		if balancers, ok = raw.([]map[string]interface{}); !ok {
			return fmt.Errorf("routing.balancers has type %T", raw)
		}
	}
	for _, b := range balancers {
		fb, _ := b["fallbackTag"].(string)
		if fb != xrayBlockTag && !(strings.HasPrefix(fb, "exit-") && outboundTags[fb]) {
			return fmt.Errorf("balancer %v has fallbackTag %q, want %q or the tag of an exit outbound", b["tag"], fb, xrayBlockTag)
		}
	}
	if len(balancers) > 0 {
		return nil
	}

	// No balancers = no healthy exit: every user inbound must be blocked.
	blocked := map[string]bool{}
	for _, r := range rules {
		if r["outboundTag"] == xrayBlockTag && isInboundOnlyRule(r) {
			for _, tag := range ruleTagList(r, "inboundTag") {
				blocked[tag] = true
			}
		}
	}
	for _, tag := range userInboundTags(inbounds) {
		if !blocked[tag] {
			return fmt.Errorf("no healthy exit, but inbound %q is not blocked", tag)
		}
	}
	return nil
}
