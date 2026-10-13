package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckTunnelHandler(t *testing.T) {
	old := TunnelCheckFn
	t.Cleanup(func() { TunnelCheckFn = old })
	post := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		CheckTunnelHandler(rec, httptest.NewRequest(http.MethodPost, path, nil))
		return rec
	}

	TunnelCheckFn = nil
	if rec := post("/api/admin/servers/2/check-tunnel"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("no hook: %d", rec.Code)
	}
	var gotID int
	TunnelCheckFn = func(_ context.Context, id int) (TunnelCheckResult, error) {
		gotID = id
		if id == 9 {
			return TunnelCheckResult{}, sql.ErrNoRows
		}
		if id == 5 {
			return TunnelCheckResult{}, errors.New("db down")
		}
		return TunnelCheckResult{ServerID: id, Summary: "Оба пути работают.",
			Paths: []TunnelPathCheck{{Path: "hy", Name: "Hysteria (UDP)", Status: "ok", RTTms: 143}}}, nil
	}
	if rec := post("/api/admin/servers/20/check-tunnel"); rec.Code != http.StatusOK || gotID != 20 ||
		!strings.Contains(rec.Body.String(), `"rtt_ms":143`) {
		t.Fatalf("ok: %d %s (id %d)", rec.Code, rec.Body.String(), gotID)
	}
	if rec := post("/api/admin/servers/9/check-tunnel"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown exit: %d", rec.Code)
	}
	if rec := post("/api/admin/servers/5/check-tunnel"); rec.Code != http.StatusBadGateway {
		t.Fatalf("failed check: %d", rec.Code)
	}
	gotID = 0
	for _, p := range []string{"/api/admin/servers/x/check-tunnel", "/api/admin/servers/0/check-tunnel",
		"/api/admin/servers/-3/check-tunnel", "/api/admin/servers/2/files/7/check-tunnel"} {
		if rec := post(p); rec.Code != http.StatusBadRequest {
			t.Fatalf("bad id %s: %d", p, rec.Code)
		}
	}
	if gotID != 0 {
		t.Fatalf("a bad path reached the check (id %d)", gotID)
	}
}

// The route takes exactly /api/admin/servers/{one segment}/check-tunnel: a node file or
// directory named check-tunnel stays with the file browser.
func TestIsCheckTunnelPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/api/admin/servers/2/check-tunnel":            true,
		"/api/admin/servers/x/check-tunnel":            true, // the handler answers 400
		"/api/admin/servers/2/files/7/check-tunnel":    false,
		"/api/admin/servers/2/files/etc/check-tunnel":  false,
		"/api/admin/servers//check-tunnel":             false,
		"/api/admin/servers/check-tunnel":              false,
		"/api/admin/servers/2/check-tunnel/":           false,
		"/api/admin/servers/2/actions":                 false,
		"/api/admin/payments/2/check-tunnel":           false,
		"/api/admin/servers/2/check-tunnel-and-delete": false,
	} {
		if got := isCheckTunnelPath(p); got != want {
			t.Errorf("isCheckTunnelPath(%q) = %v, want %v", p, got, want)
		}
	}
}
