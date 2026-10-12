package main

import (
	"context"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// The Hub's own public addresses.
//
// Anything a proxy user can ask the Hub to connect to is a potential door into the Hub itself:
// the Hysteria2 server and xray run on the Hub, so a request for 127.0.0.1 or for one of the
// Hub's own addresses reaches sshd, PostgreSQL, the API (:8000) and the xray API (:10085) from
// the inside, past the firewall and with the Hub's identity. Both generated configs therefore
// refuse private ranges, loopback and these addresses BEFORE any direct(...) / direct-freedom
// rule (hysteria2_sync.go: hysteria2ACLRules, xray_sync.go: baseSecurityRules).
//
// Ports 80 and 443 of the Hub's public addresses stay reachable (through the exit, never
// directly): the site, the API behind nginx and the subscription endpoint live there, and a
// client app talks to them while the tunnel is up. Everything else on those addresses is refused.
const (
	hubOpenPortHTTP  = 80
	hubOpenPortHTTPS = 443
	// xrayHubBlockedPorts is every port except 80 and 443, in xray's port-list syntax.
	xrayHubBlockedPorts = "1-79,81-442,444-65535"
)

// hubIPCache remembers the Hub's addresses once they have been found. An empty result is
// NEVER cached: the first sync may run before the network is up or before main_server_host is
// readable, and a Hub that "has no addresses" for the rest of the process lifetime would leave
// the generated configs without their Hub-address blocks until the next restart. Once a
// non-empty list exists it is kept, so the configs stay byte-stable (their no-op short-circuits
// compare bytes; a flapping address list would restart xray / hysteria); a changed address needs
// an API restart, which a deploy does anyway.
type hubIPCache struct {
	mu  sync.Mutex
	ips []string
}

func (c *hubIPCache) get(compute func() []string, warn func(string)) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ips) > 0 {
		return c.ips
	}
	ips := compute()
	if len(ips) == 0 {
		warn("no public address of the Hub could be determined — the Hub-address block is OMITTED from the generated " +
			"hysteria2 and xray configs this time (the kernel egress guard still applies); retrying on the next sync")
		return nil
	}
	c.ips = ips
	return ips
}

var (
	hubIPs     hubIPCache
	hubIPsWarn logGate // at most one warning an hour: the syncs run every few minutes
)

// hubOwnIPs returns the public addresses of this host: every global unicast, non-private
// address of its interfaces plus whatever main_server_host / hub_main_server_host point to
// (a NATed Hub's public address is not on any interface). See hubIPCache for the caching rules.
// An empty list means "unknown": callers omit the Hub-address rule (deterministically) and the
// warning above is logged.
func hubOwnIPs() []string {
	return hubIPs.get(func() []string {
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			log.Printf("[hub-addrs] net.InterfaceAddrs: %v", err)
		}
		hosts := []string{
			biz.GetSettingOrDefault("main_server_host", ""),
			biz.GetSettingOrDefault("hub_main_server_host", ""),
		}
		return collectHubIPs(addrs, hosts, lookupIPsTimeout)
	}, func(msg string) {
		if hubIPsWarn.allow(time.Now(), time.Hour) {
			log.Printf("[hub-addrs] WARN: %s", msg)
		}
	})
}

func lookupIPsTimeout(host string) ([]net.IP, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// isPublicUnicast: an address the Hub is reachable on from outside. Private, loopback and
// link-local ranges are refused as a class (geoip:private, 127.0.0.0/8, ::1/128) and need no
// entry of their own.
func isPublicUnicast(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate()
}

// collectHubIPs is the pure part of hubOwnIPs: interface addresses plus hosts (IP literals or
// names resolved through lookup; a failed lookup is skipped), sorted and de-duplicated.
func collectHubIPs(ifaceAddrs []net.Addr, hosts []string, lookup func(string) ([]net.IP, error)) []string {
	set := map[string]struct{}{}
	add := func(ip net.IP) {
		if isPublicUnicast(ip) {
			set[ip.String()] = struct{}{}
		}
	}
	for _, a := range ifaceAddrs {
		switch v := a.(type) {
		case *net.IPNet:
			add(v.IP)
		case *net.IPAddr:
			add(v.IP)
		}
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			add(ip)
			continue
		}
		if ips, err := lookup(h); err == nil {
			for _, ip := range ips {
				add(ip)
			}
		}
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
