package config

import (
	"net"
	"strings"
)

// exitHysteriaACLRejects are refused by an exit's hysteria server before direct(all), in this
// order: loopback, "this host", RFC1918, shared address space (CGNAT), IPv4 link-local, IPv6
// loopback/unique-local/link-local. The exit's own public address is appended after them.
var exitHysteriaACLRejects = []string{
	"127.0.0.0/8",
	"::1/128",
	"0.0.0.0/8",
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",
	"169.254.0.0/16",
	"fc00::/7",
	"fe80::/10",
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
// Output (exitIP 203.0.113.5):
//
//	acl:
//	  inline:
//	    - reject(127.0.0.0/8)
//	    ...
//	    - reject(fe80::/10)
//	    - reject(203.0.113.5)
//	    - direct(all)
//
// exitIP ends up in a root shell heredoc, so it is used only when it parses as an IP address
// and is re-rendered by net.IP.String(); otherwise the own-address rule is left out (callers
// should validate exitIP first) and the range rejects still apply. The result never contains
// shell-expandable characters, so it is safe in quoted and unquoted heredocs alike.
func ExitHysteriaACL(exitIP string) string {
	var sb strings.Builder
	sb.WriteString("acl:\n  inline:\n")
	for _, cidr := range exitHysteriaACLRejects {
		sb.WriteString("    - reject(" + cidr + ")\n")
	}
	if ip := net.ParseIP(strings.TrimSpace(exitIP)); ip != nil {
		sb.WriteString("    - reject(" + ip.String() + ")\n")
	}
	sb.WriteString("    - direct(all)\n")
	return sb.String()
}
