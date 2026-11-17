package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The Telegram alert credentials have their own endpoint (validation, audit, cache refresh).
// The generic POST must refuse them before touching the DB: db.DB is nil in tests, so a
// request that got through to biz.SetSetting would panic instead of answering 400.
func TestUpdateSettingRejectsAlertTelegramKeys(t *testing.T) {
	for _, key := range []string{
		"alert_telegram_bot_token",
		"alert_telegram_chat_id",
		"  alert_telegram_chat_id ",
		"ALERT_TELEGRAM_BOT_TOKEN",
	} {
		t.Run(strings.TrimSpace(key), func(t *testing.T) {
			body, _ := json.Marshal(settingPayload{Key: key, Value: "123456789:AAFakeTokenFakeTokenFakeTokenFake12"})
			rec := httptest.NewRecorder()
			UpdateSettingHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings", strings.NewReader(string(body))))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
			}
			var resp map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(resp["error"], "/api/admin/alerts/settings") {
				t.Fatalf("the error must point to the alerts endpoint: %q", resp["error"])
			}
			if strings.Contains(rec.Body.String(), "AAFake") {
				t.Fatalf("the value must not be echoed: %s", rec.Body.String())
			}
		})
	}
}

func TestUpdateSettingValidation(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"bad json", `{`},
		{"empty key", `{"key":"  ","value":"x"}`},
	} {
		rec := httptest.NewRecorder()
		UpdateSettingHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", tc.name, rec.Code)
		}
	}
}

// Review Focus 5 → GET /settings never returns a secret, only whether it is set.
func TestListSettingsDropsSecrets(t *testing.T) {
	old := loadAllSettings
	t.Cleanup(func() { loadAllSettings = old })
	loadAllSettings = func() (map[string]string, error) {
		return map[string]string{
			"yookassa_shop_id": "12345", "yookassa_secret_key": "live_SECRET_yk", "smtp_password": "SECRET_smtp",
			"hysteria2_obfs_password": "SECRET_obfs", "hysteria2_stats_secret": "SECRET_stats",
			"hub_tunnel_password":      "SECRET_hub",
			"alert_telegram_bot_token": "123:SECRET_tg", "vless_uuid": "SECRET-uuid", "partner_api_key": "SECRET_api",
			"main_server_host": "hiearth.ru", "site_url": "https://hiearth.ru",
		}, nil
	}
	rec := httptest.NewRecorder()
	ListSettingsHandler(rec, httptest.NewRequest(http.MethodGet, "/api/admin/settings", nil))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "SECRET") {
		t.Fatalf("status %d, a secret value leaked: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Settings   map[string]string `json:"settings"`
		SecretsSet map[string]bool   `json:"secrets_set"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Settings["main_server_host"] != "hiearth.ru" || resp.Settings["yookassa_shop_id"] != "12345" {
		t.Errorf("plain settings lost: %v", resp.Settings)
	}
	for _, k := range []string{"yookassa_secret_key", "smtp_password", "hysteria2_obfs_password", "vless_uuid", "partner_api_key"} {
		if _, leaked := resp.Settings[k]; leaked || !resp.SecretsSet[k] {
			t.Errorf("%s: in settings=%v, secrets_set=%v", k, leaked, resp.SecretsSet[k])
		}
	}
}

// The UI renders a write-only field for every listed secret, so secrets_set names each of them
// even when app_settings has no row (false) or only whitespace (false).
func TestRedactSettingsReportsUnsetSecrets(t *testing.T) {
	plain, set := redactSettings(map[string]string{"smtp_password": "  ", "smtp_host": "smtp.yandex.ru"})
	if len(plain) != 1 || plain["smtp_host"] != "smtp.yandex.ru" {
		t.Errorf("plain = %v", plain)
	}
	for _, k := range secretSettingKeys {
		if v, ok := set[k]; !ok || v {
			t.Errorf("secrets_set[%s] = %v (present %v), want false", k, v, ok)
		}
	}
}

func TestUpdateSettingRefusesEmptySecret(t *testing.T) {
	rec := httptest.NewRecorder()
	UpdateSettingHandler(rec, httptest.NewRequest(http.MethodPost, "/api/admin/settings",
		strings.NewReader(`{"key":"smtp_password","value":"  "}`)))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "секрет") {
		t.Fatalf("status %d %s: an empty write-only field must not wipe the secret", rec.Code, rec.Body.String())
	}
}
