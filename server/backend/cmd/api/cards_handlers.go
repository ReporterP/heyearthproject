package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
	"github.com/heyearth/backend/internal/tbank"
)

// ─────────────────────── User Cards ───────────────────────

// handleUserCards returns saved payment cards for the authenticated user.
func handleUserCards(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"cards": []interface{}{}})
		return
	}

	cards, err := tbClient.GetCardList(userID)
	if err != nil {
		// Not critical — user just has no saved cards
		log.Printf("[cards] GetCardList for %s: %v", userID, err)
		writeJSON(w, http.StatusOK, map[string]interface{}{"cards": []interface{}{}})
		return
	}

	// Format for frontend
	type cardResponse struct {
		CardID   string `json:"card_id"`
		PAN      string `json:"pan"`
		ExpDate  string `json:"exp_date"`
		CardType string `json:"card_type"`
	}
	var result []cardResponse
	for _, c := range cards {
		ct := "debit"
		switch c.CardType {
		case 1:
			ct = "credit"
		case 2:
			ct = "corporate"
		}
		result = append(result, cardResponse{
			CardID:   c.CardID,
			PAN:      c.PAN,
			ExpDate:  c.ExpDate,
			CardType: ct,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cards": result})
}

// handleDeleteCard removes a saved card for the authenticated user.
func handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	// CardID from URL path: /api/v1/user/cards/{cardId}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/user/cards/")
	cardID := strings.TrimSpace(path)
	if cardID == "" {
		var body struct {
			CardID string `json:"card_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		cardID = body.CardID
	}
	if cardID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "card_id required"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured"})
		return
	}

	if err := tbClient.RemoveCard(userID, cardID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "remove card: " + err.Error()})
		return
	}

	// If this was the saved payment method for auto-renew, clear it
	db.DB.Exec("UPDATE users SET saved_payment_method = NULL WHERE user_id = $1 AND saved_payment_method IN (SELECT RebillId FROM (SELECT $2 AS RebillId) t)", userID, cardID)

	writeJSON(w, http.StatusOK, map[string]string{"message": "Карта удалена"})
}

// ─────────────────────── User Refund Request ───────────────────────

// handleRefundRequest allows user to request a refund for a recent payment.
func handleRefundRequest(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	var req struct {
		PaymentID int    `json:"payment_id"`
		Reason    string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.PaymentID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payment_id required"})
		return
	}

	// Verify payment belongs to user and is refundable
	var externalID, status string
	var amount float64
	err = db.DB.QueryRow(`
		SELECT COALESCE(p.external_id, ''), p.status, p.amount
		FROM payments p
		JOIN users u ON u.id = p.user_id
		WHERE p.id = $1 AND u.user_id = $2
	`, req.PaymentID, userID).Scan(&externalID, &status, &amount)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "платёж не найден"})
		return
	}
	if status != "completed" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "возврат доступен только для завершённых платежей"})
		return
	}
	if externalID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "платёж без внешнего ID (не через T-Bank)"})
		return
	}

	tbClient, err := tbank.NewClientFromSettings()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "payments not configured"})
		return
	}

	// Full refund
	cancelResult, err := tbClient.Cancel(externalID, 0)
	if err != nil {
		log.Printf("[refund] user %s payment %d: %v", userID, req.PaymentID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "T-Bank отклонил возврат: " + err.Error()})
		return
	}

	// Update payment status
	db.DB.Exec("UPDATE payments SET status = 'refunded', notes = $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2",
		fmt.Sprintf("Refund by user: %s. T-Bank status: %s", req.Reason, cancelResult.Status), req.PaymentID)

	log.Printf("[refund] User %s: payment %d refunded (T-Bank status: %s)", userID, req.PaymentID, cancelResult.Status)
	writeJSON(w, http.StatusOK, map[string]string{"message": "Возврат оформлен", "status": cancelResult.Status})
}

// ─────────────────────── Auto-Renew Toggle ───────────────────────

// handleToggleAutoRenew allows user to enable/disable auto-renewal.
func handleToggleAutoRenew(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	var req struct {
		AutoRenew bool `json:"auto_renew"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	// Get user DB ID
	var dbUserID int
	if err := db.DB.QueryRow("SELECT id FROM users WHERE user_id = $1", userID).Scan(&dbUserID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	// Update active subscription
	result, err := db.DB.Exec("UPDATE user_subscriptions SET auto_renew = $1 WHERE user_id = $2 AND valid_until > NOW()", req.AutoRenew, dbUserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет активной подписки"})
		return
	}

	status := "включено"
	if !req.AutoRenew {
		status = "выключено"
	}
	biz.NotifySubscriptionChange()
	writeJSON(w, http.StatusOK, map[string]string{"message": "Автопродление " + status, "auto_renew": fmt.Sprintf("%v", req.AutoRenew)})
}
