package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/robokassa"
)

// ─────────────────────── Admin Payment Actions ───────────────────────
// Robokassa doesn't have API for Cancel/GetState/Resend.
// Refunds are processed through Robokassa admin panel.
// These handlers manage local payment status only.

// handleAdminPaymentCancel marks a payment as cancelled/refunded in our DB.
// Actual refund must be processed in Robokassa admin panel.
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
		Reason string `json:"reason"`
	}
	// ignore decode errors for optional body
	_ = parseJSON(r, &req)

	reason := req.Reason
	if reason == "" {
		reason = "Admin cancel"
	}

	// Try Robokassa API refund if partner ID is configured
	var opKey string
	db.DB.QueryRow("SELECT COALESCE(external_id, '') FROM payments WHERE id = $1", paymentID).Scan(&opKey)

	refundMsg := "Платёж помечен как возврат."
	roboClient, roboErr := robokassa.NewClientFromSettings()
	if roboErr == nil && opKey != "" {
		if err := roboClient.Refund(opKey, 0); err != nil {
			log.Printf("[admin-cancel] Robokassa refund for payment %d failed: %v", paymentID, err)
			refundMsg = fmt.Sprintf("Автовозврат не удался: %v. Оформите вручную в панели Robokassa.", err)
		} else {
			refundMsg = "Возврат через Robokassa API выполнен успешно."
			log.Printf("[admin-cancel] Payment %d refunded via Robokassa API", paymentID)
		}
	} else {
		refundMsg += " Оформите возврат в панели Robokassa."
	}

	_, err := db.DB.Exec("UPDATE payments SET status = 'refunded', notes = COALESCE(notes,'') || $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2",
		fmt.Sprintf("\n[ВОЗВРАТ admin] %s | %s", reason, refundMsg), paymentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": refundMsg})
}

// handleAdminPaymentCheckStatus returns the local status of a payment.
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

	var status, provider string
	var amount float64
	err := db.DB.QueryRow("SELECT status, COALESCE(provider,''), amount FROM payments WHERE id = $1", paymentID).Scan(&status, &provider, &amount)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"local_status": status,
		"provider":     provider,
		"amount":       amount,
	})
}

// handleAdminPaymentResend — not available for Robokassa.
func handleAdminPaymentResend(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "Переотправка уведомлений недоступна для Robokassa. Проверьте статус в панели Robokassa.",
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
