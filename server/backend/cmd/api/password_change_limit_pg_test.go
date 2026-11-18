package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// A stolen session can't guess the current password: after the per-account limit of wrong
// current passwords the cabinet answers 429 with a truthful Retry-After, even for the right one.
func TestPasswordChangeAnswers429PastTheLimit(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-pw-limit")
	testpg.NewUser(t, conn, "pw-limit-user")
	if err := biz.SetCloudPassword("pw-limit-user", "right-pass-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := biz.SetUsername("pw-limit-user", "pw_limit"); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"id_or_username": "pw_limit", "password": "right-pass-1"})
	login := httptest.NewRecorder()
	handleUserLogin(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(b))))
	var s struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &s); err != nil || s.Token == "" {
		t.Fatalf("login: %d %s", login.Code, login.Body)
	}
	patch := func(current string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"new_password": "new-pass-22", "current_password": current})
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/user/profile", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+s.Token)
		rec := httptest.NewRecorder()
		handleUserProfile(rec, req)
		return rec
	}
	for i := 0; i < 10; i++ {
		if rec := patch("wrong-guess"); rec.Code != http.StatusBadRequest {
			t.Fatalf("try %d: %d %s", i+1, rec.Code, rec.Body)
		}
	}
	rec := patch("right-pass-1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("past the limit: %d Retry-After=%q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
	}
}
