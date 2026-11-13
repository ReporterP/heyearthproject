package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/db"
)

// emailLogRow — одна запись в email_log
type emailLogRow struct {
	ID         int    `json:"id"`
	UserIDText string `json:"user_id_text"`
	Email      string `json:"email"`
	Template   string `json:"template"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	Attempts   int    `json:"attempts"`
	SentAt     string `json:"sent_at,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// ListEmailLogHandler — GET /api/admin/email-log
// Query params: page, page_size, status (pending|sent|failed|disabled), template, search (email)
func ListEmailLogHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)
	templateFilter := r.URL.Query().Get("template")

	where := "1=1"
	args := []any{}
	argN := 1

	if p.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", argN)
		args = append(args, p.Status)
		argN++
	}
	if templateFilter != "" {
		where += fmt.Sprintf(" AND template = $%d", argN)
		args = append(args, templateFilter)
		argN++
	}
	if p.Search != "" {
		where += fmt.Sprintf(" AND (email ILIKE $%d OR COALESCE(user_id_text,'') ILIKE $%d)", argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM email_log WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count email log")
		return
	}

	query := fmt.Sprintf(`
		SELECT id, COALESCE(user_id_text, ''), email, template, COALESCE(subject, ''),
		       status, COALESCE(error, ''), attempts, sent_at, created_at
		FROM email_log
		WHERE %s
		ORDER BY id DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	items := []emailLogRow{}
	for rows.Next() {
		var r emailLogRow
		var sentAt *time.Time
		var createdAt time.Time
		if err := rows.Scan(&r.ID, &r.UserIDText, &r.Email, &r.Template, &r.Subject,
			&r.Status, &r.Error, &r.Attempts, &sentAt, &createdAt); err != nil {
			continue
		}
		if sentAt != nil {
			r.SentAt = sentAt.Format(time.RFC3339)
		}
		r.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, r)
	}

	writeAdminJSON(w, http.StatusOK, PagedResponse{
		Items: items, Total: total, Page: p.Page, PageSize: p.PageSize,
	})
}
