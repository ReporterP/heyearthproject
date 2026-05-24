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
//   • MTProto exit-IP пользователя (передаются как голые IP, /32 добавляется внутри)
//   • IP4/IP6/CIDR4/CIDR6 всех активных direct-route сервисов плана
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

	directSites := []string{}

	if planID > 0 {
		services, err := GetDirectRouteServicesForPlan(planID)
		if err != nil {
			log.Printf("[happ-routing] GetDirectRouteServicesForPlan(plan=%d) error: %v", planID, err)
		} else {
			for _, svc := range services {
				if !svc.IsActive {
					continue
				}
				for _, d := range svc.Domains {
					if d != "" {
						directSites = append(directSites, "domain:"+d)
					}
				}
				for _, ip := range svc.IP4 {
					if ip != "" {
						directIPs = append(directIPs, normalizeIPToCIDR(ip))
					}
				}
				for _, ip := range svc.IP6 {
					if ip != "" {
						directIPs = append(directIPs, normalizeIPToCIDR(ip))
					}
				}
				for _, c := range svc.CIDR4 {
					if c != "" {
						directIPs = append(directIPs, c)
					}
				}
				for _, c := range svc.CIDR6 {
					if c != "" {
						directIPs = append(directIPs, c)
					}
				}
			}
		}
	}

	return buildHappDeeplinkFromProfile(happRoutingProfile{
		Name:           "HeYearth Smart Routing",
		GlobalProxy:    "true",
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
