package admin

import "net/http"

// AdminMeHandler — GET /api/admin/me: the caller's roles and permissions as the database has
// them now (AuthMiddleware loads them live). The panel keeps the login answer in the browser;
// without this it showed the rights of the moment of login, so a right added later (a new
// permission in a deploy, a role change) stayed invisible until the admin signed in again,
// even though the API already allowed it.
func AdminMeHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok {
		writeAdminError(w, http.StatusUnauthorized, "Missing admin session")
		return
	}
	perms := claims.Permissions
	if perms == nil {
		perms = []string{}
	}
	roles := claims.Roles
	if roles == nil {
		roles = []string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAdminJSON(w, http.StatusOK, map[string]any{
		"admin_id":    claims.AdminID,
		"roles":       roles,
		"permissions": perms,
		"tenant_id":   claims.TenantID,
	})
}
