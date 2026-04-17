package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/db"
)

// ─────────────────────── Pause / Resume Subscription ───────────────────────
//
// Пользователь может приостановить подписку на 1-3 месяца. В период паузы:
//   - доступ закрыт (can_connect=false)
//   - auto-renew не списывает
//   - сохранённая карта остаётся (юзер может возобновить без повторного ввода)
//   - history сохраняется
//
// После paused_until подписка автоматически «возобновляется» (auto_renew=true
// сработает на следующем цикле).

// POST /api/v1/user/pause-subscription
// body: {"until": "2026-06-15T00:00:00+03:00"} или {"months": 1|2|3}
func handlePauseSubscription(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	var req struct {
		Until  string `json:"until"`  // ISO timestamp
		Months int    `json:"months"` // 1|2|3
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	// Определяем pausedUntil
	var pausedUntil time.Time
	if req.Until != "" {
		pausedUntil, err = time.Parse(time.RFC3339, req.Until)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid until format, expected RFC3339"})
			return
		}
	} else if req.Months >= 1 && req.Months <= 3 {
		pausedUntil = time.Now().AddDate(0, req.Months, 0)
	} else {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provide months (1-3) or until"})
		return
	}

	// Валидация: max 3 месяца
	maxAllowed := time.Now().AddDate(0, 3, 0)
	if pausedUntil.After(maxAllowed) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "максимальная пауза — 3 месяца"})
		return
	}
	if pausedUntil.Before(time.Now().Add(1 * time.Hour)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "минимальная пауза — 1 час"})
		return
	}

	var dbUserID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	result, err := db.DB.Exec(`
		UPDATE user_subscriptions SET
			paused_until = $1,
			pause_requested_at = CURRENT_TIMESTAMP		WHERE user_id = $2 AND valid_until > NOW()
	`, pausedUntil, dbUserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "нет активной подписки для паузы"})
		return
	}

	biz.NotifySubscriptionChange()
	writeJSON(w, http.StatusOK, map[string]any{
		"message":      fmt.Sprintf("Подписка приостановлена до %s", pausedUntil.Format("02.01.2006")),
		"paused_until": pausedUntil.Format(time.RFC3339),
	})
}

// POST /api/v1/user/resume-subscription
func handleResumeSubscription(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	userID, err := extractUserFromToken(r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authorization required"})
		return
	}

	var dbUserID int
	if err := db.DB.QueryRow(`SELECT id FROM users WHERE user_id = $1`, userID).Scan(&dbUserID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	result, err := db.DB.Exec(`
		UPDATE user_subscriptions SET
			paused_until = NULL,
			pause_requested_at = NULL		WHERE user_id = $1 AND paused_until IS NOT NULL
	`, dbUserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "подписка не на паузе"})
		return
	}
	biz.NotifySubscriptionChange()
	writeJSON(w, http.StatusOK, map[string]string{"message": "Подписка возобновлена"})
}
