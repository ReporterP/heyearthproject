package biz

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// User represents a HeYearth user in the database.
type User struct {
	ID                int
	UserID            string
	TelegramID        sql.NullInt64 // legacy, kept for migration compatibility
	Username          sql.NullString
	PasswordHash      sql.NullString
	Role              string
	IsBlocked         bool
	PreferredPlatform string
	Locale            string
	VlessUUID         string // per-user UUID для VLESS подключения
}

// Subscription represents an active user subscription.
type Subscription struct {
	GroupID    int
	GroupName  string
	IsPremium  bool
	Plan       string
	PriceRub   int
	ValidUntil time.Time
}

// GetOrCreateUser finds a user by their canonical user_id or creates a new one.
func GetOrCreateUser(userID string) (*User, error) {
	user, err := getUserByField("user_id", userID)
	if err == nil {
		touchLastSeen(user.ID)
		return user, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query user: %w", err)
	}

	// Create new user
	return createUserRecord(userID, "", "ru")
}

// GetUserByID returns a user by canonical user_id.
func GetUserByID(userID string) (*User, error) {
	return getUserByField("user_id", userID)
}

// GetUserByUsername returns a user by their username.
func GetUserByUsername(username string) (*User, error) {
	return getUserByField("username", username)
}

// GetUserByIDOrUsername tries to find user by user_id first, then by username.
func GetUserByIDOrUsername(idOrUsername string) (*User, error) {
	user, err := getUserByField("user_id", idOrUsername)
	if err == nil {
		return user, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	// Try by username
	user, err = getUserByField("username", idOrUsername)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	return user, err
}

// GetUserVlessUUID возвращает per-user VLESS UUID. Если нет — генерирует.
func GetUserVlessUUID(userID string) (string, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return "", err
	}
	if user.VlessUUID != "" {
		return user.VlessUUID, nil
	}
	// Генерируем UUID если ещё нет
	var uuid string
	err = db.DB.QueryRow(`UPDATE users SET vless_uuid = gen_random_uuid() WHERE id = $1 RETURNING vless_uuid::text`, user.ID).Scan(&uuid)
	if err != nil {
		return "", fmt.Errorf("generate vless_uuid: %w", err)
	}
	return uuid, nil
}

// ListActiveVlessUUIDs возвращает VLESS UUID всех пользователей с активной подпиской.
func ListActiveVlessUUIDs() ([]string, error) {
	rows, err := db.DB.Query(`
		SELECT DISTINCT u.vless_uuid::text
		FROM users u
		JOIN user_subscriptions us ON us.user_id = u.id
		WHERE u.vless_uuid IS NOT NULL
		  AND u.is_blocked = FALSE
		  AND us.valid_until > CURRENT_TIMESTAMP
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		uuids = append(uuids, uuid)
	}
	return uuids, rows.Err()
}

// ActiveUserWithUUID — пара user_id + vless_uuid для активного пользователя.
type ActiveUserWithUUID struct {
	UserID    string
	VlessUUID string
}

// ListActiveUsersWithUUIDs возвращает user_id и vless_uuid всех пользователей с активной подпиской.
// Используется для генерации SOCKS5/HTTP аккаунтов в Xray конфиге.
func ListActiveUsersWithUUIDs() ([]ActiveUserWithUUID, error) {
	rows, err := db.DB.Query(`
		SELECT DISTINCT u.user_id, u.vless_uuid::text
		FROM users u
		JOIN user_subscriptions us ON us.user_id = u.id
		WHERE u.vless_uuid IS NOT NULL
		  AND u.is_blocked = FALSE
		  AND us.valid_until > CURRENT_TIMESTAMP
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ActiveUserWithUUID
	for rows.Next() {
		var u ActiveUserWithUUID
		if err := rows.Scan(&u.UserID, &u.VlessUUID); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}

func getUserByField(field, value string) (*User, error) {
	var user User
	query := fmt.Sprintf(`
		SELECT id, user_id, telegram_id, COALESCE(username, ''), COALESCE(password_hash, ''),
		       role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru'),
		       COALESCE(vless_uuid::text, '')
		FROM users WHERE %s = $1
	`, field)
	var username, passwordHash string
	err := db.DB.QueryRow(query, value).Scan(
		&user.ID, &user.UserID, &user.TelegramID, &username, &passwordHash,
		&user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale,
		&user.VlessUUID,
	)
	if err != nil {
		return nil, err
	}
	if username != "" {
		user.Username = sql.NullString{String: username, Valid: true}
	}
	if passwordHash != "" {
		user.PasswordHash = sql.NullString{String: passwordHash, Valid: true}
	}
	return &user, nil
}

func touchLastSeen(userDBID int) {
	_, _ = db.DB.Exec(`
		UPDATE users SET last_seen_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = $1
	`, userDBID)
}

// GetUserSubscription returns the highest-tier active subscription for a user.
func GetUserSubscription(userID int) (*Subscription, error) {
	var sub Subscription
	err := db.DB.QueryRow(`
		SELECT us.group_id, ug.name, ug.is_premium, COALESCE(us.plan, 'standard'), COALESCE(us.price_rub, 0), us.valid_until
		FROM user_subscriptions us
		JOIN user_groups ug ON us.group_id = ug.id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY ug.is_premium DESC, us.valid_until DESC
		LIMIT 1
	`, userID).Scan(&sub.GroupID, &sub.GroupName, &sub.IsPremium, &sub.Plan, &sub.PriceRub, &sub.ValidUntil)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return &sub, nil
}

// ApplyPromocode applies a promocode to a user, extending their subscription.
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
	}
	if err != nil {
		return err
	}

	if maxUses > 0 && currentUses >= maxUses {
		return fmt.Errorf("promocode usage limit reached")
	}
	if expiresAt.Valid && time.Now().After(expiresAt.Time) {
		return fmt.Errorf("promocode expired")
	}

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

	if err := tx.Commit(); err != nil {
		return err
	}
	NotifySubscriptionChange()
	return nil
}
