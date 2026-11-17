// Package tgweb is the Telegram WEB proxy (phase T): its settings, the link subscribers get, the
// install / switch-off runs on the Hub (internal/provision TGWebRecipe) and the relay's status.
// The secret never leaves this package except inside the subscriber's link and the Hub's files.
package tgweb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/config"
)

// app_settings keys.
const (
	KeyEnabled  = "tg_webproxy_enabled"   // the owner's switch: "true" | "false"
	KeyHost     = "tg_webproxy_host"      // the public hostname ("" = DefaultHost)
	KeySecret   = "tg_webproxy_secret"    // 32 hex, never printed
	KeyTokenKey = "tg_webproxy_token_key" // 32 bytes hex: the relay's token.key (the DB backup carries it)
	// KeyLiveHost is the host of the last successful install: the link is handed out only while it
	// equals the configured host (a new host first needs its certificate and nginx).
	KeyLiveHost = "tg_webproxy_live_host"
	KeyLastRun  = "tg_webproxy_last_run" // JSON of the last finished run (outputs redacted)
)

// Setting access (tests swap them).
var (
	getSetting = biz.GetSettingContext
	setSetting = func(_ context.Context, k, v string) error { return biz.SetSetting(k, v) }
)

// SetSetting writes one of this package's keys (the admin handlers).
func SetSetting(ctx context.Context, key, value string) error { return setSetting(ctx, key, value) }

func setting(ctx context.Context, key string) (string, error) {
	v, err := getSetting(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(v), err
}

// SiteDomain is the site's domain (SITE_DOMAIN / PUBLIC_BASE_URL of api.env).
func SiteDomain() string {
	for _, k := range []string{"SITE_DOMAIN", "PUBLIC_BASE_URL"} {
		v := strings.TrimSpace(os.Getenv(k))
		if v == "" {
			continue
		}
		if u, err := url.Parse(v); err == nil && u.Hostname() != "" {
			return strings.ToLower(u.Hostname())
		}
		return strings.ToLower(strings.TrimRight(v, "/"))
	}
	return "hiearth.ru"
}

// DefaultHost is tg.<site domain>.
func DefaultHost() string { return "tg." + SiteDomain() }

// Settings is the WEB proxy's configuration as stored.
type Settings struct {
	Enabled  bool
	Host     string // DefaultHost when not set
	Secret   string
	TokenKey string
	LiveHost string
}

// Load reads the settings; a failed read is an error (never a guessed default).
func Load(ctx context.Context) (Settings, error) {
	var s Settings
	vals := map[string]*string{KeyHost: &s.Host, KeySecret: &s.Secret, KeyTokenKey: &s.TokenKey, KeyLiveHost: &s.LiveHost}
	for k, p := range vals {
		v, err := setting(ctx, k)
		if err != nil {
			return s, fmt.Errorf("настройка %s не прочитана: %w", k, err)
		}
		*p = v
	}
	en, err := setting(ctx, KeyEnabled)
	if err != nil {
		return s, fmt.Errorf("настройка %s не прочитана: %w", KeyEnabled, err)
	}
	s.Enabled = en == "true"
	s.Host = strings.ToLower(s.Host)
	if s.Host == "" {
		s.Host = DefaultHost()
	}
	return s, nil
}

// LinkReady: subscribers get the link (switched on, installed for this very host, a secret).
func (s Settings) LinkReady() bool {
	return s.Enabled && s.LiveHost != "" && s.LiveHost == s.Host && config.TGWebSecretRe.MatchString(s.Secret)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ensureKeys creates the secret and the token key once (both kept forever: the token key is never
// rotated, the secret only by RotateSecret).
func ensureKeys(ctx context.Context, s *Settings) error {
	if !config.TGWebSecretRe.MatchString(s.Secret) {
		v, err := randomHex(16)
		if err != nil {
			return err
		}
		if err := setSetting(ctx, KeySecret, v); err != nil {
			return fmt.Errorf("секрет WEB-прокси не сохранён: %w", err)
		}
		s.Secret = v
	}
	if _, err := config.TGWebTokenKey(s.TokenKey); err != nil {
		v, err := randomHex(32)
		if err != nil {
			return err
		}
		if err := setSetting(ctx, KeyTokenKey, v); err != nil {
			return fmt.Errorf("ключ подписи токенов не сохранён: %w", err)
		}
		s.TokenKey = v
	}
	return nil
}

// UserLink is the WEB proxy part of the cabinet's answer.
type UserLink struct {
	Link   string `json:"link"`    // https://t.me/webproxy?server=<host>&secret=<secret>
	TGLink string `json:"tg_link"` // tg://webproxy?… — when t.me does not open the first one
	Server string `json:"server"`
	Secret string `json:"secret"`
}

// Links builds the subscriber's links for a root deployment (no base path: the plain hex secret).
func Links(host, secret string) UserLink {
	q := "server=" + url.QueryEscape(host) + "&secret=" + url.QueryEscape(secret)
	return UserLink{Link: "https://t.me/webproxy?" + q, TGLink: "tg://webproxy?" + q, Server: host, Secret: secret}
}

// ForUser is the link for an active subscriber (nil: the WEB proxy is not handed out now).
func ForUser(ctx context.Context) (*UserLink, error) {
	s, err := Load(ctx)
	if err != nil {
		return nil, err
	}
	if !s.LinkReady() {
		return nil, nil
	}
	l := Links(s.Host, s.Secret)
	return &l, nil
}
