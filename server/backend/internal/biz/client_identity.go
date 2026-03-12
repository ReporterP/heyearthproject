package biz

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

type UserProfile struct {
	UserID             string
	TelegramID         int64
	TelegramLinked     bool
	PreferredPlatform  string
	Locale             string
	IsBlocked          bool
	CurrentPlan        string
	ValidUntil         string
	LegacyHappFallback bool
	AccessStatus       string
	StatusReason       string
}

type UserAccessState struct {
	UserID                     string
	CurrentPlan                string
	ValidUntil                 string
	PreferredPlatform          string
	IsBlocked                  bool
	CanConnect                 bool
	LegacyHappFallback         bool
	Reason                     string
	AllowManualConnectionChoice bool
}

type PairingCode struct {
	ID                int
	UserDBID          int
	UserID            string
	TelegramID        sql.NullInt64
	Code              string
	ClaimToken        string
	PreferredPlatform string
	ExpiresAt         time.Time
	ConsumedAt        sql.NullTime
	CreatedAt         time.Time
}

type DeviceRegistration struct {
	DeviceID    string
	Platform    string
	DeviceLabel string
	Locale      string
	LinkedVia   string
}

type UserSubscriptionDetail struct {
	GroupID    int
	GroupName  string
	ValidUntil string
	AutoRenew  bool
	Active     bool
}

type UserPaymentDetail struct {
	ID          int
	Amount      float64
	Currency    string
	Status      string
	Provider    string
	ExternalID  string
	Notes       string
	CreatedAt   string
	ConfirmedAt string
}

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

func EnsureTelegramUser(telegramID int64, preferredPlatform, locale string) (*User, error) {
	var user User
	err := db.DB.QueryRow(`
		SELECT id, user_id, telegram_id, role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru')
		FROM users
		WHERE telegram_id = $1
	`, telegramID).Scan(&user.ID, &user.UserID, &user.TelegramID, &user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale)
	if err == nil {
		if err := updateUserProfileHints(user.ID, preferredPlatform, locale); err != nil {
			return nil, err
		}
		return GetUserByCanonicalID(user.UserID)
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("query telegram user: %w", err)
	}

	userID := newCanonicalUserID()
	user, err = createUserRecord(userID, sql.NullInt64{Int64: telegramID, Valid: true}, preferredPlatform, locale)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func GetUserByCanonicalID(userID string) (*User, error) {
	var user User
	err := db.DB.QueryRow(`
		SELECT id, user_id, telegram_id, role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru')
		FROM users
		WHERE user_id = $1
	`, strings.TrimSpace(userID)).Scan(&user.ID, &user.UserID, &user.TelegramID, &user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func UpsertLegacyTelegramUser(telegramID int64, canonicalUserID, preferredPlatform, locale string) error {
	canonicalUserID = strings.TrimSpace(canonicalUserID)
	if canonicalUserID == "" {
		canonicalUserID = newCanonicalUserID()
	}
	platform := NormalizePreferredPlatform(preferredPlatform)
	if locale == "" {
		locale = "ru"
	}

	_, err := db.DB.Exec(`
		INSERT INTO users (user_id, telegram_id, preferred_platform, locale, last_seen_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id)
		DO UPDATE SET
			telegram_id = EXCLUDED.telegram_id,
			preferred_platform = CASE WHEN EXCLUDED.preferred_platform <> '' THEN EXCLUDED.preferred_platform ELSE users.preferred_platform END,
			locale = COALESCE(NULLIF(EXCLUDED.locale, ''), users.locale),
			last_seen_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`, canonicalUserID, telegramID, platform, locale)
	if err != nil {
		return fmt.Errorf("upsert legacy telegram user: %w", err)
	}

	user, err := GetOrCreateUser(canonicalUserID)
	if err != nil {
		return err
	}
	if err := ensureFreeSubscription(user.ID); err != nil {
		return err
	}
	return nil
}

func SetPreferredPlatformByTelegram(telegramID int64, platform string) (*User, error) {
	user, err := EnsureTelegramUser(telegramID, platform, "ru")
	if err != nil {
		return nil, err
	}
	_, err = db.DB.Exec(`
		UPDATE users
		SET preferred_platform = $1, updated_at = CURRENT_TIMESTAMP, last_seen_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, NormalizePreferredPlatform(platform), user.ID)
	if err != nil {
		return nil, fmt.Errorf("update preferred platform: %w", err)
	}
	return GetUserByCanonicalID(user.UserID)
}

func CreatePairingCode(telegramID int64, preferredPlatform, locale string, ttl time.Duration) (*User, *PairingCode, error) {
	user, err := EnsureTelegramUser(telegramID, preferredPlatform, locale)
	if err != nil {
		return nil, nil, err
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}

	code, err := randomReadableCode(8)
	if err != nil {
		return nil, nil, err
	}
	token, err := randomToken(32)
	if err != nil {
		return nil, nil, err
	}
	expiresAt := time.Now().Add(ttl)

	pairing := &PairingCode{}
	err = db.DB.QueryRow(`
		INSERT INTO pairing_codes (user_id, telegram_id, code, claim_token, preferred_platform, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, telegram_id, code, claim_token, preferred_platform, expires_at, consumed_at, created_at
	`, user.ID, telegramID, code, token, NormalizePreferredPlatform(preferredPlatform), expiresAt).Scan(
		&pairing.ID,
		&pairing.UserDBID,
		&pairing.TelegramID,
		&pairing.Code,
		&pairing.ClaimToken,
		&pairing.PreferredPlatform,
		&pairing.ExpiresAt,
		&pairing.ConsumedAt,
		&pairing.CreatedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("insert pairing code: %w", err)
	}
	pairing.UserID = user.UserID
	return user, pairing, nil
}

func ClaimPairingCodeByCode(code string, registration DeviceRegistration) (*User, *PairingCode, error) {
	return claimPairingCode("code", strings.TrimSpace(strings.ToUpper(code)), registration)
}

func ClaimPairingCodeByToken(token string, registration DeviceRegistration) (*User, *PairingCode, error) {
	return claimPairingCode("claim_token", strings.TrimSpace(token), registration)
}

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
		UserID:             user.UserID,
		PreferredPlatform:  user.PreferredPlatform,
		Locale:             user.Locale,
		IsBlocked:          user.IsBlocked,
		TelegramLinked:     user.TelegramID.Valid,
		LegacyHappFallback: user.PreferredPlatform == "ios" || user.PreferredPlatform == "macos",
		AccessStatus:       "active",
	}
	if user.TelegramID.Valid {
		profile.TelegramID = user.TelegramID.Int64
	}
	if sub == nil {
		profile.CurrentPlan = "Free"
	} else {
		profile.CurrentPlan = sub.GroupName
		profile.ValidUntil = sub.ValidUntil.Format(time.RFC3339)
	}
	if profile.IsBlocked {
		profile.AccessStatus = "blocked"
		profile.StatusReason = "user_blocked"
	}
	return profile, nil
}

func GetUserAccessState(userID string, platform string) (*UserAccessState, error) {
	profile, err := GetUserProfile(userID)
	if err != nil {
		return nil, err
	}

	resolvedPlatform := NormalizePreferredPlatform(platform)
	if resolvedPlatform == "" {
		resolvedPlatform = profile.PreferredPlatform
	}

	state := &UserAccessState{
		UserID:             profile.UserID,
		CurrentPlan:        profile.CurrentPlan,
		ValidUntil:         profile.ValidUntil,
		PreferredPlatform:  profile.PreferredPlatform,
		IsBlocked:          profile.IsBlocked,
		CanConnect:         !profile.IsBlocked,
		LegacyHappFallback: resolvedPlatform == "ios" || resolvedPlatform == "macos",
	}
	if profile.IsBlocked {
		state.Reason = "user_blocked"
		return state, nil
	}

	hasSub, _ := HasActiveSubscription(userID)
	isTenantUser := isMemberOfTenant(userID)
	if !hasSub && !isTenantUser {
		state.CanConnect = false
		state.Reason = "no_active_subscription"
	}

	if state.LegacyHappFallback {
		state.CanConnect = false
		state.Reason = "legacy_happ_fallback"
	}

	state.AllowManualConnectionChoice = isUserPremium(userID)
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

func isUserPremium(userID string) bool {
	var premium bool
	err := db.DB.QueryRow(`
		SELECT COALESCE(ug.is_premium, FALSE)
		FROM users u
		JOIN user_subscriptions us ON us.user_id = u.id
		JOIN user_groups ug ON ug.id = us.group_id
		WHERE u.user_id = $1 AND us.valid_until > NOW()
		ORDER BY ug.is_premium DESC
		LIMIT 1
	`, strings.TrimSpace(userID)).Scan(&premium)
	if err != nil {
		return false
	}
	return premium
}

func ListUserSubscriptions(userID string) ([]UserSubscriptionDetail, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	rows, err := db.DB.Query(`
		SELECT us.group_id, ug.name, us.valid_until, us.auto_renew, us.valid_until > NOW()
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
		if err := rows.Scan(&item.GroupID, &item.GroupName, &validUntil, &item.AutoRenew, &item.Active); err != nil {
			return nil, err
		}
		item.ValidUntil = validUntil.Format(time.RFC3339)
		items = append(items, item)
	}
	return items, rows.Err()
}

func ListUserPayments(userID string) ([]UserPaymentDetail, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	rows, err := db.DB.Query(`
		SELECT id, amount::float8, currency, status, COALESCE(provider, ''), COALESCE(external_id, ''), COALESCE(notes, ''),
		       created_at, confirmed_at
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
		if err := rows.Scan(&item.ID, &item.Amount, &item.Currency, &item.Status, &item.Provider, &item.ExternalID, &item.Notes, &createdAt, &confirmedAt); err != nil {
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

func RegisterUserDevice(userID string, registration DeviceRegistration) error {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return err
	}
	return registerUserDevice(db.DB, user.ID, nil, registration)
}

func updateUserProfileHints(userDBID int, preferredPlatform, locale string) error {
	platform := NormalizePreferredPlatform(preferredPlatform)
	if locale == "" {
		locale = "ru"
	}
	_, err := db.DB.Exec(`
		UPDATE users
		SET preferred_platform = CASE WHEN $1 <> '' THEN $1 ELSE preferred_platform END,
		    locale = COALESCE(NULLIF($2, ''), locale),
		    last_seen_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`, platform, strings.TrimSpace(locale), userDBID)
	return err
}

func claimPairingCode(column, value string, registration DeviceRegistration) (*User, *PairingCode, error) {
	if value == "" {
		return nil, nil, fmt.Errorf("pairing value is required")
	}

	tx, err := db.DB.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	query := fmt.Sprintf(`
		SELECT pc.id, pc.user_id, u.user_id, pc.telegram_id, pc.code, pc.claim_token, pc.preferred_platform, pc.expires_at, pc.consumed_at, pc.created_at
		FROM pairing_codes pc
		JOIN users u ON u.id = pc.user_id
		WHERE pc.%s = $1
		FOR UPDATE
	`, column)
	var pairing PairingCode
	if err := tx.QueryRow(query, value).Scan(
		&pairing.ID,
		&pairing.UserDBID,
		&pairing.UserID,
		&pairing.TelegramID,
		&pairing.Code,
		&pairing.ClaimToken,
		&pairing.PreferredPlatform,
		&pairing.ExpiresAt,
		&pairing.ConsumedAt,
		&pairing.CreatedAt,
	); err == sql.ErrNoRows {
		return nil, nil, fmt.Errorf("pairing code not found")
	} else if err != nil {
		return nil, nil, err
	}

	if pairing.ConsumedAt.Valid {
		return nil, nil, fmt.Errorf("pairing code already used")
	}
	if time.Now().After(pairing.ExpiresAt) {
		return nil, nil, fmt.Errorf("pairing code expired")
	}

	if _, err := tx.Exec(`UPDATE pairing_codes SET consumed_at = CURRENT_TIMESTAMP WHERE id = $1`, pairing.ID); err != nil {
		return nil, nil, err
	}
	if err := registerUserDevice(tx, pairing.UserDBID, &pairing.ID, registration); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(`
		UPDATE users
		SET preferred_platform = CASE WHEN $1 <> '' THEN $1 ELSE preferred_platform END,
		    locale = COALESCE(NULLIF($2, ''), locale),
		    last_seen_at = CURRENT_TIMESTAMP,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`, NormalizePreferredPlatform(registration.Platform), strings.TrimSpace(registration.Locale), pairing.UserDBID); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}

	user, err := GetUserByCanonicalID(pairing.UserID)
	if err != nil {
		return nil, nil, err
	}
	return user, &pairing, nil
}

type execQueryer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func registerUserDevice(q execQueryer, userDBID int, pairingCodeID *int, registration DeviceRegistration) error {
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
		INSERT INTO user_devices (user_id, device_id, platform, device_label, locale, linked_via, pairing_code_id, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
		ON CONFLICT (user_id, platform, device_id)
		DO UPDATE SET
			device_label = EXCLUDED.device_label,
			locale = EXCLUDED.locale,
			linked_via = EXCLUDED.linked_via,
			pairing_code_id = EXCLUDED.pairing_code_id,
			last_seen_at = CURRENT_TIMESTAMP
	`, userDBID, deviceID, platform, label, locale, linkedVia, pairingCodeID)
	return err
}

func createUserRecord(userID string, telegramID sql.NullInt64, preferredPlatform, locale string) (User, error) {
	platform := NormalizePreferredPlatform(preferredPlatform)
	if locale == "" {
		locale = "ru"
	}
	var user User
	err := db.DB.QueryRow(`
		INSERT INTO users (user_id, telegram_id, preferred_platform, locale, last_seen_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		RETURNING id, user_id, telegram_id, role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru')
	`, userID, telegramID, platform, locale).Scan(&user.ID, &user.UserID, &user.TelegramID, &user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	if err := ensureFreeSubscription(user.ID); err != nil {
		return User{}, err
	}
	return user, nil
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
	validUntil := time.Now().AddDate(100, 0, 0)
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

func randomReadableCode(length int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	buf := make([]byte, length)
	randomBytes := make([]byte, length)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = alphabet[int(randomBytes[i])%len(alphabet)]
	}
	return string(buf), nil
}
