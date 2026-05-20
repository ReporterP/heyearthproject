package admin

import (
	"net/http"

	"github.com/heyearth/backend/internal/db"
	"github.com/lib/pq"
)

// adminAccessibleTenants returns the list of tenant IDs this admin may see,
// or nil if the admin is a super-admin (unscoped) — in which case callers
// should NOT filter and treat the admin as omnipotent.
//
// Semantics:
//   admin_users.assigned_tenants IS NULL  → super-admin (returns nil, ok=true)
//   admin_users.assigned_tenants = '{}'   → no access (returns empty slice)
//   admin_users.assigned_tenants = '{a,b}'→ scoped
func adminAccessibleTenants(adminID int) (tenantIDs []int, isSuperAdmin bool, err error) {
	var ids pq.Int64Array
	var isNull bool
	// Use a CTE-style query that distinguishes NULL from empty array.
	row := db.DB.QueryRow(`
		SELECT COALESCE(assigned_tenants, '{}'::INT[]),
		       assigned_tenants IS NULL
		FROM admin_users WHERE id = $1
	`, adminID)
	if err = row.Scan(&ids, &isNull); err != nil {
		return nil, false, err
	}
	if isNull {
		return nil, true, nil
	}
	out := make([]int, len(ids))
	for i, v := range ids {
		out[i] = int(v)
	}
	return out, false, nil
}

// adminCanAccessTenant returns true if the given admin may operate on tenantID.
// Super-admins always return true; scoped admins must have the tenant in their
// assigned_tenants array. On any error (DB or claims missing), returns false
// so we fail closed.
func adminCanAccessTenant(r *http.Request, tenantID int) bool {
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims == nil {
		return false
	}
	ids, isSuper, err := adminAccessibleTenants(claims.AdminID)
	if err != nil {
		return false
	}
	if isSuper {
		return true
	}
	for _, id := range ids {
		if id == tenantID {
			return true
		}
	}
	return false
}

// applyTenantScopeToQuery appends WHERE-clause arguments for filtering by the
// admin's assigned_tenants. Returns the SQL fragment (e.g. " AND id = ANY($3)")
// and the appended args. For super-admins returns ("", original-args).
//
// Use in tenant list queries: see ListTenantsHandler.
func applyTenantScopeToQuery(r *http.Request, baseWhere string, baseArgs []any, argIdx int) (whereExtra string, args []any, nextIdx int) {
	args = baseArgs
	nextIdx = argIdx
	claims, ok := ClaimsFromContext(r.Context())
	if !ok || claims == nil {
		return "", args, nextIdx
	}
	ids, isSuper, err := adminAccessibleTenants(claims.AdminID)
	if err != nil || isSuper {
		return "", args, nextIdx
	}
	// Even an empty array results in id = ANY('{}') which matches nothing —
	// that's the intended "locked out" behaviour.
	int64s := make(pq.Int64Array, len(ids))
	for i, v := range ids {
		int64s[i] = int64(v)
	}
	whereExtra = " AND id = ANY($" + intToStr(nextIdx) + ")"
	args = append(args, int64s)
	nextIdx++
	return whereExtra, args, nextIdx
}

// intToStr is a tiny helper to avoid importing strconv in this file for one
// call site. (Keeps the helper self-contained.)
func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
