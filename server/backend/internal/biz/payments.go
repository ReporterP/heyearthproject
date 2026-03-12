package biz

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type PaymentRecord struct {
	ID               int
	UserID           string
	Amount           float64
	Currency         string
	Status           string
	Provider         string
	YookassaID       string
	YookassaStatus   string
	Description      string
	ConfirmationURL  string
	PaidAt           *time.Time
	CreatedAt        time.Time
}

func CreatePaymentRecord(userID string, amount float64, currency, provider, description string) (int, error) {
	var id int
	err := db.DB.QueryRow(`
		INSERT INTO payments (user_id_text, amount, currency, status, provider, payment_method, description, created_at, updated_at)
		VALUES ($1, $2, $3, 'pending', $4, $4, $5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, userID, amount, currency, provider, description).Scan(&id)
	return id, err
}

func UpdatePaymentYookassa(paymentDBID int, yookassaID, yookassaStatus, confirmationURL string) error {
	_, err := db.DB.Exec(`
		UPDATE payments
		SET yookassa_payment_id = $1, yookassa_status = $2, confirmation_url = $3, updated_at = CURRENT_TIMESTAMP
		WHERE id = $4
	`, yookassaID, yookassaStatus, confirmationURL, paymentDBID)
	return err
}

func MarkPaymentCompleted(paymentDBID int) error {
	_, err := db.DB.Exec(`
		UPDATE payments
		SET status = 'completed', yookassa_status = 'succeeded', paid_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, paymentDBID)
	return err
}

func GetPaymentByYookassaID(yookassaID string) (*PaymentRecord, error) {
	var p PaymentRecord
	err := db.DB.QueryRow(`
		SELECT id, COALESCE(user_id_text,''), amount, COALESCE(currency,'RUB'), status, COALESCE(provider,'yookassa'),
		       COALESCE(yookassa_payment_id,''), COALESCE(yookassa_status,''), COALESCE(description,''),
		       COALESCE(confirmation_url,''), paid_at, created_at
		FROM payments WHERE yookassa_payment_id = $1
	`, yookassaID).Scan(
		&p.ID, &p.UserID, &p.Amount, &p.Currency, &p.Status, &p.Provider,
		&p.YookassaID, &p.YookassaStatus, &p.Description,
		&p.ConfirmationURL, &p.PaidAt, &p.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &p, err
}

func HasActiveSubscription(userID string) (bool, error) {
	var count int
	err := db.DB.QueryRow(`
		SELECT COUNT(*) FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.valid_until > CURRENT_TIMESTAMP
	`, userID).Scan(&count)
	return count > 0, err
}

func ActivateSubscription(userID string, groupID int, plan string, durationDays int, priceRub int) error {
	var dbUserID int
	err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID)
	if err != nil {
		return fmt.Errorf("resolve user %s: %w", userID, err)
	}
	validUntil := time.Now().AddDate(0, 0, durationDays)
	_, err = db.DB.Exec(`
		INSERT INTO user_subscriptions (user_id, group_id, valid_until, auto_renew, plan, price_rub)
		VALUES ($1, $2, $3, true, $4, $5)
		ON CONFLICT (user_id, group_id) DO UPDATE
		SET valid_until = GREATEST(user_subscriptions.valid_until, $3),
		    plan = $4, price_rub = $5, auto_renew = true
	`, dbUserID, groupID, validUntil, plan, priceRub)
	return err
}

// Tenant wallet

func CreditTenantWallet(tenantID int, amount float64, description string, paymentID *int) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO tenant_wallet_transactions (tenant_id, amount, direction, description, payment_id)
		VALUES ($1, $2, 'credit', $3, $4)
	`, tenantID, amount, description, paymentID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE tenants SET balance = balance + $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, amount, tenantID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func DebitTenantWallet(tenantID int, amount float64, description string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var balance float64
	if err := tx.QueryRow(`SELECT balance FROM tenants WHERE id = $1 FOR UPDATE`, tenantID).Scan(&balance); err != nil {
		return err
	}
	if balance < amount {
		return fmt.Errorf("insufficient balance: have %.2f, need %.2f", balance, amount)
	}

	_, err = tx.Exec(`
		INSERT INTO tenant_wallet_transactions (tenant_id, amount, direction, description)
		VALUES ($1, $2, 'debit', $3)
	`, tenantID, amount, description)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE tenants SET balance = balance - $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`, amount, tenantID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// Tenant claim codes

func RedeemTenantClaimCode(code, userID, userName string) (tenantID int, err error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var codeID, maxUses, usedCount int
	err = tx.QueryRow(`
		SELECT id, tenant_id, max_uses, used_count FROM tenant_claim_codes
		WHERE code = $1 AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
	`, code).Scan(&codeID, &tenantID, &maxUses, &usedCount)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("invalid or expired code")
	}
	if err != nil {
		return 0, err
	}
	if maxUses > 0 && usedCount >= maxUses {
		return 0, fmt.Errorf("code usage limit reached")
	}

	_, err = tx.Exec(`UPDATE tenant_claim_codes SET used_count = used_count + 1 WHERE id = $1`, codeID)
	if err != nil {
		return 0, err
	}

	_, err = tx.Exec(`UPDATE users SET tenant_id = $1 WHERE user_id = $2`, tenantID, userID)
	if err != nil {
		return 0, err
	}

	return tenantID, tx.Commit()
}

// Multiple accounts per telegram user

func LinkTelegramUser(telegramID int64, userID, label string) error {
	_, err := db.DB.Exec(`
		INSERT INTO telegram_linked_users (telegram_id, user_id, label)
		VALUES ($1, $2, $3)
		ON CONFLICT (telegram_id, user_id) DO UPDATE SET label = $3
	`, telegramID, userID, label)
	return err
}

type LinkedAccount struct {
	UserID   string
	Label    string
	IsActive bool
}

func GetLinkedAccounts(telegramID int64) ([]LinkedAccount, error) {
	rows, err := db.DB.Query(`
		SELECT user_id, label, is_active FROM telegram_linked_users
		WHERE telegram_id = $1 ORDER BY created_at
	`, telegramID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var accounts []LinkedAccount
	for rows.Next() {
		var a LinkedAccount
		if err := rows.Scan(&a.UserID, &a.Label, &a.IsActive); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, nil
}

func SetActiveLinkedAccount(telegramID int64, userID string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE telegram_linked_users SET is_active = FALSE WHERE telegram_id = $1`, telegramID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE telegram_linked_users SET is_active = TRUE WHERE telegram_id = $1 AND user_id = $2`, telegramID, userID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
