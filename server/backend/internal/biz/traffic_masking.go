package biz

import (
	"context"
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
	return ListActiveTrafficMaskingRulesContext(context.Background())
}

// ListActiveTrafficMaskingRulesContext is ListActiveTrafficMaskingRules bounded
// by ctx. Query and iteration errors are returned; a row that cannot be scanned
// is skipped (that is the same on every call, so the result stays stable).
func ListActiveTrafficMaskingRulesContext(ctx context.Context) ([]TrafficMaskingRule, error) {
	rows, err := db.DB.QueryContext(ctx, `
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
