package biz

import (
	"log"

	"github.com/heyearth/backend/internal/db"
)

// TrafficMaskingRule — правило маскировки трафика.
type TrafficMaskingRule struct {
	ID           int
	SourceDomain string
	MaskDomain   string
	TenantID     *int
}

// ListActiveTrafficMaskingRules возвращает все активные правила маскировки.
func ListActiveTrafficMaskingRules() ([]TrafficMaskingRule, error) {
	rows, err := db.DB.Query(`
		SELECT id, source_domain, mask_domain, tenant_id
		FROM traffic_masking_rules
		WHERE is_active = TRUE
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []TrafficMaskingRule
	for rows.Next() {
		var r TrafficMaskingRule
		if err := rows.Scan(&r.ID, &r.SourceDomain, &r.MaskDomain, &r.TenantID); err != nil {
			log.Printf("scan traffic masking rule: %v", err)
			continue
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}
