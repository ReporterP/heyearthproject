// Package biz — Happ routing-profile helper.
//
// Per https://www.happ.su/main/dev-docs/routing.md, a subscription can ship a
// routing profile via the special deeplink `happ://routing/onadd/<base64-json>`
// embedded in the subscription body. Happ parses this on subscription import
// and applies the routing rules.
//
// We use this to add a DirectIp exception for our MTProto-proxy IPs — so that
// when a user has BOTH VPN (Happ) and MTProto-proxy (Telegram) enabled, the
// Telegram → MTProto-proxy connection bypasses the VPN tunnel (avoiding the
// "traffic loops back to the same exit" problem that breaks MTProto).
package biz

import (
	"encoding/base64"
	"encoding/json"
)

// happRoutingProfile is the JSON shape Happ accepts. Field names match Happ's
// `happ://routing/onadd/<json>` example verbatim (PascalCase). "GlobalProxy"
// is a STRING "true"/"false" per Happ's example, NOT a bool.
type happRoutingProfile struct {
	Name           string            `json:"Name"`
	GlobalProxy    string            `json:"GlobalProxy"`
	RemoteDns      string            `json:"RemoteDns"`
	DomesticDns    string            `json:"DomesticDns"`
	Geoipurl       string            `json:"Geoipurl"`
	Geositeurl     string            `json:"Geositeurl"`
	DnsHosts       map[string]string `json:"DnsHosts"`
	DirectSites    []string          `json:"DirectSites"`
	DirectIp       []string          `json:"DirectIp"`
	ProxySites     []string          `json:"ProxySites"`
	ProxyIp        []string          `json:"ProxyIp"`
	BlockSites     []string          `json:"BlockSites"`
	BlockIp        []string          `json:"BlockIp"`
	DomainStrategy string            `json:"DomainStrategy"`
}

// BuildHappRoutingDeeplink generates the `happ://routing/onadd/<b64>` URL that
// — when included in a subscription body — installs a routing profile in Happ.
//
// directIPs is the list of CIDRs (or bare IPs) to exclude from VPN routing.
// Empty list returns "" (no routing deeplink emitted).
//
// The resulting profile:
//   • GlobalProxy = "true" → all traffic through VPN by default
//   • DirectIp    = directIPs → these IPs bypass VPN
//   • Everything else fields empty / defaults
func BuildHappRoutingDeeplink(directIPs []string) string {
	if len(directIPs) == 0 {
		return ""
	}

	profile := happRoutingProfile{
		Name:           "HeYearth Smart Routing",
		GlobalProxy:    "true",
		RemoteDns:      "",
		DomesticDns:    "",
		Geoipurl:       "",
		Geositeurl:     "",
		DnsHosts:       map[string]string{},
		DirectSites:    []string{},
		DirectIp:       directIPs,
		ProxySites:     []string{},
		ProxyIp:        []string{},
		BlockSites:     []string{},
		BlockIp:        []string{},
		DomainStrategy: "AsIs",
	}

	js, err := json.Marshal(profile)
	if err != nil {
		return ""
	}
	return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(js)
}
