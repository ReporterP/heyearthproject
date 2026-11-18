package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// The public business sign-up is unauthenticated: it must never make an existing account the
// admin of a new company just because the e-mail matches. The signer gets a claim link and
// joins by opening it while signed in. Seat and server limits from the form are bounded.
func TestPublicCreateTenantDoesNotAdoptExistingAccounts(t *testing.T) {
	conn := testpg.Open(t)
	prev := tenantWelcomeEmail
	tenantWelcomeEmail = func(int, string, string, string) {}
	t.Cleanup(func() { tenantWelcomeEmail = prev })
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES ('b2b_enabled', 'true')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	victim := testpg.NewUser(t, conn, "f4f4f4f4-4444-4444-8444-444444444444")
	testpg.MustExec(t, conn, `UPDATE users SET email = 'victim@example.test' WHERE id = $1`, victim)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/public/create-tenant", strings.NewReader(body))
		rec := httptest.NewRecorder()
		PublicCreateTenantHandler(rec, req)
		return rec
	}
	rec := post(`{"company_name":"Stranger Co","email":"victim@example.test","max_users":5}`)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "/claim/tn-r-admin-") {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var memberships int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM tenant_members WHERE user_id = $1`, victim).Scan(&memberships)
	if memberships != 0 {
		t.Errorf("the existing account was made a member (%d memberships) without consent", memberships)
	}
	for _, bad := range []string{
		`{"company_name":"Neg Co","email":"a@example.test","max_users":-5}`,
		`{"company_name":"Huge Co","email":"b@example.test","max_users":100000000}`,
		`{"company_name":"Srv Co","email":"c@example.test","max_users":5,"max_servers":-1}`,
	} {
		if rec := post(bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", bad, rec.Code, rec.Body)
		}
	}
}
