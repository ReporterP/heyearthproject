package main

import (
	"fmt"
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
//   - with no healthy exit, every user inbound is blocked.
//
// Otherwise unmatched traffic and balancer failures egress via the first
// outbound, which used to be "direct", i.e. the Hub's RU IP.
const (
	xrayBlockTag  = "block"
	xrayDirectTag = "direct"  // Hub-local egress, used only by xray's own DNS
	xrayDNSTag    = "dns-out" // dns.tag: inboundTag of xray's built-in DNS queries
	xrayAPIInTag  = "api-in"
)

func newBlockOutbound() map[string]interface{} {
	return map[string]interface{}{
		"protocol": "blackhole",
		"tag":      xrayBlockTag,
		"settings": map[string]interface{}{"response": map[string]string{"type": "none"}},
	}
}

// newBalancer builds a balancer whose fallback is never the default outbound.
// selector holds only exits our health monitor reports healthy (exitTags in
// SyncXrayConfig), so when observatory briefly marks them all dead the
// fallback is the first of them; with an empty selector it is block.
func newBalancer(tag string, selector []string, strategy string) map[string]interface{} {
	fallback := xrayBlockTag
	if len(selector) > 0 && strings.HasPrefix(selector[0], "exit-") {
		fallback = selector[0]
	}
	return map[string]interface{}{
		"tag":         tag,
		"selector":    selector,
		"strategy":    map[string]string{"type": strategy},
		"fallbackTag": fallback,
	}
}

// userInboundTags returns, in config order, the tags of all inbounds that carry
// user traffic: everything except the stats API inbound.
func userInboundTags(inbounds []map[string]interface{}) []string {
	tags := make([]string, 0, len(inbounds))
	for _, in := range inbounds {
		if tag, _ := in["tag"].(string); tag != "" && tag != xrayAPIInTag {
			tags = append(tags, tag)
		}
	}
	return tags
}

// noExitBlockRule blocks all user inbounds. It is appended after the explicit
// RU-direct rules, so RU-direct keeps working while every exit is down.
func noExitBlockRule(inboundTags []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "field",
		"inboundTag":  inboundTags,
		"outboundTag": xrayBlockTag,
	}
}

// dnsDirectRule sends xray's own DNS queries out of the Hub. Their only
// consumers are freedom outbounds with UseIP (direct-freedom, mask-*), which
// connect from the Hub anyway. Without it the queries would hit the default
// outbound (block) whenever no exit is up.
func dnsDirectRule() map[string]interface{} {
	return map[string]interface{}{
		"type":        "field",
		"inboundTag":  []string{xrayDNSTag},
		"outboundTag": xrayDirectTag,
	}
}

// checkNoDirectLeak is the last guard before the config is written: a config
// violating the invariants is rejected and the running one stays in place.
func checkNoDirectLeak(outbounds []map[string]interface{}, routing map[string]interface{}) error {
	if len(outbounds) == 0 {
		return fmt.Errorf("no outbounds")
	}
	if tag, _ := outbounds[0]["tag"].(string); tag != xrayBlockTag {
		return fmt.Errorf("default outbound is %q, want %q", tag, xrayBlockTag)
	}
	if proto, _ := outbounds[0]["protocol"].(string); proto != "blackhole" {
		return fmt.Errorf("default outbound protocol is %q, want blackhole", proto)
	}
	raw, ok := routing["balancers"]
	if !ok {
		return nil
	}
	balancers, ok := raw.([]map[string]interface{})
	if !ok {
		return fmt.Errorf("routing.balancers has type %T", raw)
	}
	for _, b := range balancers {
		fb, _ := b["fallbackTag"].(string)
		if fb != xrayBlockTag && !strings.HasPrefix(fb, "exit-") {
			return fmt.Errorf("balancer %v has fallbackTag %q, want %q or an exit-* tag", b["tag"], fb, xrayBlockTag)
		}
	}
	return nil
}
