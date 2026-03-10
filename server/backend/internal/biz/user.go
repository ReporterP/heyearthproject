package biz

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type User struct {
	ID                int
	UserID            string
	TelegramID        sql.NullInt64
	Role              string
	IsBlocked         bool
	PreferredPlatform string
	Locale            string
}

type Subscription struct {
	GroupID    int
	GroupName  string
	ValidUntil time.Time
}

func GetOrCreateUser(userID string) (*User, error) {
	var user User
	err := db.DB.QueryRow(`
		SELECT id, user_id, telegram_id, role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru')
		FROM users
		WHERE user_id = $1
	`, userID).
		Scan(&user.ID, &user.UserID, &user.TelegramID, &user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale)

	if err == sql.ErrNoRows {
		err = db.DB.QueryRow(`
			INSERT INTO users (user_id, locale, last_seen_at)
			VALUES ($1, 'ru', CURRENT_TIMESTAMP)
			RETURNING id, user_id, telegram_id, role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru')
		`, userID).Scan(&user.ID, &user.UserID, &user.TelegramID, &user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale)
		if err != nil {
			return nil, fmt.Errorf("failed to create user: %w", err)
		}

		if err := ensureFreeSubscription(user.ID); err != nil {
			return nil, fmt.Errorf("failed to assign free subscription: %w", err)
		}

		return &user, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to query user: %w", err)
	}

	_, _ = db.DB.Exec(`
		UPDATE users
		SET last_seen_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, user.ID)

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

	baseValidUntil := time.Now().AddDate(0, 0, durationDays)
	_, err = tx.Exec(`
		INSERT INTO user_subscriptions (user_id, group_id, valid_until) 
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, group_id) 
		DO UPDATE SET valid_until =
			CASE
				WHEN user_subscriptions.valid_until > NOW()
					THEN user_subscriptions.valid_until + ($4 * INTERVAL '1 day')
				ELSE NOW() + ($4 * INTERVAL '1 day')
			END
	`, userID, groupID, baseValidUntil, durationDays)

	if err != nil {
		return err
	}

	return tx.Commit()
}
