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

func ListPaymentsHandler(w http.ResponseWriter, r *http.Request) {
	p := parsePagination(r)

	where := "1=1"
	args := []any{}
	argN := 1
	if p.Status != "" {
		where += fmt.Sprintf(" AND p.status = $%d", argN)
		args = append(args, p.Status)
		argN++
	}
	if p.Search != "" {
		where += fmt.Sprintf(" AND (COALESCE(p.user_id_text,'') ILIKE $%d OR COALESCE(p.external_id,'') ILIKE $%d)", argN, argN)
		args = append(args, "%"+p.Search+"%")
		argN++
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM payments p LEFT JOIN users u ON u.user_id = p.user_id_text WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count payments")
		return
	}

	query := fmt.Sprintf(`
		SELECT p.id, COALESCE(p.user_id, 0), COALESCE(p.user_id_text, COALESCE(u.user_id, '')),
		       p.amount::float8, p.currency, p.status, COALESCE(p.provider, ''),
		       COALESCE(p.external_id, ''), COALESCE(p.notes, ''), COALESCE(p.metadata, '{}'::jsonb),
		       p.created_at, COALESCE(p.updated_at, p.created_at), p.confirmed_at,
		       COALESCE(p.yookassa_payment_id, ''), COALESCE(p.yookassa_status, ''),
		       COALESCE(p.payment_method_type, ''),
		       COALESCE(p.payment_method_details, '{}'::jsonb),
		       COALESCE(p.refunded_amount, 0)::float8,
		       COALESCE(p.receipt_registered, FALSE),
		       COALESCE(p.test, FALSE),
		       COALESCE(p.description, ''),
		       COALESCE(p.payer_email, ''),
		       COALESCE(p.cancellation_reason, '')
		FROM payments p
		LEFT JOIN users u ON u.user_id = p.user_id_text
		WHERE %s
		ORDER BY p.created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, argN, argN+1)
	args = append(args, p.PageSize, p.Offset())

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to list payments")
		return
	}
	defer rows.Close()

	var items []paymentResponse
	for rows.Next() {
		var item paymentResponse
		var createdAt, updatedAt time.Time
		var confirmedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.UserDBID, &item.UserID, &item.Amount, &item.Currency, &item.Status,
			&item.Provider, &item.ExternalID, &item.Notes, &item.Metadata,
			&createdAt, &updatedAt, &confirmedAt,
			&item.YookassaPaymentID, &item.YookassaStatus,
			&item.PaymentMethodType, &item.PaymentMethodDetails,
			&item.RefundedAmount, &item.ReceiptRegistered, &item.Test,
			&item.Description, &item.PayerEmail, &item.CancellationReason); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to scan payment")
			return
		}
		item.CreatedAt = createdAt.Format(time.RFC3339)
		item.UpdatedAt = updatedAt.Format(time.RFC3339)
		if confirmedAt.Valid {
			item.ConfirmedAt = confirmedAt.Time.Format(time.RFC3339)
		}
		items = append(items, item)
	}
	writeAdminJSON(w, http.StatusOK, PagedResponse{Items: items, Total: total, Page: p.Page, PageSize: p.PageSize})
}

func CreatePaymentHandler(w http.ResponseWriter, r *http.Request) {
	var payload paymentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if payload.UserDBID == 0 || payload.Amount <= 0 || strings.TrimSpace(payload.Status) == "" {
		writeAdminError(w, http.StatusBadRequest, "user_db_id, amount and status are required")
		return
	}
	currency := strings.TrimSpace(payload.Currency)
	if currency == "" {
		currency = "USD"
	}
	metadata := payload.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if _, err := db.DB.Exec(`
		INSERT INTO payments (user_id, amount, currency, status, provider, external_id, notes, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, payload.UserDBID, payload.Amount, currency, strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Provider), strings.TrimSpace(payload.ExternalID), strings.TrimSpace(payload.Notes), metadata); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to create payment")
		return
	}
	writeAdminJSON(w, http.StatusCreated, map[string]string{"message": "Payment created"})
}

func UpdatePaymentHandler(w http.ResponseWriter, r *http.Request) {
	paymentID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Invalid payment id")
		return
	}
	var payload paymentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	metadata := payload.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if _, err := db.DB.Exec(`
		UPDATE payments
		SET user_id = COALESCE(NULLIF($1, 0), user_id),
		    amount = CASE WHEN $2 > 0 THEN $2 ELSE amount END,
		    currency = COALESCE(NULLIF($3, ''), currency),
		    status = COALESCE(NULLIF($4, ''), status),
		    provider = COALESCE(NULLIF($5, ''), provider),
		    external_id = COALESCE(NULLIF($6, ''), external_id),
		    notes = $7,
		    metadata = $8,
		    confirmed_at = CASE WHEN $4 = 'confirmed' THEN CURRENT_TIMESTAMP ELSE confirmed_at END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $9
	`, payload.UserDBID, payload.Amount, strings.TrimSpace(payload.Currency), strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Provider), strings.TrimSpace(payload.ExternalID), strings.TrimSpace(payload.Notes), metadata, paymentID); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to update payment")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Payment updated"})
}
