package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// A HiEarth ID signs in to an account without a password, so the company cabinet never hands
// out members' full IDs — not even to the company's admin. «(вы)» comes from is_me.
func TestBusinessMeNeverReturnsFullMemberIDs(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-b2b-ids")
	const adminGUID = "a1a1a1a1-1111-4111-8111-111111111111"
	const memberGUID = "b2b2b2b2-2222-4222-8222-222222222222"
	adminID := testpg.NewUser(t, conn, adminGUID)
	memberID := testpg.NewUser(t, conn, memberGUID)
	var tenantID int
	if err := conn.QueryRow(`INSERT INTO tenants (name, slug, status, plan, contact_email, max_users)
		VALUES ('QA Co', 'qa-co', 'active', 'basic', 'co@example.test', 10) RETURNING id`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	testpg.MustExec(t, conn, `INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'admin'), ($1, $3, 'viewer')`,
		tenantID, adminID, memberID)

	token, err := issueUserJWT(adminGUID, adminID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/business/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handleBusinessMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("business/me: %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, guid := range []string{adminGUID, memberGUID} {
		if strings.Contains(body, guid) {
			t.Errorf("the admin got a full HiEarth ID %s", guid)
		}
	}
	var resp struct {
		Members []struct {
			UserDBID int  `json:"user_db_id"`
			IsMe     bool `json:"is_me"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, m := range resp.Members {
		if m.IsMe != (m.UserDBID == adminID) {
			t.Errorf("member %d: is_me=%v", m.UserDBID, m.IsMe)
		}
	}
}
