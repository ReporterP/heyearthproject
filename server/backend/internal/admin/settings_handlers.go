package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

type settingPayload struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func ListSettingsHandler(w http.ResponseWriter, r *http.Request) {
	settings, err := biz.GetAllSettings()
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to load settings")
		return
	}
	writeAdminJSON(w, http.StatusOK, settings)
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

func UpdateSettingHandler(w http.ResponseWriter, r *http.Request) {
	var payload settingPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if strings.TrimSpace(payload.Key) == "" {
		writeAdminError(w, http.StatusBadRequest, "key is required")
		return
	}
	if err := biz.SetSetting(strings.TrimSpace(payload.Key), payload.Value); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save setting")
		return
	}
	// Never write sensitive values to audit log in plaintext.
	auditValue := payload.Value
	if isSensitiveSettingKey(payload.Key) {
		auditValue = maskSecret(payload.Value)
	}
	WriteAuditLog(r, "update_setting", "setting", payload.Key, map[string]string{"value": auditValue})
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Setting saved"})
}
