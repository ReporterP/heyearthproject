package biz

// direct_routes_query.go — read-only хелперы для downstream-консьюмеров.
//
// Используется в:
//   - Phase 2 (xray_sync) — чтобы знать какие сервисы выводить из туннеля
//     для каждого плана и сгенерировать соответствующие routing rules.
//   - Phase 2 (Happ profile builder) — чтобы клиентский конфиг знал
//     какие домены/IP не отправлять в туннель в первую очередь.
//   - Phase 4 (admin dashboard) — статистика и текущая версия для UI.
//
// Все запросы фильтруют по is_active=TRUE, кроме GetDirectRoutesStats,
// который возвращает и total, и active отдельно.

import (
	"fmt"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// GetDirectRouteServicesForPlan returns all is_active services attached to a plan.
func GetDirectRouteServicesForPlan(planID int) ([]DirectRouteService, error) {
	if planID <= 0 {
		return nil, fmt.Errorf("get services for plan: plan id is required")
	}
	q := `
		SELECT s.id, s.slug, s.name, s.group_name, s.source,
		       s.domains, s.ip4, s.ip6, s.cidr4, s.cidr6, s.dns_servers,
		       s.timeout_sec, s.is_active, s.notes,
		       s.created_at::text, s.updated_at::text
		FROM direct_route_services s
		JOIN plan_direct_services pds ON pds.service_id = s.id
		WHERE pds.plan_id = $1 AND s.is_active = TRUE
		ORDER BY s.group_name, s.name
	`
	rows, err := db.DB.Query(q, planID)
	if err != nil {
		return nil, fmt.Errorf("services for plan %d: %w", planID, err)
	}
	defer rows.Close()

	out := []DirectRouteService{}
	for rows.Next() {
		var s DirectRouteService
		var domains, ip4, ip6, cidr4, cidr6, dns pq.StringArray
		if err := rows.Scan(&s.ID, &s.Slug, &s.Name, &s.GroupName, &s.Source,
			&domains, &ip4, &ip6, &cidr4, &cidr6, &dns,
			&s.TimeoutSec, &s.IsActive, &s.Notes, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan service for plan: %w", err)
		}
		s.Domains = []string(domains)
		s.IP4 = []string(ip4)
		s.IP6 = []string(ip6)
		s.CIDR4 = []string(cidr4)
		s.CIDR6 = []string(cidr6)
		s.DNSServers = []string(dns)
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetPlanIDsWithDirectRoutes returns distinct plan ids that have at least one
// active direct-route service attached. Used by xray_sync to know which plans
// need extra routing rules.
func GetPlanIDsWithDirectRoutes() ([]int, error) {
	rows, err := db.DB.Query(`
		SELECT DISTINCT pds.plan_id
		FROM plan_direct_services pds
		JOIN direct_route_services s ON s.id = pds.service_id
		WHERE s.is_active = TRUE
		ORDER BY pds.plan_id
	`)
	if err != nil {
		return nil, fmt.Errorf("plan ids with direct routes: %w", err)
	}
	defer rows.Close()

	out := []int{}
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			return nil, fmt.Errorf("scan plan id: %w", err)
		}
		out = append(out, pid)
	}
	return out, rows.Err()
}

// GetDirectRoutesStats returns a flat map ready to be serialised to JSON for the admin dashboard.
func GetDirectRoutesStats() (map[string]any, error) {
	stats := map[string]any{
		"total_services":  0,
		"active_services": 0,
		"total_domains":   0,
		"total_ips":       0,
		"total_cidrs":     0,
		"by_group":        map[string]int{},
		"plans_count":     0,
	}

	var total, active int
	if err := db.DB.QueryRow(`
		SELECT
		    COUNT(*) AS total,
		    COUNT(*) FILTER (WHERE is_active) AS active
		FROM direct_route_services
	`).Scan(&total, &active); err != nil {
		return nil, fmt.Errorf("count services: %w", err)
	}
	stats["total_services"] = total
	stats["active_services"] = active

	var totalDomains, totalIPs, totalCIDRs int
	if err := db.DB.QueryRow(`
		SELECT
		    COALESCE(SUM(COALESCE(array_length(domains, 1), 0)), 0),
		    COALESCE(SUM(COALESCE(array_length(ip4, 1), 0) + COALESCE(array_length(ip6, 1), 0)), 0),
		    COALESCE(SUM(COALESCE(array_length(cidr4, 1), 0) + COALESCE(array_length(cidr6, 1), 0)), 0)
		FROM direct_route_services
		WHERE is_active = TRUE
	`).Scan(&totalDomains, &totalIPs, &totalCIDRs); err != nil {
		return nil, fmt.Errorf("sum array lengths: %w", err)
	}
	stats["total_domains"] = totalDomains
	stats["total_ips"] = totalIPs
	stats["total_cidrs"] = totalCIDRs

	byGroup := map[string]int{}
	rows, err := db.DB.Query(`
		SELECT group_name, COUNT(*)
		FROM direct_route_services
		WHERE is_active = TRUE
		GROUP BY group_name
		ORDER BY group_name
	`)
	if err != nil {
		return nil, fmt.Errorf("group counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var g string
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			return nil, fmt.Errorf("scan group count: %w", err)
		}
		byGroup[g] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate group counts: %w", err)
	}
	stats["by_group"] = byGroup

	var plansCount int
	if err := db.DB.QueryRow(`
		SELECT COUNT(DISTINCT pds.plan_id)
		FROM plan_direct_services pds
		JOIN direct_route_services s ON s.id = pds.service_id
		WHERE s.is_active = TRUE
	`).Scan(&plansCount); err != nil {
		return nil, fmt.Errorf("plans count: %w", err)
	}
	stats["plans_count"] = plansCount

	return stats, nil
}

// GetDirectRoutesVersion returns the current direct_routes_version setting (defaults to "1").
func GetDirectRoutesVersion() string {
	return GetSettingOrDefault("direct_routes_version", "1")
}
