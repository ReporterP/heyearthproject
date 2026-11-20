package hubnet

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

// Roles of one Hub address as the wizard shows them.
const (
	RoleUsers    = "users"    // main_server_host: where users' Hub lines and the entries point
	RoleSource   = "source"   // the default route's source
	RoleGateway  = "gateway"  // its prefix holds the gateway: removing it breaks routing
	RoleNew      = "new"      // the address a change moves to
	RoleRetiring = "retiring" // the address a change moves away from
	RoleOther    = "other"
)

// AddrRole is one IPv4 address of the default-route interface with its roles.
type AddrRole struct {
	Addr   netip.Addr `json:"addr"`
	Prefix int        `json:"prefix"`
	Roles  []string   `json:"roles"`
}

// gatewayCarrier is the interface address whose prefix contains the gateway.
func gatewayCarrier(ifaceAddrs []netip.Prefix, gw netip.Addr) (netip.Addr, bool) {
	for _, p := range ifaceAddrs {
		if p.Bits() < 32 && p.Masked().Contains(gw) {
			return p.Addr(), true
		}
	}
	return netip.Addr{}, false
}

// Roles lists ifaceAddrs (IPv4 only) with their roles; newIP/oldIP are the active change's
// addresses ("" without one).
func Roles(ifaceAddrs []netip.Prefix, route DefaultRoute, mainHost, newIP, oldIP string) []AddrRole {
	gw, hasGW := gatewayCarrier(ifaceAddrs, route.Gateway)
	var out []AddrRole
	for _, p := range ifaceAddrs {
		a := p.Addr().Unmap()
		if !a.Is4() {
			continue
		}
		var rs []string
		if a.String() == strings.TrimSpace(mainHost) {
			rs = append(rs, RoleUsers)
		}
		if route.Src.IsValid() && a == route.Src.Unmap() {
			rs = append(rs, RoleSource)
		}
		if hasGW && a == gw {
			rs = append(rs, RoleGateway)
		}
		if a.String() == newIP {
			rs = append(rs, RoleNew)
		}
		if a.String() == oldIP {
			rs = append(rs, RoleRetiring)
		}
		if len(rs) == 0 {
			rs = []string{RoleOther}
		}
		out = append(out, AddrRole{Addr: a, Prefix: p.Bits(), Roles: rs})
	}
	return out
}

// IsGatewayCarrier says whether a carries the gateway on this interface.
func IsGatewayCarrier(ifaceAddrs []netip.Prefix, route DefaultRoute, a netip.Addr) bool {
	gw, ok := gatewayCarrier(ifaceAddrs, route.Gateway)
	return ok && gw == a
}

// ValidateNew checks the address the owner typed: a public IPv4, not a node's, not the current
// one, not the gateway carrier.
func ValidateNew(in string, hubAddrs []netip.Prefix, nodeIPs []string, route DefaultRoute, cur string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(in))
	if err != nil || !a.Unmap().Is4() {
		return netip.Addr{}, fmt.Errorf("%q — нужен адрес IPv4 вида 198.51.100.20", strings.TrimSpace(in))
	}
	a = a.Unmap()
	if a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() || !a.IsGlobalUnicast() {
		return netip.Addr{}, fmt.Errorf("%s — частный или служебный адрес, снаружи до него не достучаться", a)
	}
	for _, n := range nodeIPs {
		if x, err := netip.ParseAddr(strings.TrimSpace(n)); err == nil && x.Unmap() == a {
			return netip.Addr{}, fmt.Errorf("%s — адрес одного из серверов (выход или вход), не хаба", a)
		}
	}
	if a.String() == strings.TrimSpace(cur) {
		return netip.Addr{}, fmt.Errorf("%s — хаб уже работает на этом адресе", a)
	}
	if IsGatewayCarrier(hubAddrs, route, a) {
		return netip.Addr{}, fmt.Errorf("%s — через этот адрес идёт шлюз; переводить пользователей на него нельзя", a)
	}
	return a, nil
}

// ErrNoRoute is returned by callers when the Hub's default route cannot be read.
var ErrNoRoute = errors.New("маршрут по умолчанию хаба не прочитан")
