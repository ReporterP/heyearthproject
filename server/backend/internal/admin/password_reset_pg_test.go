package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/heyearth/backend/internal/biz"
	"github.com/heyearth/backend/internal/testpg"
)

// «Сбросить пароль»: only users.password_reset may; the temporary password is in the answer
// only (not in the audit log, not in email_log); every session of the client closes; the
// flag is raised; a confirmed address gets a letter; an account without a password is refused.
func TestUserPasswordReset(t *testing.T) {
	conn := testpg.Open(t)
	t.Setenv("JWT_SECRET", "test-secret-password-reset")

	// Migration 000087 seeds the right (before the API's catalog seeding runs).
	var seeded bool
	_ = conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM admin_permissions WHERE permission_key = 'users.password_reset')`).Scan(&seeded)
	if !seeded {
		t.Fatal("migration 000087 did not add users.password_reset")
	}
	if err := seedPermissionCatalog(); err != nil {
		t.Fatal(err)
	}
	if err := ensureSuperAdminRole(); err != nil {
		t.Fatal(err)
	}
	var superHas bool
	_ = conn.QueryRow(`SELECT EXISTS (SELECT 1 FROM admin_role_permissions rp JOIN admin_roles r ON r.id = rp.role_id
		JOIN admin_permissions p ON p.id = rp.permission_id WHERE r.role_key = 'super_admin' AND p.permission_key = 'users.password_reset')`).Scan(&superHas)
	if !superHas {
		t.Error("the super admin role lacks users.password_reset")
	}

	// Two admins: support with users.read + users.write (not enough), and one with the right.
	testpg.MustExec(t, conn, `INSERT INTO admin_users (username, password_hash, is_active) VALUES ('desk', 'x', TRUE), ('resetter', 'x', TRUE)`)
	testpg.MustExec(t, conn, `INSERT INTO admin_roles (role_key, name, is_system) VALUES ('desk', 'Desk', FALSE), ('resetter', 'Resetter', FALSE)`)
	testpg.MustExec(t, conn, `INSERT INTO admin_role_permissions (role_id, permission_id)
		SELECT r.id, p.id FROM admin_roles r, admin_permissions p
		 WHERE (r.role_key = 'desk' AND p.permission_key IN ('users.read', 'users.write'))
		    OR (r.role_key = 'resetter' AND p.permission_key = 'users.password_reset')`)
	testpg.MustExec(t, conn, `INSERT INTO admin_user_roles (admin_user_id, role_id)
		SELECT u.id, r.id FROM admin_users u, admin_roles r
		 WHERE (u.username = 'desk' AND r.role_key = 'desk') OR (u.username = 'resetter' AND r.role_key = 'resetter')`)
	token := func(username string) string {
		var id int
		if err := conn.QueryRow(`SELECT id FROM admin_users WHERE username = $1`, username).Scan(&id); err != nil {
			t.Fatal(err)
		}
		tok, err := GenerateToken(AdminUser{ID: id, Username: username})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	deskTok, resetTok := token("desk"), token("resetter")
	reset := func(tok string, userDBID int) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+strconv.Itoa(userDBID)+"/password/reset", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		RequirePermission(permUserPasswordReset, UserPasswordResetHandler)(rec, req)
		return rec
	}

	uid := testpg.NewUser(t, conn, "reset-me")
	noPass := testpg.NewUser(t, conn, "no-pass")
	if err := biz.SetCloudPassword("reset-me", "old-pass", "", ""); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := biz.GenerateUserSessionToken("reset-me", uid); err != nil {
			t.Fatal(err)
		}
	}
	// A confirmed address and SMTP switched on (a port nobody listens on: the send fails
	// in the background, which is fine — the letter was handed over).
	testpg.MustExec(t, conn, `UPDATE users SET email = 'client@example.test', email_verified_at = NOW() WHERE id = $1`, uid)
	testpg.MustExec(t, conn, `INSERT INTO app_settings (key, value) VALUES
		('smtp_enabled', 'true'), ('smtp_host', '127.0.0.1'), ('smtp_port', '1'), ('smtp_from_email', 'noreply@example.test')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)

	if rec := reset(deskTok, uid); rec.Code != http.StatusForbidden {
		t.Fatalf("users.write alone reset a password: %d %s", rec.Code, rec.Body)
	}
	if rec := reset(resetTok, noPass); rec.Code != http.StatusConflict {
		t.Errorf("no password: %d %s", rec.Code, rec.Body)
	}
	if rec := reset(resetTok, 999999); rec.Code != http.StatusNotFound {
		t.Errorf("unknown user: %d %s", rec.Code, rec.Body)
	}

	rec := reset(resetTok, uid)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	var out struct {
		TempPassword   string `json:"temp_password"`
		SessionsClosed int64  `json:"sessions_closed"`
		EmailSent      bool   `json:"email_sent"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[a-hjkmnp-z2-9]{4}-[a-hjkmnp-z2-9]{4}-[a-hjkmnp-z2-9]{4}$`).MatchString(out.TempPassword) {
		t.Errorf("temporary password %q", out.TempPassword)
	}
	if out.SessionsClosed != 2 || !out.EmailSent {
		t.Errorf("answer %+v, want 2 sessions closed and the letter sent", out)
	}

	var sessions int
	var mustChange bool
	var resetAt *time.Time
	_ = conn.QueryRow(`SELECT COUNT(*) FROM user_sessions WHERE user_id = $1`, uid).Scan(&sessions)
	_ = conn.QueryRow(`SELECT password_must_change, password_reset_at FROM users WHERE id = $1`, uid).Scan(&mustChange, &resetAt)
	if sessions != 0 || !mustChange || resetAt == nil {
		t.Errorf("after the reset: %d sessions, must_change=%v, reset_at=%v", sessions, mustChange, resetAt)
	}
	if _, err := biz.LoginByIDOrUsername("reset-me", "old-pass"); err == nil {
		t.Error("the old password still logs in")
	}
	if u, err := biz.LoginByIDOrUsername("reset-me", out.TempPassword); err != nil || !u.PasswordMustChange {
		t.Errorf("temporary password: %v, must_change=%v", err, u != nil && u.PasswordMustChange)
	}

	// Audit: who, whom, how many sessions — never the password.
	var adminID int
	var details string
	if err := conn.QueryRow(`SELECT COALESCE(admin_id, 0), COALESCE(details::text, '') FROM admin_audit_log
		WHERE action = 'user.password_reset' AND target_type = 'user' AND target_id = $1`, strconv.Itoa(uid)).Scan(&adminID, &details); err != nil {
		t.Fatalf("no audit row: %v", err)
	}
	if adminID == 0 || !strings.Contains(details, `"sessions_closed": 2`) && !strings.Contains(details, `"sessions_closed":2`) {
		t.Errorf("audit admin %d details %s", adminID, details)
	}
	if strings.Contains(details, out.TempPassword) {
		t.Error("the temporary password is in the audit log")
	}

	// The letter: logged by template and subject only; wait for the background send to finish
	// so it does not outlive the test database.
	deadline := time.Now().Add(10 * time.Second)
	var status, subject, to string
	for {
		if err := conn.QueryRow(`SELECT status, subject, email FROM email_log WHERE template = 'password_reset' AND user_id = $1`, uid).Scan(&status, &subject, &to); err != nil {
			t.Fatalf("no email_log row: %v", err)
		}
		if status != "pending" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if to != "client@example.test" || strings.Contains(subject, out.TempPassword) {
		t.Errorf("letter to %q subject %q", to, subject)
	}

	// The user list shows the state the dialog needs.
	listRec := asSuper(ListUsersHandler, http.MethodGet, "/api/admin/users?page_size=50", "")
	var page struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(listRec.Body.Bytes(), &page)
	found := false
	for _, it := range page.Items {
		switch it["user_id"] {
		case "reset-me":
			found = true
			if it["has_password"] != true || it["password_must_change"] != true || it["has_confirmed_email"] != true || it["password_reset_at"] == nil {
				t.Errorf("list row %v", it)
			}
		case "no-pass":
			if it["has_password"] != false || it["password_must_change"] != false || it["has_confirmed_email"] != false {
				t.Errorf("list row %v", it)
			}
		}
	}
	if !found {
		t.Fatalf("reset-me not in the list: %d %s", listRec.Code, listRec.Body)
	}

	// A second reset: a new password, the first temporary one stops working.
	rec2 := reset(resetTok, uid)
	var out2 struct {
		TempPassword string `json:"temp_password"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &out2)
	if rec2.Code != http.StatusOK || out2.TempPassword == out.TempPassword {
		t.Fatalf("second reset: %d, same password %v", rec2.Code, out2.TempPassword == out.TempPassword)
	}
	if _, err := biz.LoginByIDOrUsername("reset-me", out.TempPassword); err == nil {
		t.Error("the first temporary password still logs in")
	}
	// wait for the second letter too
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		var pending int
		_ = conn.QueryRow(`SELECT COUNT(*) FROM email_log WHERE template = 'password_reset' AND status = 'pending'`).Scan(&pending)
		if pending == 0 {
			break
		}
	}
}
