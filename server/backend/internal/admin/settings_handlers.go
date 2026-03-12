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
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "Setting saved"})
}
