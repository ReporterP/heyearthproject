package main

import (
	"regexp"
	"strings"
	"testing"
)

// The port-hop rule must never install REDIRECT (it rewrites the destination to the
// interface's primary IP and the server's replies then clash in conntrack, see
// hysteria2PortHopRule). Removing a stale REDIRECT is fine; adding one is the bug.
func TestHysteria2HubScriptNeverAddsRedirect(t *testing.T) {
	script := hysteria2HubScript()
	addRedirect := regexp.MustCompile(`iptables -t nat -[AI] PREROUTING[^\n;|&]*-j REDIRECT`)
	if m := addRedirect.FindString(script); m != "" {
		t.Fatalf("script installs a REDIRECT rule: %q", m)
	}
	if strings.Contains(script, "__PORTHOP__") || strings.Contains(script, "__VERSION__") {
		t.Fatal("placeholder left unreplaced")
	}
	// Applied live and in the unit's ExecStartPre (re-applied on every start, incl. boot).
	if n := strings.Count(script, hysteria2PortHopRule); n != 2 {
		t.Fatalf("port-hop rule appears %d times, want 2 (live + ExecStartPre)", n)
	}
	if !strings.Contains(script, "ExecStartPre=+/bin/sh -c '"+hysteria2PortHopRule+"'") {
		t.Fatal("ExecStartPre does not carry the port-hop rule")
	}
}

func TestHysteria2PortHopRuleShape(t *testing.T) {
	r := hysteria2PortHopRule
	if strings.Contains(r, "'") {
		t.Fatal("rule contains a single quote; it is embedded in sh -c '...'")
	}
	// Port-only DNAT keeps the client's destination IP, so the route-sourced reply matches
	// the conntrack reply tuple.
	for _, want := range []string{
		"-C PREROUTING -p udp --dport 30000:65000 -j DNAT --to-destination :50443",
		"-I PREROUTING 1 -p udp --dport 30000:65000 -j DNAT --to-destination :50443",
		"-D PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("rule lacks %q", want)
		}
	}
	if regexp.MustCompile(`--to-destination \d`).MatchString(r) {
		t.Error("DNAT must not pin an IP (port-only)")
	}
}
