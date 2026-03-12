package admin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/db"
)

func ListPromocodesHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Search != "" {
		where += fmt.Sprintf(" AND p.code ILIKE $%d", argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM promocodes p WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count promocodes")
		return
	}

	query := fmt.Sprintf(`
		SELECT p.code, p.group_id_granted, ug.name, p.duration_days, p.max_uses, p.current_uses,
		       p.expires_at, COALESCE(p.created_at, CURRENT_TIMESTAMP), COALESCE(p.updated_at, CURRENT_TIMESTAMP)
		FROM promocodes p
		LEFT JOIN user_groups ug ON ug.id = p.group_id_granted
		WHERE %s
		ORDER BY p.code ASC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list promocodes")
		return
	}
	defer rows.Close()

	var items []promocodeResponse
	for rows.Next() {
		var item promocodeResponse
		var expiresAt sql.NullTime
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&item.Code, &item.GroupID, &item.GroupName, &item.DurationDays, &item.MaxUses, &item.CurrentUses, &expiresAt, &createdAt, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan promocode")
			return
		}
		if expiresAt.Valid {
			item.ExpiresAt = expiresAt.Time.Format(time.RFC3339)
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		item.UpdatedAt = updatedAt.Format(time.RFC3339)
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func CreatePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	var payload promocodePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Code) == "" || payload.GroupID == 0 || payload.DurationDays <= 0 {
		writeAdminError(w, http.StatusBadRequest, "code, group_id_granted and duration_days are required")
		return
	}
	var expiresAt any
	if strings.TrimSpace(payload.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ExpiresAt))
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = parsed
	}
	if _, err := db.DB.Exec(`
		INSERT INTO promocodes (code, group_id_granted, duration_days, max_uses, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, strings.TrimSpace(payload.Code), payload.GroupID, payload.DurationDays, payload.MaxUses, expiresAt); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create promocode")
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]string{"message": "Promocode created"})
}

func UpdatePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/promocodes/"), "/")
	if code == "" {
		writeAdminError(w, http.StatusBadRequest, "Invalid promocode")
		return
	}
	var payload promocodePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	var expiresAt any
	if strings.TrimSpace(payload.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(payload.ExpiresAt))
		if err != nil {
			writeAdminError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = parsed
	}
	if _, err := db.DB.Exec(`
		UPDATE promocodes
		SET group_id_granted = COALESCE(NULLIF($1, 0), group_id_granted),
		    duration_days = CASE WHEN $2 > 0 THEN $2 ELSE duration_days END,
		    max_uses = CASE WHEN $3 >= 0 THEN $3 ELSE max_uses END,
		    expires_at = COALESCE($4, expires_at),
		    updated_at = CURRENT_TIMESTAMP
		WHERE code = $5
	`, payload.GroupID, payload.DurationDays, payload.MaxUses, expiresAt, code); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update promocode")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Promocode updated"})
}

func DeletePromocodeHandler(w http.ResponseWriter, r *http.Request) {
	code := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/promocodes/"), "/")
	if code == "" {
		writeAdminError(w, http.StatusBadRequest, "Invalid promocode")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM promocodes WHERE code = $1`, code); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete promocode")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Promocode deleted"})
}
