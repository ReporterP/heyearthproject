// Package biz — Happ routing-profile helper.
//
// Per https://www.happ.su/main/dev-docs/routing.md, a subscription can ship a
// routing profile via the special deeplink `happ://routing/onadd/<base64-json>`
// embedded in the subscription body. Happ parses this on subscription import
// and applies the routing rules.
//
// We use this to add a DirectIp + DirectSites exception list — when a user has
// VPN enabled, the listed IPs and domains bypass the VPN tunnel.
// Two sources of direct entries:
//   - MTProto-proxy exit IPs (so Telegram → MTProto bypasses the VPN loop)
//   - Per-plan direct-route services (Russian banks, госуслуги, Yandex/VK)
package biz

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// collapseToETLDPlusOne reduces a list of fully-qualified domains down to
// their unique effective-TLD+1 (registrable domain). Example:
//
//	mail.yandex.ru, lxc.lavka.yandex.net, 3ds.bank.yandex.ru
//
// becomes:
//
//	yandex.ru, yandex.net
//
// Why: Xray-syntax `domain:yandex.ru` already matches every subdomain
// (mail.yandex.ru, bank.yandex.ru, ...). Emitting 3 413 FQDN entries for
// the Yandex service is O(N) busy-work on every Happ connection — the
// list explodes the subscription body, balloons client memory, and slows
// route decisions in the hot path. eTLD+1 dedup shrinks 20 149 → ~100.
func collapseToETLDPlusOne(domains []string) []string {
	seen := make(map[string]struct{}, len(domains))
	out := make([]string, 0, 256)
	for _, d := range domains {
		d = strings.TrimSpace(strings.ToLower(d))
		if d == "" {
			continue
		}
		// publicsuffix handles co.uk / com.ua / .рф correctly.
		base, err := publicsuffix.EffectiveTLDPlusOne(d)
		if err != nil || base == "" {
			// Fall back to the domain as-is (rare — IDN or oddly-formed).
			base = d
		}
		if _, dup := seen[base]; dup {
			continue
		}
		seen[base] = struct{}{}
		out = append(out, base)
	}
	return out
}

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

// normalizeIPToCIDR превращает голый IP в CIDR (/32 для IPv4, /128 для IPv6).
// Если строка уже содержит "/" — возвращается как есть.
func normalizeIPToCIDR(ip string) string {
	if strings.Contains(ip, "/") {
		return ip
	}
	if strings.Contains(ip, ":") {
		return ip + "/128"
	}
	return ip + "/32"
}

// buildHappDeeplinkFromProfile сериализует profile → JSON → base64 → deeplink.
// Возвращает "" если profile пустой (нет DirectIp И DirectSites).
func buildHappDeeplinkFromProfile(profile happRoutingProfile) string {
	if len(profile.DirectIp) == 0 && len(profile.DirectSites) == 0 {
		return ""
	}
	js, err := json.Marshal(profile)
	if err != nil {
		log.Printf("[happ-routing] marshal error: %v", err)
		return ""
	}
	return "happ://routing/onadd/" + base64.StdEncoding.EncodeToString(js)
}

// BuildHappRoutingDeeplinkForPlan собирает Happ routing-профиль под конкретный план.
//
// В DirectIp попадают:
//   - MTProto exit-IP пользователя (передаются как голые IP, /32 добавляется внутри)
//   - IP4/IP6/CIDR4/CIDR6 всех активных direct-route сервисов плана
//
// В DirectSites попадают домены этих же сервисов, каждый с префиксом "domain:"
// (Xray-синтаксис, который Happ принимает — матчит сам домен и все поддомены).
//
// DnsHosts оставлен пустым: dns_servers сервисов хранятся в БД, но в Happ-профиль
// не пушатся (out of scope согласно PRD).
//
// Возвращает "" если для плана нет ни MTProto-IP, ни direct-сервисов.
//
// Если biz.GetDirectRouteServicesForPlan вернёт ошибку — логируем и обрабатываем
// как «нет сервисов» (никогда не валим subscription endpoint из-за direct-routes).
func BuildHappRoutingDeeplinkForPlan(planID int, mtprotoIPs []string) string {
	directIPs := make([]string, 0, len(mtprotoIPs))
	for _, ip := range mtprotoIPs {
		if ip == "" {
			continue
		}
		directIPs = append(directIPs, normalizeIPToCIDR(ip))
	}

	// Accumulate raw FQDNs across all services, then collapse to eTLD+1
	// at the end. Pre-collapse a yandex.ru service can ship 3 400+ FQDNs
	// that Xray's `domain:yandex.ru` rule already covers transitively.
	rawDomains := make([]string, 0, 1024)
	// Dedup IPs/CIDRs while accumulating — multiple services often list
	// overlapping infra (CDN edges shared between gov + telecom services).
	seenIP := make(map[string]struct{}, 512)
	addIP := func(s string) {
		if s == "" {
			return
		}
		cidr := normalizeIPToCIDR(s)
		if _, dup := seenIP[cidr]; dup {
			return
		}
		seenIP[cidr] = struct{}{}
		directIPs = append(directIPs, cidr)
	}

	if planID > 0 {
		services, err := GetDirectRouteServicesForPlan(planID)
		if err != nil {
			log.Printf("[happ-routing] GetDirectRouteServicesForPlan(plan=%d) error: %v", planID, err)
		} else {
			for _, svc := range services {
				if !svc.IsActive {
					continue
				}
				rawDomains = append(rawDomains, svc.Domains...)
				for _, ip := range svc.IP4 {
					addIP(ip)
				}
				for _, ip := range svc.IP6 {
					addIP(ip)
				}
				for _, c := range svc.CIDR4 {
					addIP(c)
				}
				for _, c := range svc.CIDR6 {
					addIP(c)
				}
			}
		}
	}

	// Collapse all collected FQDNs to unique eTLD+1 — the Xray "domain:"
	// prefix matches all subdomains, so emitting `domain:yandex.ru` is
	// equivalent to listing every yandex.ru / yandex.net FQDN explicitly.
	directSites := []string{}
	for _, base := range collapseToETLDPlusOne(rawDomains) {
		directSites = append(directSites, "domain:"+base)
	}

	// The MTProto fronting domain must be direct too, not just the exit IP.
	// A TLS-sniffing client rewrites the destination of the fake-TLS flow from
	// <exit-ip>:<port> to <fronting-domain>:<port>, after which the DirectIp
	// /32 entry can no longer match — so without this the Telegram-proxy flow
	// is pushed into the VPN tunnel and reaches the node the long way round
	// (client → Hub → tunnel → exit → back to itself), or not at all.
	if fronting := MTProtoFrontingDomain(); fronting != "" {
		directSites = append(directSites, "domain:"+fronting)
	}

	// Prepend `geoip:ru` shortcut — Happ's trie-lookup against the bundled
	// geoip.dat catches every RU IP in O(log) vs the linear scan over the
	// individual /32 list below. We still keep the explicit IPs as a safety
	// net (non-RU infra that we want direct, e.g. CDN edges, MTProto).
	directIPs = append([]string{"geoip:ru"}, directIPs...)

	return buildHappDeeplinkFromProfile(happRoutingProfile{
		Name:        "HeYearth Smart Routing",
		GlobalProxy: "true",
		// DNS — explicit DoH to avoid leaks/slow public resolvers. Happ uses
		// DomesticDns for hosts matched as direct (DirectSites/DirectIp) and
		// RemoteDns for everything going through the proxy.
		RemoteDns:   "https://1.1.1.1/dns-query",
		DomesticDns: "https://77.88.8.8/dns-query",
		// Geoipurl points at our self-hosted geoip.dat (refreshed monthly by
		// the hysteria geoip cron — same file Hysteria2 ACL uses). Lets us
		// use `geoip:ru` / `geoip:cn` shortcuts in DirectIp / ProxyIp
		// instead of shipping tens of thousands of individual addresses.
		Geoipurl:       "https://hiearth.ru/static/geoip.dat",
		DnsHosts:       map[string]string{},
		DirectSites:    directSites,
		DirectIp:       directIPs,
		ProxySites:     []string{},
		ProxyIp:        []string{},
		BlockSites:     []string{},
		BlockIp:        []string{},
		DomainStrategy: "AsIs",
	})
}

// BuildHappRoutingDeeplink — backwards-compat обёртка. Принимает уже отформатированные
// CIDR (как делал старый MTProto-only callsite). Новый код должен использовать
// BuildHappRoutingDeeplinkForPlan.
func BuildHappRoutingDeeplink(directIPs []string) string {
	if len(directIPs) == 0 {
		return ""
	}
	return buildHappDeeplinkFromProfile(happRoutingProfile{
		Name:           "HeYearth Smart Routing",
		GlobalProxy:    "true",
		RemoteDns:      "https://1.1.1.1/dns-query",
		DomesticDns:    "https://77.88.8.8/dns-query",
		Geoipurl:       "https://hiearth.ru/static/geoip.dat",
		DnsHosts:       map[string]string{},
		DirectSites:    []string{},
		DirectIp:       directIPs,
		ProxySites:     []string{},
		ProxyIp:        []string{},
		BlockSites:     []string{},
		BlockIp:        []string{},
		DomainStrategy: "AsIs",
	})
}
