package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A request with no token was never signed in: «Нужен вход», not «Сессия истекла» (that one is
// for a token that stopped working).
func TestNoTokenSaysSignInNotExpired(t *testing.T) {
	_, err := extractUserFromToken(httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil))
	if err == nil {
		t.Fatal("no Authorization header must be an error")
	}
	rec := httptest.NewRecorder()
	writeAuthError(rec, err)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Нужен вход") {
		t.Fatalf("no token: %d %s", rec.Code, rec.Body)
	}
}
