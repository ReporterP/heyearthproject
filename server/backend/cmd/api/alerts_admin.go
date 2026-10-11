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
		token := biz.GetSettingOrDefault(alertTokenSetting, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"token_set":  token != "",
			"token_hint": maskAlertToken(token), // the token itself is never returned
			"chat_id":    biz.GetSettingOrDefault(alertChatIDSetting, ""),
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
		if upd.setToken {
			if err := biz.SetSetting(alertTokenSetting, upd.token); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save token"})
				return
			}
		}
		if upd.setChatID {
			if err := biz.SetSetting(alertChatIDSetting, upd.chatID); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save chat_id"})
				return
			}
		}
		details := map[string]any{"token_changed": upd.setToken, "chat_id_changed": upd.setChatID}
		if upd.setChatID {
			details["chat_id"] = upd.chatID
		}
		admin.WriteAuditLog(r, "update_alert_settings", "setting", "alert_telegram", details)
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
