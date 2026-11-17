package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// GET /api/admin/me gives the rights the database has now: a right granted after login (a new
// permission in a deploy) shows up without signing in again; no token is 401.
func TestAdminMeLivePermissions(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-admin-me")
	if err := seedPermissionCatalog(); err != nil {
		t.Fatal(err)
	}
	testpg.MustExec(t, conn, `INSERT INTO admin_users (username, password_hash, is_active) VALUES ('desk', 'x', TRUE)`)
	testpg.MustExec(t, conn, `INSERT INTO admin_roles (role_key, name, is_system) VALUES ('desk', 'Desk', FALSE)`)
	testpg.MustExec(t, conn, `INSERT INTO admin_role_permissions (role_id, permission_id)
		SELECT r.id, p.id FROM admin_roles r, admin_permissions p WHERE r.role_key = 'desk' AND p.permission_key = 'users.read'`)
	testpg.MustExec(t, conn, `INSERT INTO admin_user_roles (admin_user_id, role_id)
		SELECT u.id, r.id FROM admin_users u, admin_roles r WHERE u.username = 'desk' AND r.role_key = 'desk'`)
	var id int
	if err := conn.QueryRow(`SELECT id FROM admin_users WHERE username = 'desk'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// The token is issued with the rights of the moment of login (users.read only).
	tok, err := GenerateToken(AdminUser{ID: id, Username: "desk", Permissions: []string{"users.read"}})
	if err != nil {
		t.Fatal(err)
	}
	me := func(bearer string) (int, []string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		AuthMiddleware(AdminMeHandler)(rec, req)
		var out struct {
			Permissions []string `json:"permissions"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Permissions
	}
	if code, _ := me(""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	code, perms := me(tok)
	if code != http.StatusOK || !slices.Equal(perms, []string{"users.read"}) {
		t.Fatalf("before: %d %v", code, perms)
	}
	// A right added after login (as a deploy's migration does) is there on the next call.
	testpg.MustExec(t, conn, `INSERT INTO admin_role_permissions (role_id, permission_id)
		SELECT r.id, p.id FROM admin_roles r, admin_permissions p WHERE r.role_key = 'desk' AND p.permission_key = 'users.password_reset'`)
	if code, perms := me(tok); code != http.StatusOK || !slices.Contains(perms, "users.password_reset") {
		t.Errorf("after the grant: %d %v", code, perms)
	}
}
