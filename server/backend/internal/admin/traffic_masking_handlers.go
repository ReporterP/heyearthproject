package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

type TrafficMaskingRule struct {
	ID              int    `json:"id"`
	SourceDomain    string `json:"source_domain"`
	MaskDomain      string `json:"mask_domain"`
	IsActive        bool   `json:"is_active"`
	CreatedByAdminID *int  `json:"created_by_admin_id,omitempty"`
	TenantID        *int   `json:"tenant_id,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func ListTrafficMaskingRulesHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query(`
		SELECT id, source_domain, mask_domain, is_active,
		       created_by_admin_id, tenant_id, created_at, updated_at
		FROM traffic_masking_rules
		ORDER BY id DESC
	`)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	var rules []TrafficMaskingRule
	for rows.Next() {
		var rule TrafficMaskingRule
		var createdAt, updatedAt string
		if err := rows.Scan(&rule.ID, &rule.SourceDomain, &rule.MaskDomain, &rule.IsActive,
			&rule.CreatedByAdminID, &rule.TenantID, &createdAt, &updatedAt); err != nil {
			writeAdminError(w, http.StatusInternalServerError, err.Error())
			return
		}
		rule.CreatedAt = createdAt
		rule.UpdatedAt = updatedAt
		rules = append(rules, rule)
	}
	if rules == nil {
		rules = []TrafficMaskingRule{}
	}
	writeAdminJSON(w, http.StatusOK, rules)
}

func CreateTrafficMaskingRuleHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceDomain string `json:"source_domain"`
		MaskDomain   string `json:"mask_domain"`
		IsActive     *bool  `json:"is_active"`
		TenantID     *int   `json:"tenant_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if strings.TrimSpace(req.SourceDomain) == "" || strings.TrimSpace(req.MaskDomain) == "" {
		writeAdminError(w, http.StatusBadRequest, "source_domain and mask_domain required")
		return
	}

	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}

	// Get admin ID from context
	adminID := getAdminIDFromContext(r)

	var id int
	err := db.DB.QueryRow(`
		INSERT INTO traffic_masking_rules (source_domain, mask_domain, is_active, created_by_admin_id, tenant_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, strings.TrimSpace(req.SourceDomain), strings.TrimSpace(req.MaskDomain), isActive, adminID, req.TenantID).Scan(&id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": id, "message": "rule created"})
}

func UpdateTrafficMaskingRuleHandler(w http.ResponseWriter, r *http.Request) {
	idStr := extractPathID(r.URL.Path, "/api/admin/traffic-masking/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var req struct {
		SourceDomain *string `json:"source_domain"`
		MaskDomain   *string `json:"mask_domain"`
		IsActive     *bool   `json:"is_active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid body")
		return
	}

	sets := []string{}
	args := []any{}
	argIdx := 1

	if req.SourceDomain != nil {
		sets = append(sets, fmt.Sprintf("source_domain = $%d", argIdx))
		args = append(args, strings.TrimSpace(*req.SourceDomain))
		argIdx++
	}
	if req.MaskDomain != nil {
		sets = append(sets, fmt.Sprintf("mask_domain = $%d", argIdx))
		args = append(args, strings.TrimSpace(*req.MaskDomain))
		argIdx++
	}
	if req.IsActive != nil {
		sets = append(sets, fmt.Sprintf("is_active = $%d", argIdx))
		args = append(args, *req.IsActive)
		argIdx++
	}

	if len(sets) == 0 {
		writeAdminError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	sets = append(sets, "updated_at = CURRENT_TIMESTAMP")
	args = append(args, id)
	query := fmt.Sprintf("UPDATE traffic_masking_rules SET %s WHERE id = $%d", strings.Join(sets, ", "), argIdx)

	result, err := db.DB.Exec(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeAdminError(w, http.StatusNotFound, "rule not found")
		return
	}

	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "rule updated"})
}

func DeleteTrafficMaskingRuleHandler(w http.ResponseWriter, r *http.Request) {
	idStr := extractPathID(r.URL.Path, "/api/admin/traffic-masking/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "invalid id")
		return
	}

	result, err := db.DB.Exec("DELETE FROM traffic_masking_rules WHERE id = $1", id)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		writeAdminError(w, http.StatusNotFound, "rule not found")
		return
	}

	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "rule deleted"})
}

func extractPathID(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimSuffix(trimmed, "/")
	return trimmed
}

func getAdminIDFromContext(r *http.Request) *int {
	// Extract admin ID from JWT context (set by AuthMiddleware)
	adminIDVal := r.Context().Value("admin_id")
	if adminIDVal != nil {
		if id, ok := adminIDVal.(int); ok {
			return &id
		}
	}
	return nil
}
