package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type auditCall struct {
	action  string
	details map[string]any
}

// withAlertAdminFakes swaps every side effect of the alert admin handlers: the settings store,
// the audit log, the journal reader, the test sender and the alert manager.
func withAlertAdminFakes(t *testing.T, fs *fakeSettings) (*[]auditCall, *alertHarness) {
	t.Helper()
	withFakeTelegram(t, fs)
	oldSet, oldAudit, oldList, oldTest, oldAlerts := alertSettingSet, alertAudit, alertEventsList, alertSendTest, alerts
	t.Cleanup(func() {
		alertSettingSet, alertAudit, alertEventsList, alertSendTest, alerts = oldSet, oldAudit, oldList, oldTest, oldAlerts
	})
	alertSettingSet = fs.set
	var audits []auditCall
	alertAudit = func(r *http.Request, action, targetType, targetID string, details any) {
		d, _ := details.(map[string]any)
		audits = append(audits, auditCall{action: action, details: d})
	}
	alertEventsList = func(limit int) ([]alertEvent, error) { return []alertEvent{}, nil }
	h := newAlertHarness()
	alerts = h.m
	return &audits, h
}

func serveAlertAdmin(handler http.HandlerFunc, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func TestAdminAlertSettingsGetNeverReturnsToken(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-1001234567890"}}
	withAlertAdminFakes(t, fs)

	rec, resp := serveAlertAdmin(handleAdminAlertSettings, http.MethodGet, "/api/admin/alerts/settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "AAFakeToken") || strings.Contains(rec.Body.String(), fakeBotToken) {
		t.Fatalf("the token leaked into the response: %s", rec.Body.String())
	}
	if resp["token_set"] != true || resp["token_hint"] != "1234…12" || resp["chat_id"] != "-1001234567890" {
		t.Fatalf("response: %v", resp)
	}

	// nothing stored: not configured (a missing row and a blank value alike)
	fs.values = map[string]string{alertChatIDSetting: "  "}
	if rec, resp := serveAlertAdmin(handleAdminAlertSettings, http.MethodGet, "/api/admin/alerts/settings", ""); rec.Code != http.StatusOK ||
		resp["token_set"] != false || resp["token_hint"] != "" || resp["chat_id"] != "" {
		t.Fatalf("empty settings: %d %v", rec.Code, resp)
	}
}

// A DB outage must not look like "not configured": the form would then offer to save empty values.
func TestAdminAlertSettingsGetDBErrorIsAnError(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-100"}, down: true}
	withAlertAdminFakes(t, fs)
	rec, resp := serveAlertAdmin(handleAdminAlertSettings, http.MethodGet, "/api/admin/alerts/settings", "")
	if rec.Code != http.StatusServiceUnavailable || resp["error"] == nil || resp["token_set"] != nil {
		t.Fatalf("want 503 with an error and no token_set: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "5432") {
		t.Fatalf("connection details must stay in the log: %s", rec.Body.String())
	}
}

func TestAdminAlertSettingsPut(t *testing.T) {
	const newToken = "987654321:BBFakeTokenFakeTokenFakeTokenFake34"
	for _, tc := range []struct {
		name       string
		body       string
		down       bool
		wantStatus int
		wantToken  string
		wantChat   string
		wantAudit  bool
	}{
		{"token only keeps chat_id", `{"bot_token":"` + newToken + `"}`, false, 200, newToken, "-100", true},
		{"empty token keeps the token", `{"bot_token":"","chat_id":"@alerts_ch"}`, false, 200, fakeBotToken, "@alerts_ch", true},
		{"explicit empty chat_id clears it", `{"chat_id":""}`, false, 200, fakeBotToken, "", true},
		{"clear_token", `{"clear_token":true}`, false, 200, "", "-100", true},
		{"bad token", `{"bot_token":"nope"}`, false, 400, fakeBotToken, "-100", false},
		{"bad chat_id", `{"chat_id":"not a chat"}`, false, 400, fakeBotToken, "-100", false},
		{"bad json", `{`, false, 400, fakeBotToken, "-100", false},
		{"db down", `{"chat_id":"-200"}`, true, 500, fakeBotToken, "-100", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSettings{values: map[string]string{alertTokenSetting: fakeBotToken, alertChatIDSetting: "-100"}}
			audits, _ := withAlertAdminFakes(t, fs)
			fs.down = tc.down
			rec, _ := serveAlertAdmin(handleAdminAlertSettings, http.MethodPut, "/api/admin/alerts/settings", tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			fs.down = false
			if fs.values[alertTokenSetting] != tc.wantToken || fs.values[alertChatIDSetting] != tc.wantChat {
				t.Fatalf("stored token=%q chat=%q", fs.values[alertTokenSetting], fs.values[alertChatIDSetting])
			}
			if got := len(*audits) == 1 && (*audits)[0].action == "update_alert_settings"; got != tc.wantAudit {
				t.Fatalf("audit %v, want audited=%v", *audits, tc.wantAudit)
			}
			if tc.wantAudit && strings.Contains(fmt.Sprint((*audits)[0].details), "FakeToken") {
				t.Fatalf("the token must not reach the audit log: %v", (*audits)[0].details)
			}
		})
	}
}

// After a PUT the cache holds the new values, so a DB outage right afterwards still delivers
// with them; keys waiting out the "not configured" backoff retry at once.
func TestAdminAlertSettingsPutRefreshesCacheAndRetries(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{}}
	_, h := withAlertAdminFakes(t, fs)
	h.m.send = sendTelegramAlert // the real path: credentials source + fake route
	h.m.Raise("k", AlertCritical, "T", "")
	if und := h.m.undelivered(); len(und) != 1 || !und[0].NotConfigured {
		t.Fatalf("setup: the raise must fail as not configured: %+v", und)
	}

	body := `{"bot_token":"` + fakeBotToken + `","chat_id":"-100"}`
	if rec, _ := serveAlertAdmin(handleAdminAlertSettings, http.MethodPut, "/api/admin/alerts/settings", body); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	fs.down = true // the DB goes away right after the save
	h.advance(time.Minute)
	h.m.Raise("k", AlertCritical, "T", "")
	if und := h.m.undelivered(); len(und) != 0 {
		t.Fatalf("the saved credentials must be used at once, from the cache: %+v", und)
	}
	if !h.events[len(h.events)-1].Delivered {
		t.Fatalf("last event: %+v", h.events[len(h.events)-1])
	}
}

func TestAdminAlertTestStatusMappingAndAudit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"delivered", nil, http.StatusOK},
		{"not configured", errAlertsNotConfigured, http.StatusBadRequest},
		{"db down, no cache", fmt.Errorf("%w: настройки Telegram не прочитаны: connection refused", errAlertsNoDB), http.StatusServiceUnavailable},
		{"telegram 401", &telegramAPIError{Status: 401, Description: "Unauthorized", FromTelegram: true}, http.StatusBadGateway},
		{"telegram 403", &telegramAPIError{Status: 403, Description: "Forbidden: bot was blocked by the user", FromTelegram: true}, http.StatusBadGateway},
		{"transport", errors.New("напрямую: dial tcp: i/o timeout"), http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeSettings{values: map[string]string{}}
			audits, h := withAlertAdminFakes(t, fs)
			alertSendTest = func() error { return tc.err }
			h.m.Raise("k", AlertWarn, "T", "")
			h.m.state["k"].retryAt = h.now.Add(time.Hour) // as if a delivery had failed

			rec, resp := serveAlertAdmin(handleAdminAlertTest, http.MethodPost, "/api/admin/alerts/test", "")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatal("401/403 log the admin out")
			}
			if len(*audits) != 1 || (*audits)[0].action != "send_test_alert" || (*audits)[0].details["delivered"] != (tc.err == nil) {
				t.Fatalf("the test send must be audited in every outcome: %+v", *audits)
			}
			if tc.err != nil {
				if resp["error"] != tc.err.Error() || (*audits)[0].details["error"] != tc.err.Error() {
					t.Fatalf("error must reach the admin and the audit: resp=%v audit=%v", resp, (*audits)[0].details)
				}
			}
			lifted := h.m.state["k"].retryAt.IsZero()
			if lifted != (tc.err == nil) {
				t.Fatalf("a successful test lifts the delivery backoffs, a failed one does not: lifted=%v", lifted)
			}
		})
	}
	rec, _ := serveAlertAdmin(handleAdminAlertTest, http.MethodGet, "/api/admin/alerts/test", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /test: %d", rec.Code)
	}
}

func TestAdminAlertEventsIncludesUndelivered(t *testing.T) {
	fs := &fakeSettings{values: map[string]string{}}
	_, h := withAlertAdminFakes(t, fs)
	h.m.send = func(string) error { return errAlertsNotConfigured }
	h.m.Raise("entry-unreachable-external", AlertCritical, "Вход недоступен снаружи", "d")
	var gotLimit int
	alertEventsList = func(limit int) ([]alertEvent, error) {
		gotLimit = limit
		return h.events, nil
	}

	rec, _ := serveAlertAdmin(handleAdminAlertEvents, http.MethodGet, "/api/admin/alerts/events?limit=50", "")
	if rec.Code != http.StatusOK || gotLimit != 50 {
		t.Fatalf("status %d, limit %d", rec.Code, gotLimit)
	}
	var resp struct {
		Events      []alertEvent       `json:"events"`
		Undelivered []alertUndelivered `json:"undelivered"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Events) != 1 || len(resp.Undelivered) != 1 {
		t.Fatalf("response: %s", rec.Body.String())
	}
	if u := resp.Undelivered[0]; u.Key != "entry-unreachable-external" || !u.NotConfigured || u.Attempts != 1 || u.Level != AlertCritical {
		t.Fatalf("undelivered: %+v", u)
	}

	serveAlertAdmin(handleAdminAlertEvents, http.MethodGet, "/api/admin/alerts/events?limit=100000", "")
	if gotLimit != 100 {
		t.Fatalf("an out-of-range limit falls back to 100, got %d", gotLimit)
	}
	alertEventsList = func(int) ([]alertEvent, error) { return nil, errAlertsNoDB }
	if rec, resp := serveAlertAdmin(handleAdminAlertEvents, http.MethodGet, "/api/admin/alerts/events", ""); rec.Code != http.StatusInternalServerError || resp["error"] == nil {
		t.Fatalf("journal read failure: %d %v", rec.Code, resp)
	}
}
