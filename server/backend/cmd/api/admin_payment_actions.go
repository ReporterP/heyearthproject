package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/tbank"
)

// ─────────────────────── Admin Payment Actions ───────────────────────
// These handlers are registered in admin routes with RequirePermission("payments.write").

// handleAdminPaymentCancel cancels/refunds a payment via T-Bank.
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
		Amount int    `json:"amount"` // kopecks, 0 = full refund
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Get external payment ID
	var externalID, status string
	err := db.DB.QueryRow("SELECT COALESCE(external_id, ''), status FROM payments WHERE id = $1", paymentID).Scan(&externalID, &status)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
		return
	}
	if externalID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no external ID (not T-Bank payment)"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "T-Bank not configured"})
		return
	}

	cancelResult, err := tbClient.Cancel(externalID, uint64(req.Amount))
	if err != nil {
		log.Printf("[admin-cancel] payment %d: %v", paymentID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "T-Bank: " + err.Error()})
		return
	}

	// Update payment status
	newStatus := "cancelled"
	if cancelResult.Status == "REFUNDED" || cancelResult.Status == "PARTIAL_REFUNDED" {
		newStatus = "refunded"
	}
	db.DB.Exec("UPDATE payments SET status = $1, notes = $2, updated_at = CURRENT_TIMESTAMP WHERE id = $3",
		newStatus, fmt.Sprintf("Admin cancel: %s. T-Bank: %s", req.Reason, cancelResult.Status), paymentID)

	log.Printf("[admin-cancel] Payment %d: %s (T-Bank: %s)", paymentID, newStatus, cancelResult.Status)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":    "Платёж отменён",
		"status":     cancelResult.Status,
		"new_amount": cancelResult.NewAmount,
	})
}

// handleAdminPaymentCheckStatus checks current payment status via T-Bank GetState.
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

	var externalID string
	err := db.DB.QueryRow("SELECT COALESCE(external_id, '') FROM payments WHERE id = $1", paymentID).Scan(&externalID)
	if err != nil || externalID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found or no external ID"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "T-Bank not configured"})
		return
	}

	state, err := tbClient.GetState(externalID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "T-Bank: " + err.Error()})
		return
	}

	// Update local status to match T-Bank
	localStatus := mapTBankStatus(state.Status)
	db.DB.Exec("UPDATE payments SET yookassa_status = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2", state.Status, paymentID)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tbank_status": state.Status,
		"local_status": localStatus,
		"amount":       state.Amount,
		"order_id":     state.OrderID,
	})
}

// handleAdminPaymentResend re-sends webhook notifications from T-Bank.
func handleAdminPaymentResend(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "T-Bank not configured"})
		return
	}

	if err := tbClient.Resend(); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "T-Bank Resend: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Уведомления переотправлены"})
}

// ─── Helpers ───

func extractPathID(path, prefix, suffix string) int {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, suffix)
	s = strings.Trim(s, "/")
	id, _ := strconv.Atoi(s)
	return id
}

func mapTBankStatus(tbankStatus string) string {
	switch tbankStatus {
	case "NEW", "FORM_SHOWED":
		return "pending"
	case "AUTHORIZING", "3DS_CHECKING", "3DS_CHECKED", "PREAUTHORIZING":
		return "pending"
	case "AUTHORIZED":
		return "authorized"
	case "CONFIRMING", "CONFIRMED":
		return "completed"
	case "REVERSED", "PARTIAL_REVERSED":
		return "cancelled"
	case "REFUNDED", "PARTIAL_REFUNDED", "REFUNDING":
		return "refunded"
	case "REJECTED", "AUTH_FAIL":
		return "failed"
	case "DEADLINE_EXPIRED", "CANCELED":
		return "cancelled"
	default:
		return "unknown"
	}
}
