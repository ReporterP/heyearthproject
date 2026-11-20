package config

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Reserve entry in RU ("relay", spec 2026-10-12 «Пул нод», section 4): a VPS of another provider
// that forwards the Hub's user ports to the Hub with nftables DNAT + masquerade. No apps and no
// user data on it; the Hub sees the relay's address as the client. One set of templates for the
// admin wizard and the CLI (internal/provision RelayRecipe writes them).
//
// What is forwarded by nft, and nothing else (TCP 443 and 80 go through the relay's nginx stream
// with the client's address in a PROXY header instead: relay_stream.go):
//
//	TCP 2443 (RelayDNATTCPPorts)           -> the same port of the Hub (XHTTP «Дуб», which checks
//	                                          the Host header, not the address)
//	UDP 30000-65000 (50443 «Бамбук» incl.) -> Hub:50443
//
// Only packets addressed to the relay itself (fib daddr type local) are rewritten, so SSH and any
// other local port stay local and transit traffic is never redirected. The forward chain accepts
// only flows our DNAT sent to the Hub (their replies and related ICMP included) and drops every
// other forwarded packet: with ip_forward on the relay would otherwise route for anyone who points
// a route at it — it is not a router and never an open proxy. ip_forward lives with the table: the
// unit turns it on only after the rules loaded and off when it stops (RelayUnit), never in sysctl.d
// (RelaySysctl), so no boot window, failed load or stopped unit leaves a relay routing unfiltered.
// The masquerade covers the same DNAT flows only, so the Hub's replies come back through the relay.
//
// The ruleset lives in its own table, RelayNftTable, in RelayNftPath: the file creates, deletes
// and defines the table in one nft transaction (idempotent, no window without forwarding, no
// duplicates on a re-run), like the exit guards (exit_xray_guard.go, exit_agent_guard.go). The
// oneshot RelayUnitName loads it at boot and again after `systemctl restart nftables` (PartOf=)
// or `systemctl reload nftables` (ReloadPropagatedFrom= + ExecReload=): both start with
// `flush ruleset`, which would drop the table.
const (
	RelayNftTable   = "heyearth_relay"
	RelayUnitName   = "heyearth-relay.service"
	RelayNftPath    = "/etc/heyearth/relay.nft"
	RelayHopMin     = 30000
	RelayHopMax     = 65000
	RelayBambooPort = 50443
)

// RelayTCPPorts: every TCP port a relay takes for users — 80 and 443 (its nginx stream,
// RelayStreamTCPPorts) and 2443 (nft DNAT, RelayDNATTCPPorts). They must be free on a new relay
// and can never be its SSH port.
var RelayTCPPorts = []int{80, 443, 2443}

// RelayDNATTCPPorts go to the same port of the Hub by nft DNAT: 2443 (XHTTP «Дуб»).
var RelayDNATTCPPorts = []int{2443}

// RelayDNATTCPMatch is the port match of the TCP DNAT rule exactly as `nft list` prints it: a set
// of one element is printed as the bare value ("2443"), not "{ 2443 }" — the relay recipe compares
// the live rule with this text (13.11: a relay install rolled back because of the braces).
func RelayDNATTCPMatch() string {
	ps := make([]string, len(RelayDNATTCPPorts))
	for i, p := range RelayDNATTCPPorts {
		ps[i] = strconv.Itoa(p)
	}
	if len(ps) == 1 {
		return ps[0]
	}
	return "{ " + strings.Join(ps, ", ") + " }"
}

// relayNonPublicV4 are IPv4 ranges a Hub address can never be in, beyond what netip reports as
// private, loopback, link-local, multicast or unspecified: the exit's refused ranges
// (exitBlockedRanges: "this host" 0/8, CGNAT 100.64/10, …) and the reserved 240/4 with the
// limited broadcast.
var relayNonPublicV4 = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range append(ExitBlockedRanges(), "240.0.0.0/4") {
		if p := netip.MustParsePrefix(s); p.Addr().Is4() {
			out = append(out, p)
		}
	}
	return out
}()

// RelayHubIP validates the DNAT target: a public IPv4 literal (the table is family ip), no names,
// ports, prefixes or IPv4-mapped IPv6. The caller passes the Hub address its replies come from
// (main_server_host — 203.0.113.225 in production), never a filtered secondary address: Bamboo
// replies leave the Hub from its route source, and the relay's conntrack only matches replies
// from the address it sent to.
func RelayHubIP(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !a.Is4() {
		return netip.Addr{}, fmt.Errorf("адрес Hub %q: нужен IPv4-адрес", s)
	}
	public := !(a.IsPrivate() || a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast() || a.IsMulticast())
	for _, p := range relayNonPublicV4 {
		public = public && !p.Contains(a)
	}
	if !public {
		return netip.Addr{}, fmt.Errorf("адрес Hub %s не публичный", a)
	}
	return a, nil
}

// RelayNftRuleset renders RelayNftPath for the Hub hubIP (RelayHubIP). Its first statements make
// `nft -f` replace the table in one transaction, also when it does not exist yet.
//
// UDP: the whole hop range (50443 included) goes to Hub:50443 itself. Keeping the client's hop
// port would let two flows that the relay's masquerade left on one source port collide in the
// Hub's own hop DNAT (identical reply tuples → the Hub drops the second flow). With :50443 the
// relay's masquerade allocates unique source ports; the Hub's rule (30000:65000 → :50443, dst
// LOCAL — cmd/deploy hysteria2PortHopRule) still applies and is the identity. Proven by
// scripts/relay_netns_lab.sh (Task 3.3), which loads testdata/relay_nft_lab.golden.
func RelayNftRuleset(hubIP string) (string, error) { return RelayNftRulesetVia(hubIP, "") }

// RelayNftRulesetVia renders the rules that forward through the GRE tunnel to the Hub's tunnel
// address hubTun (relay_tunnel.go; "" = straight to hubIP, the layout before the tunnel): the
// DNAT targets, the accept of the replies and the masquerade name hubTun, so the masquerade
// takes the address of the tunnel device and the Hub answers into the tunnel. The SYNs going into
// the tunnel get their MSS clamped to its MTU (rt mtu): the client advertises 1460, the tunnel
// carries 1436.
func RelayNftRulesetVia(hubIP, hubTun string) (string, error) {
	hub, err := RelayHubIP(hubIP)
	if err != nil {
		return "", err
	}
	h, via := hub.String(), ""
	if hubTun != "" {
		t, err := relayTunnelTarget(hubTun)
		if err != nil {
			return "", err
		}
		h, via = t.String(), " through the tunnel "+t.String()+" ("+RelayTunnelDev+")"
	}
	var b strings.Builder
	b.WriteString("# Generated by heyearth (config.RelayNftRuleset): reserve entry, forwards to the Hub " + hub.String() + via + ". Do not edit.\n")
	b.WriteString("table ip " + RelayNftTable + "\ndelete table ip " + RelayNftTable + "\ntable ip " + RelayNftTable + " {\n")
	b.WriteString("  chain prerouting {\n    type nat hook prerouting priority dstnat; policy accept;\n")
	b.WriteString("    fib daddr type local tcp dport " + RelayDNATTCPMatch() + " dnat to " + h + "\n")
	fmt.Fprintf(&b, "    fib daddr type local udp dport %d-%d dnat to %s:%d\n", RelayHopMin, RelayHopMax, h, RelayBambooPort)
	b.WriteString("  }\n")
	b.WriteString("  chain forward {\n    type filter hook forward priority filter; policy accept;\n")
	if via != "" {
		b.WriteString("    " + RelayTunnelClampRule + "\n")
	}
	b.WriteString("    ct status dnat ct reply ip saddr " + h + " accept\n")
	b.WriteString("    counter drop\n  }\n")
	b.WriteString("  chain postrouting {\n    type nat hook postrouting priority srcnat; policy accept;\n")
	b.WriteString("    ip daddr " + h + " ct status dnat masquerade\n  }\n}\n")
	return b.String(), nil
}

// RelayTunnelClampRule clamps the MSS of the SYNs that leave through the tunnel. The live check
// looks for RelayTunnelClampMark only: nft versions print the flag match differently.
const (
	RelayTunnelClampRule = `oifname "` + RelayTunnelDev + `" tcp flags syn / syn,rst tcp option maxseg size set rt mtu`
	RelayTunnelClampMark = "maxseg size set rt mtu"
)

// RelayUnit is the boot unit of the forwarding (/etc/systemd/system/RelayUnitName) for the rule
// file nftPath (an absolute path without spaces; RelayNftPath on a relay). Ordered after
// nftables.service and ufw.service, so their flushes land before our load; a restart of nftables
// re-runs it (PartOf=), a reload re-applies the file (ReloadPropagatedFrom= + ExecReload=: PartOf=
// does not propagate reloads). reload = the same atomic replace; stop turns ip_forward off and only
// then removes the table — "-" on both because a table someone already flushed must not fail the
// stop half of a restart.
//
// ip_forward follows the forward filter: ExecStartPost turns it on only after `nft -f` loaded the
// table (a failed load never runs it); the first ExecStop turns it off BEFORE the second drops the
// table (systemd runs ExecStop lines in order) — the other order leaves a window in which the relay
// routes transit without its filter, on every stop, deprovision and nftables restart;
// ExecStopPost turns it off again after a failed start (ExecStop does not run then). Nothing else
// sets it (RelaySysctl does not), so the relay never routes without the filter. Callers:
// "forwarding is on" is the unit's state — the relay recipe (Task 3.4) checks
// `sysctl -n net.ipv4.ip_forward` = 1 in its unit step, not in the sysctl step.
func RelayUnit(nftPath string) string {
	ports := make([]string, len(RelayDNATTCPPorts))
	for i, p := range RelayDNATTCPPorts {
		ports[i] = strconv.Itoa(p)
	}
	return fmt.Sprintf(`[Unit]
Description=HiEarth reserve entry: forward TCP %[1]s and UDP %[2]d-%[3]d to the Hub
After=network-pre.target nftables.service ufw.service
PartOf=nftables.service
ReloadPropagatedFrom=nftables.service
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f %[4]s
ExecStartPost=/usr/sbin/sysctl -q -w net.ipv4.ip_forward=1
ExecReload=/usr/sbin/nft -f %[4]s
ExecStop=-/usr/sbin/sysctl -q -w net.ipv4.ip_forward=0
ExecStop=-/usr/sbin/nft delete table ip %[5]s
ExecStopPost=-/usr/sbin/sysctl -q -w net.ipv4.ip_forward=0
[Install]
WantedBy=multi-user.target
`, strings.Join(ports, "/"), RelayHopMin, RelayHopMax, nftPath, RelayNftTable)
}

// RelaySysctl is the relay's sysctl.d file: no ICMP redirects (in and out on one NIC); no IPv6
// forwarding (the table is IPv4 only); conntrack sized for NAT of user traffic — 262144 entries,
// established TCP kept 2 h 4 min (RFC 5382 minimum) instead of 5 days that would fill the table,
// Bamboo's UDP flows (a new one on every port hop) expire after 60 s unanswered / 180 s answered.
// The nf_conntrack keys need the module loaded first (the relay recipe loads it and lists it in
// modules-load.d). Deliberately without net.ipv4.ip_forward: RelayUnit owns it (on only while the
// forward filter is loaded), so applying this file never turns forwarding on — nor off under a
// running unit.
func RelaySysctl() string {
	return `# Generated by heyearth (config.RelaySysctl): reserve entry conntrack; ip_forward is set by ` + RelayUnitName + `. Do not edit.
net.ipv4.conf.all.send_redirects = 0
net.ipv4.conf.default.send_redirects = 0
net.ipv6.conf.all.forwarding = 0
net.netfilter.nf_conntrack_max = 262144
net.netfilter.nf_conntrack_tcp_timeout_established = 7440
net.netfilter.nf_conntrack_udp_timeout = 60
net.netfilter.nf_conntrack_udp_timeout_stream = 180
`
}

// RelayJail is fail2ban's sshd jail for the relay's real SSH port (systemd journal backend; bans
// with nftables, which the relay always has, unlike iptables on a fresh Debian). The Hub, which
// provisions and checks the relay over SSH, is never banned: hubIP is written to ignoreip only when
// it is a public IPv4 (RelayHubIP), so a forged value never reaches the file; an SSH port out of
// 1..65535 falls back to the service name ssh (22).
func RelayJail(sshPort int, hubIP string) string {
	port := "ssh"
	if sshPort >= 1 && sshPort <= 65535 {
		port = strconv.Itoa(sshPort)
	}
	ignore := "127.0.0.1/8 ::1"
	if a, err := RelayHubIP(hubIP); err == nil {
		ignore += " " + a.String()
	}
	return `# Generated by heyearth (config.RelayJail): reserve entry SSH. Do not edit.
[sshd]
enabled = true
port = ` + port + `
backend = systemd
banaction = nftables-multiport
maxretry = 5
findtime = 10m
bantime = 1h
ignoreip = ` + ignore + `
`
}
