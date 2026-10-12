package config

import (
	"net"
	"strconv"
	"strings"
)

// DefaultMTProtoPort is the default of app_settings.mtproto_port: the TCP port of the MTProxy
// (Telegram) daemon that cmd/deploy/install-mtproxy.sh installs on every exit, the same on all
// exits.
const DefaultMTProtoPort = 8443

// ParseMTProtoPort parses an app_settings.mtproto_port value; empty or invalid gives
// DefaultMTProtoPort.
func ParseMTProtoPort(v string) int {
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || p < 1 || p > 65535 {
		return DefaultMTProtoPort
	}
	return p
}

// exitBlockedRanges are refused as tunnel targets on an exit, in this order: loopback, "this
// host" (v4 and v6), RFC1918, shared address space (CGNAT), IPv4 link-local, IPv6 unique-local,
// IPv6 link-local. The exit's own public address is handled separately (with its allowances).
var exitBlockedRanges = []string{
	"127.0.0.0/8",
	"::1/128",
	"0.0.0.0/8",
	"::/128",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"fc00::/7",
	"fe80::/10",
}

// ExitBlockedRanges returns a copy of the ranges an exit refuses as tunnel targets (used by the
// hysteria ACL and the exit's xray routing).
func ExitBlockedRanges() []string { return append([]string(nil), exitBlockedRanges...) }

// canonicalIP canonicalises an exit address; "" when it is not an IP address.
func canonicalIP(s string) string {
	if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
		return ip.String()
	}
	return ""
}

// validPorts keeps the TCP ports in 1..65535, in order, without duplicates.
func validPorts(ports []int) []int {
	var out []int
	seen := map[int]bool{}
	for _, p := range ports {
		if p >= 1 && p <= 65535 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// ExitHysteriaACL renders the acl block appended to an exit node's
// /opt/heyearth/hysteria/server.yaml. It is the single definition shared by every exit
// provisioning path (cmd/deploy --setup-exit-node and the admin panel's exit setup step 3).
//
// The exit's hysteria server is the egress of the Hub→Exit tunnel: whatever a Hub-side client
// asks for, the exit connects to directly. Without an ACL that includes the exit's own
// loopback services (node-agent, xray inbounds, sshd), its private-network neighbours and its
// own public address, all reached from the inside, past the exit's firewall. Hysteria matches
// ACL rules against the resolved IP too, so a domain that resolves into one of these ranges is
// rejected as well.
//
// allowTCPPorts are services on the exit's own address that tunnel users must still reach — the
// MTProxy (app_settings.mtproto_port): a full-tunnel client (AWG AllowedIPs 0.0.0.0/0) sends
// <exitIP>:<mtproto port> into the tunnel, and the exit must deliver it to its own mtg.
//
// Output (exitIP 203.0.113.5, allowTCPPorts 8443):
//
//	acl:
//	  inline:
//	    - reject(127.0.0.0/8)
//	    ...
//	    - reject(fe80::/10)
//	    - direct(203.0.113.5, tcp/8443)
//	    - reject(203.0.113.5)
//	    - direct(all)
//
// exitIP ends up in a root shell heredoc, so it is used only when it parses as an IP address
// and is re-rendered by net.IP.String(); otherwise the own-address rules are left out (callers
// should validate exitIP first) and the range rejects still apply. The result never contains
// shell-expandable characters, so it is safe in quoted and unquoted heredocs alike.
func ExitHysteriaACL(exitIP string, allowTCPPorts ...int) string {
	var sb strings.Builder
	sb.WriteString("acl:\n  inline:\n")
	for _, cidr := range exitBlockedRanges {
		sb.WriteString("    - reject(" + cidr + ")\n")
	}
	if ip := canonicalIP(exitIP); ip != "" {
		for _, p := range validPorts(allowTCPPorts) {
			sb.WriteString("    - direct(" + ip + ", tcp/" + strconv.Itoa(p) + ")\n")
		}
		sb.WriteString("    - reject(" + ip + ")\n")
	}
	sb.WriteString("    - direct(all)\n")
	return sb.String()
}

// ExitXrayGuardRules renders the leading routing rules of an exit's xray config (JSON array
// elements, comma-separated, no trailing comma): the same refusals as ExitHysteriaACL for the
// SS / VLESS resilience tunnels — the exit's own TCP allowances go out "direct" first, then the
// blocked ranges and the exit's own address go to "block". directTag / blockTag are the
// outbound tags of the exit config.
func ExitXrayGuardRules(exitIP, directTag, blockTag string, allowTCPPorts ...int) string {
	var rules []string
	ip := canonicalIP(exitIP)
	if ip != "" {
		if ports := validPorts(allowTCPPorts); len(ports) > 0 {
			ps := make([]string, len(ports))
			for i, p := range ports {
				ps[i] = strconv.Itoa(p)
			}
			rules = append(rules, `{"type": "field", "ip": ["`+ip+`"], "network": "tcp", "port": "`+strings.Join(ps, ",")+`", "outboundTag": "`+directTag+`"}`)
		}
	}
	blocked := append([]string{"geoip:private"}, exitBlockedRanges...)
	if ip != "" {
		blocked = append(blocked, ip)
	}
	rules = append(rules, `{"type": "field", "ip": ["`+strings.Join(blocked, `", "`)+`"], "outboundTag": "`+blockTag+`"}`)
	return strings.Join(rules, ",\n")
}
