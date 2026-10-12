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
