package biz

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// User represents a HeYearth user in the database.
type User struct {
	ID           int
	UserID       string
	TelegramID   sql.NullInt64 // legacy, kept for migration compatibility
	Username     sql.NullString
	PasswordHash sql.NullString
	// PasswordMustChange: the password is a temporary one support issued
	// (ResetPasswordByAdmin); SetCloudPassword clears it.
	PasswordMustChange bool
	Role               string
	IsBlocked          bool
	PreferredPlatform  string
	Locale             string
	VlessUUID          string // per-user UUID для VLESS подключения
}

// Subscription represents an active user subscription.
type Subscription struct {
	ID         int
	PlanID     int
	PlanName   string
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

// ActiveVlessClient — VLESS клиент с информацией о premium для Xray policy level.
// The servers of the client's plan are not part of it: the xray sync builds
// the per-plan balancers from plan_servers itself (ListPlanServersContext),
// independently of which users a plan currently has.
type ActiveVlessClient struct {
	UUID      string
	UserID    string
	IsPremium bool
	PlanID    int
}

// ListActiveVlessClientsContext is ListActiveVlessClients bounded by ctx. Any
// query, scan or iteration error is returned: the xray sync must never build a
// config from a partial client list (that would revoke the missing users).
func ListActiveVlessClientsContext(ctx context.Context) ([]ActiveVlessClient, error) {
	rows, err := db.DB.QueryContext(ctx, activeVlessClientsQuery)
	if err != nil {
		return nil, fmt.Errorf("active vless clients: %w", err)
	}
	defer rows.Close()
	var result []ActiveVlessClient
	for rows.Next() {
		var c ActiveVlessClient
		if err := rows.Scan(&c.UUID, &c.UserID, &c.IsPremium, &c.PlanID); err != nil {
			return nil, fmt.Errorf("active vless clients scan: %w", err)
		}
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("active vless clients rows: %w", err)
	}
	return result, nil
}

// activeVlessClientsQuery lists the VLESS clients of ListActiveVlessClients.
// Premium derivation: any active subscription = premium (groups removed).
// best_plan_id is the plan of the effective subscription (effectiveSubOrderBy).
//
// Traffic is NOT filtered here: the per-cycle quota of Reality/XHTTP is the
// quota cutoff of the xray sync (ListQuotaExceededUserIDs, armed only by
// app_settings quota_cutoff_enabled). The former legacy filter
// `us.traffic_limit IS NULL OR us.traffic_used < us.traffic_limit` judged the
// lifetime counter traffic_used and bypassed that flag; nothing writes
// us.traffic_limit any more. Hysteria2 (Express) keeps its own, independent
// quota on purpose (hysteria2AuthPlanQuery, ValidateHysteria2Auth).
var activeVlessClientsQuery = `
		SELECT u.vless_uuid::text, u.user_id,
		       TRUE AS is_premium,
		       COALESCE(
		         (SELECT sub.plan_id FROM user_subscriptions sub
		          WHERE sub.user_id = u.id AND sub.plan_id IS NOT NULL
		            AND sub.valid_until > CURRENT_TIMESTAMP
		          ORDER BY ` + effectiveSubOrderBy("sub") + ` LIMIT 1),
		         0
		       ) AS best_plan_id
		FROM users u
		JOIN user_subscriptions us ON us.user_id = u.id
		WHERE u.vless_uuid IS NOT NULL
		  AND u.is_blocked = FALSE
		  AND us.valid_until > CURRENT_TIMESTAMP
		GROUP BY u.vless_uuid, u.user_id, u.id
		ORDER BY u.user_id
	`

// Revocation reasons reported by RevocationReasonsContext.
const (
	RevokedBlocked = "blocked" // users.is_blocked
	RevokedExpired = "expired" // no active subscription left
	RevokedNoUUID  = "no-uuid" // users.vless_uuid cleared
	RevokedDeleted = "deleted" // no such user any more
	RevokedUnknown = "unknown" // still qualifies as a VLESS client (or the lookup failed)
)

// RevocationReasonsContext explains why users (users.user_id, compared
// case-insensitively like xray emails) are no longer VLESS clients of
// activeVlessClientsQuery. Keys of the result are lower-cased. For logs and
// alerts only — it decides nothing.
func RevocationReasonsContext(ctx context.Context, userIDs []string) (map[string]string, error) {
	lower := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		lower = append(lower, strings.ToLower(id))
	}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT lower(u.user_id), u.is_blocked, u.vless_uuid IS NOT NULL,
		       EXISTS (SELECT 1 FROM user_subscriptions us
		               WHERE us.user_id = u.id AND us.valid_until > CURRENT_TIMESTAMP)
		FROM users u
		WHERE lower(u.user_id) = ANY($1)
	`, pq.Array(lower))
	if err != nil {
		return nil, fmt.Errorf("revocation reasons: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string, len(lower))
	for _, id := range lower {
		out[id] = RevokedDeleted
	}
	for rows.Next() {
		var id string
		var blocked, hasUUID, active bool
		if err := rows.Scan(&id, &blocked, &hasUUID, &active); err != nil {
			return nil, fmt.Errorf("revocation reasons scan: %w", err)
		}
		switch {
		case blocked:
			out[id] = RevokedBlocked
		case !active:
			out[id] = RevokedExpired
		case !hasUUID:
			out[id] = RevokedNoUUID
		default:
			out[id] = RevokedUnknown
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("revocation reasons rows: %w", err)
	}
	return out, nil
}

// effectivePlanIDQuery is best_plan_id of activeVlessClientsQuery for one user
// (users.user_id = $1): same filters, same ordering.
var effectivePlanIDQuery = `
		SELECT us.plan_id
		FROM user_subscriptions us
		JOIN users u ON u.id = us.user_id
		WHERE u.user_id = $1 AND us.plan_id IS NOT NULL
		  AND us.valid_until > CURRENT_TIMESTAMP
		ORDER BY ` + effectiveSubOrderBy("us") + `
		LIMIT 1
	`

// EffectivePlanID returns the plan of the user's effective subscription — the
// PlanID ListActiveVlessClients reports, i.e. the plan the Hub routes the user
// by — so per-plan client settings (the Happ routing deeplink) match the Hub's
// per-plan rules. (0, nil) when the user has no active subscription with a plan;
// a database failure is returned as an error.
func EffectivePlanID(userID string) (int, error) {
	var planID int
	err := db.DB.QueryRow(effectivePlanIDQuery, userID).Scan(&planID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("effective plan: %w", err)
	}
	return planID, nil
}

func getUserByField(field, value string) (*User, error) {
	var user User
	query := fmt.Sprintf(`
		SELECT id, user_id, telegram_id, COALESCE(username, ''), COALESCE(password_hash, ''),
		       role, COALESCE(is_blocked, FALSE), COALESCE(preferred_platform, ''), COALESCE(locale, 'ru'),
		       COALESCE(vless_uuid::text, ''), COALESCE(password_must_change, FALSE)
		FROM users WHERE %s = $1
	`, field)
	var username, passwordHash string
	err := db.DB.QueryRow(query, value).Scan(
		&user.ID, &user.UserID, &user.TelegramID, &username, &passwordHash,
		&user.Role, &user.IsBlocked, &user.PreferredPlatform, &user.Locale,
		&user.VlessUUID, &user.PasswordMustChange,
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

// GetUserSubscription returns the best active subscription for a user.
// Returns the longest-running active subscription (by valid_until), the member's own before
// a company-paid one.
func GetUserSubscription(userID int) (*Subscription, error) {
	var sub Subscription
	var planID sql.NullInt64
	err := db.DB.QueryRow(`
		SELECT us.id,
		       COALESCE(us.plan_id, 0),
		       COALESCE(p.name, 'standard'),
		       COALESCE(p.price_rub > 200, FALSE),
		       COALESCE(p.slug, us.plan, 'standard'),
		       COALESCE(us.price_paid, p.price_rub, us.price_rub, 0),
		       us.valid_until
		FROM user_subscriptions us
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE us.user_id = $1 AND us.valid_until > NOW()
		ORDER BY (us.tenant_id IS NULL) DESC, us.valid_until DESC
		LIMIT 1
	`, userID).Scan(&sub.ID, &planID, &sub.PlanName, &sub.IsPremium, &sub.Plan, &sub.PriceRub, &sub.ValidUntil)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	if planID.Valid {
		sub.PlanID = int(planID.Int64)
	}
	return &sub, nil
}

// UserHasActiveSubscription returns true if user has at least one
// non-expired subscription, regardless of plan.
func UserHasActiveSubscription(userID string) (bool, error) {
	var count int
	err := db.DB.QueryRow(`
		SELECT COUNT(*) FROM user_subscriptions us
		 JOIN users u ON u.id = us.user_id
		 WHERE u.user_id = $1 AND us.valid_until > NOW()
	`, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
