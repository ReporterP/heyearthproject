package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func useCanaryHTTP(t *testing.T) (*canaryHarness, *[]string) {
	t.Helper()
	canaryQueuedFailing = map[int64][]string{}
	h, r := newCanaryHarness(t)
	var audits []string
	prevLive, prevAudit := canaryLive, canaryAudit
	t.Cleanup(func() { canaryLive, canaryAudit = prevLive, prevAudit })
	canaryLive = r
	canaryAudit = func(r *http.Request, action, targetType, targetID string, details any) {
		audits = append(audits, action+" "+targetID)
	}
	return h, &audits
}

func postFlag(path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handleAdminOpsFlagAction(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return rec
}

func TestCanaryHTTP(t *testing.T) {
	_, audits := useCanaryHTTP(t)
	if rec := postFlag("/api/admin/ops/flags/sub_json_dns/test", `{"to":"on"}`); rec.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec.Code, rec.Body.String())
	}
	rec := postFlag("/api/admin/ops/flags/sub_json_dns/promote", ``)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "выдержка") {
		t.Errorf("promote from test: %d %s — want 409 with the verdict", rec.Code, rec.Body.String())
	}
	if rec := postFlag("/api/admin/ops/flags/sub_direct_by_name/test", `{"to":"on"}`); rec.Code != http.StatusConflict {
		t.Errorf("a second change: %d, want 409", rec.Code)
	}
	if rec := postFlag("/api/admin/ops/flags/sub_json_dns/rollback", ``); rec.Code != http.StatusOK {
		t.Errorf("rollback: %d %s", rec.Code, rec.Body.String())
	}
	if rec := postFlag("/api/admin/ops/flags/sub_json_dns/bypass", `{"to":"on"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bypass without a reason: %d, want 400", rec.Code)
	}
	if rec := postFlag("/api/admin/ops/flags/sub_json_dns/nope", ``); rec.Code != http.StatusNotFound {
		t.Errorf("unknown action: %d", rec.Code)
	}
	if len(*audits) != 2 || (*audits)[0] != "flag.test sub_json_dns" || (*audits)[1] != "flag.rollback sub_json_dns" {
		t.Errorf("audit %v — one record per performed action", *audits)
	}
}

func TestCanaryRoutesPermissions(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-canary-routes-0123456789abcdef")
	for path, perm := range canaryRoutePerms {
		want := "settings.write"
		if path == "/api/admin/ops/flags" {
			want = "servers.read"
		}
		if perm != want {
			t.Errorf("%s: permission %s, want %s", path, perm, want)
		}
	}
	mux := http.NewServeMux()
	// the same wrapping registerCanaryRoutes uses, on a private mux
	mux.HandleFunc("/api/admin/ops/flags/", adminWrap(canaryRoutePerms["/api/admin/ops/flags/"], handleAdminOpsFlagAction))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/admin/ops/flags/sub_json_dns/test", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
}
