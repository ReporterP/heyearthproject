package biz

import (
	"database/sql"
	"fmt"
	"heyearth/internal/db"
	"time"
)

type User struct {
	ID         int
	TelegramID int64
	Role       string
}

type Subscription struct {
	GroupID    int
	GroupName  string
	ValidUntil time.Time
}

func GetOrCreateUser(telegramID int64) (*User, error) {
	var user User
	err := db.DB.QueryRow("SELECT id, telegram_id, role FROM users WHERE telegram_id = $1", telegramID).
		Scan(&user.ID, &user.TelegramID, &user.Role)

	if err == sql.ErrNoRows {
		// Create user
		err = db.DB.QueryRow(
			"INSERT INTO users (telegram_id) VALUES ($1) RETURNING id, telegram_id, role",
			telegramID,
		).Scan(&user.ID, &user.TelegramID, &user.Role)
		if err != nil {
			return nil, fmt.Errorf("failed to create user: %w", err)
		}
		
		// Assign to default Free group
		var freeGroupID int
		err = db.DB.QueryRow("SELECT id FROM user_groups WHERE name = 'Free' LIMIT 1").Scan(&freeGroupID)
		if err == nil {
			// Give 100 years of free access
			validUntil := time.Now().AddDate(100, 0, 0)
			db.DB.Exec("INSERT INTO user_subscriptions (user_id, group_id, valid_until) VALUES ($1, $2, $3)", user.ID, freeGroupID, validUntil)
		}

		return &user, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	return &user, nil
}

func GetUserSubscription(userID int) (*Subscription, error) {
	var sub Subscription
	// Get the highest tier active subscription
	err := db.DB.QueryRow(`
		SELECT us.group_id, ug.name, us.valid_until 
		FROM user_subscriptions us
		JOIN user_groups ug ON us.group_id = ug.id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY ug.is_premium DESC, us.valid_until DESC
		LIMIT 1
	`, userID).Scan(&sub.GroupID, &sub.GroupName, &sub.ValidUntil)

	if err == sql.ErrNoRows {
		return nil, nil // No active subscription
	} else if err != nil {
		return nil, fmt.Errorf("failed to get subscription: %w", err)
	}

	return &sub, nil
}

func ApplyPromocode(userID int, code string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var groupID, durationDays, maxUses, currentUses int
	var expiresAt sql.NullTime

	err = tx.QueryRow(`
		SELECT group_id_granted, duration_days, max_uses, current_uses, expires_at 
		FROM promocodes WHERE code = $1 FOR UPDATE
	`, code).Scan(&groupID, &durationDays, &maxUses, &currentUses, &expiresAt)

	if err == sql.ErrNoRows {
		return fmt.Errorf("invalid promocode")
	} else if err != nil {
		return err
	}

	if maxUses > 0 && currentUses >= maxUses {
		return fmt.Errorf("promocode usage limit reached")
	}

	if expiresAt.Valid && time.Now().After(expiresAt.Time) {
		return fmt.Errorf("promocode expired")
	}

	// Update usage
	_, err = tx.Exec("UPDATE promocodes SET current_uses = current_uses + 1 WHERE code = $1", code)
	if err != nil {
		return err
	}

	// Apply subscription
	validUntil := time.Now().AddDate(0, 0, durationDays)
	
	// Upsert subscription
	_, err = tx.Exec(`
		INSERT INTO user_subscriptions (user_id, group_id, valid_until) 
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, group_id) 
		DO UPDATE SET valid_until = GREATEST(user_subscriptions.valid_until, EXCLUDED.valid_until) + INTERVAL '1 day' * $4
	`, userID, groupID, validUntil, durationDays)
	
	if err != nil {
		return err
	}

	return tx.Commit()
}
