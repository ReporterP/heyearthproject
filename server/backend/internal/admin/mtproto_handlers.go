package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/heyearth/backend/internal/biz"
)

// MTProtoSettings — admin-facing representation of all mtproto_* settings.
type MTProtoSettings struct {
	Enabled        bool   `json:"enabled"`
	Host           string `json:"host"`
	Port           string `json:"port"`
	Secret         string `json:"secret"`
	SponsorChannel string `json:"sponsor_channel"`
}

// GetMTProtoSettings — GET /api/admin/mtproto
func GetMTProtoSettings(w http.ResponseWriter, r *http.Request) {
	s := MTProtoSettings{
		Enabled:        biz.GetSettingOrDefault("mtproto_enabled", "false") == "true",
		Host:           biz.GetSettingOrDefault("mtproto_host", ""),
		Port:           biz.GetSettingOrDefault("mtproto_port", "8443"),
		Secret:         biz.GetSettingOrDefault("mtproto_secret", ""),
		SponsorChannel: biz.GetSettingOrDefault("mtproto_sponsor_channel", ""),
	}
	writeAdminJSON(w, http.StatusOK, s)
}

// UpdateMTProtoSettings — PUT /api/admin/mtproto
func UpdateMTProtoSettings(w http.ResponseWriter, r *http.Request) {
	var s MTProtoSettings
	if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}

	enabled := "false"
	if s.Enabled {
		enabled = "true"
	}
	for k, v := range map[string]string{
		"mtproto_enabled":         enabled,
		"mtproto_host":            s.Host,
		"mtproto_port":            s.Port,
		"mtproto_secret":          s.Secret,
		"mtproto_sponsor_channel": s.SponsorChannel,
	} {
		if err := biz.SetSetting(k, v); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to save: "+k)
			return
		}
	}

	biz.BumpSubscriptionVersion()

	WriteAuditLog(r, "update_mtproto_settings", "settings", "mtproto", nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "MTProto settings updated"})
}

// GenerateMTProtoSecret — POST /api/admin/mtproto/generate-secret
// Produces 32 hex chars (16 random bytes) and saves to mtproto_secret.
func GenerateMTProtoSecret(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to generate random")
		return
	}
	secret := hex.EncodeToString(buf)
	if err := biz.SetSetting("mtproto_secret", secret); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save secret")
		return
	}
	biz.BumpSubscriptionVersion()
	WriteAuditLog(r, "generate_mtproto_secret", "settings", "mtproto", nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"secret": secret})
}
