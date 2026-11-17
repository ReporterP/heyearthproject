package admin

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/heyearth/backend/internal/testpg"
)

// C1: a role made in the panel, given to an admin, acts on the very next request; the system
// role and the last active super admin cannot be broken from the API.
func TestAdminsAndRoles(t *testing.T) {
	conn := testpg.Open(t)
	if err := seedPermissionCatalog(); err != nil {
		t.Fatal(err)
	}
	if err := ensureSuperAdminRole(); err != nil {
		t.Fatal(err)
	}
	owner := func() int {
		var id int
		_ = conn.QueryRow(`SELECT u.id FROM admin_users u JOIN admin_user_roles ur ON ur.admin_user_id = u.id JOIN admin_roles r ON r.id = ur.role_id WHERE r.role_key = 'super_admin' ORDER BY u.id LIMIT 1`).Scan(&id)
		return id
	}()
	if owner == 0 {
		testpg.MustExec(t, conn, `INSERT INTO admin_users (username, password_hash, is_active) VALUES ('owner', 'x', TRUE)`)
		testpg.MustExec(t, conn, `INSERT INTO admin_roles (role_key, name, is_system) VALUES ('super_admin', 'Super Admin', TRUE) ON CONFLICT (role_key) DO NOTHING`)
		testpg.MustExec(t, conn, `INSERT INTO admin_user_roles (admin_user_id, role_id) SELECT u.id, r.id FROM admin_users u, admin_roles r WHERE u.username = 'owner' AND r.role_key = 'super_admin'`)
		_ = conn.QueryRow(`SELECT id FROM admin_users WHERE username = 'owner'`).Scan(&owner)
	}
	prevAudit := auditLogExec
	t.Cleanup(func() { auditLogExec = prevAudit })
	auditLogExec = func(string, ...any) (sql.Result, error) { return driver.RowsAffected(1), nil }

	// A role with two rights.
	rec := asSuper(CreateAdminRoleHandler, "POST", "/api/admin/admin-roles",
		`{"role_key": "support", "name": "Поддержка", "permission_keys": ["users.read", "payments.read"], "is_system": true}`)
	if rec.Code != 201 {
		t.Fatalf("create role: %d %s", rec.Code, rec.Body)
	}
	var role AdminRole
	_ = json.Unmarshal(rec.Body.Bytes(), &role)
	if role.IsSystem {
		t.Error("the API made a system role")
	}
	if rec := asSuper(CreateAdminRoleHandler, "POST", "/api/admin/admin-roles", `{"role_key": "Bad Key", "name": "x"}`); rec.Code != 400 {
		t.Errorf("bad key: %d", rec.Code)
	}
	if rec := asSuper(CreateAdminRoleHandler, "POST", "/api/admin/admin-roles", `{"role_key": "support", "name": "x"}`); rec.Code != 409 {
		t.Errorf("duplicate key: %d", rec.Code)
	}

	// An admin with it: the rights act at once.
	rec = asSuper(CreateAdminUserHandler, "POST", "/api/admin/admin-users",
		`{"username": "helper", "password": "long-enough-pass", "role_keys": ["support"]}`)
	if rec.Code != 201 {
		t.Fatalf("create admin: %d %s", rec.Code, rec.Body)
	}
	var helper AdminUser
	_ = json.Unmarshal(rec.Body.Bytes(), &helper)
	live, err := adminLiveAccess(helper.ID)
	if err != nil || !live.Active || !slices.Contains(live.Permissions, "users.read") || slices.Contains(live.Permissions, "users.write") {
		t.Fatalf("live access %+v %v", live, err)
	}
	if rec := asSuper(CreateAdminUserHandler, "POST", "/api/admin/admin-users", `{"username": "x", "password": "short", "role_keys": ["support"]}`); rec.Code != 400 {
		t.Errorf("short password: %d", rec.Code)
	}

	// The role gains a right: the next request has it.
	path := "/api/admin/admin-roles/" + strconv.Itoa(role.ID)
	if rec := asSuper(UpdateAdminRoleHandler, "PATCH", path, `{"name": "Поддержка", "permission_keys": ["users.read", "users.write"]}`); rec.Code != 200 {
		t.Fatalf("update role: %d %s", rec.Code, rec.Body)
	}
	live, _ = adminLiveAccess(helper.ID)
	if !slices.Contains(live.Permissions, "users.write") || slices.Contains(live.Permissions, "payments.read") {
		t.Errorf("after the change: %v", live.Permissions)
	}

	// The system role is not edited.
	var superID int
	_ = conn.QueryRow(`SELECT id FROM admin_roles WHERE role_key = 'super_admin'`).Scan(&superID)
	if rec := asSuper(UpdateAdminRoleHandler, "PATCH", "/api/admin/admin-roles/"+strconv.Itoa(superID), `{"permission_keys": []}`); rec.Code != 409 {
		t.Errorf("system role edited: %d %s", rec.Code, rec.Body)
	}

	// Without the super admin role, admins.write is no way up (review C1).
	if rec := callAs(CreateAdminUserHandler, "POST", "/api/admin/admin-users", `{"username": "x2", "password": "long-enough-pass", "role_keys": ["super_admin"]}`, "admins.write"); rec.Code != 403 {
		t.Errorf("super_admin given without being one: %d %s", rec.Code, rec.Body)
	}
	if rec := callAs(CreateAdminUserHandler, "POST", "/api/admin/admin-users", `{"username": "x3", "password": "long-enough-pass", "role_keys": ["support"]}`, "admins.write"); rec.Code != 403 {
		t.Errorf("a role with rights the editor lacks: %d %s", rec.Code, rec.Body)
	}
	if rec := callAs(CreateAdminUserHandler, "POST", "/api/admin/admin-users", `{"username": "x4", "password": "long-enough-pass", "role_keys": ["support"]}`, "admins.write", "users.read", "users.write"); rec.Code != 201 {
		t.Errorf("a role within the editor's rights: %d %s", rec.Code, rec.Body)
	}
	if rec := callAs(UpdateAdminRoleHandler, "PATCH", path, `{"name": "Поддержка", "permission_keys": ["users.read", "payments.refund"]}`, "admins.write", "users.read"); rec.Code != 403 {
		t.Errorf("a right the editor lacks added to a role: %d %s", rec.Code, rec.Body)
	}
	if rec := asSuper(UpdateAdminUserHandler, "PATCH", "/api/admin/admin-users/"+strconv.Itoa(helper.ID), `{"role_keys": []}`); rec.Code != 400 {
		t.Errorf("an admin left without roles: %d %s", rec.Code, rec.Body)
	}

	// The last active super admin cannot be switched off or lose the role.
	ownerPath := "/api/admin/admin-users/" + strconv.Itoa(owner)
	if rec := asSuper(UpdateAdminUserHandler, "PATCH", ownerPath, `{"is_active": false}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "последний") {
		t.Errorf("last super admin deactivated: %d %s", rec.Code, rec.Body)
	}
	if rec := asSuper(UpdateAdminUserHandler, "PATCH", ownerPath, `{"role_keys": ["support"]}`); rec.Code != 409 {
		t.Errorf("last super admin lost the role: %d %s", rec.Code, rec.Body)
	}
	var active bool
	_ = conn.QueryRow(`SELECT is_active FROM admin_users WHERE id = $1`, owner).Scan(&active)
	if !active {
		t.Error("the refused change was written")
	}
	// With a second super admin it is allowed.
	if rec := asSuper(UpdateAdminUserHandler, "PATCH", "/api/admin/admin-users/"+strconv.Itoa(helper.ID), `{"role_keys": ["super_admin"]}`); rec.Code != 200 {
		t.Fatalf("second super admin: %d %s", rec.Code, rec.Body)
	}
	if rec := asSuper(UpdateAdminUserHandler, "PATCH", ownerPath, `{"is_active": false}`); rec.Code != 200 {
		t.Errorf("with another super admin: %d %s", rec.Code, rec.Body)
	}
}

// asSuper is callAs for a super admin (every right, the super_admin role).
func asSuper(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), adminClaimsContextKey, &Claims{AdminID: 1, Roles: []string{superAdminRoleKey}, Permissions: []string{"*"}}))
	h(rec, req)
	return rec
}
