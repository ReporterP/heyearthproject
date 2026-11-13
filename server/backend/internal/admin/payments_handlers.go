package admin

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/biz"
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
	if err := dateRangeWhere(r, "p.created_at", &where, &args, &argN); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Scope by user DB id — used by the user drawer (Трафик+Платежи tab) to
	// fetch per-user payment history. Without this, the drawer was showing
	// the global payments list. Match against either the int FK (p.user_id)
	// or the joined users.id from p.user_id_text — many older rows only have
	// the text UUID populated, not the int FK.
	if udbStr := strings.TrimSpace(r.URL.Query().Get("user_db_id")); udbStr != "" {
		if udb, err := strconv.Atoi(udbStr); err == nil && udb > 0 {
			where += fmt.Sprintf(" AND (p.user_id = $%d OR u.id = $%d)", argN, argN)
			args = append(args, udb)
			argN++
		}
	}

	var total int
	if err := db.DB.QueryRow("SELECT COUNT(*) FROM payments p LEFT JOIN users u ON u.user_id = p.user_id_text WHERE "+where, args...).Scan(&total); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to count payments")
		return
	}

	// Revenue aggregate for the current filter — only REAL (non-test) paid payments
	// (the shared paid list: YooKassa payments are 'succeeded'). This is what's shown in
	// the page header so admins don't conflate YooKassa sandbox traffic with actual income.
	var realTotal float64
	if err := db.DB.QueryRow(`
		SELECT COALESCE(SUM(p.amount), 0)::float8
		FROM payments p
		LEFT JOIN users u ON u.user_id = p.user_id_text
		WHERE `+where+`
		  AND p.status IN (`+biz.PaidPaymentStatusesSQL+`)
		  AND COALESCE(p.test, FALSE) = FALSE
	`, args...).Scan(&realTotal); err != nil {
		// Non-fatal — we'll return 0 rather than break the list endpoint.
		realTotal = 0
	}

	// Sortable columns whitelist (Phase 1 Task 3).
	allowedSort := map[string]string{
		"id":         "p.id",
		"created_at": "p.created_at",
		"amount":     "p.amount",
		"status":     "p.status",
		"user_id":    "p.user_id",
	}
	sortCol := "p.created_at"
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

	query := fmt.Sprintf(`
		SELECT p.id, COALESCE(p.user_id, 0), COALESCE(p.user_id_text, COALESCE(u.user_id, '')),
		       p.amount::float8, p.currency, p.status, COALESCE(p.provider, ''),
		       COALESCE(p.external_id, ''), COALESCE(p.notes, ''), COALESCE(p.metadata, '{}'::jsonb),
		       p.created_at, COALESCE(p.updated_at, p.created_at), p.confirmed_at,
		       COALESCE(p.yookassa_payment_id, ''), COALESCE(p.yookassa_status, ''),
		       COALESCE(p.payment_method_type, ''),
		       COALESCE(p.payment_method_details, '{}'::jsonb),
		       COALESCE(p.refunded_amount, 0)::float8,
		       (`+biz.RefundableAmountSQL+`)::float8,
		       COALESCE(p.receipt_registered, FALSE),
		       COALESCE(p.test, FALSE),
		       COALESCE(p.description, ''),
		       COALESCE(p.payer_email, ''),
		       COALESCE(p.cancellation_reason, '')
		FROM payments p
		LEFT JOIN users u ON u.user_id = p.user_id_text
		WHERE %s
		ORDER BY %s %s
		LIMIT $%d OFFSET $%d
	`, where, sortCol, dir, argN, argN+1)
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
			&item.RefundedAmount, &item.RefundableAmount, &item.ReceiptRegistered, &item.Test,
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
	// Response extends PagedResponse with a `real_total_amount` field — the sum
	// of amount across non-test succeeded payments matching the current filter.
	// Frontend uses this instead of summing the visible page, which would undercount.
	writeAdminJSON(w, http.StatusOK, struct {
		Items           []paymentResponse `json:"items"`
		Total           int               `json:"total"`
		Page            int               `json:"page"`
		PageSize        int               `json:"page_size"`
		RealTotalAmount float64           `json:"real_total_amount"`
	}{items, total, p.Page, p.PageSize, realTotal})
}

// Manual payments (C7): a record of money taken outside YooKassa (cash, a transfer). Only
// these are created and edited by hand; a YooKassa payment changes only through YooKassa
// (its webhook, «Статус», «Вернуть»). A manual record does not extend a subscription.
var manualPaymentStatuses = map[string]bool{"pending": true, "succeeded": true, "canceled": true}

const manualOnlyMsg = "Это платёж ЮKassa: его статус и сумма меняются только через ЮKassa («Статус», «Вернуть»). Вручную правятся только ручные записи"

type manualPaymentPayload struct {
	UserDBID int     `json:"user_db_id"`
	Amount   float64 `json:"amount"`
	Status   string  `json:"status"`
	Notes    string  `json:"notes"`
}

func validManualPayment(p manualPaymentPayload, create bool) error {
	if create && p.UserDBID <= 0 {
		return fmt.Errorf("укажите пользователя (ID в базе)")
	}
	if p.Amount <= 0 || p.Amount > 1000000 {
		return fmt.Errorf("сумма — от 0,01 до 1 000 000 ₽")
	}
	if !manualPaymentStatuses[strings.TrimSpace(p.Status)] {
		return fmt.Errorf("статус: pending, succeeded или canceled")
	}
	return nil
}

func CreatePaymentHandler(w http.ResponseWriter, r *http.Request) {
	var payload manualPaymentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	if err := validManualPayment(payload, true); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	var userText string
	if err := db.DB.QueryRow(`SELECT user_id FROM users WHERE id = $1`, payload.UserDBID).Scan(&userText); err != nil {
		writeAdminError(w, http.StatusNotFound, "Пользователь не найден")
		return
	}
	var id int
	if err := db.DB.QueryRow(`
		INSERT INTO payments (user_id, user_id_text, amount, currency, status, provider, notes, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, 'RUB', $4, 'manual', $5, '{"kind": "manual"}'::jsonb, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id`, payload.UserDBID, userText, payload.Amount, strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Notes)).Scan(&id); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось создать платёж")
		return
	}
	WriteAuditLog(r, "payment.manual_create", "payment", strconv.Itoa(id), payload)
	writeAdminJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func UpdatePaymentHandler(w http.ResponseWriter, r *http.Request) {
	paymentID, ok := parseLastPathInt(r.URL.Path)
	if !ok {
		writeAdminError(w, http.StatusBadRequest, "Неверный id платежа")
		return
	}
	var payload manualPaymentPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	if err := validManualPayment(payload, false); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := db.DB.Begin()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить")
		return
	}
	defer tx.Rollback()
	var provider, status, notes string
	var amount float64
	err = tx.QueryRow(`SELECT COALESCE(provider, ''), status, amount::float8, COALESCE(notes, '') FROM payments WHERE id = $1 FOR UPDATE`, paymentID).
		Scan(&provider, &status, &amount, &notes)
	if err == sql.ErrNoRows {
		writeAdminError(w, http.StatusNotFound, "Платёж не найден")
		return
	}
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось загрузить платёж")
		return
	}
	if provider != "manual" {
		writeAdminError(w, http.StatusConflict, manualOnlyMsg)
		return
	}
	if _, err := tx.Exec(`
		UPDATE payments SET amount = $2, status = $3, notes = $4, updated_at = CURRENT_TIMESTAMP WHERE id = $1`,
		paymentID, payload.Amount, strings.TrimSpace(payload.Status), strings.TrimSpace(payload.Notes)); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить")
		return
	}
	if err := tx.Commit(); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Не удалось сохранить")
		return
	}
	WriteAuditLog(r, "payment.manual_update", "payment", strconv.Itoa(paymentID), map[string]any{
		"before": map[string]any{"amount": amount, "status": status, "notes": notes}, "after": payload})
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Сохранено"})
}
