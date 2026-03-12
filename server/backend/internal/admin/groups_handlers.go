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

func ListGroupsHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Search != "" {
		where += fmt.Sprintf(" AND name ILIKE $%d", argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM user_groups WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count groups")
		return
	}

	query := fmt.Sprintf(`
		SELECT id, name, COALESCE(description, ''), is_premium, company_id, COALESCE(updated_at, CURRENT_TIMESTAMP)
		FROM user_groups
		WHERE %s
		ORDER BY name ASC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list groups")
		return
	}
	defer rows.Close()

	var groups []groupResponse
	for rows.Next() {
		var response groupResponse
		var companyID sql.NullInt64
		var updatedAt time.Time
		if err := rows.Scan(&response.ID, &response.Name, &response.Description, &response.IsPremium, &companyID, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan group")
			return
		}
		if companyID.Valid {
			value := int(companyID.Int64)
			response.CompanyID = &value
		}
		response.UpdatedAt = updatedAt.Format(time.RFC3339)
		groups = append(groups, response)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: groups, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func CreateGroupHandler(w http.ResponseWriter, r *http.Request) {
	var payload groupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeAdminError(w, http.StatusBadRequest, "name is required")
		return
	}
	if _, err := db.DB.Exec(`
		INSERT INTO user_groups (name, description, is_premium, company_id, updated_at)
		VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP)
	`, strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.IsPremium, payload.CompanyID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create group")
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]string{"message": "Group created"})
}

func UpdateGroupHandler(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid group id")
		return
	}
	var payload groupPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if _, err := db.DB.Exec(`
		UPDATE user_groups
		SET name = COALESCE(NULLIF($1, ''), name),
		    description = $2,
		    is_premium = $3,
		    company_id = $4,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`, strings.TrimSpace(payload.Name), strings.TrimSpace(payload.Description), payload.IsPremium, payload.CompanyID, groupID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update group")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Group updated"})
}

func DeleteGroupHandler(w http.ResponseWriter, r *http.Request) {
	groupID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid group id")
		return
	}
	if _, err := db.DB.Exec(`DELETE FROM user_groups WHERE id = $1`, groupID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to delete group")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Group deleted"})
}
