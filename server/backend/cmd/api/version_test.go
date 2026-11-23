package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuildInfoDefaults(t *testing.T) {
	info := buildInfo()
	if info["sha"] != "dev" {
		t.Errorf("sha = %q, want dev for a build without -ldflags", info["sha"])
	}
	if _, ok := info["built_at"]; !ok {
		t.Error("built_at key missing")
	}
	started, err := time.Parse(time.RFC3339, info["started_at"])
	if err != nil || time.Since(started) > time.Hour || time.Since(started) < 0 {
		t.Errorf("started_at = %q (%v), want the process start", info["started_at"], err)
	}
}

// The build of prod is not public: /api/v1/health stays as it is, the version needs an admin.
func TestAdminVersionRequiresAuth(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-version-0123456789abcdef")
	rec := httptest.NewRecorder()
	adminVersionHandler()(rec, httptest.NewRequest(http.MethodGet, "/api/admin/version", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminVersionBody(t *testing.T) {
	rec := httptest.NewRecorder()
	handleAdminVersion(rec, httptest.NewRequest(http.MethodGet, "/api/admin/version", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, key := range []string{`"sha":"dev"`, `"built_at"`, `"started_at"`} {
		if !strings.Contains(rec.Body.String(), key) {
			t.Errorf("body lacks %s: %s", key, rec.Body.String())
		}
	}
	rec = httptest.NewRecorder()
	handleAdminVersion(rec, httptest.NewRequest(http.MethodPost, "/api/admin/version", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rec.Code)
	}
}
