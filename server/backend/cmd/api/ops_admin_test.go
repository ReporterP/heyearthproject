package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
)

// overviewHarness replaces what the overview reads.
type overviewHarness struct {
	now      time.Time
	alerts   []activeAlert
	feed     []biz.FeedItem
	feedFrom time.Time
	feedTo   time.Time
	raised   map[string]time.Time
	feedErr  error
}

func withOverview(t *testing.T) *overviewHarness {
	t.Helper()
	h := &overviewHarness{now: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), raised: map[string]time.Time{}}
	old := opsOverview
	opsOverview = opsOverviewDeps{
		now: func() time.Time { return h.now },
		paths: func(context.Context, time.Time) []pathState {
			return []pathState{{ID: "hub", Label: "Хаб", State: "ok", CheckedAt: h.now.Add(-42 * time.Second)},
				{ID: "entry:6", Label: "Вход 6", State: "off", Detail: "вне ротации", CheckedAt: h.now.Add(-time.Minute)}}
		},
		alerts: func() []activeAlert { return h.alerts },
		feed: func(_ context.Context, from, to time.Time, _ int) ([]biz.FeedItem, error) {
			h.feedFrom, h.feedTo = from, to
			return h.feed, h.feedErr
		},
		raisedAt: func(_ context.Context, key string) (time.Time, bool, error) {
			at, ok := h.raised[key]
			return at, ok, nil
		},
	}
	t.Cleanup(func() { opsOverview = old })
	return h
}

func getOverview(t *testing.T, query string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	handleAdminOpsOverview(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ops/overview"+query, nil))
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func TestOverviewShape(t *testing.T) {
	h := withOverview(t)
	h.alerts = []activeAlert{
		{Key: "relay-6-unreachable-external", Level: AlertWarn, Title: "Резервный вход недоступен снаружи", Details: "3 проверки подряд", Since: h.now.Add(-14 * time.Minute)},
		{Key: "all-exits-down", Level: AlertCritical, Title: "Все выходы недоступны", Since: h.now.Add(-2 * time.Minute)},
		{Key: "relay-6-unreachable-external~unstable", Level: AlertWarn, Title: "Нестабильно", Since: h.now.Add(-time.Minute)},
		{Key: "exit-path-down-3-hy", Level: AlertWarn, Title: "Путь", InhibitedBy: "exit-down-3", Since: h.now.Add(-time.Minute)},
	}
	h.feed = []biz.FeedItem{{At: h.now.Add(-5 * time.Minute), Source: "ops", Kind: "restart", Actor: "system", Target: "xray", Summary: "xray перезапущен: structural", Impact: "K3", OK: true}}
	rec, resp := getOverview(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, key := range []string{"build", "paths", "alerts", "feed"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("the response has no %q", key)
		}
	}
	build := resp["build"].(map[string]any)
	if build["sha"] != "dev" || build["started_at"] == "" {
		t.Errorf("build = %v", build)
	}
	paths := resp["paths"].([]any)
	hub := paths[0].(map[string]any)
	if hub["id"] != "hub" || hub["state"] != "ok" || hub["age_sec"] != float64(42) {
		t.Errorf("path = %v", hub)
	}
	alerts := resp["alerts"].([]any)
	if len(alerts) != 4 {
		t.Fatalf("%d alerts", len(alerts))
	}
	byKey := map[string]map[string]any{}
	for _, a := range alerts {
		m := a.(map[string]any)
		byKey[m["key"].(string)] = m
	}
	relay := byKey["relay-6-unreachable-external"]
	if relay["meaning"] == "" || relay["usual"] == "" || len(relay["actions"].([]any)) == 0 {
		t.Errorf("an alert with a catalogue entry came without its explanation: %v", relay)
	}
	if relay["flapping"] != true {
		t.Errorf("relay-6-unreachable-external has an active ~unstable twin: flapping = %v", relay["flapping"])
	}
	if relay["last_details"] != "3 проверки подряд" || relay["level"] != "warn" || relay["since"] == "" {
		t.Errorf("alert = %v", relay)
	}
	if byKey["exit-path-down-3-hy"]["inhibited_by"] != "exit-down-3" || byKey["all-exits-down"]["flapping"] != false {
		t.Errorf("alerts = %v", byKey)
	}
	feed := resp["feed"].([]any)[0].(map[string]any)
	if feed["source"] != "ops" || feed["kind"] != "restart" || feed["impact"] != "K3" || feed["target"] != "xray" || feed["ok"] != true {
		t.Errorf("feed item = %v", feed)
	}
	// by default the feed covers 24 hours; feed_hours is capped at a week
	if got := h.now.Sub(h.feedFrom); got != 24*time.Hour {
		t.Errorf("default feed window = %s", got)
	}
	getOverview(t, "?feed_hours=500")
	if got := h.now.Sub(h.feedFrom); got != 168*time.Hour {
		t.Errorf("feed_hours=500: window = %s, want it capped at 168 h", got)
	}
	// empty lists are lists, not null: the page maps over them
	h.alerts, h.feed = nil, nil
	rec, _ = getOverview(t, "")
	if !strings.Contains(rec.Body.String(), `"alerts":[]`) || !strings.Contains(rec.Body.String(), `"feed":[]`) {
		t.Errorf("empty lists: %s", rec.Body.String())
	}
}

func TestOverviewHidesSecrets(t *testing.T) {
	h := withOverview(t)
	h.alerts = []activeAlert{{Key: "entry-unreachable-external", Level: AlertCritical, Title: "Вход 203.0.113.7 недоступен",
		Details: "exit :1090 → 203.0.113.7:443, пользователь 11111111-2222-3333-4444-555555555555", Since: h.now}}
	h.feed = []biz.FeedItem{{At: h.now, Source: "audit", Kind: "update_setting", Actor: "owner", Target: "server:198.51.100.20", Summary: "адрес 198.51.100.20"}}
	rec, _ := getOverview(t, "")
	body := rec.Body.String()
	if regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}\b`).MatchString(body) {
		t.Errorf("an address is in the overview:\n%s", body)
	}
	if strings.Contains(body, "2222-3333") {
		t.Errorf("a full user identifier is in the overview:\n%s", body)
	}
	if !strings.Contains(body, "11111111…") {
		t.Errorf("the user must stay recognisable by 8 characters:\n%s", body)
	}
}

func TestOverviewBefore(t *testing.T) {
	h := withOverview(t)
	raised := h.now.Add(-3 * time.Hour)
	h.raised["exit-down-3"] = raised
	h.feed = []biz.FeedItem{{At: raised.Add(-10 * time.Minute), Source: "audit", Kind: "update_setting", Actor: "owner", Summary: "x"}}
	rec, resp := getOverview(t, "?before=exit-down-3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !h.feedFrom.Equal(raised.Add(-time.Hour)) || !h.feedTo.Equal(raised) {
		t.Fatalf("feed window %s … %s, want the hour before %s", h.feedFrom, h.feedTo, raised)
	}
	if len(resp["feed"].([]any)) != 1 || resp["before"] != "exit-down-3" {
		t.Errorf("response = %v", resp)
	}
	if rec, _ := getOverview(t, "?before=nobody-raised-this"); rec.Code != http.StatusNotFound {
		t.Errorf("an alert that was never raised: status %d, want 404", rec.Code)
	}
}

func TestOverviewFeedUnavailable(t *testing.T) {
	h := withOverview(t)
	h.feedErr = errors.New("connection refused")
	h.alerts = []activeAlert{{Key: "db-unreachable", Level: AlertCritical, Title: "База данных недоступна", Since: h.now}}
	captureLog(t)
	rec, resp := getOverview(t, "")
	// the database is down: this is exactly when the page must still show paths and alerts
	if rec.Code != http.StatusOK || len(resp["alerts"].([]any)) != 1 || resp["feed_error"] == nil {
		t.Fatalf("status %d, response %v; want the alerts and a feed_error", rec.Code, resp)
	}
}

func TestOverviewPermission(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-ops-overview-0123456789abcdef")
	rec := httptest.NewRecorder()
	adminOpsOverviewHandler()(rec, httptest.NewRequest(http.MethodGet, "/api/admin/ops/overview", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", rec.Code)
	}
	rec = httptest.NewRecorder()
	handleAdminOpsOverview(rec, httptest.NewRequest(http.MethodPost, "/api/admin/ops/overview", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}

// The manager lists its open incidents with when they began and what they last said.
func TestAlertManagerActive(t *testing.T) {
	h := newAlertHarness()
	h.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "первая причина")
	began := h.now
	h.advance(5 * time.Minute)
	h.m.Raise("exit-down-3", AlertWarn, "Выход 3 недоступен", "свежая причина")
	h.m.Raise("exit-path-down-3-hy", AlertWarn, "Путь", "d")
	h.m.Raise("disk-usage", AlertWarn, "Диск", "90%")
	h.m.Resolve("disk-usage", "ok")
	got := h.m.active()
	if len(got) != 2 || got[0].Key != "exit-down-3" || got[1].Key != "exit-path-down-3-hy" {
		t.Fatalf("active = %+v, want the two open incidents sorted by key", got)
	}
	if !got[0].Since.Equal(began) || got[0].Details != "свежая причина" || got[0].Level != AlertWarn || got[0].Title != "Выход 3 недоступен" {
		t.Errorf("exit-down-3 = %+v, want it since the first raise with the latest details", got[0])
	}
	if got[1].InhibitedBy != "exit-down-3" {
		t.Errorf("exit-path-down-3-hy = %+v, want it marked as explained by exit-down-3", got[1])
	}
	// after a restart the journal gives the same picture
	replayed := replayAlertEvents(h.events)
	if st := replayed["exit-down-3"]; st == nil || !st.since.Equal(began) || st.details == "" {
		t.Errorf("replayed exit-down-3 = %+v, want since and details from the journal", st)
	}
}
