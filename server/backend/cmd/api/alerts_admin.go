package main

import (
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/heyearth/backend/internal/admin"
	"github.com/heyearth/backend/internal/biz"
)

var (
	telegramTokenRe  = regexp.MustCompile(`^\d{5,}:[A-Za-z0-9_-]{30,}$`)
	telegramChatIDRe = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{4,31})$`)
)

// Side effects of the handlers; tests swap them.
var (
	alertSettingSet = biz.SetSetting
	alertAudit      = admin.WriteAuditLog
	alertEventsList = listAlertEvents
	alertSendTest   = sendTestAlert
)

// registerAlertAdminRoutes: GET/PUT /api/admin/alerts/settings, POST /api/admin/alerts/test,
// GET /api/admin/alerts/events. RBAC: settings.read / settings.write.
func registerAlertAdminRoutes() {
	http.HandleFunc("/api/admin/alerts/settings", func(w http.ResponseWriter, r *http.Request) {
		perm := "settings.write"
		if r.Method == http.MethodGet {
			perm = "settings.read"
		}
		admin.RequirePermission(perm, handleAdminAlertSettings)(w, r)
	})
	http.HandleFunc("/api/admin/alerts/test", admin.RequirePermission("settings.write", handleAdminAlertTest))
	http.HandleFunc("/api/admin/alerts/events", admin.RequirePermission("settings.read", handleAdminAlertEvents))
}

func maskAlertToken(t string) string {
	if t == "" {
		return ""
	}
	if len(t) <= 8 {
		return "***"
	}
	return t[:4] + "…" + t[len(t)-2:]
}

// alertSettingsRequest is the PUT body. Absent fields keep the stored value:
// an empty bot_token keeps the token (unless clear_token), a missing chat_id keeps the chat_id.
type alertSettingsRequest struct {
	BotToken   string  `json:"bot_token"`
	ChatID     *string `json:"chat_id"`
	ClearToken bool    `json:"clear_token"`
}

type alertSettingsUpdate struct {
	token     string
	setToken  bool
	chatID    string
	setChatID bool
}

// validate returns what to write, or a user-facing error message.
func (req alertSettingsRequest) validate() (alertSettingsUpdate, string) {
	var upd alertSettingsUpdate
	token := strings.TrimSpace(req.BotToken)
	if token != "" && !telegramTokenRe.MatchString(token) {
		return upd, "неверный формат токена (ожидается 123456789:AA…)"
	}
	if req.ChatID != nil {
		chatID := strings.TrimSpace(*req.ChatID)
		if chatID != "" && !telegramChatIDRe.MatchString(chatID) {
			return upd, "неверный chat_id: число (например -1001234567890) или @channel"
		}
		upd.chatID, upd.setChatID = chatID, true
	}
	upd.token, upd.setToken = token, req.ClearToken || token != ""
	return upd, ""
}

func handleAdminAlertSettings(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	switch r.Method {
	case http.MethodGet:
		// A DB error is an error, not "not configured": the form would offer to overwrite the
		// stored values with empty ones.
		cfg, err := alertTelegram.fresh()
		if err != nil {
			log.Printf("[alerts] чтение настроек Telegram: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "БД недоступна: не удалось прочитать настройки алертов"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"token_set":  cfg.token != "",
			"token_hint": maskAlertToken(cfg.token), // the token itself is never returned
			"chat_id":    cfg.chatID,
		})
	case http.MethodPut:
		var req alertSettingsRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		upd, msg := req.validate()
		if msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		// Whatever got written (even when the second write fails) must reach the credentials
		// cache, and keys waiting out a "not configured" backoff should retry with the new values.
		var written alertSettingsUpdate
		defer func() {
			if written.setToken || written.setChatID {
				alertTelegram.afterWrite(written)
				alerts.retryNow()
			}
		}()
		if upd.setToken {
			if err := alertSettingSet(alertTokenSetting, upd.token); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save token"})
				return
			}
			written.token, written.setToken = upd.token, true
		}
		if upd.setChatID {
			if err := alertSettingSet(alertChatIDSetting, upd.chatID); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save chat_id"})
				return
			}
			written.chatID, written.setChatID = upd.chatID, true
		}
		details := map[string]any{"token_changed": upd.setToken, "chat_id_changed": upd.setChatID}
		if upd.setChatID {
			details["chat_id"] = upd.chatID
		}
		alertAudit(r, "update_alert_settings", "setting", "alert_telegram", details)
		writeJSON(w, http.StatusOK, map[string]string{"message": "saved"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func handleAdminAlertTest(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	err := alertSendTest()
	details := map[string]any{"delivered": err == nil}
	if err != nil {
		details["error"] = err.Error() // the token is redacted by telegramSendMessage
	}
	alertAudit(r, "send_test_alert", "setting", "alert_telegram", details)
	if err != nil {
		writeJSON(w, alertTestErrorStatus(err), map[string]string{"error": err.Error()})
		return
	}
	alerts.retryNow() // Telegram works: undelivered alerts need not wait out their backoff
	writeJSON(w, http.StatusOK, map[string]string{"message": "Тестовое сообщение отправлено"})
}

// alertTestErrorStatus maps a test-send error; never 401/403: admin_web treats those as logout.
func alertTestErrorStatus(err error) int {
	switch {
	case errors.Is(err, errAlertsNotConfigured):
		return http.StatusBadRequest
	case errors.Is(err, errAlertsNoDB):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway // Telegram (or the way to it) refused
	}
}

func handleAdminAlertEvents(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET only"})
		return
	}
	limit := 100
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	events, err := alertEventsList(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load alert events"})
		return
	}
	// undelivered: active incidents nobody has been told about (live state, not the journal:
	// repeated failures are not journaled, see alertManager.settle).
	writeJSON(w, http.StatusOK, map[string]any{"events": events, "undelivered": alerts.undelivered()})
}
