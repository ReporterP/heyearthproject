package biz

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// ─────────────────────── Types ───────────────────────

// UserProfile is the full user profile returned to clients.
type UserProfile struct {
	UserID            string `json:"user_id"`
	Username          string `json:"username"`
	HasPassword       bool   `json:"has_password"`
	PreferredPlatform string `json:"preferred_platform,omitempty"`
	Locale            string `json:"locale,omitempty"`
	IsBlocked         bool   `json:"is_blocked"`
	CurrentPlan       string `json:"current_plan,omitempty"`
	CurrentPlanSlug   string `json:"current_plan_slug,omitempty"`
	ValidUntil        string `json:"valid_until,omitempty"`
	PausedUntil       string `json:"paused_until,omitempty"`
	IsPremium         bool   `json:"is_premium"`
	AccessStatus      string `json:"access_status"`
	StatusReason      string `json:"status_reason,omitempty"`
}

// UserAccessState describes whether a user can connect.
type UserAccessState struct {
	UserID                  string
	CurrentPlan             string
	ValidUntil              string
	PreferredPlatform       string
	IsBlocked               bool
	CanConnect              bool
	Reason                  string
	IsPremium               bool
	TrafficMaskingAvailable bool
}

// DeviceRegistration: the platform and locale a new account starts with.
type DeviceRegistration struct {
	Platform string
	Locale   string
}

// UserSubscriptionDetail is returned in subscription lists.
type UserSubscriptionDetail struct {
	ID         int    `json:"id"`
	PlanID     int    `json:"plan_id"`
	PlanName   string `json:"plan_name"`
	ValidUntil string `json:"valid_until"`
	AutoRenew  bool   `json:"auto_renew"`
	Active     bool   `json:"active"`
	Plan       string `json:"plan"`
	PriceRub   int    `json:"price_rub"`
}

// UserPaymentDetail is returned in payment lists.
type UserPaymentDetail struct {
	ID          int     `json:"id"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	Status      string  `json:"status"`
	Provider    string  `json:"provider,omitempty"`
	ExternalID  string  `json:"external_id,omitempty"`
	Notes       string  `json:"notes,omitempty"`
	CreatedAt   string  `json:"created_at"`
	ConfirmedAt string  `json:"confirmed_at,omitempty"`
}

// ─────────────────────── Anonymous Auth ───────────────────────

// RegisterAnonymousUser creates a new user with a random HeYearth ID.
// Returns the user and nil error on success.
func RegisterAnonymousUser(device DeviceRegistration) (*User, error) {
	userID := newCanonicalUserID()
	user, err := createUserRecord(userID, device.Platform, device.Locale)
	if err != nil {
		return nil, fmt.Errorf("register anonymous user: %w", err)
	}
	return user, nil
}

// LoginByIDOrUsername authenticates a user by their HeYearth ID or username.
// If the user has a cloud password set, the provided password must match.
func LoginByIDOrUsername(idOrUsername, password string) (*User, error) {
	user, err := GetUserByIDOrUsername(strings.TrimSpace(idOrUsername))
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}
	if user.IsBlocked {
		return nil, fmt.Errorf("user is blocked")
	}

	// If user has a cloud password, verify it
	if user.PasswordHash.Valid && user.PasswordHash.String != "" {
		if password == "" {
			return nil, fmt.Errorf("password required")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(password)); err != nil {
			return nil, fmt.Errorf("invalid password")
		}
	}

	touchLastSeen(user.ID)
	return user, nil
}

// SetUsername sets or updates the user's username (alias for their ID).
func SetUsername(userID, username string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(username) < 5 || len(username) > 64 {
		return fmt.Errorf("имя пользователя должно быть от 5 до 64 символов")
	}
	// Only allow alphanumeric, underscore, hyphen
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return fmt.Errorf("username can only contain letters, numbers, underscore and hyphen")
		}
	}

	// Для установки username необходим облачный пароль
	var hasPassword bool
	_ = db.DB.QueryRow(`SELECT password_hash IS NOT NULL AND password_hash != '' FROM users WHERE user_id = $1`, userID).Scan(&hasPassword)
	if !hasPassword {
		return fmt.Errorf("для установки username необходимо сначала задать облачный пароль")
	}

	_, err := db.DB.Exec(`
		UPDATE users SET username = $1, updated_at = CURRENT_TIMESTAMP WHERE user_id = $2
	`, username, userID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return fmt.Errorf("username already taken")
		}
		return fmt.Errorf("set username: %w", err)
	}
	return nil
}

// SetCloudPassword sets or changes the user's cloud password.
// If currentPassword is provided and user already has a password, it must match.
// Changing an existing password closes every other session of the user (cleanup plan D2):
// whoever logged in with the old one is signed out; keepToken (the caller's own session,
// "" = none) stays. Setting the first password closes nothing.
func SetCloudPassword(userID, newPassword, currentPassword, keepToken string) error {
	if newPassword == "" {
		return fmt.Errorf("new password cannot be empty")
	}
	if len(newPassword) < 4 {
		return fmt.Errorf("password must be at least 4 characters")
	}

	user, err := GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("user not found")
	}

	// If user already has a password, verify the current one
	replaced := user.PasswordHash.Valid && user.PasswordHash.String != ""
	if replaced {
		if currentPassword == "" {
			return fmt.Errorf("current password required")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(currentPassword)); err != nil {
			return fmt.Errorf("invalid current password")
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	tx, err := db.DB.Begin()
	if err != nil {
		return fmt.Errorf("set cloud password: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`
		UPDATE users SET password_hash = $1, updated_at = CURRENT_TIMESTAMP WHERE user_id = $2
	`, string(hash), userID); err != nil {
		return fmt.Errorf("set cloud password: %w", err)
	}
	if replaced {
		if _, err = tx.Exec(revokeOtherSessionsQuery, user.ID, hashToken(keepToken)); err != nil {
			return fmt.Errorf("close other sessions: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("set cloud password: %w", err)
	}
	return nil
}

// ─────────────────────── Profile & Access ───────────────────────

// NormalizePreferredPlatform validates and normalizes platform strings.
func NormalizePreferredPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "android":
		return "android"
	case "windows":
		return "windows"
	case "ios":
		return "ios"
	case "macos":
		return "macos"
	case "linux":
		return "linux"
	default:
		return ""
	}
}

// GetUserProfile returns the full user profile.
func GetUserProfile(userID string) (*UserProfile, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	sub, err := GetUserSubscription(user.ID)
	if err != nil {
		return nil, err
	}

	profile := &UserProfile{
		UserID:            user.UserID,
		PreferredPlatform: user.PreferredPlatform,
		Locale:            user.Locale,
		IsBlocked:         user.IsBlocked,
		HasPassword:       user.PasswordHash.Valid && user.PasswordHash.String != "",
		AccessStatus:      "active",
	}
	if user.Username.Valid {
		profile.Username = user.Username.String
	}

	if sub == nil {
		profile.CurrentPlan = ""
	} else {
		profile.CurrentPlan = sub.PlanName // plan name (from plans.name)
		profile.CurrentPlanSlug = sub.Plan // plan slug (from plans.slug)
		profile.ValidUntil = sub.ValidUntil.Format(time.RFC3339)
		profile.IsPremium = sub.IsPremium
	}

	// Pause state (активная подписка в паузе). No row = not paused; a failed
	// read is an error, not "not paused".
	var pausedUntil sql.NullTime
	err = db.DB.QueryRow(`
		SELECT us.paused_until FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.paused_until IS NOT NULL AND us.paused_until > NOW()
		ORDER BY us.valid_until DESC LIMIT 1
	`, userID).Scan(&pausedUntil)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("pause state: %w", err)
	}
	if pausedUntil.Valid {
		profile.PausedUntil = pausedUntil.Time.Format(time.RFC3339)
	}
	if profile.IsBlocked {
		profile.AccessStatus = "blocked"
		profile.StatusReason = "user_blocked"
	}
	return profile, nil
}

// GetUserAccessState determines whether a user can connect and their permissions.
// Every read is strict: a database failure is returned as an error, never read
// as "no subscription" / "not a tenant" / "not paused". Callers answer it as a
// temporary failure (503 / Unavailable), so clients keep their cached profile
// instead of being told they lost access.
func GetUserAccessState(userID string, platform string) (*UserAccessState, error) {
	profile, err := GetUserProfile(userID)
	if err != nil {
		return nil, err
	}

	state := &UserAccessState{
		UserID:            profile.UserID,
		CurrentPlan:       profile.CurrentPlan,
		ValidUntil:        profile.ValidUntil,
		PreferredPlatform: profile.PreferredPlatform,
		IsBlocked:         profile.IsBlocked,
		CanConnect:        !profile.IsBlocked,
		IsPremium:         profile.IsPremium,
	}
	if profile.IsBlocked {
		state.Reason = "user_blocked"
		return state, nil
	}

	hasSub, err := HasActiveSubscription(userID)
	if err != nil {
		return nil, fmt.Errorf("active subscription: %w", err)
	}
	isTenant, err := isMemberOfTenant(userID)
	if err != nil {
		return nil, err
	}
	if !hasSub && !isTenant {
		state.CanConnect = false
		state.Reason = "no_active_subscription"
	}

	// Проверка паузы — если подписка на паузе, доступ закрыт
	if hasSub {
		paused, err := isSubscriptionPaused(userID)
		if err != nil {
			return nil, err
		}
		if paused {
			state.CanConnect = false
			state.Reason = "subscription_paused"
		}
	}

	// Traffic masking is available only for premium users
	state.TrafficMaskingAvailable = profile.IsPremium

	return state, nil
}

// isMemberOfTenant: the user belongs to a B2B tenant. An unknown user is not; a
// failed read is an error.
func isMemberOfTenant(userID string) (bool, error) {
	var tenantID int
	err := db.DB.QueryRow(`
		SELECT COALESCE(tenant_id, 0) FROM users WHERE user_id = $1
	`, strings.TrimSpace(userID)).Scan(&tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("tenant membership: %w", err)
	}
	return tenantID > 0, nil
}

// isSubscriptionPaused — есть ли активная подписка с paused_until > NOW().
func isSubscriptionPaused(userID string) (bool, error) {
	var count int
	err := db.DB.QueryRow(`
		SELECT COUNT(*) FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.paused_until IS NOT NULL AND us.paused_until > NOW()
	`, strings.TrimSpace(userID)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("pause check: %w", err)
	}
	return count > 0, nil
}

// ─────────────────────── Subscriptions & Payments Lists ───────────────────────

// ListUserSubscriptions returns all subscriptions for a user.
func ListUserSubscriptions(userID string) ([]UserSubscriptionDetail, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	rows, err := db.DB.Query(`
		SELECT us.id, COALESCE(us.plan_id, 0), COALESCE(p.name, 'standard'),
		       us.valid_until, us.auto_renew, us.valid_until > NOW(),
		       COALESCE(p.slug, us.plan, 'standard'), COALESCE(us.price_paid, p.price_rub, us.price_rub, 0)
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1
		ORDER BY us.valid_until DESC
	`, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list user subscriptions: %w", err)
	}
	defer rows.Close()

	var items []UserSubscriptionDetail
	for rows.Next() {
		var item UserSubscriptionDetail
		var validUntil time.Time
		if err := rows.Scan(&item.ID, &item.PlanID, &item.PlanName, &validUntil, &item.AutoRenew, &item.Active, &item.Plan, &item.PriceRub); err != nil {
			return nil, err
		}
		item.ValidUntil = validUntil.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListUserPayments returns all payments for a user.
func ListUserPayments(userID string) ([]UserPaymentDetail, error) {
	rows, err := db.DB.Query(`
		SELECT id, amount::float8, currency, status, COALESCE(provider, ''), COALESCE(external_id, ''),
		       COALESCE(description, ''), created_at, paid_at
		FROM payments
		WHERE user_id_text = $1
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user payments: %w", err)
	}
	defer rows.Close()

	var items []UserPaymentDetail
	for rows.Next() {
		var item UserPaymentDetail
		var createdAt time.Time
		var confirmedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.Amount, &item.Currency, &item.Status, &item.Provider,
			&item.ExternalID, &item.Notes, &createdAt, &confirmedAt); err != nil {
			return nil, err
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		if confirmedAt.Valid {
			item.ConfirmedAt = confirmedAt.Time.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ─────────────────────── Devices ───────────────────────

// ─────────────────────── Internal Helpers ───────────────────────

func createUserRecord(userID, preferredPlatform, locale string) (*User, error) {
	platform := NormalizePreferredPlatform(preferredPlatform)
	if locale == "" {
		locale = "ru"
	}
	var user User
	var username, passwordHash string
	err := db.DB.QueryRow(`
		INSERT INTO users (user_id, preferred_platform, locale, last_seen_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP)
		RETURNING id, user_id, telegram_id, COALESCE(username, ''), COALESCE(password_hash, ''),
		          role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru')
	`, userID, platform, locale).Scan(
		&user.ID, &user.UserID, &user.TelegramID, &username, &passwordHash,
		&user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	if username != "" {
		user.Username = sql.NullString{String: username, Valid: true}
	}
	if passwordHash != "" {
		user.PasswordHash = sql.NullString{String: passwordHash, Valid: true}
	}
	// Бесплатная подписка больше не выдаётся при регистрации.
	// Пользователь должен оплатить или ввести промокод.
	return &user, nil
}

func newCanonicalUserID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("user-%d", time.Now().UnixNano())
	}
	// Format as UUID v4
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(buf[0])<<24|uint32(buf[1])<<16|uint32(buf[2])<<8|uint32(buf[3]),
		uint16(buf[4])<<8|uint16(buf[5]),
		uint16(buf[6])<<8|uint16(buf[7]),
		uint16(buf[8])<<8|uint16(buf[9]),
		uint64(buf[10])<<40|uint64(buf[11])<<32|uint64(buf[12])<<24|uint64(buf[13])<<16|uint64(buf[14])<<8|uint64(buf[15]),
	)
}
