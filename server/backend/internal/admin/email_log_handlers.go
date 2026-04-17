package admin

import (
	"encoding/json"
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

// ─── Payment retry queue ───

type retryRow struct {
	ID                int     `json:"id"`
	UserID            int     `json:"user_id"`
	UserIDText        string  `json:"user_id_text"`
	PlanID            int     `json:"plan_id"`
	PlanName          string  `json:"plan_name"`
	OriginalPaymentID *int    `json:"original_payment_id,omitempty"`
	Attempt           int     `json:"attempt"`
	MaxAttempts       int     `json:"max_attempts"`
	Status            string  `json:"status"`
	LastError         string  `json:"last_error,omitempty"`
	FailedAt          string  `json:"failed_at"`
	NextRetryAt       string  `json:"next_retry_at"`
	GraceUntil        string  `json:"grace_until,omitempty"`
	SucceededPaymentID *int   `json:"succeeded_payment_id,omitempty"`
}

// ListRetryQueueHandler — GET /api/admin/payment-retries
func ListRetryQueueHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Status != "" {
		where += fmt.Sprintf(" AND rq.status = $%d", argN)
		args = append(args, p.Status)
		argN++
	}
	if p.Search != "" {
		where += fmt.Sprintf(" AND (u.user_id ILIKE $%d OR COALESCE(rq.last_error,'') ILIKE $%d)", argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	_ = db.DB.QueryRow("SELECT COUNT(*) FROM payment_retry_queue rq JOIN users u ON u.id = rq.user_id WHERE "+where, args...).Scan(&total)

	query := fmt.Sprintf(`
		SELECT rq.id, rq.user_id, u.user_id, rq.plan_id, COALESCE(p.name, ''),
		       rq.original_payment_id, rq.attempt, rq.max_attempts,
		       rq.status, COALESCE(rq.last_error, ''),
		       rq.failed_at, rq.next_retry_at, rq.grace_until, rq.succeeded_payment_id
		FROM payment_retry_queue rq
		JOIN users u ON u.id = rq.user_id
		LEFT JOIN plans p ON p.id = rq.plan_id
		WHERE %s
		ORDER BY rq.id DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	items := []retryRow{}
	for rows.Next() {
		var r retryRow
		var failedAt, nextRetryAt time.Time
		var graceUntil *time.Time
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserIDText, &r.PlanID, &r.PlanName,
			&r.OriginalPaymentID, &r.Attempt, &r.MaxAttempts,
			&r.Status, &r.LastError,
			&failedAt, &nextRetryAt, &graceUntil, &r.SucceededPaymentID); err != nil {
			continue
		}
		r.FailedAt = failedAt.Format(time.RFC3339)
		r.NextRetryAt = nextRetryAt.Format(time.RFC3339)
		if graceUntil != nil {
			r.GraceUntil = graceUntil.Format(time.RFC3339)
		}
		items = append(items, r)
	}

	writeAdminJSON(w, http.StatusOK, PagedResponse{
		Items: items, Total: total, Page: p.Page, PageSize: p.PageSize,
	})
}

// CancelRetryHandler — POST /api/admin/payment-retries/{id}/cancel — админ отменяет active retry
func CancelRetryHandler(w http.ResponseWriter, r *http.Request) {
	retryID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "invalid retry id")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Reason == "" {
		body.Reason = "admin_cancel"
	}
	_, err := db.DB.Exec(`
		UPDATE payment_retry_queue SET
			status = 'canceled',
			last_error = COALESCE(last_error || ' | ', '') || $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2 AND status IN ('pending', 'in_progress')
	`, body.Reason, retryID)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "retry canceled"})
}
