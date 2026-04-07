package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	// Status filter: "active" = not blocked, "blocked" = blocked
	if p.Status == "active" {
		where += " AND COALESCE(u.is_blocked, FALSE) = FALSE"
	} else if p.Status == "blocked" {
		where += " AND u.is_blocked = TRUE"
	}

	var total int
	err := db.DB.QueryRow("SELECT COUNT(*) FROM users u WHERE "+where, args...).Scan(&total)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count users")
		return
	}

	query := fmt.Sprintf(`
		SELECT u.id, u.user_id, COALESCE(u.username, ''), u.telegram_id, u.role, COALESCE(u.is_blocked, FALSE),
		       COALESCE(u.preferred_platform, ''),
		       u.created_at, COALESCE(u.updated_at, u.created_at),
		       COALESCE(p.name, ug.name, 'Нет подписки') AS current_plan,
		       us.valid_until
		FROM users u
		LEFT JOIN LATERAL (
			SELECT us2.group_id, us2.plan_id, us2.valid_until
			FROM user_subscriptions us2
			WHERE us2.user_id = u.id AND us2.valid_until > NOW()
			ORDER BY us2.valid_until DESC
			LIMIT 1
		) us ON TRUE
		LEFT JOIN user_groups ug ON ug.id = us.group_id
		LEFT JOIN plans p ON p.id = us.plan_id
		WHERE %s
		ORDER BY u.created_at DESC, u.id DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
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
		if err := rows.Scan(&row.ID, &row.UserID, &row.Username, &row.TelegramID, &row.Role, &row.IsBlocked, &row.PreferredPlatform, &row.CreatedAt, &row.UpdatedAt, &row.CurrentPlan, &row.ValidUntil); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan user")
			return
		}
		payload := map[string]any{
			"id":                 row.ID,
			"user_id":            row.UserID,
			"username":           row.Username,
			"role":               row.Role,
			"is_blocked":         row.IsBlocked,
			"preferred_platform": row.PreferredPlatform,
			"current_plan":       row.CurrentPlan,
			"created_at":         row.CreatedAt.Format(time.RFC3339),
			"updated_at":         row.UpdatedAt.Format(time.RFC3339),
		}
		if row.TelegramID.Valid {
			payload["telegram_id"] = row.TelegramID.Int64
		}
		if row.ValidUntil.Valid {
			payload["valid_until"] = row.ValidUntil.Time.Format(time.RFC3339)
		} else {
			payload["valid_until"] = ""
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
