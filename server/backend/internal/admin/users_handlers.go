package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

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
		       COALESCE(p.traffic_limit_bytes, 0) AS traffic_limit_bytes
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
			SELECT MAX(ud.last_seen_at) AS last_seen
			FROM user_devices ud
			WHERE ud.user_id = u.id
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

func CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	var payload userPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.UserID) == "" {
		writeAdminError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	isBlocked := false
	if payload.IsBlocked != nil {
		isBlocked = *payload.IsBlocked
	}
	role := strings.TrimSpace(payload.Role)
	if role == "" {
		role = "user"
	}

	var telegramValue any
	if payload.TelegramID != nil {
		telegramValue = *payload.TelegramID
	}

	var id int
	err := db.DB.QueryRow(`
		INSERT INTO users (user_id, telegram_id, role, is_blocked, updated_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
		RETURNING id
	`, strings.TrimSpace(payload.UserID), telegramValue, role, isBlocked).Scan(&id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create user")
		return
	}
	WriteAuditLog(r, "create_user", "user", fmt.Sprintf("%d", id), map[string]string{"user_id": payload.UserID})
	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	var payload userPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var telegramValue any
	if payload.TelegramID != nil {
		telegramValue = *payload.TelegramID
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
		writeAdminError(w, http.StatusInternalServerError, "Failed to update user")
		return
	}
	WriteAuditLog(r, "update_user", "user", fmt.Sprintf("%d", userID), nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "User updated"})
}

func DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
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

func ListUserDevicesHandler(w http.ResponseWriter, r *http.Request) {
	userID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "devices")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	rows, err := db.DB.Query(`
		SELECT id, platform, device_id, COALESCE(device_label, ''), COALESCE(linked_via, ''), created_at, last_seen_at
		FROM user_devices
		WHERE user_id = $1
		ORDER BY last_seen_at DESC
	`, userID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list devices")
		return
	}
	defer rows.Close()

	var devices []map[string]any
	for rows.Next() {
		var (
			id          int
			platform    string
			deviceID    string
			deviceLabel string
			linkedVia   string
			createdAt   time.Time
			lastSeenAt  time.Time
		)
		if err := rows.Scan(&id, &platform, &deviceID, &deviceLabel, &linkedVia, &createdAt, &lastSeenAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan device")
			return
		}
		devices = append(devices, map[string]any{
			"id":           id,
			"platform":     platform,
			"device_id":    deviceID,
			"device_label": deviceLabel,
			"linked_via":   linkedVia,
			"created_at":   createdAt.Format(time.RFC3339),
			"last_seen_at": lastSeenAt.Format(time.RFC3339),
		})
	}
	if devices == nil {
		devices = []map[string]any{}
	}
	writeAdminJSON(w, http.StatusOK, devices)
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
	userDbID, ok := parseLastPathSegmentBeforeFinal(r.URL.Path, "traffic")
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid user id")
		return
	}

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			days = n
		}
	}

	// Look up user_id (TEXT UUID) — traffic_stats joins on users.user_id,
	// not users.id — and the current plan limit.
	var userTextID string
	var limitBytes int64
	err := db.DB.QueryRow(`
		SELECT u.user_id, COALESCE(p.traffic_limit_bytes, 0)
		FROM users u
		LEFT JOIN LATERAL (
			SELECT us2.plan_id
			FROM user_subscriptions us2
			WHERE us2.user_id = u.id AND us2.valid_until > NOW()
			ORDER BY us2.valid_until DESC
			LIMIT 1
		) us ON TRUE
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE u.id = $1
	`, userDbID).Scan(&userTextID, &limitBytes)
	if err != nil {
		writeAdminError(w, http.StatusNotFound, "user not found")
		return
	}

	// Daily breakdown — group by date, sum upload+download.
	rows, err := db.DB.Query(`
		SELECT recorded_at::date AS day,
		       COALESCE(SUM(upload_bytes + download_bytes), 0) AS bytes
		FROM traffic_stats
		WHERE user_id = $1
		  AND recorded_at >= NOW() - ($2 || ' days')::interval
		GROUP BY day
		ORDER BY day
	`, userTextID, fmt.Sprintf("%d", days))
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "traffic query error: "+err.Error())
		return
	}
	defer rows.Close()

	type dayRow struct {
		Day   string `json:"day"`
		Bytes int64  `json:"bytes"`
	}
	byDay := []dayRow{}
	var total int64
	for rows.Next() {
		var d time.Time
		var b int64
		if err := rows.Scan(&d, &b); err != nil {
			continue
		}
		byDay = append(byDay, dayRow{Day: d.Format("2006-01-02"), Bytes: b})
		total += b
	}

	// Per-country breakdown — placeholder. traffic_stats has no server_id /
	// exit_node_id column, so we cannot attribute bytes to an exit country.
	// TODO: when traffic_stats grows a server_id column, join servers.country_code
	// and SUM(upload+download) GROUP BY country_code ORDER BY bytes DESC.
	byCountry := []map[string]any{}

	writeAdminJSON(w, http.StatusOK, map[string]any{
		"user_db_id":  userDbID,
		"days":        days,
		"limit_bytes": limitBytes,
		"total_bytes": total,
		"by_day":      byDay,
		"by_country":  byCountry,
	})
}
