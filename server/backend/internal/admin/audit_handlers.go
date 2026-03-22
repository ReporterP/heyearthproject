package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Audit Log ───────────────────────
//
// Логирование всех изменяющих действий администраторов.
// Таблица: admin_audit_log (миграция 000019)

type auditLogEntry struct {
	ID            int64           `json:"id"`
	AdminID       int             `json:"admin_id"`
	AdminUsername string          `json:"admin_username"`
	Action        string          `json:"action"`
	TargetType    string          `json:"target_type"`
	TargetID      string          `json:"target_id"`
	Details       json.RawMessage `json:"details"`
	IPAddress     string          `json:"ip_address"`
	CreatedAt     string          `json:"created_at"`
}

// WriteAuditLog записывает действие в audit log.
// Вызывать из handler после успешного действия.
func WriteAuditLog(r *http.Request, action, targetType, targetID string, details any) {
	claims, _ := ClaimsFromContext(r.Context())
	adminID := 0
	adminUsername := ""
	if claims != nil {
		adminID = claims.AdminID
		adminUsername = claims.Username
	}

	ip := extractClientIP(r)

	var detailsJSON []byte
	if details != nil {
		detailsJSON, _ = json.Marshal(details)
	}

	_, err := db.DB.Exec(`
		INSERT INTO admin_audit_log (admin_id, admin_username, action, target_type, target_id, details, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, adminID, adminUsername, action, targetType, targetID, detailsJSON, ip)
	if err != nil {
		log.Printf("[audit] Ошибка записи в audit log: %v", err)
	}
}

// ListAuditLogHandler — GET /api/admin/audit-log
// Список записей audit log с пагинацией и фильтрацией.
func ListAuditLogHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	targetType := strings.TrimSpace(r.URL.Query().Get("target_type"))

	where := "1=1"
	args := []any{}
	argN := 1

	if p.Search != "" {
		where += fmt.Sprintf(" AND (a.admin_username ILIKE $%d OR a.target_id ILIKE $%d OR a.action ILIKE $%d)", argN, argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}
	if action != "" {
		where += fmt.Sprintf(" AND a.action = $%d", argN)
		args = append(args, action)
		argN++
	}
	if targetType != "" {
		where += fmt.Sprintf(" AND a.target_type = $%d", argN)
		args = append(args, targetType)
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM admin_audit_log a WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "count error: "+err.Error())
		return
	}

	query := fmt.Sprintf(`
		SELECT a.id, a.admin_id, a.admin_username, a.action,
		       COALESCE(a.target_type, ''), COALESCE(a.target_id, ''),
		       COALESCE(a.details::text, '{}'), COALESCE(a.ip_address, ''),
		       a.created_at
		FROM admin_audit_log a
		WHERE %s
		ORDER BY a.created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "query error: "+err.Error())
		return
	}
	defer rows.Close()

	var entries []auditLogEntry
	for rows.Next() {
		var e auditLogEntry
		var detailsStr string
		if err := rows.Scan(&e.ID, &e.AdminID, &e.AdminUsername, &e.Action, &e.TargetType, &e.TargetID, &detailsStr, &e.IPAddress, &e.CreatedAt); err != nil {
			continue
		}
		e.Details = json.RawMessage(detailsStr)
		entries = append(entries, e)
	}

	writeAdminJSON(w, http.StatusOK, PagedResponse{
		Items:    entries,
		Total:    total,
		Page:     p.Page,
		PageSize: p.PageSize,
	})
}

func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, _ := strings.Cut(r.RemoteAddr, ":")
	return host
}
