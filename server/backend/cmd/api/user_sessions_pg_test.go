package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// The cabinet's «Входы»: a person sees their sign-ins (where from, when, which one is this
// device — never an address), closes one, or closes all but this one. A password-less account
// can do it too: a leaked HiEarth ID is no longer a session nobody can end (QA 2026-11-18).
func TestUserSessionsListAndClose(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-sessions")
	const me = "c3c3c3c3-3333-4333-8333-333333333333"
	uid := testpg.NewUser(t, conn, me)
	other := testpg.NewUser(t, conn, "d4d4d4d4-4444-4444-8444-444444444444")

	site, err := biz.GenerateUserSessionToken(me, uid, biz.SessionSourceSite)
	if err != nil {
		t.Fatal(err)
	}
	app, _ := biz.GenerateUserSessionToken(me, uid, biz.SessionSourceApp)
	tg, _ := biz.GenerateUserSessionToken(me, uid, biz.SessionSourceTelegram)
	foreign, _ := biz.GenerateUserSessionToken("d4d4d4d4-4444-4444-8444-444444444444", other, biz.SessionSourceSite)

	do := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		if strings.HasPrefix(path, "/api/v1/user/sessions/") {
			handleUserSessionAction(rec, req)
		} else {
			handleUserSessions(rec, req)
		}
		return rec
	}
	type item struct {
		ID      int    `json:"id"`
		Source  string `json:"source"`
		Current bool   `json:"current"`
	}
	list := func() []item {
		rec := do(http.MethodGet, "/api/v1/user/sessions", site)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "ip") {
			t.Errorf("the list carries an address field: %s", rec.Body)
		}
		var out struct {
			Items []item `json:"items"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.Items
	}
	items := list()
	if len(items) != 3 {
		t.Fatalf("want 3 own sessions, got %+v", items)
	}
	var appID, foreignID int
	for _, it := range items {
		if it.Current != (it.Source == biz.SessionSourceSite) {
			t.Errorf("current flag wrong: %+v", it)
		}
		if it.Source == biz.SessionSourceApp {
			appID = it.ID
		}
	}
	_ = conn.QueryRow(`SELECT id FROM user_sessions WHERE user_id = $1`, other).Scan(&foreignID)

	if rec := do(http.MethodDelete, "/api/v1/user/sessions/"+strconv.Itoa(foreignID), site); rec.Code != http.StatusNotFound {
		t.Errorf("closing someone else's session: %d, want 404", rec.Code)
	}
	if _, err := biz.ValidateUserToken(foreign); err != nil {
		t.Errorf("someone else's session was closed: %v", err)
	}
	if rec := do(http.MethodDelete, "/api/v1/user/sessions/"+strconv.Itoa(appID), site); rec.Code != http.StatusOK {
		t.Fatalf("close one: %d %s", rec.Code, rec.Body)
	}
	if _, err := biz.ValidateUserToken(app); err == nil {
		t.Error("the closed session still works")
	}
	if rec := do(http.MethodPost, "/api/v1/user/sessions/close-others", site); rec.Code != http.StatusOK {
		t.Fatalf("close others: %d %s", rec.Code, rec.Body)
	}
	if _, err := biz.ValidateUserToken(tg); err == nil {
		t.Error("close-others left another session")
	}
	if _, err := biz.ValidateUserToken(site); err != nil {
		t.Errorf("close-others closed the caller's own session: %v", err)
	}
}
