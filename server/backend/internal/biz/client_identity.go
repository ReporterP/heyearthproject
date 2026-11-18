package biz

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/heyearth/backend/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// ─────────────────────── Types ───────────────────────

// UserProfile is the full user profile returned to clients.
type UserProfile struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	// MustChangePassword: signed in with a temporary password from support; the cabinet
	// asks for a new one on every page until SetCloudPassword clears it.
	MustChangePassword bool   `json:"must_change_password"`
	PreferredPlatform  string `json:"preferred_platform,omitempty"`
	Locale             string `json:"locale,omitempty"`
	IsBlocked          bool   `json:"is_blocked"`
	CurrentPlan        string `json:"current_plan,omitempty"`
	CurrentPlanSlug    string `json:"current_plan_slug,omitempty"`
	ValidUntil         string `json:"valid_until,omitempty"`
	IsPremium          bool   `json:"is_premium"`
	AccessStatus       string `json:"access_status"`
	StatusReason       string `json:"status_reason,omitempty"`
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
	// Company: the row is the company-paid access (jzd5.24), absent for the member's own.
	Company *SubscriptionCompany `json:"company,omitempty"`
}

// SubscriptionCompany names the company that pays for a subscription row. Status is the
// company's status while the user is a member (active, suspended, inactive), «left» after.
type SubscriptionCompany struct {
	Name   string `json:"name"`
	Status string `json:"status"`
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
	return LoginByIDOrUsernameFrom(idOrUsername, password, "")
}

// LoginByIDOrUsernameFrom is LoginByIDOrUsername with the client's address (site, Mini App,
// gRPC peer) for the wrong-password throttle; "" counts in the account-wide backstop only.
func LoginByIDOrUsernameFrom(idOrUsername, password, source string) (*User, error) {
	login := strings.TrimSpace(idOrUsername)
	user, err := GetUserByIDOrUsername(login)
	if err != nil {
		// A login name is short and guessable: an unknown one answers like a known one with a
		// wrong or missing password (every login name has a password), with a bcrypt run for a
		// like timing, so login names can't be enumerated. A HiEarth ID is a random UUID —
		// nobody guesses one — so «user not found» stays to help a person who mistyped theirs.
		if err.Error() == "user not found" && !LooksLikeHiEarthID(login) {
			if password == "" {
				return nil, fmt.Errorf("password required")
			}
			burnPasswordCheck(password)
			return nil, fmt.Errorf("invalid password")
		}
		return nil, fmt.Errorf("user not found")
	}
	// A blocked account with a password says «blocked» only after the right password: otherwise
	// blocked login names could be enumerated (review of the QA 2026-11-18 fixes).
	hasPassword := user.PasswordHash.Valid && user.PasswordHash.String != ""
	if user.IsBlocked && !hasPassword {
		return nil, fmt.Errorf("user is blocked")
	}

	// If user has a cloud password, verify it
	if user.PasswordHash.Valid && user.PasswordHash.String != "" {
		if password == "" {
			return nil, fmt.Errorf("password required")
		}
		// Wrong passwords are throttled per (account, address): loginFailMax in loginFailWindow,
		// then that address gets the same answer until the window passes — a stranger cannot lock
		// the owner out from elsewhere. An account-wide backstop (loginFailMaxAll per hour from all
		// addresses) stops a distributed guess (security audit F15/F18/F46). The texts stay as they
		// are: the site and the bot match on them.
		if loginThrottled(user.ID, source) {
			burnPasswordCheck(password) // the same timing as a real check
			return nil, fmt.Errorf("invalid password")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(password)); err != nil {
			loginFailed(user.ID, source)
			return nil, fmt.Errorf("invalid password")
		}
		loginSucceeded(user.ID, source)
	}
	if user.IsBlocked {
		return nil, fmt.Errorf("user is blocked")
	}

	touchLastSeen(user.ID)
	return user, nil
}

var hiEarthIDRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// LooksLikeHiEarthID: the input has the shape of a HiEarth ID (a UUID), not of a login name.
func LooksLikeHiEarthID(s string) bool { return hiEarthIDRe.MatchString(s) }

var dummyHash struct {
	once sync.Once
	h    []byte
}

// burnPasswordCheck spends the time of one bcrypt check, so a refusal without a real check
// (unknown login name, throttled address) takes as long as one with it.
func burnPasswordCheck(password string) {
	dummyHash.once.Do(func() {
		dummyHash.h, _ = bcrypt.GenerateFromPassword([]byte("heyearth-dummy-password"), bcrypt.DefaultCost)
	})
	_ = bcrypt.CompareHashAndPassword(dummyHash.h, []byte(password))
}

// ErrTooManyPasswordAttempts: too many wrong current passwords on a password change.
var ErrTooManyPasswordAttempts = errors.New("too many attempts")

// Wrong current passwords on a password change are counted per account (by HiEarth ID): whoever
// guesses holds a session of that very account (a stolen token), so an address split would not help.
const (
	pwChangeFailMax    = 10
	pwChangeFailWindow = 15 * time.Minute
)

func pwChangeKey(userID string) string { return "pw|" + userID }

// pwChangeReserve takes one attempt for the account under one lock, before the slow bcrypt
// check, so parallel requests can't all pass a «still under the limit» test (false when spent).
// A right password gives the attempt back via pwChangeSucceeded.
func pwChangeReserve(userID string) bool {
	loginFails.Lock()
	defer loginFails.Unlock()
	k := pwChangeKey(userID)
	st := loginFails.m[k]
	if st == nil || time.Since(st.start) >= pwChangeFailWindow {
		st = &loginFailState{start: time.Now()}
		loginFails.m[k] = st
	}
	if st.n >= pwChangeFailMax {
		return false
	}
	st.n++
	return true
}

func pwChangeSucceeded(userID string) {
	loginFails.Lock()
	defer loginFails.Unlock()
	delete(loginFails.m, pwChangeKey(userID))
}

const (
	loginFailMax       = 10
	loginFailWindow    = 15 * time.Minute
	loginFailMaxAll    = 200
	loginFailWindowAll = time.Hour
)

var loginFails = struct {
	sync.Mutex
	m map[string]*loginFailState
}{m: map[string]*loginFailState{}}

type loginFailState struct {
	n     int
	start time.Time
}

func loginKeys(userDBID int, source string) (pair, all string) {
	return fmt.Sprintf("%d|%s", userDBID, source), fmt.Sprintf("%d|*", userDBID)
}

func loginCount(k string, window time.Duration) int {
	if st := loginFails.m[k]; st != nil && time.Since(st.start) < window {
		return st.n
	}
	return 0
}

func loginThrottled(userDBID int, source string) bool {
	loginFails.Lock()
	defer loginFails.Unlock()
	pair, all := loginKeys(userDBID, source)
	return (source != "" && loginCount(pair, loginFailWindow) >= loginFailMax) || loginCount(all, loginFailWindowAll) >= loginFailMaxAll
}

func loginFailed(userDBID int, source string) {
	loginFails.Lock()
	defer loginFails.Unlock()
	if len(loginFails.m) > 200000 {
		loginFails.m = map[string]*loginFailState{}
	}
	pair, all := loginKeys(userDBID, source)
	for _, kw := range []struct {
		k string
		w time.Duration
	}{{pair, loginFailWindow}, {all, loginFailWindowAll}} {
		if kw.k == pair && source == "" {
			continue
		}
		st := loginFails.m[kw.k]
		if st == nil || time.Since(st.start) >= kw.w {
			st = &loginFailState{start: time.Now()}
			loginFails.m[kw.k] = st
		}
		st.n++
	}
}

func loginSucceeded(userDBID int, source string) {
	loginFails.Lock()
	defer loginFails.Unlock()
	pair, _ := loginKeys(userDBID, source)
	delete(loginFails.m, pair)
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
// A temporary password from support (ResetPasswordByAdmin) is replaced the same way — it is
// the current password — and the new one must differ from it; the change clears
// password_must_change.
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
		if !pwChangeReserve(user.UserID) {
			return ErrTooManyPasswordAttempts
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash.String), []byte(currentPassword)); err != nil {
			return fmt.Errorf("invalid current password")
		}
		pwChangeSucceeded(user.UserID)
		if user.PasswordMustChange && newPassword == currentPassword {
			return ErrSameAsTemporaryPassword
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
	// Only over the password that was just checked: a reset by support (or another change)
	// in between leaves the current password wrong, not silently overwritten.
	res, err := tx.Exec(`
		UPDATE users SET password_hash = $1, password_must_change = FALSE, updated_at = CURRENT_TIMESTAMP
		 WHERE user_id = $2 AND COALESCE(password_hash, '') = $3
	`, string(hash), userID, user.PasswordHash.String)
	if err != nil {
		return fmt.Errorf("set cloud password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("invalid current password")
	}
	// The other sessions close also when the first password is set: they were opened with the
	// bare HiEarth ID, which may be exactly why a password is being set now (audit F31).
	if _, err = tx.Exec(revokeOtherSessionsQuery, user.ID, hashToken(keepToken)); err != nil {
		return fmt.Errorf("close other sessions: %w", err)
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
	if profile.HasPassword {
		profile.MustChangePassword = user.PasswordMustChange
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

	if profile.IsBlocked {
		profile.AccessStatus = "blocked"
		profile.StatusReason = "user_blocked"
	}
	return profile, nil
}

// GetUserAccessState determines whether a user can connect and their permissions.
// Every read is strict: a database failure is returned as an error, never read
// as "no subscription" / "not a tenant". Callers answer it as a
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

	// A member of an active company has a company-paid subscription row (SyncTenantAccessTx),
	// so membership alone no longer means access: users.tenant_id was never cleared when a
	// member left or the company was suspended, and it let such a client «connect» to nothing.
	hasSub, err := HasActiveSubscription(userID)
	if err != nil {
		return nil, fmt.Errorf("active subscription: %w", err)
	}
	if !hasSub {
		state.CanConnect = false
		state.Reason = "no_active_subscription"
	}

	// Traffic masking is available only for premium users
	state.TrafficMaskingAvailable = profile.IsPremium

	return state, nil
}

// ─────────────────────── Subscriptions & Payments Lists ───────────────────────

// ListUserSubscriptions returns all subscriptions for a user.
func ListUserSubscriptions(userID string) ([]UserSubscriptionDetail, error) {
	user, err := GetOrCreateUser(userID)
	if err != nil {
		return nil, err
	}
	// Live rows first, the member's own before the company-paid one (the order the access is
	// used in, effectiveSubOrderBy): the cabinet shows the first live row as «the» subscription.
	rows, err := db.DB.Query(`
		SELECT us.id, COALESCE(us.plan_id, 0), COALESCE(p.name, 'standard'),
		       us.valid_until, us.auto_renew, us.valid_until > NOW(),
		       COALESCE(p.slug, us.plan, 'standard'), COALESCE(us.price_paid, p.price_rub, us.price_rub, 0),
		       COALESCE(t.name, ''),
		       CASE WHEN t.id IS NULL THEN ''
		            WHEN EXISTS (SELECT 1 FROM tenant_members m WHERE m.tenant_id = t.id AND m.user_id = us.user_id) THEN t.status
		            ELSE 'left' END
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		LEFT JOIN tenants t ON t.id = us.tenant_id
		WHERE us.user_id = $1
		ORDER BY (us.valid_until > NOW()) DESC, (us.tenant_id IS NULL) DESC, us.valid_until DESC
	`, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list user subscriptions: %w", err)
	}
	defer rows.Close()

	var items []UserSubscriptionDetail
	for rows.Next() {
		var item UserSubscriptionDetail
		var validUntil time.Time
		var company, companyStatus string
		if err := rows.Scan(&item.ID, &item.PlanID, &item.PlanName, &validUntil, &item.AutoRenew, &item.Active, &item.Plan, &item.PriceRub,
			&company, &companyStatus); err != nil {
			return nil, err
		}
		item.ValidUntil = validUntil.Format(time.RFC3339)
		if companyStatus != "" {
			item.Company = &SubscriptionCompany{Name: company, Status: companyStatus}
		}
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
