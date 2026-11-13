package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── User Saved Payment Methods (YooKassa) ───────────────────────
// YooKassa хранит payment_method_id, который мы привязываем к пользователю
// при payment.succeeded с save_payment_method:true. Детали (last4, card_type и тд)
// берутся из saved_payment_methods таблицы.

func handleUserCards(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var dbUserID int
	if err := db.DB.QueryRow("SELECT id FROM users WHERE user_id = $1", userID).Scan(&dbUserID); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"cards": []interface{}{}})
		return
	}

	rows, err := db.DB.Query(`
		SELECT id, yookassa_payment_method_id, method_type,
		       COALESCE(card_last4, ''), COALESCE(card_type, ''), COALESCE(card_issuer, ''),
		       COALESCE(title, ''), created_at
		FROM saved_payment_methods
		WHERE user_id = $1 AND is_active = TRUE
		ORDER BY created_at DESC
	`, dbUserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	cards := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var ykID, mtype, last4, ctype, issuer, title string
		var createdAt string
		if err := rows.Scan(&id, &ykID, &mtype, &last4, &ctype, &issuer, &title, &createdAt); err != nil {
			continue
		}
		card := map[string]interface{}{
			"id":                id,
			"payment_method_id": ykID,
			"type":              mtype,
			"title":             title,
			"created_at":        createdAt,
		}
		if last4 != "" {
			card["last4"] = last4
		}
		if ctype != "" {
			card["card_type"] = ctype
		}
		if issuer != "" {
			card["issuer"] = issuer
		}
		cards = append(cards, card)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cards": cards})
}

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
		writeAuthError(w, err)
		return
	}

	var req struct {
		PaymentMethodID string `json:"payment_method_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var dbUserID int
	if err := db.DB.QueryRow("SELECT id FROM users WHERE user_id = $1", userID).Scan(&dbUserID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	// Деактивируем в saved_payment_methods
	if req.PaymentMethodID != "" {
		_, _ = db.DB.Exec(`
			UPDATE saved_payment_methods SET is_active = FALSE, updated_at = CURRENT_TIMESTAMP
			WHERE user_id = $1 AND yookassa_payment_method_id = $2
		`, dbUserID, req.PaymentMethodID)
	} else {
		// Делаем все неактивными
		_, _ = db.DB.Exec(`UPDATE saved_payment_methods SET is_active = FALSE WHERE user_id = $1`, dbUserID)
	}

	// Сбрасываем users.saved_payment_method и отключаем автопродление
	_, _ = db.DB.Exec(`UPDATE users SET saved_payment_method = NULL WHERE id = $1`, dbUserID)
	_, _ = db.DB.Exec(`UPDATE user_subscriptions SET auto_renew = FALSE WHERE user_id = $1`, dbUserID)

	writeJSON(w, http.StatusOK, map[string]string{"message": "Способ оплаты удалён"})
}

// ─────────────────────── User Refund Request ───────────────────────

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
		writeAuthError(w, err)
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

	// Verify payment belongs to user
	var status string
	err = db.DB.QueryRow(`SELECT p.status FROM payments p WHERE p.id = $1 AND p.user_id_text = $2`,
		req.PaymentID, userID).Scan(&status)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "платёж не найден"})
		return
	}
	if !biz.IsPaidPaymentStatus(status) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "заявка на возврат доступна только для оплаченных платежей"})
		return
	}

	reason := req.Reason
	if reason == "" {
		reason = "без указания причины"
	}
	_, err = db.DB.Exec(`UPDATE payments SET notes = COALESCE(notes, '') || $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`,
		fmt.Sprintf("\n[ЗАЯВКА НА ВОЗВРАТ от %s] %s", userID, reason), req.PaymentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[refund-request] User %s requested refund for payment %d: %s", userID, req.PaymentID, reason)
	RaiseAlert(fmt.Sprintf("refund-request-%d", req.PaymentID), AlertWarn, "Заявка на возврат",
		fmt.Sprintf("Пользователь %s просит вернуть платёж #%d. Причина: %s. Решение — в «Платежах» (кнопка «Возврат»).", userID, req.PaymentID, reason))
	writeJSON(w, http.StatusOK, map[string]string{"message": "Заявка на возврат отправлена. Мы рассмотрим её в ближайшее время."})
}

// ─────────────────────── Auto-Renew Toggle ───────────────────────

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
		writeAuthError(w, err)
		return
	}

	var req struct {
		AutoRenew bool `json:"auto_renew"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	var dbUserID int
	if err := db.DB.QueryRow("SELECT id FROM users WHERE user_id = $1", userID).Scan(&dbUserID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

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
