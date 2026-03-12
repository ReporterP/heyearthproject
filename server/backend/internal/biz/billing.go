package biz

import (
	"fmt"
	"log"

	"github.com/heyearth/backend/internal/db"
)

type TenantBillingInfo struct {
	TenantID           int
	Name               string
	Plan               string
	MaxUsers           int
	MaxServers         int
	UseProviderServers bool
	Balance            float64
}

func CalculateTenantMonthlyCharge(t TenantBillingInfo) float64 {
	if t.Plan == "super" {
		return 20000
	}
	charge := float64(t.MaxUsers) * 250
	if t.UseProviderServers {
		charge += 1000
	}
	extraServers := t.MaxServers - 5
	if extraServers > 0 {
		charge += float64(extraServers) * 100
	}
	return charge
}

func RunMonthlyTenantBilling() error {
	rows, err := db.DB.Query(`
		SELECT id, name, plan, max_users, max_servers, use_provider_servers, balance::float8
		FROM tenants
		WHERE status = 'active'
	`)
	if err != nil {
		return fmt.Errorf("query tenants: %w", err)
	}
	defer rows.Close()

	var tenants []TenantBillingInfo
	for rows.Next() {
		var t TenantBillingInfo
		if err := rows.Scan(&t.TenantID, &t.Name, &t.Plan, &t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance); err != nil {
			return fmt.Errorf("scan tenant: %w", err)
		}
		tenants = append(tenants, t)
	}

	for _, t := range tenants {
		charge := CalculateTenantMonthlyCharge(t)
		err := DebitTenantWallet(t.TenantID, charge, fmt.Sprintf("Monthly billing: %s", t.Name))
		if err != nil {
			log.Printf("billing: tenant %d (%s): debit %.2f failed: %v", t.TenantID, t.Name, charge, err)
			_, _ = db.DB.Exec(`UPDATE tenants SET status = 'suspended', updated_at = CURRENT_TIMESTAMP WHERE id = $1`, t.TenantID)
			log.Printf("billing: tenant %d (%s) suspended due to insufficient balance", t.TenantID, t.Name)
			continue
		}
		log.Printf("billing: tenant %d (%s): charged %.2f", t.TenantID, t.Name, charge)
	}
	return nil
}
