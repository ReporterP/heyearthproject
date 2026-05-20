package biz

import (
	"fmt"
	"log"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/email"
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
			go sendTenantSuspensionEmail(t.TenantID, t.Name, charge, t.Balance)
			continue
		}
		log.Printf("billing: tenant %d (%s): charged %.2f", t.TenantID, t.Name, charge)
		// Email summary so the company sees what was billed and what remains.
		newBalance := t.Balance - charge
		go sendTenantBilledEmail(t.TenantID, t.Name, charge, newBalance)
	}
	return nil
}

// ── B2B notification helpers ────────────────────────────────────────────────
// All send best-effort; failures are logged but don't bubble back to callers.

func tenantContactEmail(tenantID int) string {
	var em string
	_ = db.DB.QueryRow(`SELECT COALESCE(contact_email, '') FROM tenants WHERE id = $1`, tenantID).Scan(&em)
	return em
}

// sendTenantBilledEmail tells the company "we just took N rubles for this month".
func sendTenantBilledEmail(tenantID int, name string, charge, newBalance float64) {
	to := tenantContactEmail(tenantID)
	if to == "" {
		return
	}
	body := fmt.Sprintf(
		"Здравствуйте!\n\nЕжемесячное списание для компании «%s»: %.2f ₽.\nОстаток на кошельке: %.2f ₽.\n\nЕсли остаток меньше следующего ежемесячного платежа — пополните кошелёк в личном кабинете компании, иначе аккаунт будет приостановлен 1-го числа следующего месяца.\n\nКабинет компании: https://hiearth.ru/business/dashboard\n\n—\nHiEarth",
		name, charge, newBalance,
	)
	_ = email.Send(email.Message{
		To:       to,
		Subject:  fmt.Sprintf("[HiEarth] Списание %.0f ₽ — «%s»", charge, name),
		TextBody: body,
		Template: "tenant_billed",
	})
}

// sendTenantSuspensionEmail tells the company "we tried to bill but you ran out".
func sendTenantSuspensionEmail(tenantID int, name string, charge, balance float64) {
	to := tenantContactEmail(tenantID)
	if to == "" {
		return
	}
	body := fmt.Sprintf(
		"Здравствуйте!\n\nКомпания «%s» приостановлена: не хватило средств для ежемесячного списания.\nТребовалось: %.2f ₽. Остаток на кошельке: %.2f ₽.\n\nПополните кошелёк в кабинете компании — после первого успешного платежа аккаунт автоматически возобновится.\n\nКабинет компании: https://hiearth.ru/business/dashboard\n\n—\nHiEarth",
		name, charge, balance,
	)
	_ = email.Send(email.Message{
		To:       to,
		Subject:  fmt.Sprintf("[HiEarth] Аккаунт приостановлен: «%s»", name),
		TextBody: body,
		Template: "tenant_suspended",
	})
}

// RunTenantBalanceWarnings — daily cron. For each active tenant whose balance
// is below ~50% of the next monthly charge, send a one-time warning email
// per calendar month. Tracked via tenant_wallet_transactions with description
// prefix "[notify]" so we don't spam the same tenant multiple times per month.
//
// Threshold logic:
//   estimatedNextCharge = CalculateTenantMonthlyCharge(tenant)
//   IF balance < 0.5 * estimatedNextCharge → warn
//
// Idempotent within a calendar month via dedup on a synthetic
// 'low_balance_notify' wallet entry (amount=0, direction='credit').
func RunTenantBalanceWarnings() error {
	rows, err := db.DB.Query(`
		SELECT id, name, plan, max_users, max_servers, use_provider_servers, balance::float8
		FROM tenants
		WHERE status = 'active'
	`)
	if err != nil {
		return fmt.Errorf("query tenants for warnings: %w", err)
	}
	defer rows.Close()

	var tenants []TenantBillingInfo
	for rows.Next() {
		var t TenantBillingInfo
		if err := rows.Scan(&t.TenantID, &t.Name, &t.Plan, &t.MaxUsers, &t.MaxServers, &t.UseProviderServers, &t.Balance); err != nil {
			return err
		}
		tenants = append(tenants, t)
	}

	for _, t := range tenants {
		estimated := CalculateTenantMonthlyCharge(t)
		if t.Balance >= 0.5*estimated {
			continue
		}
		// Dedup: did we already send a low-balance warning this month?
		var already int
		_ = db.DB.QueryRow(`
			SELECT COUNT(*) FROM tenant_wallet_transactions
			WHERE tenant_id = $1
			  AND description LIKE '[notify-low-balance]%'
			  AND created_at >= date_trunc('month', NOW())
		`, t.TenantID).Scan(&already)
		if already > 0 {
			continue
		}
		// Send + record marker (amount=0 credit so it doesn't affect balance).
		go sendTenantLowBalanceEmail(t.TenantID, t.Name, t.Balance, estimated)
		_, _ = db.DB.Exec(`
			INSERT INTO tenant_wallet_transactions (tenant_id, amount, direction, description)
			VALUES ($1, 0, 'credit', $2)
		`, t.TenantID, fmt.Sprintf("[notify-low-balance] balance=%.2f, next≈%.2f", t.Balance, estimated))
		log.Printf("notify: tenant %d (%s) low balance: %.2f < 0.5*%.2f", t.TenantID, t.Name, t.Balance, estimated)
	}
	return nil
}

func sendTenantLowBalanceEmail(tenantID int, name string, balance, estimatedNext float64) {
	to := tenantContactEmail(tenantID)
	if to == "" {
		return
	}
	// Days until next 1st-of-month.
	now := time.Now()
	year, month, _ := now.Date()
	nextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, now.Location())
	daysLeft := int(nextMonth.Sub(now).Hours() / 24)

	body := fmt.Sprintf(
		"Здравствуйте!\n\nНа кошельке компании «%s» осталось %.2f ₽. Следующее ежемесячное списание примерно %.2f ₽ — этого может не хватить.\n\nДо списания: %d дней.\n\nПополните кошелёк в кабинете компании, чтобы доступ к HiEarth не прерывался: https://hiearth.ru/business/dashboard\n\n—\nHiEarth",
		name, balance, estimatedNext, daysLeft,
	)
	_ = email.Send(email.Message{
		To:       to,
		Subject:  fmt.Sprintf("[HiEarth] Низкий баланс — «%s»", name),
		TextBody: body,
		Template: "tenant_low_balance",
	})
}
