package biz

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	ValidUntil        string `json:"valid_until,omitempty"`
	IsPremium         bool   `json:"is_premium"`
	AccessStatus      string `json:"access_status"`
	StatusReason      string `json:"status_reason,omitempty"`
}

// UserAccessState describes whether a user can connect.
type UserAccessState struct {
	UserID                   string
	CurrentPlan              string
	ValidUntil               string
	PreferredPlatform        string
	IsBlocked                bool
	CanConnect               bool
	Reason                   string
	IsPremium                bool
	TrafficMaskingAvailable  bool
}

// DeviceRegistration contains info about a user's device.
type DeviceRegistration struct {
	DeviceID    string
	Platform    string
	DeviceLabel string
	Locale      string
	LinkedVia   string
}

// UserSubscriptionDetail is returned in subscription lists.
type UserSubscriptionDetail struct {
	GroupID    int    `json:"group_id"`
	GroupName  string `json:"group_name"`
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
	if device.DeviceID != "" {
		_ = RegisterUserDevice(user.UserID, device)
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
	if len(username) < 3 || len(username) > 64 {
		return fmt.Errorf("username must be 3-64 characters")
	}
	// Only allow alphanumeric, underscore, hyphen
	for _, c := range username {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return fmt.Errorf("username can only contain letters, numbers, underscore and hyphen")
		}
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
func SetCloudPassword(userID, newPassword, currentPassword string) error {
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
	if user.PasswordHash.Valid && user.PasswordHash.String != "" {
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

	_, err = db.DB.Exec(`
		UPDATE users SET password_hash = $1, updated_at = CURRENT_TIMESTAMP WHERE user_id = $2
	`, string(hash), userID)
	if err != nil {
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
		profile.CurrentPlan = sub.GroupName
		profile.ValidUntil = sub.ValidUntil.Format(time.RFC3339)
		profile.IsPremium = sub.IsPremium
	}
	if profile.IsBlocked {
		profile.AccessStatus = "blocked"
		profile.StatusReason = "user_blocked"
	}
	return profile, nil
}

// GetUserAccessState determines whether a user can connect and their permissions.
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

	hasSub, _ := HasActiveSubscription(userID)
	isTenant := isMemberOfTenant(userID)
	if !hasSub && !isTenant {
		state.CanConnect = false
		state.Reason = "no_active_subscription"
	}

	// Traffic masking is available only for premium users
	state.TrafficMaskingAvailable = profile.IsPremium

	return state, nil
}

func isMemberOfTenant(userID string) bool {
	var tenantID int
	err := db.DB.QueryRow(`
		SELECT COALESCE(tenant_id, 0) FROM users WHERE user_id = $1
	`, strings.TrimSpace(userID)).Scan(&tenantID)
	if err != nil {
		return false
	}
	return tenantID > 0
}

// ─────────────────────── Subscriptions & Payments Lists ───────────────────────

// ListUserSubscriptions returns all subscriptions for a user.
func ListUserSubscriptions(userID string) ([]UserSubscriptionDetail, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	rows, err := db.DB.Query(`
		SELECT us.group_id, ug.name, us.valid_until, us.auto_renew, us.valid_until > NOW(),
		       COALESCE(us.plan, 'standard'), COALESCE(us.price_rub, 0)
		FROM user_subscriptions us
		JOIN user_groups ug ON ug.id = us.group_id
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
		if err := rows.Scan(&item.GroupID, &item.GroupName, &validUntil, &item.AutoRenew, &item.Active, &item.Plan, &item.PriceRub); err != nil {
			return nil, err
		}
		item.ValidUntil = validUntil.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListUserPayments returns all payments for a user.
func ListUserPayments(userID string) ([]UserPaymentDetail, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	rows, err := db.DB.Query(`
		SELECT id, amount::float8, currency, status, COALESCE(provider, ''), COALESCE(yookassa_payment_id, ''),
		       COALESCE(description, ''), created_at, paid_at
		FROM payments
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, user.ID)
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

// RegisterUserDevice records a device for a user.
func RegisterUserDevice(userID string, registration DeviceRegistration) error {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return err
	}
	return insertUserDevice(db.DB, user.ID, registration)
}

func insertUserDevice(q execQueryer, userDBID int, registration DeviceRegistration) error {
	deviceID := strings.TrimSpace(registration.DeviceID)
	platform := NormalizePreferredPlatform(registration.Platform)
	if deviceID == "" {
		deviceID = "default"
	}
	if platform == "" {
		platform = "unknown"
	}
	linkedVia := strings.TrimSpace(registration.LinkedVia)
	if linkedVia == "" {
		linkedVia = "manual"
	}
	locale := strings.TrimSpace(registration.Locale)
	if locale == "" {
		locale = "ru"
	}
	label := strings.TrimSpace(registration.DeviceLabel)

	_, err := q.Exec(`
		INSERT INTO user_devices (user_id, device_id, platform, device_label, locale, linked_via, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id, platform, device_id)
		DO UPDATE SET
			device_label = EXCLUDED.device_label,
			locale = EXCLUDED.locale,
			linked_via = EXCLUDED.linked_via,
			last_seen_at = CURRENT_TIMESTAMP
	`, userDBID, deviceID, platform, label, locale, linkedVia)
	return err
}

type execQueryer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

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

func ensureFreeSubscription(userDBID int) error {
	var freeGroupID int
	err := db.DB.QueryRow(`SELECT id FROM user_groups WHERE name = 'Free' LIMIT 1`).Scan(&freeGroupID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("load free group: %w", err)
	}
	validUntil := time.Now().AddDate(100, 0, 0) // 100 years
	_, err = db.DB.Exec(`
		INSERT INTO user_subscriptions (user_id, group_id, valid_until)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, group_id) DO NOTHING
	`, userDBID, freeGroupID, validUntil)
	return err
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

func randomToken(bytesCount int) (string, error) {
	buf := make([]byte, bytesCount)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
