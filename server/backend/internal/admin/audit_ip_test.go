package admin

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heyearth/backend/internal/middleware"
)

// WriteAuditLog stores the client IP of the API's single trust model, never a client-written
// X-Forwarded-For entry.
func TestWriteAuditLogClientIP(t *testing.T) {
	var gotIP any
	orig := auditLogExec
	auditLogExec = func(query string, args ...any) (sql.Result, error) {
		if len(args) != 7 {
			t.Fatalf("INSERT got %d args, want 7: %v", len(args), args)
		}
		gotIP = args[6]
		return nil, nil
	}
	t.Cleanup(func() { auditLogExec = orig })

	tests := []struct {
		name    string
		nginx   bool // accepted by the nginx-facing (TrustProxyHeaders) listener
		remote  string
		headers map[string]string
		want    string
	}{
		{"admin via nginx", true, "127.0.0.1:40000", map[string]string{"X-Real-IP": "198.51.100.20", "X-Forwarded-For": "198.51.100.20"}, "198.51.100.20"},
		// the client wrote the left-most entry; nginx appended the real address
		{"spoofed left-most XFF via nginx", true, "127.0.0.1:40000", map[string]string{"X-Real-IP": "198.51.100.20", "X-Forwarded-For": "10.1.2.3, 198.51.100.20"}, "198.51.100.20"},
		{"spoofed left-most XFF via nginx, no X-Real-IP", true, "127.0.0.1:40000", map[string]string{"X-Forwarded-For": "10.1.2.3, 198.51.100.20"}, "198.51.100.20"},
		{"spoofed headers from a non-loopback peer", true, "203.0.113.9:5000", map[string]string{"X-Real-IP": "10.1.2.3", "X-Forwarded-For": "10.1.2.3"}, "203.0.113.9"},
		{"spoofed headers on a non-nginx listener", false, "127.0.0.1:40000", map[string]string{"X-Real-IP": "10.1.2.3", "X-Forwarded-For": "10.1.2.3"}, "127.0.0.1"},
		{"IPv6 peer keeps its full address", false, "[2001:db8::7]:5000", nil, "2001:db8::7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotIP = nil
			r := httptest.NewRequest(http.MethodPost, "/api/admin/users", nil)
			r.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			ctx := context.WithValue(r.Context(), adminClaimsContextKey, &Claims{AdminID: 1, Username: "root"})
			if tc.nginx {
				ctx = middleware.TrustProxyHeaders(ctx)
			}
			WriteAuditLog(r.WithContext(ctx), "users.update", "user", "42", map[string]int{"n": 1})
			if gotIP != tc.want {
				t.Fatalf("audit ip_address = %v, want %q", gotIP, tc.want)
			}
		})
	}
}
