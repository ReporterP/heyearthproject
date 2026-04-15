package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/yookassa"
)

// ─────────────────────── Admin Payment Actions (YooKassa) ───────────────────────

// handleAdminPaymentCancel — полный или частичный возврат через YooKassa Refund API.
// Принимает optional `amount` (в рублях) и `reason` в body. При пустом amount — полный возврат.
func handleAdminPaymentCancel(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	paymentID := extractPathID(r.URL.Path, "/api/admin/payments/", "/cancel")
	if paymentID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payment ID"})
		return
	}

	var req struct {
		Amount float64 `json:"amount"`
		Reason string  `json:"reason"`
	}
	_ = parseJSON(r, &req)
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "Admin refund"
	}

	// Найти платёж
	var yookassaID, status, description string
	var amount float64
	err := db.DB.QueryRow(`
		SELECT COALESCE(yookassa_payment_id, ''), COALESCE(status, ''),
		       COALESCE(description, ''), COALESCE(amount, 0)
		FROM payments WHERE id = $1
	`, paymentID).Scan(&yookassaID, &status, &description, &amount)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if yookassaID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нет yookassa_payment_id"})
		return
	}

	refundAmount := req.Amount
	if refundAmount <= 0 {
		refundAmount = amount
	}
	if refundAmount > amount {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "сумма возврата больше суммы платежа"})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "yookassa not configured"})
		return
	}

	refund, err := client.CreateRefund(yookassa.CreateRefundRequest{
		PaymentID:   yookassaID,
		Amount:      yookassa.Amount{Value: fmt.Sprintf("%.2f", refundAmount), Currency: "RUB"},
		Description: reason,
	})
	if err != nil {
		log.Printf("[admin-refund] YooKassa refund failed for payment %d: %v", paymentID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	// Запись в payment_refunds (и обновление refunded_amount) — произойдёт через webhook
	// Но для мгновенного UI-отклика — записываем сразу pending
	_, _ = db.DB.Exec(`
		INSERT INTO payment_refunds (payment_id, yookassa_refund_id, amount, currency, status, description)
		VALUES ($1, $2, $3::numeric, $4, $5, $6)
		ON CONFLICT (yookassa_refund_id) DO UPDATE
		SET status = EXCLUDED.status, updated_at = CURRENT_TIMESTAMP
	`, paymentID, refund.ID, refund.Amount.Value, refund.Amount.Currency, refund.Status, reason)

	log.Printf("[admin-refund] Created refund %s for payment %d (%s₽, status=%s)",
		refund.ID, paymentID, refund.Amount.Value, refund.Status)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":   "Возврат создан",
		"refund_id": refund.ID,
		"status":    refund.Status,
		"amount":    refund.Amount.Value,
	})
}

// handleAdminPaymentCheckStatus запрашивает актуальный статус из ЮKassa и обновляет локально.
func handleAdminPaymentCheckStatus(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	paymentID := extractPathID(r.URL.Path, "/api/admin/payments/", "/check-status")
	if paymentID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payment ID"})
		return
	}

	var yookassaID, localStatus string
	err := db.DB.QueryRow(`SELECT COALESCE(yookassa_payment_id, ''), COALESCE(status, '') FROM payments WHERE id = $1`,
		paymentID).Scan(&yookassaID, &localStatus)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if yookassaID == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"local_status":    localStatus,
			"yookassa_status": "not_initiated",
		})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "yookassa not configured"})
		return
	}

	payment, err := client.GetPayment(yookassaID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "yookassa: " + err.Error()})
		return
	}

	// Обновляем локально + прогоняем processPaymentEvent (активация подписки если succeeded)
	if err := processPaymentEvent(payment); err != nil {
		log.Printf("[admin-check-status] process failed: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"local_status":    localStatus,
		"yookassa_status": payment.Status,
		"yookassa_id":     payment.ID,
		"amount":          payment.Amount.Value,
		"paid":            payment.Paid,
	})
}

// handleAdminPaymentResend — у ЮKassa нет ручной переотправки webhook'ов,
// но мы можем заново запустить processPaymentEvent для синхронизации.
func handleAdminPaymentResend(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	paymentID := extractPathID(r.URL.Path, "/api/admin/payments/", "/resend")
	if paymentID == 0 {
		// Массовый resend — пройтись по pending платежам и synchронизировать
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "Используйте кнопку 'Проверить статус' для конкретного платежа — это пересинхронизирует его с ЮKassa.",
		})
		return
	}

	var yookassaID string
	err := db.DB.QueryRow(`SELECT COALESCE(yookassa_payment_id, '') FROM payments WHERE id = $1`, paymentID).Scan(&yookassaID)
	if err != nil || yookassaID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found or no yookassa id"})
		return
	}

	client, err := yookassa.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "yookassa not configured"})
		return
	}
	payment, err := client.GetPayment(yookassaID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if err := processPaymentEvent(payment); err != nil {
		log.Printf("[admin-resend] process error: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Платёж пересинхронизирован с ЮKassa",
		"status":  payment.Status,
	})
}

// ─── Helpers ───

func extractPathID(path, prefix, suffix string) int {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, suffix)
	s = strings.Trim(s, "/")
	id, _ := strconv.Atoi(s)
	return id
}

func parseJSON(r *http.Request, v interface{}) error {
	if r.Body == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(v)
}
