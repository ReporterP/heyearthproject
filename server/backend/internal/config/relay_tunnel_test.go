package config

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

// The relay's and the Hub's tunnel addresses (relay id 6): RFC 1918 pool addresses, never real.
const (
	labHubTun   = "10.77.0.25"
	labRelayTun = "10.77.0.26"
	labRelay    = "198.51.100.42"
)

func TestRelayTunnelAddrs(t *testing.T) {
	for _, c := range []struct {
		id         int
		hub, relay string
	}{{6, labHubTun, labRelayTun}, {1, "10.77.0.5", "10.77.0.6"}, {64, "10.77.1.1", "10.77.1.2"}, {RelayTunnelMaxID, "10.77.255.253", "10.77.255.254"}} {
		h, r, err := RelayTunnelAddrs(c.id)
		if err != nil || h.String() != c.hub || r.String() != c.relay {
			t.Errorf("id %d: %v %v %v, want %s %s", c.id, h, r, err, c.hub, c.relay)
		}
		b, err := RelayTunnelBlock(c.id)
		if err != nil || b.Bits() != 30 || !b.Contains(h) || !b.Contains(r) {
			t.Errorf("id %d: block %v %v", c.id, b, err)
		}
	}
	for _, id := range []int{0, -1, RelayTunnelMaxID + 1} {
		if _, _, err := RelayTunnelAddrs(id); err == nil {
			t.Errorf("id %d accepted", id)
		}
	}
	// Two neighbours never share a block.
	a, _ := RelayTunnelBlock(5)
	b, _ := RelayTunnelBlock(6)
	if a.Overlaps(b) {
		t.Errorf("blocks of 5 and 6 overlap: %v %v", a, b)
	}
	if HubTunnelDev(6) != "he-r6" || HubTunnelUnitName(6) != "heyearth-tunnel-r6.service" || HubTunnelUFWComment(6) != "heyearth-tunnel-6" {
		t.Error("hub tunnel names")
	}
	if len(HubTunnelDev(RelayTunnelMaxID)) > 15 {
		t.Errorf("device name %q longer than IFNAMSIZ-1", HubTunnelDev(RelayTunnelMaxID))
	}
}

func TestTunnelUnit(t *testing.T) {
	hub, relay := netip.MustParseAddr(labHubTun), netip.MustParseAddr(labRelayTun)
	r, err := TunnelUnit(RelayTunnelDev, labRelay, labHub, relay, hub)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"ExecStartPre=-ip link del he-hub\n",
		"ExecStart=ip tunnel add he-hub mode gre local " + labRelay + " remote " + labHub + " ttl 64\n",
		"ExecStart=ip addr add " + labRelayTun + " peer " + labHubTun + " dev he-hub\n",
		"ExecStart=ip link set he-hub mtu 1476 up\n",
		"ExecStop=-ip link del he-hub\n",
		"Type=oneshot\nRemainAfterExit=yes\n",
		"WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(r, must) {
			t.Errorf("relay unit lacks %q:\n%s", must, r)
		}
	}
	golden(t, "relay_tunnel_unit.golden", []byte(r))
	h, err := TunnelUnit(HubTunnelDev(6), labHub, labRelay, hub, relay)
	if err != nil || !strings.Contains(h, "ip tunnel add he-r6 mode gre local "+labHub+" remote "+labRelay+" ttl 64\n") ||
		!strings.Contains(h, "ip addr add "+labHubTun+" peer "+labRelayTun+" dev he-r6\n") {
		t.Fatalf("hub unit %v:\n%s", err, h)
	}
	golden(t, "hub_tunnel_unit.golden", []byte(h))
	for _, bad := range []struct {
		dev, local, remote string
		addr, peer         netip.Addr
	}{
		{"eth0", labRelay, labHub, relay, hub},                                                             // not ours
		{"he-hub; rm -rf /", labRelay, labHub, relay, hub},                                                 // injected
		{"he-hub", "10.0.0.1", labHub, relay, hub},                                                         // private outer address
		{"he-hub", labRelay, labRelay, relay, hub},                                                         // one address both ends
		{"he-hub", labRelay, labHub, netip.MustParseAddr("192.168.0.2"), hub},                              // outside the pool
		{"he-hub", labRelay, labHub, relay, relay},                                                         // same inner address
		{"he-hub", labRelay + "\nExecStart=/bin/sh", labHub, relay, hub},                                   // injected address
		{"he-hub0123456789", labRelay, labHub, relay, hub},                                                 // too long
		{"he-hub", labRelay, labHub, netip.MustParseAddr("10.78.0.1"), hub},                                // next /16
		{"he-hub", labRelay, labHub, netip.Addr{}, hub},                                                    // zero
		{"he-hub", labRelay, "203.0.113.10 ttl 1", relay, hub},                                             // extra words
		{"he-hub", labRelay, "::ffff:203.0.113.10", relay, hub},                                            // mapped IPv6
		{"he-hub", labRelay, labHub, netip.MustParseAddr("fd00::1"), hub},                                  // IPv6
		{"he-hub", labRelay, labHub, netip.MustParseAddr("10.77.0.26"), netip.MustParseAddr("10.77.0.26")}, // equal
	} {
		if u, err := TunnelUnit(bad.dev, bad.local, bad.remote, bad.addr, bad.peer); err == nil {
			t.Errorf("TunnelUnit(%q, %q, %q, %v, %v) accepted:\n%s", bad.dev, bad.local, bad.remote, bad.addr, bad.peer, u)
		}
	}
}

// Real `ip -4 -o` output shapes (a relay with DHCP, the Hub with policy routing and docker).
const (
	relayRoutes = `default via 198.51.100.1 dev eth0 table main proto dhcp src 198.51.100.42 metric 100
198.51.100.0/24 dev eth0 table main proto kernel scope link src 198.51.100.42 metric 100
169.254.169.0/24 dev eth0 table main proto dhcp scope link src 198.51.100.42 metric 100
local 127.0.0.0/8 dev lo table local proto kernel scope host src 127.0.0.1
local 198.51.100.42 dev eth0 table local proto kernel scope host src 198.51.100.42
broadcast 198.51.100.255 dev eth0 table local proto kernel scope link src 198.51.100.42`
	relayAddrs = `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
2: eth0    inet 198.51.100.42/24 metric 100 brd 198.51.100.255 scope global dynamic eth0\       valid_lft 86000sec preferred_lft 86000sec`
)

func TestTunnelOverlap(t *testing.T) {
	block, _ := RelayTunnelBlock(6)
	if err := TunnelOverlap(relayRoutes, relayAddrs, RelayTunnelDev, block); err != nil {
		t.Errorf("a plain relay: %v", err)
	}
	// A re-run: the tunnel's own routes and address are not a collision.
	own := relayRoutes + "\n" + labHubTun + " dev he-hub table main proto kernel scope link src " + labRelayTun +
		"\nlocal " + labRelayTun + " dev he-hub table local proto kernel scope host src " + labRelayTun
	ownAddr := relayAddrs + "\n5: he-hub    inet " + labRelayTun + " peer " + labHubTun + "/32 scope global he-hub\\       valid_lft forever preferred_lft forever"
	if err := TunnelOverlap(own, ownAddr, RelayTunnelDev, block); err != nil {
		t.Errorf("own tunnel: %v", err)
	}
	// The Hub with another relay's tunnel: a different block, no collision.
	other := "10.77.0.22 dev he-r5 table main proto kernel scope link src 10.77.0.21"
	otherAddr := "6: he-r5    inet 10.77.0.21 peer 10.77.0.22/32 scope global he-r5"
	if err := TunnelOverlap(other, otherAddr, HubTunnelDev(6), block); err != nil {
		t.Errorf("another relay's tunnel: %v", err)
	}
	for name, c := range map[string]struct{ routes, addrs, dev string }{
		"provider 10/8":       {relayRoutes + "\n10.0.0.0/8 via 198.51.100.1 dev eth0 table main", relayAddrs, RelayTunnelDev},
		"private network":     {relayRoutes, relayAddrs + "\n3: eth1    inet 10.77.0.25/24 brd 10.77.0.255 scope global eth1", RelayTunnelDev},
		"host route":          {relayRoutes + "\n10.77.0.26 via 198.51.100.1 dev eth0 table 5", relayAddrs, RelayTunnelDev},
		"blackhole":           {relayRoutes + "\nblackhole 10.77.0.0/16 table main", relayAddrs, RelayTunnelDev},
		"someone else's peer": {relayRoutes, relayAddrs + "\n7: wg0    inet 10.9.0.1 peer 10.77.0.25/32 scope global wg0", RelayTunnelDev},
		"another device name": {own, ownAddr, "he-other"},
		"unreadable route":    {relayRoutes + "\nfoo-bar dev eth0", relayAddrs, RelayTunnelDev},
	} {
		if err := TunnelOverlap(c.routes, c.addrs, c.dev, block); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Through the tunnel: DNAT, the accept of the replies and the masquerade name the Hub's tunnel
// address; the SYNs into the tunnel are clamped; the public Hub address stays only in the header.
func TestRelayNftRulesetVia(t *testing.T) {
	rs, err := RelayNftRulesetVia(labHub, labHubTun)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"forwards to the Hub " + labHub + " through the tunnel " + labHubTun + " (he-hub). Do not edit.\n",
		"    fib daddr type local tcp dport 2443 dnat to " + labHubTun + "\n",
		"    fib daddr type local udp dport 30000-65000 dnat to " + labHubTun + ":50443\n",
		"    " + RelayTunnelClampRule + "\n    ct status dnat ct reply ip saddr " + labHubTun + " accept\n    counter drop\n",
		"    ip daddr " + labHubTun + " ct status dnat masquerade\n",
	} {
		if !strings.Contains(rs, must) {
			t.Errorf("ruleset lacks %q:\n%s", must, rs)
		}
	}
	if countLines(rs, "dnat to ") != 2 || strings.Count(rs, "masquerade") != 1 || countLines(rs, labHub) != 1 {
		t.Errorf("DNAT/masquerade counts or the public address outside the header:\n%s", rs)
	}
	golden(t, "relay_nft_tunnel.golden", []byte(rs))
	nftCheck(t, rs)
	for _, bad := range []string{"203.0.113.10", "10.78.0.1", "10.77.0.25:50443", "x"} {
		if _, err := RelayNftRulesetVia(labHub, bad); err == nil {
			t.Errorf("tunnel target %q accepted", bad)
		}
	}
	plain, _ := RelayNftRuleset(labHub)
	if via, _ := RelayNftRulesetVia(labHub, ""); via != plain {
		t.Error(`RelayNftRulesetVia(hub, "") differs from RelayNftRuleset(hub)`)
	}
}

// Through the tunnel nginx tries the Hub's tunnel address first, then its public address as
// backup: RelayUpstreamTries servers per upstream in all, the tries and timeouts unchanged.
func TestRelayNginxConfVia(t *testing.T) {
	c, err := RelayNginxConfVia(labHub, labHubTun)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, "# Generated by heyearth (config.RelayNginxConf): reserve entry, TCP 443/80 to the Hub "+labHub+" through the tunnel "+labHubTun+". Do not edit.\n") {
		t.Errorf("header:\n%s", c)
	}
	for _, port := range []string{":8447", ":80"} {
		if n := strings.Count(c, "server "+labHubTun+port+" max_fails=0;\n"); n != RelayTunnelTries {
			t.Errorf("tunnel %s: %d copies, want %d", port, n, RelayTunnelTries)
		}
		if n := strings.Count(c, "server "+labHub+port+" max_fails=0 backup;\n"); n != RelayUpstreamTries-RelayTunnelTries {
			t.Errorf("backup %s: %d copies, want %d", port, n, RelayUpstreamTries-RelayTunnelTries)
		}
	}
	// No plain (non-backup) public server: the tunnel is always tried first.
	if strings.Contains(c, "server "+labHub+":8447 max_fails=0;") || strings.Contains(c, "server "+labHub+":80 max_fails=0;") {
		t.Errorf("a public server is primary:\n%s", c)
	}
	if strings.Count(c, "proxy_next_upstream_tries "+strconv.Itoa(RelayUpstreamTries)+";") != 2 || strings.Count(c, "proxy_protocol on;") != 1 {
		t.Errorf("tries or PROXY:\n%s", c)
	}
	golden(t, "relay_nginx_tunnel.golden", []byte(c))
	if _, err := RelayNginxConfVia(labHub, labHub); err == nil {
		t.Error("a public tunnel address accepted")
	}
	plain, _ := RelayNginxConf(labHub)
	if via, _ := RelayNginxConfVia(labHub, ""); via != plain {
		t.Error(`RelayNginxConfVia(hub, "") differs from RelayNginxConf(hub)`)
	}
}

func TestHubRelayTrustFileVia(t *testing.T) {
	f, err := HubRelayTrustFileVia(labRelay, labRelayTun)
	if err != nil || !strings.Contains(f, "set_real_ip_from "+labRelay+";\nset_real_ip_from "+labRelayTun+";\n") {
		t.Errorf("trust file %v:\n%s", err, f)
	}
	for _, bad := range []string{"192.168.1.5", "10.78.0.2", labRelay, "10.77.0.26;\nset_real_ip_from 0.0.0.0/0"} {
		if _, err := HubRelayTrustFileVia(labRelay, bad); err == nil {
			t.Errorf("tunnel address %q trusted", bad)
		}
	}
	plain, _ := HubRelayTrustFile(labRelay)
	if via, _ := HubRelayTrustFileVia(labRelay, ""); via != plain {
		t.Error("no tunnel: the file must stay as it was")
	}
}
