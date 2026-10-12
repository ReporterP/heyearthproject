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
		"-C PREROUTING -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443",
		"-I PREROUTING 1 -p udp --dport 30000:65000 -m addrtype --dst-type LOCAL -j DNAT --to-destination :50443",
		"while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j REDIRECT --to-ports 50443 2>/dev/null; do :; done",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("rule lacks %q", want)
		}
	}
	if regexp.MustCompile(`--to-destination \d`).MatchString(r) {
		t.Error("DNAT must not pin an IP (port-only)")
	}
}

// PREROUTING also carries forwarded traffic (AWG users on awg0, docker). Every DNAT the
// rule adds must be restricted to packets addressed to the Hub itself, otherwise their
// UDP flows to 30000-65000 get the dport rewritten to 50443 (Discord voice, calls, games).
func TestHysteria2PortHopRuleOnlyLocalDestinations(t *testing.T) {
	r := hysteria2PortHopRule
	adds := regexp.MustCompile(`iptables -t nat -(?:[AI]|C) PREROUTING[^;|&]*`).FindAllString(r, -1)
	if len(adds) != 2 {
		t.Fatalf("want exactly one -C and one -I PREROUTING call, got %d: %q", len(adds), adds)
	}
	for _, a := range adds {
		if !strings.Contains(a, "-m addrtype --dst-type LOCAL") {
			t.Errorf("DNAT not restricted to LOCAL destinations: %q", a)
		}
	}
	// Stale unrestricted DNAT rules (the previous version of this rule) are removed in a
	// loop, before the restricted rule is checked/inserted.
	del := "while iptables -t nat -D PREROUTING -p udp --dport 30000:65000 -j DNAT --to-destination :50443 2>/dev/null; do :; done; "
	i := strings.Index(r, del)
	if i < 0 {
		t.Fatalf("rule does not remove the stale unrestricted DNAT (want %q)", del)
	}
	if c := strings.Index(r, "-C PREROUTING"); c < i {
		t.Error("stale unrestricted DNAT must be removed before the -C/-I of the restricted rule")
	}
	// The -I must stay the last command: ExecStartPre fails loudly if it cannot insert.
	if !strings.HasSuffix(r, "-j DNAT --to-destination :50443") || !strings.Contains(r[strings.LastIndex(r, "||"):], "-I PREROUTING 1") {
		t.Error("the -I of the restricted rule must be the last command")
	}
}
