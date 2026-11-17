package admin

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/heyearth/backend/internal/biz"
)

// MTProtoSettings — admin-facing representation of all mtproto_* settings.
type MTProtoSettings struct {
	Enabled bool   `json:"enabled"`
	Port    string `json:"port"` // read-only here: the port every exit listens on (mtproto_port)
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
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	s.Secret, s.FrontingDomain = strings.TrimSpace(s.Secret), strings.TrimSpace(s.FrontingDomain)
	if s.Secret != "" && !mtprotoSecretFor(s.Secret, s.FrontingDomain) {
		writeAdminError(w, http.StatusBadRequest, "Секрет не подходит к домену маскировки: нажмите «Сгенерировать» после смены домена")
		return
	}
	if s.Enabled && (s.Secret == "" || s.FrontingDomain == "") {
		writeAdminError(w, http.StatusBadRequest, "Чтобы включить MTProto, задайте домен маскировки и секрет")
		return
	}
	prevSecret := biz.GetSettingOrDefault("mtproto_secret", "")

	enabled := "false"
	if s.Enabled {
		enabled = "true"
	}
	autoInstall := "false"
	if s.AutoInstall {
		autoInstall = "true"
	}
	// host and port are not this form's: the host is picked per request, the port is fixed.
	for k, v := range map[string]string{
		"mtproto_enabled":         enabled,
		"mtproto_secret":          s.Secret,
		"mtproto_fronting_domain": s.FrontingDomain,
		"mtproto_sponsor_channel": s.SponsorChannel,
		"mtproto_auto_install":    autoInstall,
	} {
		if err := biz.SetSetting(k, v); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Failed to save: "+k)
			return
		}
	}

	biz.BumpSubscriptionVersion()

	WriteAuditLog(r, "update_mtproto_settings", "settings", "mtproto", map[string]any{
		"enabled": s.Enabled, "auto_install": s.AutoInstall, "fronting_domain": s.FrontingDomain, "secret_changed": s.Secret != prevSecret})
	writeAdminJSON(w, http.StatusOK, map[string]string{"message": "MTProto settings updated"})
}

// GenerateMTProtoSecret — POST /api/admin/mtproto/generate-secret {fronting_domain?}
//
// Produces a full mtg fake-TLS secret: "ee" + 16 random bytes + hex(fronting domain) — for
// the domain in the form (or the stored one). It only returns it: nothing changes until
// «Сохранить» (C6), so a stray click does not break every client's deeplink.
func GenerateMTProtoSecret(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FrontingDomain string `json:"fronting_domain"`
	}
	_ = decodeOptional(r, &req)
	domain := strings.TrimSpace(req.FrontingDomain)
	if domain == "" {
		domain = biz.MTProtoFrontingDomain()
	}
	secret, err := biz.BuildMTProtoSecret(domain)
	if err != nil {
		writeAdminError(w, http.StatusBadRequest, "Задайте домен маскировки перед генерацией секрета — он кодируется внутрь секрета")
		return
	}
	writeAdminJSON(w, http.StatusOK, map[string]string{"secret": secret})
}

// mtprotoSecretFor: an mtg fake-TLS secret («ee» + 32 hex + hex of the domain).
func mtprotoSecretFor(secret, domain string) bool {
	s := strings.ToLower(secret)
	want := "ee"
	if len(s) != len(want)+32+2*len(domain) || !strings.HasPrefix(s, want) {
		return false
	}
	if _, err := hex.DecodeString(s[2:]); err != nil {
		return false
	}
	return s[34:] == hex.EncodeToString([]byte(strings.ToLower(domain)))
}

// mtprotoOn: the Telegram proxy is switched on in «MTProto» (a new exit gets it only then).
var mtprotoOn = func() bool { return biz.GetSettingOrDefault("mtproto_enabled", "false") == "true" }
