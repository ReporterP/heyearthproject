package admin

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

// MTProtoSettings — admin-facing representation of all mtproto_* settings.
type MTProtoSettings struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    string `json:"port"`
	Secret  string `json:"secret"`
	// FrontingDomain — fake-TLS domain baked into Secret. MUST resolve to the
	// MTProto exit nodes: TLS-sniffing clients override the connection
	// destination with it. See biz.MTProtoFrontingDomain.
	FrontingDomain string `json:"fronting_domain"`
	SponsorChannel string `json:"sponsor_channel"`
	AutoInstall    bool   `json:"auto_install"`
}

// GetMTProtoSettings — GET /api/admin/mtproto
func GetMTProtoSettings(w http.ResponseWriter, r *http.Request) {
	s := MTProtoSettings{
		Enabled:        biz.GetSettingOrDefault("mtproto_enabled", "false") == "true",
		Host:           biz.GetSettingOrDefault("mtproto_host", ""),
		Port:           biz.GetSettingOrDefault("mtproto_port", "8443"),
		Secret:         biz.GetSettingOrDefault("mtproto_secret", ""),
		FrontingDomain: biz.MTProtoFrontingDomain(),
		SponsorChannel: biz.GetSettingOrDefault("mtproto_sponsor_channel", ""),
		AutoInstall:    biz.GetSettingOrDefault("mtproto_auto_install", "true") == "true",
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
	autoInstall := "false"
	if s.AutoInstall {
		autoInstall = "true"
	}
	for k, v := range map[string]string{
		"mtproto_enabled":         enabled,
		"mtproto_host":            s.Host,
		"mtproto_port":            s.Port,
		"mtproto_secret":          s.Secret,
		"mtproto_fronting_domain": strings.TrimSpace(s.FrontingDomain),
		"mtproto_sponsor_channel": s.SponsorChannel,
		"mtproto_auto_install":    autoInstall,
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
//
// Produces a full mtg fake-TLS secret: "ee" + 16 random bytes + hex(fronting
// domain). It used to emit bare 32 hex chars, which is NOT an mtg secret — the
// installer rejected the format and silently regenerated its own secret on the
// node, so app_settings and the daemon disagreed and every client deeplink
// broke. Requires mtproto_fronting_domain to be configured.
func GenerateMTProtoSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := biz.BuildMTProtoSecret(biz.MTProtoFrontingDomain())
	if err != nil {
		writeAdminError(w, http.StatusBadRequest,
			"Задайте Fronting domain перед генерацией секрета — он кодируется внутрь секрета")
		return
	}
	if err := biz.SetSetting("mtproto_secret", secret); err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Failed to save secret")
		return
	}
	biz.BumpSubscriptionVersion()
	WriteAuditLog(r, "generate_mtproto_secret", "settings", "mtproto", nil)
	writeAdminJSON(w, http.StatusOK, map[string]string{"secret": secret})
}
