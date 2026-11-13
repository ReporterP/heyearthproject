package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

type settingPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// secretSettingKeys never leave the API (Review Focus 5): GET /settings drops them and reports
// only whether each is set; POST /settings still writes them (write-only fields in the UI, like
// the bot token on «Алерты»). Any other key isSensitiveSettingKey flags is dropped as well.
var secretSettingKeys = []string{
	"yookassa_secret_key", "smtp_password",
	"hysteria2_obfs_password", "hysteria2_stats_secret", "hub_tunnel_password",
	"awg_server_private_key", "alert_telegram_bot_token", "vless_uuid",
}

// loadAllSettings is biz.GetAllSettings; tests swap it.
var loadAllSettings = biz.GetAllSettings

func isSecretSetting(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	return slices.Contains(secretSettingKeys, k) || isSensitiveSettingKey(k)
}

// redactSettings splits app_settings into what the browser may see and, for every secret (the
// listed ones always, other sensitive-looking ones when present), whether it is set.
func redactSettings(all map[string]string) (map[string]string, map[string]bool) {
	plain, set := make(map[string]string, len(all)), map[string]bool{}
	for _, k := range secretSettingKeys {
		set[k] = false
	}
	for k, v := range all {
		if isSecretSetting(k) {
			set[k] = strings.TrimSpace(v) != ""
			continue
		}
		plain[k] = v
	}
	return plain, set
}

func ListSettingsHandler(w http.ResponseWriter, r *http.Request) {
	all, err := loadAllSettings()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	plain, set := redactSettings(all)
	writeAdminJSON(w, http.StatusOK, map[string]any{"settings": plain, "secrets_set": set})
}

// isSensitiveSettingKey returns true for settings whose values must never be
// written to the audit log in plaintext (API secrets, tokens, passwords, etc).
func isSensitiveSettingKey(key string) bool {
	k := strings.ToLower(key)
	patterns := []string{"secret", "token", "password", "api_key", "private_key", "webhook_key", "client_secret"}
	for _, p := range patterns {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// maskSecret returns a safe placeholder for sensitive values. For values longer
// than 8 chars, preserves first and last 2 chars to help identify key rotations
// while keeping the bulk of the secret hidden.
func maskSecret(v string) string {
	if len(v) == 0 {
		return ""
	}
	if len(v) <= 8 {
		return "***"
	}
	return v[:2] + strings.Repeat("*", len(v)-4) + v[len(v)-2:]
}

// settingsWithOwnEndpoint are app_settings keys the generic POST must not write: their own
// endpoint validates the value, audits the change and refreshes in-memory state (the Telegram
// alert credentials are cached for DB outages, see cmd/api/alerts.go).
var settingsWithOwnEndpoint = map[string]string{
	"alert_telegram_bot_token": "/api/admin/alerts/settings",
	"alert_telegram_chat_id":   "/api/admin/alerts/settings",
}

func UpdateSettingHandler(w http.ResponseWriter, r *http.Request) {
	var payload settingPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	key := strings.TrimSpace(payload.Key)
	if key == "" {
		writeAdminError(w, http.StatusBadRequest, "key is required")
		return
	}
	if endpoint, ok := settingsWithOwnEndpoint[strings.ToLower(key)]; ok {
		writeAdminError(w, http.StatusBadRequest,
			fmt.Sprintf("%s меняется только на странице «Алерты» (%s): там значение проверяется и попадает в аудит", key, endpoint))
		return
	}
	if isSecretSetting(key) && strings.TrimSpace(payload.Value) == "" {
		writeAdminError(w, http.StatusBadRequest,
			"Пустое значение не сохраняется: секрет хранится только на сервере, поле в админке всегда пустое. Введите новое значение, чтобы заменить.")
		return
	}
	// Only the keys of the page, each checked (C9): a typo never becomes a new setting.
	value, err := normalizeSetting(key, payload.Value)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload.Value = value
	if err := biz.SetSetting(key, payload.Value); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save setting")
		return
	}
	// Never write sensitive values to audit log in plaintext.
	auditValue := payload.Value
	if isSensitiveSettingKey(key) {
		auditValue = maskSecret(payload.Value)
	}
	WriteAuditLog(r, "update_setting", "setting", key, map[string]string{"value": auditValue})
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Setting saved"})
}
