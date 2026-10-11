package main

import (
	"errors"
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

func handleAdminAlertSettings(w http.ResponseWriter, r *http.Request) {
	setCORS(w, r)
	switch r.Method {
	case http.MethodGet:
		token := biz.GetSettingOrDefault(alertTokenSetting, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"token_set":  token != "",
			"token_hint": maskAlertToken(token), // the token itself is never returned
			"chat_id":    biz.GetSettingOrDefault(alertChatIDSetting, ""),
		})
	case http.MethodPut:
		var req struct {
			BotToken   string `json:"bot_token"` // empty = keep the stored token
			ChatID     string `json:"chat_id"`
			ClearToken bool   `json:"clear_token"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		token, chatID := strings.TrimSpace(req.BotToken), strings.TrimSpace(req.ChatID)
		if token != "" && !telegramTokenRe.MatchString(token) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный формат токена (ожидается 123456789:AA…)"})
			return
		}
		if chatID != "" && !telegramChatIDRe.MatchString(chatID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неверный chat_id: число (например -1001234567890) или @channel"})
			return
		}
		tokenChanged := req.ClearToken || token != ""
		if tokenChanged {
			if err := biz.SetSetting(alertTokenSetting, token); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save token"})
				return
			}
		}
		if err := biz.SetSetting(alertChatIDSetting, chatID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save chat_id"})
			return
		}
		admin.WriteAuditLog(r, "update_alert_settings", "setting", "alert_telegram",
			map[string]any{"chat_id": chatID, "token_changed": tokenChanged})
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
	if err := sendTestAlert(); err != nil {
		status := http.StatusBadGateway // never 401/403: admin_web treats those as logout
		if errors.Is(err, errAlertsNotConfigured) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	admin.WriteAuditLog(r, "send_test_alert", "setting", "alert_telegram", map[string]any{"delivered": true})
	writeJSON(w, http.StatusOK, map[string]string{"message": "Тестовое сообщение отправлено"})
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
	events, err := listAlertEvents(limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load alert events"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
