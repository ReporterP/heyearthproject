package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/heyearth/backend/internal/config"
	"github.com/heyearth/backend/internal/provision"
	"github.com/heyearth/backend/internal/tgweb"
)

// The «Telegram» page's WEB proxy card (phase T). The secret is never returned.

type tgWebArtifact struct {
	Present bool   `json:"present"`
	Commit  string `json:"commit"`
}

type tgWebView struct {
	Enabled     bool              `json:"enabled"`
	Host        string            `json:"host"`
	DefaultHost string            `json:"default_host"`
	LiveHost    string            `json:"live_host"`
	SecretSet   bool              `json:"secret_set"`
	LinkReady   bool              `json:"link_ready"`
	Artifact    tgWebArtifact     `json:"artifact"`
	Status      *tgweb.Status     `json:"status"`
	Run         *provision.HubRun `json:"run"`
}

// Seams (tests swap them).
var (
	tgManager = tgweb.Default
	tgCheck   = func(ctx context.Context, host, secret string) *tgweb.Status {
		s := tgweb.Check(ctx, provision.LocalRemote{}, host, secret)
		return &s
	}
	tgArtifactPresent = func() bool { _, err := tgweb.ReadArtifact(); return err == nil }
)

func tgWebPayload(ctx context.Context) (tgWebView, error) {
	s, err := tgweb.Load(ctx)
	if err != nil {
		return tgWebView{}, err
	}
	v := tgWebView{
		Enabled: s.Enabled, Host: s.Host, DefaultHost: tgweb.DefaultHost(), LiveHost: s.LiveHost,
		SecretSet: config.TGWebSecretRe.MatchString(s.Secret), LinkReady: s.LinkReady(),
		Artifact: tgWebArtifact{Present: tgArtifactPresent(), Commit: config.TProxyCommit[:7]},
		Run:      tgManager.Run(ctx),
	}
	v.Status = tgCheck(ctx, s.Host, s.Secret)
	return v, nil
}

func writeTGWeb(w http.ResponseWriter, r *http.Request) {
	v, err := tgWebPayload(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, "Настройки WEB-прокси не прочитаны: "+err.Error())
		return
	}
	writeAdminJSON(w, http.StatusOK, v)
}

// GetTGWebProxy — GET /api/admin/telegram/webproxy
func GetTGWebProxy(w http.ResponseWriter, r *http.Request) { writeTGWeb(w, r) }

func tgStartError(w http.ResponseWriter, err error) {
	if errors.Is(err, tgweb.ErrBusy) {
		writeAdminError(w, http.StatusConflict, err.Error())
		return
	}
	writeAdminError(w, http.StatusBadRequest, err.Error())
}

// UpdateTGWebProxy — PUT /api/admin/telegram/webproxy {enabled, host}: saves; switching on (or a
// new host while on) starts an install, switching off starts the switch-off run.
func UpdateTGWebProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool   `json:"enabled"`
		Host    string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAdminError(w, http.StatusBadRequest, "Неверное тело запроса")
		return
	}
	host := strings.ToLower(strings.TrimSpace(req.Host))
	if host == "" {
		host = tgweb.DefaultHost()
	}
	if err := config.ValidTGWebHost(host); err != nil {
		writeAdminError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	prev, err := tgweb.Load(ctx)
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tgManager.Running() {
		writeAdminError(w, http.StatusConflict, tgweb.ErrBusy.Error())
		return
	}
	enabled := "false"
	if req.Enabled {
		enabled = "true"
	}
	for k, v := range map[string]string{tgweb.KeyEnabled: enabled, tgweb.KeyHost: host} {
		if err := tgSetSetting(k, v); err != nil {
			writeAdminError(w, http.StatusInternalServerError, "Не сохранено: "+k)
			return
		}
	}
	WriteAuditLog(r, "update_tg_webproxy", "settings", "tg_webproxy", map[string]any{
		"enabled": req.Enabled, "host": host, "was_enabled": prev.Enabled, "was_host": prev.Host})
	var startErr error
	switch {
	case req.Enabled && (!prev.Enabled || host != prev.Host || prev.LiveHost != host):
		startErr = tgManager.Start(context.WithoutCancel(ctx), provision.KindTGWebInstall)
	case !req.Enabled && prev.Enabled:
		startErr = tgManager.Start(context.WithoutCancel(ctx), provision.KindTGWebOff)
	}
	if startErr != nil {
		tgStartError(w, startErr)
		return
	}
	writeTGWeb(w, r)
}

// InstallTGWebProxy — POST /api/admin/telegram/webproxy/install («Установить / обновить»).
func InstallTGWebProxy(w http.ResponseWriter, r *http.Request) {
	s, err := tgweb.Load(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.Enabled {
		writeAdminError(w, http.StatusBadRequest, "WEB-прокси выключен — включите его, установка начнётся сама")
		return
	}
	if err := tgManager.Start(context.WithoutCancel(r.Context()), provision.KindTGWebInstall); err != nil {
		tgStartError(w, err)
		return
	}
	WriteAuditLog(r, "install_tg_webproxy", "settings", "tg_webproxy", map[string]any{"host": s.Host})
	writeTGWeb(w, r)
}

// RotateTGWebProxySecret — POST /api/admin/telegram/webproxy/rotate-secret: every link handed
// out before stops working; the install run applies the new secret.
func RotateTGWebProxySecret(w http.ResponseWriter, r *http.Request) {
	s, err := tgweb.Load(r.Context())
	if err != nil {
		writeAdminError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !s.Enabled {
		writeAdminError(w, http.StatusBadRequest, "WEB-прокси выключен — секрет меняют, когда он включён")
		return
	}
	if err := tgManager.RotateSecret(context.WithoutCancel(r.Context())); err != nil {
		tgStartError(w, err)
		return
	}
	WriteAuditLog(r, "rotate_tg_webproxy_secret", "settings", "tg_webproxy", map[string]any{"host": s.Host})
	writeTGWeb(w, r)
}

// tgSetSetting is biz.SetSetting via the tgweb seam's context form.
var tgSetSetting = func(k, v string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return tgweb.SetSetting(ctx, k, v)
}
