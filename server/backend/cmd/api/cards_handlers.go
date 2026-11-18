package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

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

	// The reason is the person's own text: one line, at most 500 characters, before it reaches
	// the notes, the journal and the owner's alert (audit F32).
	reason := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, req.Reason)), " ")
	if rs := []rune(reason); len(rs) > 500 {
		reason = string(rs[:500]) + "…"
	}
	if reason == "" {
		reason = "без указания причины"
	}
	_, err = db.DB.Exec(`UPDATE payments SET notes = COALESCE(notes, '') || $1, updated_at = CURRENT_TIMESTAMP WHERE id = $2`,
		fmt.Sprintf("\n[ЗАЯВКА НА ВОЗВРАТ от %s] %s", userID, reason), req.PaymentID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	log.Printf("[refund-request] user %s requested refund for payment %d: %q", shortHiEarthID(userID), req.PaymentID, reason)
	RaiseAlert(fmt.Sprintf("refund-request-%d", req.PaymentID), AlertWarn, "Заявка на возврат",
		fmt.Sprintf("Пользователь %s просит вернуть платёж #%d. Причина: «%s». Решение — в «Платежах» (кнопка «Возврат»).", shortHiEarthID(userID), req.PaymentID, reason))
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
