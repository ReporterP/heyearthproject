package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A6: the Happ import link is built locally (happ://add/<subscription url>), without
// calling crypto.happ.su.
func TestHappLinkIsLocal(t *testing.T) {
	conn := testpg.Open(t)
	id := testpg.NewUser(t, conn, "happ-user")
	var token string
	if err := conn.QueryRow(`SELECT subscribe_token::text FROM users WHERE id = $1`, id).Scan(&token); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handleGetHappLink(rec, httptest.NewRequest(http.MethodGet, "/api/v1/user/happ-link/"+token, nil))
	var out struct {
		HappLink     string `json:"happ_link"`
		SubscribeURL string `json:"subscribe_url"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != http.StatusOK || out.HappLink != "happ://add/"+out.SubscribeURL || !strings.HasSuffix(out.SubscribeURL, "/api/v1/user/subscribe/"+token) {
		t.Fatalf("%d %+v", rec.Code, out)
	}
	rec = httptest.NewRecorder()
	handleGetHappLink(rec, httptest.NewRequest(http.MethodGet, "/api/v1/user/happ-link/00000000-0000-0000-0000-000000000000", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown token: %d", rec.Code)
	}
}
