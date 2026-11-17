package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

func ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Search != "" {
		where += fmt.Sprintf(" AND (u.user_id ILIKE $%d OR COALESCE(u.username,'') ILIKE $%d OR COALESCE(u.role,'') ILIKE $%d)", argN, argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}
	// Status filter:
	//   "active"   = not blocked AND has valid subscription (совпадает с Dashboard active_users)
	//   "blocked"  = blocked
	//   "expiring" = not blocked AND has subscription expiring in the next 7 days
	//   "no_plan"  = no active (valid_until > NOW) subscription
	if p.Status == "active" {
		where += ` AND COALESCE(u.is_blocked, FALSE) = FALSE
			AND EXISTS (SELECT 1 FROM user_subscriptions us_f
			            WHERE us_f.user_id = u.id AND us_f.valid_until > NOW())`
	} else if p.Status == "blocked" {
		where += " AND COALESCE(u.is_blocked, FALSE) = TRUE"
	} else if p.Status == "expiring" {
		where += ` AND COALESCE(u.is_blocked, FALSE) = FALSE
			AND EXISTS (SELECT 1 FROM user_subscriptions us_f
			            WHERE us_f.user_id = u.id
			              AND us_f.valid_until BETWEEN NOW() AND NOW() + INTERVAL '7 days')`
	} else if p.Status == "no_plan" {
		where += ` AND NOT EXISTS (SELECT 1 FROM user_subscriptions us_f
			            WHERE us_f.user_id = u.id AND us_f.valid_until > NOW())`
	}

	var total int
	err := db.DB.QueryRow("SELECT COUNT(*) FROM users u WHERE "+where, args...).Scan(&total)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count users")
		return
	}

	// Sortable columns whitelist (Phase 1 Task 3).
	// Map keys are accepted ?sort= values; map values are the actual SQL
	// expressions (with proper alias prefix). Invalid values fall back to
	// the default ORDER BY.
	allowedSort := map[string]string{
		"id":                "u.id",
		"user_id":           "u.user_id",
		"username":          "u.username",
		"created_at":        "u.created_at",
		"updated_at":        "u.updated_at",
		"role":              "u.role",
		"last_seen":         "ls.last_seen",
		"traffic_30d_bytes": "t30.total_bytes",
		"valid_until":       "us.valid_until",
	}
	sortCol := "u.created_at"
	if v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("sort"))); v != "" {
		if c, ok := allowedSort[v]; ok {
			sortCol = c
		}
	}
	dir := "DESC"
	if d := strings.ToLower(r.URL.Query().Get("dir")); d == "asc" {
		dir = "ASC"
	} else if d == "desc" {
		dir = "DESC"
	}

	// NULLS LAST for nullable sort columns so missing data sinks to the end
	// regardless of direction (consistent UX across asc/desc).
	nullsClause := ""
	if sortCol == "ls.last_seen" || sortCol == "t30.total_bytes" || sortCol == "us.valid_until" {
		nullsClause = " NULLS LAST"
	}

	query := fmt.Sprintf(`
		SELECT u.id, u.user_id, COALESCE(u.username, ''), u.telegram_id, u.role, COALESCE(u.is_blocked, FALSE),
		       COALESCE(u.preferred_platform, ''),
		       u.created_at, COALESCE(u.updated_at, u.created_at),
		       COALESCE(p.name, 'Нет подписки') AS current_plan,
		       us.valid_until,
		       ls.last_seen,
		       COALESCE(t30.total_bytes, 0) AS traffic_30d_bytes,
		       COALESCE(p.traffic_limit_bytes, 0) AS traffic_limit_bytes,
		       COALESCE(u.password_hash, '') <> '' AS has_password,
		       COALESCE(u.password_must_change, FALSE), u.password_reset_at,
		       (u.email_verified_at IS NOT NULL AND COALESCE(u.email, '') <> '') AS has_confirmed_email
		FROM users u
		LEFT JOIN LATERAL (
			SELECT us2.plan_id, us2.valid_until
			FROM user_subscriptions us2
			WHERE us2.user_id = u.id AND us2.valid_until > NOW()
			ORDER BY us2.valid_until DESC
			LIMIT 1
		) us ON TRUE
		LEFT JOIN plans p ON p.id = us.plan_id
		LEFT JOIN LATERAL (
			SELECT COALESCE(
			         (SELECT MAX(lt.recorded_at) FROM traffic_stats lt WHERE lt.user_id = u.user_id),
			         (SELECT MAX(ld.day)::timestamptz FROM traffic_stats_daily ld WHERE ld.user_id = u.user_id)
			       ) AS last_seen
		) ls ON TRUE
		LEFT JOIN LATERAL (
			SELECT COALESCE(SUM(ts.upload_bytes + ts.download_bytes), 0) AS total_bytes
			FROM traffic_stats ts
			WHERE ts.user_id = u.user_id
			  AND ts.recorded_at >= NOW() - INTERVAL '30 days'
		) t30 ON TRUE
		WHERE %s
		ORDER BY %s %s%s, u.id DESC
		LIMIT $%d OFFSET $%d
	`, where, sortCol, dir, nullsClause, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list users")
		return
	}
	defer rows.Close()

	var users []map[string]any
	for rows.Next() {
		var row adminUserRow
		if err := rows.Scan(
			&row.ID, &row.UserID, &row.Username, &row.TelegramID, &row.Role, &row.IsBlocked, &row.PreferredPlatform,
			&row.CreatedAt, &row.UpdatedAt, &row.CurrentPlan, &row.ValidUntil,
			&row.LastSeen, &row.Traffic30dBytes, &row.TrafficLimitBytes,
			&row.HasPassword, &row.PasswordMustChange, &row.PasswordResetAt, &row.HasConfirmedEmail,
		); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan user")
			return
		}
		payload := map[string]any{
			"id":                  row.ID,
			"user_id":             row.UserID,
			"username":            row.Username,
			"role":                row.Role,
			"is_blocked":          row.IsBlocked,
			"preferred_platform":  row.PreferredPlatform,
			"current_plan":        row.CurrentPlan,
			"created_at":          row.CreatedAt.Format(time.RFC3339),
			"updated_at":          row.UpdatedAt.Format(time.RFC3339),
			"traffic_30d_bytes":   row.Traffic30dBytes,
			"traffic_limit_bytes": row.TrafficLimitBytes,
			// «Сбросить пароль»: only an account with a password has one to reset; the
			// dialog says in advance whether the temporary password also goes by mail.
			"has_password":         row.HasPassword,
			"password_must_change": row.HasPassword && row.PasswordMustChange,
			"has_confirmed_email":  row.HasConfirmedEmail,
		}
		if row.PasswordResetAt.Valid {
			payload["password_reset_at"] = row.PasswordResetAt.Time.Format(time.RFC3339)
		}
		if row.TelegramID.Valid {
			payload["telegram_id"] = row.TelegramID.Int64
		}
		if row.ValidUntil.Valid {
			payload["valid_until"] = row.ValidUntil.Time.Format(time.RFC3339)
		} else {
			payload["valid_until"] = ""
		}
		if row.LastSeen.Valid {
			payload["last_seen"] = row.LastSeen.Time.Format(time.RFC3339)
		} else {
			payload["last_seen"] = nil
		}
		users = append(users, payload)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: users, Total: total, Page: p.Page, PageSize: p.PageSize})
}

// userHasMoneyQuery: any paid payment or wallet movement of the user.
const userHasMoneyQuery = `SELECT EXISTS (SELECT 1 FROM payments p JOIN users u ON u.id = $1 WHERE p.user_id = u.id OR p.user_id_text = u.user_id)
	OR EXISTS (SELECT 1 FROM wallet_transactions WHERE user_id = $1)`

func UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	var payload userPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса (telegram_id — число)")
		return
	}

	var telegramValue any
	if payload.TelegramID != nil {
		if *payload.TelegramID <= 0 {
			writeAdminError(w, http.StatusBadRequest, "Telegram ID — положительное число")
			return
		}
		telegramValue = *payload.TelegramID
	}
	// The HiEarth ID and the Telegram ID both open the account (an ID-only sign-in, a Mini App
	// sign-in of that Telegram): changing them is the same power as a password reset (audit F12).
	identityChange := strings.TrimSpace(payload.UserID) != "" || payload.TelegramID != nil
	if identityChange {
		claims, _ := ClaimsFromContext(r.Context())
		if claims == nil || !claimsAllow(claims, "users.password_reset") {
			writeAdminError(w, http.StatusForbidden, "HiEarth ID и Telegram ID меняет администратор с правом «Сбросить пароль» — они открывают вход в аккаунт")
			return
		}
	}
	platform := strings.ToLower(strings.TrimSpace(payload.PreferredPlatform))
	if _, err := db.DB.Exec(`
		UPDATE users
		SET user_id = COALESCE(NULLIF($1, ''), user_id),
		    telegram_id = CASE WHEN $2::bigint IS NULL THEN telegram_id ELSE $2 END,
		    role = COALESCE(NULLIF($3, ''), role),
		    is_blocked = COALESCE($4, is_blocked),
		    preferred_platform = CASE WHEN $5 = '' THEN preferred_platform ELSE $5 END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $6
	`, strings.TrimSpace(payload.UserID), telegramValue, strings.TrimSpace(payload.Role), payload.IsBlocked, platform, userID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить пользователя")
		return
	}
	WriteAuditLog(r, "update_user", "user", fmt.Sprintf("%d", userID), payload)
	if identityChange {
		// The old ID / Telegram must not keep a way in.
		_, _ = db.DB.Exec(`DELETE FROM user_sessions WHERE user_id = $1`, userID)
	}
	if payload.IsBlocked != nil {
		biz.NotifySubscriptionChange() // a ban or an unban reaches xray and Bamboo at once
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "User updated"})
}

func DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	// Money stays on record (54-ФЗ receipts, the wallet ledger): a user with any payment (pending
	// ones too) or a wallet movement is blocked, never deleted (C2).
	var hasMoney bool
	if err := db.DB.QueryRow(userHasMoneyQuery, userID).Scan(&hasMoney); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось проверить платежи пользователя")
		return
	}
	if hasMoney {
		writeAdminError(w, http.StatusConflict, "У пользователя есть платежи (в том числе незавершённые) или движения по кошельку — удалять нельзя (учёт и чеки). Заблокируйте его")
		return
	}
	result, err := db.DB.Exec(`DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete user")
		return
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		writeAdminError(w, http.StatusNotFound, "User not found")
		return
	}
	WriteAuditLog(r, "delete_user", "user", fmt.Sprintf("%d", userID), nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "User deleted"})
}

// ListUserTrafficHandler — GET /api/admin/users/{id}/traffic?days=N
//
// Returns daily upload+download breakdown for the user (joined via users.user_id),
// total bytes over the window, the user's current plan limit, and a per-country
// breakdown. by_country is always [] in v1 — traffic_stats has no server_id /
// exit_node_id column, so bytes cannot be attributed to an exit country.
//
// `days` is clamped to [1, 90]; default 30.
func ListUserTrafficHandler(w http.ResponseWriter, r *http.Request) {
	// Path: /api/admin/users/{id}/traffic
	userDbID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "traffic")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}
	var userUUID string
	if err := db.DB.QueryRow(`SELECT user_id FROM users WHERE id = $1`, userDbID).Scan(&userUUID); err != nil {
		writeAdminError(w, http.StatusNotFound, "User not found")
		return
	}
	resp, err := biz.BuildTrafficResponse(userDbID, userUUID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, resp)
}
